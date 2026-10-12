package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRoutingPreservesAuthenticationAndErrors(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct {
		method, path, token string
		status              int
		code                string
	}{
		{"GET", "/api/session", "", 401, "invalid_token"},
		{"GET", "/api/admin/missing", "", 401, "invalid_admin_token"},
		{"GET", "/api/admin/missing", f.token, 401, "invalid_admin_token"},
		{"GET", "/api/admin/missing", adminTestToken, 404, "not_found"},
		{"GET", "/api/unknown", adminTestToken, 404, "not_found"},
		{"POST", "/api/session", "", 401, "invalid_admin_token"},
		{"POST", "/api/session", adminTestToken, 404, "not_found"},
		{"PATCH", "/api/admin/users", adminTestToken, 404, "not_found"},
		{"GET", "/api/admin/users/", adminTestToken, 404, "not_found"},
		{"GET", "/api/admin//users", adminTestToken, 404, "not_found"},
		{"GET", "/api/me/missing", "", 401, "invalid_api_key"},
		{"GET", "/api/me/missing", adminTestToken, 401, "invalid_api_key"},
		{"GET", "/api/me/missing", f.token, 404, "not_found"},
		{"POST", "/api/me/missing", f.token, 405, "method_not_allowed"},
		{"POST", "/api/me", f.token, 405, "method_not_allowed"},
		{"GET", "/v1", "", 401, "invalid_api_key"},
		{"GET", "/v1", f.token, 404, "unsupported_endpoint"},
		{"GET", "/v1/unknown", adminTestToken, 401, "invalid_api_key"},
		{"GET", "/v1/unknown", f.token, 404, "unsupported_endpoint"},
		{"GET", "/v1/models/", "", 401, "invalid_api_key"},
		{"GET", "/v1/models/", f.token, 404, "unsupported_endpoint"},
		{"GET", "/v1//models", f.token, 404, "unsupported_endpoint"},
		{"GET", "/v1/Models", f.token, 404, "unsupported_endpoint"},
	} {
		t.Run(tc.method+" "+tc.path+" "+tc.code, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			f.server.Config.Handler.ServeHTTP(w, req)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != tc.code {
				t.Fatalf("got %d %s; want %d %s", w.Code, w.Body, tc.status, tc.code)
			}
			if w.Header().Get("Location") != "" {
				t.Fatal("API path was redirected")
			}
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
				t.Fatal("API error fell through to the SPA")
			}
			if w.Header().Get("X-Request-Id") == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("fallback bypassed security middleware")
			}
			if strings.HasPrefix(tc.path, "/api/") && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("console error is cacheable")
			}
		})
	}
}

func TestPreflightRunsBeforeAuthenticationOnAllRoutes(t *testing.T) {
	f := setup(t)
	for _, path := range []string{"/api/admin/users", "/api/me/usage", "/v1/models", "/api/missing", "/v1/missing", "/console"} {
		for _, origin := range []string{"", "https://allowed.example", "https://evil.example"} {
			status := http.StatusNoContent
			if origin == "https://evil.example" {
				status = http.StatusForbidden
			}
			resp := f.request("OPTIONS", path, "", "", map[string]string{"Origin": origin})
			body := read(t, resp, status)
			if status == http.StatusNoContent && len(body) != 0 {
				t.Fatal("preflight response has a body")
			}
			allowed := resp.Header.Get("Access-Control-Allow-Origin")
			if origin == "https://allowed.example" {
				if allowed != origin || resp.Header.Get("Vary") != "Origin" {
					t.Fatal("allowed origin missing from preflight response")
				}
			} else if allowed != "" {
				t.Fatal("unexpected cross-origin permission")
			}
		}
	}
}

func TestFrontendFallbackAndStaticAssets(t *testing.T) {
	f := setup(t)
	s := f.server.Config.Handler.(*Server)
	s.assets = fstest.MapFS{
		"index.html":    {Data: []byte("<html>console</html>")},
		"assets/app.js": {Data: []byte("console.log('ready')")},
	}
	s.files = http.FileServer(http.FS(s.assets))
	for _, path := range []string{"/", "/usage", "/users/alice"} {
		resp := f.request("GET", path, "", "", nil)
		if body := read(t, resp, 200); string(body) != "<html>console</html>" {
			t.Fatalf("bad SPA fallback: %s", body)
		}
		if resp.Header.Get("Cache-Control") != "no-cache" || resp.Header.Get("Content-Security-Policy") == "" {
			t.Fatal("SPA headers missing")
		}
		if body := read(t, f.request("HEAD", path, "", "", nil), 200); len(body) != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
	resp := f.request("GET", "/assets/app.js", "", "", nil)
	if body := read(t, resp, 200); string(body) != "console.log('ready')" {
		t.Fatalf("asset was replaced with HTML: %s", body)
	}
	if resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal("asset cache policy lost")
	}
	read(t, f.request("HEAD", "/assets/app.js", "", "", nil), 200)
	body := read(t, f.request("GET", "/assets/app.js", "", "", map[string]string{"Range": "bytes=0-6"}), 206)
	if string(body) != "console" {
		t.Fatalf("asset range request failed: %s", body)
	}
	read(t, f.request("GET", "/assets/missing.js", "", "", nil), 404)
	read(t, f.request("POST", "/usage", "", "", nil), 404)
}

func TestAdminJSONValidationBeforeMutation(t *testing.T) {
	f := setup(t)
	initial := len(f.store.Snapshot().Users)
	for _, body := range []string{
		`{"name":"bad","unknown":true}`,
		`{"name":"first"} {"name":"second"}`,
		`{"name":"` + strings.Repeat("x", 1<<20) + `"}`,
	} {
		result := read(t, f.request("POST", "/api/admin/users", adminTestToken, body, nil), 400)
		if !bytes.Contains(result, []byte(`"code":"invalid_body"`)) {
			t.Fatalf("unexpected validation response: %s", result)
		}
	}
	if len(f.store.Snapshot().Users) != initial {
		t.Fatal("invalid request mutated users")
	}
}

func TestRecoveryPreservesErrorsAndAbortsStartedStreams(t *testing.T) {
	f := setup(t)
	s := f.server.Config.Handler.(*Server)
	s.router.GET("/panic-before-write", func(c *gin.Context) {
		panic("sensitive-request-content")
	})
	s.router.GET("/panic-after-flush", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = io.WriteString(c.Writer, ": connected\n\n")
		c.Writer.Flush()
		panic("sensitive-request-content")
	})
	body := read(t, f.request("GET", "/panic-before-write", "", "", nil), 500)
	if !bytes.Contains(body, []byte(`"code":"internal_error"`)) || bytes.Contains(body, []byte("sensitive")) {
		t.Fatalf("unsafe panic response: %s", body)
	}
	resp := f.request("GET", "/panic-after-flush", "", "", nil)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != ": connected\n\n" || err == nil {
		t.Fatalf("stream panic did not abort transport: status=%d body=%s err=%v", resp.StatusCode, body, err)
	}
}

type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
	flushErr error
}

func (w *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func (w *deadlineWriter) FlushError() error {
	w.ResponseRecorder.Flush()
	return w.flushErr
}

func TestStreamingFlushFailureIsRecordedAsDisconnect(t *testing.T) {
	f := setup(t)
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	f.add("stream", u.URL+"/v1", "secret", 1, nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+f.token)
	w := &deadlineWriter{
		ResponseRecorder: httptest.NewRecorder(),
		flushErr:         errors.New("client connection closed"),
	}
	f.server.Config.Handler.ServeHTTP(w, req)
	logs := f.proxy.Logs()
	if len(logs) != 1 || logs[0].Status != 499 || logs[0].Error != "client_disconnected" {
		t.Fatalf("flush failure was hidden: %+v", logs)
	}
}

func TestGinWriterPreservesStreamWriteDeadlines(t *testing.T) {
	f := setup(t)
	s := f.server.Config.Handler.(*Server)
	deadline := time.Now().Add(time.Second)
	s.router.GET("/write-deadline", func(c *gin.Context) {
		controller := http.NewResponseController(c.Writer)
		if err := controller.SetWriteDeadline(deadline); err != nil {
			t.Fatalf("Gin writer hides stream write deadlines: %v", err)
		}
		_, _ = io.WriteString(c.Writer, ": connected\n\n")
		if err := controller.Flush(); err != nil {
			t.Fatal(err)
		}
	})
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	s.ServeHTTP(w, httptest.NewRequest("GET", "/write-deadline", nil))
	if !w.deadline.Equal(deadline) || !w.Flushed || w.Body.String() != ": connected\n\n" {
		t.Fatalf("stream writer lost deadline, flush, or bytes: %+v", w)
	}
}
