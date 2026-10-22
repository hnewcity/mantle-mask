package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
)

func TestUpstreamAPIKeyRotationIsLive(t *testing.T) {
	f := setup(t)
	var rotated atomic.Bool
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		key, model := "old-key", "old-model"
		if rotated.Load() {
			key, model = "new-key", "new-model"
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("upstream received stale or client credentials")
		}
		switch r.URL.Path {
		case "/v1/models":
			gateway.JSON(w, 200, map[string]any{"data": []map[string]string{{"id": model, "object": "model"}}})
		case "/v1/chat/completions":
			if !rotated.Load() {
				gateway.Error(w, 429, "rate_limit", "try later")
				return
			}
			gateway.JSON(w, 200, map[string]any{"id": "chat-rotated", "choices": []any{}})
		default:
			gateway.JSON(w, 200, map[string]any{"id": "resp_rotated", "object": "response", "output": []any{}})
		}
	})
	id := f.add("temporary", u.URL+"/v1", "old-key", 1, nil)
	read(t, f.request("GET", "/v1/models", f.token, "", nil), 200)
	read(t, f.request("POST", "/v1/responses", f.token, `{"model":"m","input":"hello"}`, nil), 200)
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[]}`, nil), 429)
	resp := f.request("PUT", "/api/admin/upstreams/"+id+"/api-key", adminTestToken, `{"api_key":"new-key"}`, nil)
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("rotation response can be cached")
	}
	body := read(t, resp, 200)
	var result struct {
		ID      string `json:"id"`
		Updated bool   `json:"updated"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.ID != id || !result.Updated {
		t.Fatalf("unexpected rotation result: %s", body)
	}
	if bytes.Contains(body, []byte("new-key")) || bytes.Contains(body, []byte("encrypted_credentials")) {
		t.Fatal("rotation response leaked credentials")
	}
	rotated.Store(true)
	// The next inference must bypass the old credential's cooldown immediately.
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[]}`, nil), 200)
	models := read(t, f.request("GET", "/v1/models", f.token, "", nil), 200)
	if !bytes.Contains(models, []byte("new-model")) || bytes.Contains(models, []byte("old-model")) {
		t.Fatal("rotation reused the old model cache")
	}
	// Credential rotation preserves response affinity, including across restarts.
	read(t, f.request("GET", "/v1/responses/resp_rotated", f.token, "", nil), 200)
	f.restart()
	read(t, f.request("GET", "/v1/responses/resp_rotated", f.token, "", nil), 200)
}

func TestUpstreamAPIKeyRotationRejectsInvalidRequests(t *testing.T) {
	f := setup(t)
	id := f.add("temporary", "https://example.test/v1", "old-key", 1, nil)
	sigID, err := f.store.SaveUpstream("", config.UpstreamInput{Upstream: config.Upstream{
		Name: "role", BaseURL: "https://example.test/v1", AuthMode: "sigv4", Region: "us-east-1", Weight: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.Snapshot()
	for _, tc := range []struct {
		name, id, token, body string
		status                int
		code                  string
	}{
		{"unauthenticated", id, "", `{"api_key":"new-key"}`, 401, "invalid_admin_token"},
		{"user key", id, f.token, `{"api_key":"new-key"}`, 401, "invalid_admin_token"},
		{"wrong token", id, "wrong-token", `{"api_key":"new-key"}`, 401, "invalid_admin_token"},
		{"unknown upstream", "missing", adminTestToken, `{"api_key":"new-key"}`, 404, "upstream_not_found"},
		{"wrong auth mode", sigID, adminTestToken, `{"api_key":"new-key"}`, 400, "invalid_auth_mode"},
		{"missing key", id, adminTestToken, `{}`, 400, "invalid_api_key"},
		{"empty key", id, adminTestToken, `{"api_key":""}`, 400, "invalid_api_key"},
		{"blank key", id, adminTestToken, `{"api_key":" \t\n"}`, 400, "invalid_api_key"},
		{"null object", id, adminTestToken, `null`, 400, "invalid_api_key"},
		{"null key", id, adminTestToken, `{"api_key":null}`, 400, "invalid_api_key"},
		{"header injection", id, adminTestToken, `{"api_key":"new-key\r\nInjected: value"}`, 400, "invalid_api_key"},
		{"internal whitespace", id, adminTestToken, `{"api_key":"new key"}`, 400, "invalid_api_key"},
		{"control character", id, adminTestToken, `{"api_key":"new\u0000key"}`, 400, "invalid_api_key"},
		{"wrong type", id, adminTestToken, `{"api_key":123}`, 400, "invalid_body"},
		{"unknown field", id, adminTestToken, `{"api_key":"new-key","enabled":false}`, 400, "invalid_body"},
		{"malformed JSON", id, adminTestToken, `{"api_key":`, 400, "invalid_body"},
		{"multiple objects", id, adminTestToken, `{"api_key":"new-key"} {}`, 400, "invalid_body"},
		{"oversized", id, adminTestToken, `{"api_key":"` + strings.Repeat("x", 1<<20) + `"}`, 400, "invalid_body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := read(t, f.request("PUT", "/api/admin/upstreams/"+tc.id+"/api-key", tc.token, tc.body, nil), tc.status)
			var result struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &result); err != nil || result.Error.Code != tc.code {
				t.Fatalf("unexpected error: %s", body)
			}
			if !reflect.DeepEqual(f.store.Snapshot(), before) {
				t.Fatal("rejected rotation changed configuration")
			}
		})
	}
}

func TestUpstreamAPIKeyRotationKeepsActiveStream(t *testing.T) {
	f := setup(t)
	finish := make(chan struct{}, 1)
	t.Cleanup(func() { close(finish) })
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer old-key" {
			t.Error("active stream changed credentials")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			t.Error("rotation blocked on an active stream")
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	id := f.add("temporary", u.URL+"/v1", "old-key", 1, nil)
	resp := f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[],"stream":true}`, nil)
	defer resp.Body.Close()
	read(t, f.request("PUT", "/api/admin/upstreams/"+id+"/api-key", adminTestToken, `{"api_key":"new-key"}`, nil), 200)
	finish <- struct{}{}
	body := read(t, resp, 200)
	if !bytes.Contains(body, []byte("hello")) || !bytes.Contains(body, []byte("data: [DONE]")) {
		t.Fatalf("active stream did not finish: %s", body)
	}
}
