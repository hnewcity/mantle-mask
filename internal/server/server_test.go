package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
)

const adminTestToken = "test-admin-token-minimum-24-characters"

type fixture struct {
	t      *testing.T
	dir    string
	store  *config.Store
	proxy  *gateway.Gateway
	server *httptest.Server
	token  string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir()}
	var err error
	f.store, err = config.Open(f.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	_, f.token, err = f.store.CreateKey("test-client")
	if err != nil {
		t.Fatal(err)
	}
	f.restart()
	return f
}

func (f *fixture) restart() {
	f.t.Helper()
	if f.server != nil {
		f.server.Close()
		f.proxy.Close()
	}
	store, err := config.Open(f.dir, "")
	if err != nil {
		f.t.Fatal(err)
	}
	f.store = store
	f.proxy, err = gateway.New(f.store, f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	f.server = httptest.NewServer(New(f.store, f.proxy, adminTestToken, "https://allowed.example"))
	proxy := f.proxy
	f.t.Cleanup(func() { proxy.Close() })
	f.t.Cleanup(f.server.Close)
}

func (f *fixture) add(name, url, secret string, weight int, models map[string]string) string {
	f.t.Helper()
	id, err := f.store.SaveUpstream("", config.UpstreamInput{
		Upstream:    config.Upstream{Name: name, BaseURL: url, Region: "us-east-1", Weight: weight, Enabled: true, AuthMode: "api_key", Models: models},
		Credentials: config.Credentials{APIKey: secret},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) request(method, path, token, body string, headers map[string]string) *http.Response {
	f.t.Helper()
	req, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	return resp
}

func read(t *testing.T, resp *http.Response, status int) []byte {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		t.Fatalf("want %d got %d: %s", status, resp.StatusCode, data)
	}
	return data
}

func upstream(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestAuthorizationIsolationAndRedaction(t *testing.T) {
	f := setup(t)
	f.add("one", "http://127.0.0.1:10001/v1", "dont-leak-this-secret", 1, nil)
	read(t, f.request("GET", "/api/admin/upstreams", f.token, "", nil), 401)
	read(t, f.request("GET", "/v1/models", adminTestToken, "", nil), 401)
	read(t, f.request("POST", "/v1/chat/completions", "", `{}`, nil), 401)
	body := read(t, f.request("GET", "/api/admin/upstreams", adminTestToken, "", nil), 200)
	if bytes.Contains(body, []byte("dont-leak")) || bytes.Contains(body, []byte("encrypted_credentials")) {
		t.Fatal("admin API leaked credentials")
	}
	read(t, f.request("GET", "/healthz", "", "", nil), 200)
	resp := f.request("OPTIONS", "/v1/models", "", "", map[string]string{"Origin": "https://evil.example"})
	read(t, resp, 403)
	resp = f.request("OPTIONS", "/v1/models", "", "", map[string]string{"Origin": "https://allowed.example"})
	read(t, resp, 204)
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://allowed.example" {
		t.Fatal("missing exact CORS origin")
	}
}

func TestWeightedRoutingAndUnknownFields(t *testing.T) {
	f := setup(t)
	var counts [2]atomic.Int64
	for i, weight := range []int{1, 3} {
		i := i
		u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
			counts[i].Add(1)
			if r.URL.Path != "/openai/v1/chat/completions" {
				t.Errorf("wrong path: %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer aws-secret" {
				t.Error("client credential forwarded")
			}
			if r.Header.Get("Cookie") != "" {
				t.Error("cookie forwarded")
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if string(body["model"]) != `"real-model"` || string(body["large_integer"]) != "9007199254740993" {
				t.Errorf("request was changed: %s", body)
			}
			if !bytes.Contains(body["tools"], []byte("test_tool")) {
				t.Error("tools dropped")
			}
			gateway.JSON(w, 200, map[string]any{"id": "chatcmpl-test", "object": "chat.completion", "choices": []any{}, "usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 5}})
		})
		f.add("test", u.URL+"/openai/v1", "aws-secret", weight, map[string]string{"alias": "real-model"})
	}
	for range 8 {
		read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"alias","messages":[],"tools":[{"type":"function","function":{"name":"test_tool"}}],"large_integer":9007199254740993}`, map[string]string{"Cookie": "private=secret"}), 200)
	}
	if counts[0].Load() != 2 || counts[1].Load() != 6 {
		t.Fatalf("wrong weighted distribution: %d:%d", counts[0].Load(), counts[1].Load())
	}
	m := f.proxy.Metrics()
	if m.Requests != 8 || m.Tokens != 64 {
		t.Fatalf("invalid metrics %+v", m)
	}
}

func TestFailoverAndModelRemapping(t *testing.T) {
	f := setup(t)
	var first, second atomic.Int64
	bad := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		first.Add(1)
		w.Header().Set("Retry-After", "7")
		gateway.Error(w, 503, "overloaded", "try later")
	})
	good := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		second.Add(1)
		b, _ := io.ReadAll(r.Body)
		if !bytes.Contains(b, []byte(`"model":"second-model"`)) {
			t.Errorf("wrong mapped model %s", b)
		}
		gateway.JSON(w, 200, map[string]string{"object": "chat.completion"})
	})
	f.add("bad", bad.URL+"/v1", "bad-secret", 100, map[string]string{"alias": "first-model"})
	goodID := f.add("good", good.URL+"/v1", "good-secret", 1, map[string]string{"alias": "second-model"})
	for range 2 {
		read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"alias","messages":[]}`, nil), 200)
	}
	if first.Load() != 1 || second.Load() != 2 {
		t.Fatalf("cooldown/failover failed: %d %d", first.Load(), second.Load())
	}
	if f.proxy.Logs()[1].Attempts != 2 {
		t.Fatal("attempts not counted")
	}
	report := usageReport(t, f, "/api/admin/usage?all=true", adminTestToken)
	if report.Totals.Requests != 2 || len(report.ByUpstream) != 1 || report.ByUpstream[0].ID != goodID || report.ByUpstream[0].Requests != 2 {
		t.Fatalf("failover duplicated requests or attributed them to the failed upstream: %+v", report)
	}
}

func TestStreamingPassthroughFlushAndToolCalls(t *testing.T) {
	f := setup(t)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	first := ": heartbeat\r\n\r\ndata: {\"id\":\"chatcmpl-x\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"x\\\":\"}}]}}]}\r\n\r\n"
	last := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, last)
	})
	f.add("stream", u.URL+"/v1", "secret", 1, nil)
	resp := f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[],"stream":true}`, nil)
	defer resp.Body.Close()
	prefix := make([]byte, len(first))
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(resp.Body, prefix); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream was buffered")
	}
	if string(prefix) != first {
		t.Fatal("tool-call SSE bytes modified")
	}
	once.Do(func() { close(release) })
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != last {
		t.Fatalf("stream tail changed: %s %v", rest, err)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("missing stream buffering override")
	}
}

func TestResponsesAffinitySurvivesRestartAndIsScoped(t *testing.T) {
	f := setup(t)
	var a, b atomic.Int64
	aUp := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		a.Add(1)
		if r.Header.Get("OpenAI-Project") != "proj_test" {
			t.Error("project lost")
		}
		id := "resp_first"
		if r.Method == "POST" {
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte("previous_response_id")) {
				id = "resp_second"
			}
		}
		gateway.JSON(w, 200, map[string]any{"id": id, "object": "response", "output": []any{}})
	})
	bUp := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		b.Add(1)
		gateway.JSON(w, 200, map[string]string{"id": "resp_wrong", "object": "response"})
	})
	f.add("a", aUp.URL+"/v1", "a-secret", 1, nil)
	f.add("b", bUp.URL+"/v1", "b-secret", 1, nil)
	headers := map[string]string{"OpenAI-Project": "proj_test"}
	read(t, f.request("POST", "/v1/responses", f.token, `{"model":"m","input":"hello"}`, headers), 200)
	f.restart()
	read(t, f.request("GET", "/v1/responses/resp_first", f.token, "", headers), 200)
	read(t, f.request("POST", "/v1/responses", f.token, `{"previous_response_id":"resp_first","input":"next"}`, headers), 200)
	_, other, err := f.store.CreateKey("another")
	if err != nil {
		t.Fatal(err)
	}
	read(t, f.request("GET", "/v1/responses/resp_first", other, "", headers), 404)
	read(t, f.request("GET", "/v1/responses/resp_first", f.token, "", nil), 404)
	read(t, f.request("GET", "/v1/responses/unknown", f.token, "", headers), 404)
	if a.Load() != 3 || b.Load() != 0 {
		t.Fatalf("state crossed upstreams: %d %d", a.Load(), b.Load())
	}
}

func TestStreamingResponsesAffinity(t *testing.T) {
	f := setup(t)
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			gateway.JSON(w, 200, map[string]string{"id": "resp_stream", "object": "response"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\r\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stream\",\"object\":\"response\"}}\r\n\r\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream\",\"usage\":{\"input_tokens\":6,\"output_tokens\":7}}}\n\n")
	})
	f.add("stream", u.URL+"/v1", "secret", 1, nil)
	read(t, f.request("POST", "/v1/responses", f.token, `{"model":"m","input":"hello","stream":true}`, nil), 200)
	f.restart()
	read(t, f.request("GET", "/v1/responses/resp_stream/input_items?limit=10", f.token, "", nil), 200)
}

func TestNoFailoverForValidationOrPinnedRequest(t *testing.T) {
	f := setup(t)
	var status atomic.Int64
	status.Store(200)
	var other atomic.Int64
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if status.Load() != 200 {
			gateway.Error(w, int(status.Load()), "bad", "bad")
			return
		}
		gateway.JSON(w, 200, map[string]string{"id": "resp_pinned", "object": "response"})
	})
	v := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		other.Add(1)
		gateway.JSON(w, 200, map[string]string{"id": "wrong", "object": "response"})
	})
	f.add("one", u.URL+"/v1", "secret", 100, nil)
	f.add("two", v.URL+"/v1", "secret", 1, nil)
	read(t, f.request("POST", "/v1/responses", f.token, `{"model":"m","input":"a"}`, nil), 200)
	status.Store(503)
	read(t, f.request("GET", "/v1/responses/resp_pinned", f.token, "", nil), 503)
	if other.Load() != 0 {
		t.Fatal("pinned request failed over")
	}
	// A fresh fixture avoids the cooldown from the previous failure.
	f2 := setup(t)
	status.Store(400)
	f2.add("one", u.URL+"/v1", "secret", 100, nil)
	f2.add("two", v.URL+"/v1", "secret", 1, nil)
	read(t, f2.request("POST", "/v1/chat/completions", f2.token, `{"model":"m","messages":[]}`, nil), 400)
	if other.Load() != 0 {
		t.Fatal("validation error was retried")
	}
}

func TestModelsAggregationAndPartialFailure(t *testing.T) {
	f := setup(t)
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gateway.JSON(w, 200, map[string]any{"object": "list", "data": []map[string]string{{"id": "native-model", "object": "model"}}})
	})
	v := upstream(t, func(w http.ResponseWriter, r *http.Request) { gateway.Error(w, 401, "bad_key", "not authorized") })
	f.add("one", u.URL+"/v1", "secret", 1, nil)
	f.add("two", u.URL+"/v1", "secret", 1, map[string]string{"public-model": "actual-model"})
	f.add("bad", v.URL+"/v1", "secret", 1, nil)
	resp := f.request("GET", "/v1/models", f.token, "", nil)
	b := read(t, resp, 200)
	if !bytes.Contains(b, []byte("native-model")) || !bytes.Contains(b, []byte("public-model")) || bytes.Contains(b, []byte("actual-model")) {
		t.Fatalf("bad model list %s", b)
	}
	if resp.Header.Get("X-Mantle-Partial") != "true" {
		t.Fatal("partial discovery not disclosed")
	}
}

func TestSigV4SigningAndRedirectRefusal(t *testing.T) {
	f := setup(t)
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.Contains(auth, "AWS4-HMAC-SHA256") || !strings.Contains(auth, "/us-east-1/bedrock-mantle/aws4_request") || !strings.Contains(auth, "Credential=AKIDEXAMPLE/") {
			t.Errorf("bad AWS signature scope: %s", auth)
		}
		if r.Header.Get("X-Amz-Security-Token") != "temporary-session" {
			t.Error("session token absent")
		}
		gateway.JSON(w, 200, map[string]string{"object": "chat.completion"})
	})
	_, err := f.store.SaveUpstream("", config.UpstreamInput{Upstream: config.Upstream{Name: "sigv4", BaseURL: u.URL + "/v1", AuthMode: "sigv4", Region: "us-east-1", Weight: 1, Enabled: true}, Credentials: config.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret-example", SessionToken: "temporary-session"}})
	if err != nil {
		t.Fatal(err)
	}
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[]}`, nil), 200)
	f2 := setup(t)
	var leaked atomic.Bool
	destination := upstream(t, func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) })
	redirect := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	})
	f2.add("redirect", redirect.URL+"/v1", "do-not-forward", 1, nil)
	read(t, f2.request("POST", "/v1/chat/completions", f2.token, `{"model":"m","messages":[]}`, nil), 502)
	if leaked.Load() {
		t.Fatal("followed credential-bearing redirect")
	}
}

func TestInvalidRequestsHaveOpenAIErrorShape(t *testing.T) {
	f := setup(t)
	for _, body := range []string{`{`, `[]`, `null`, `{} {}`, `{"model":12}`, `{"model":"m","stream":"true"}`, `{"model":"m","conversation":"conv_123"}`} {
		result := read(t, f.request("POST", "/v1/chat/completions", f.token, body, nil), 400)
		var envelope map[string]any
		if json.Unmarshal(result, &envelope) != nil || envelope["error"] == nil {
			t.Fatalf("invalid error response %s", result)
		}
	}
	read(t, f.request("POST", "/v1/embeddings", f.token, `{}`, nil), 404)
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[]}`, nil), 503)
}

func TestInterruptedStreamReturnsErrorWithoutFailover(t *testing.T) {
	f := setup(t)
	var second atomic.Int64
	first := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		// EOF without [DONE] must not be reported as successful completion.
	})
	other := upstream(t, func(w http.ResponseWriter, r *http.Request) { second.Add(1) })
	f.add("first", first.URL+"/v1", "secret", 100, nil)
	f.add("second", other.URL+"/v1", "secret", 1, nil)
	body := read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[],"stream":true}`, nil), 200)
	if !bytes.Contains(body, []byte(`"code":"stream_interrupted"`)) {
		t.Fatalf("missing SSE error: %s", body)
	}
	if second.Load() != 0 {
		t.Fatal("retried an already-started stream")
	}
	if f.proxy.Logs()[0].Status != 502 {
		t.Fatal("interrupted stream recorded as success")
	}
}

func TestClientCancellationReachesUpstream(t *testing.T) {
	f := setup(t)
	canceled := make(chan struct{})
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": connected\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	})
	f.add("cancel", u.URL+"/v1", "secret", 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(": connected\n\n"))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream was not canceled")
	}
}

func TestMantleChatTerminalChoiceWithoutDone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		n        int
		terminal string
		success  bool
	}{
		{"single stop", 1, `{"index":0,"delta":{},"finish_reason":"stop"}`, true},
		{"tool calls", 1, `{"index":0,"delta":{},"finish_reason":"tool_calls"}`, true},
		{"missing second choice", 2, `{"index":0,"delta":{},"finish_reason":"stop"}`, false},
		{"all choices", 2, `{"index":0,"delta":{},"finish_reason":"stop"},{"index":1,"delta":{},"finish_reason":"length"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			raw := "data: {\"object\":\"chat.completion.chunk\",\"choices\":[" + tc.terminal + "]}\n\n"
			u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, raw)
			})
			f.add("mantle", u.URL+"/v1", "secret", 1, nil)
			request, _ := json.Marshal(map[string]any{"model": "m", "messages": []any{}, "stream": true, "n": tc.n})
			body := read(t, f.request("POST", "/v1/chat/completions", f.token, string(request), nil), 200)
			if !bytes.HasPrefix(body, []byte(raw)) {
				t.Fatal("original SSE bytes changed")
			}
			if tc.success {
				if string(body) != raw+"data: [DONE]\n\n" {
					t.Fatalf("missing normalized sentinel: %s", body)
				}
			} else {
				if bytes.Contains(body, []byte("[DONE]")) || !bytes.Contains(body, []byte("stream_interrupted")) {
					t.Fatalf("incomplete choices reported as complete: %s", body)
				}
			}
		})
	}
}
