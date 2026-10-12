package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

func usageReport(t *testing.T, f *fixture, path, token string) usage.Report {
	t.Helper()
	var report usage.Report
	if err := json.Unmarshal(read(t, f.request("GET", path, token, "", nil), 200), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestUserAPIAndPrivateUsage(t *testing.T) {
	f := setup(t)
	var alice, bob config.User
	for _, entry := range []struct {
		name string
		dest *config.User
	}{{"Alice", &alice}, {"Bob", &bob}} {
		body := read(t, f.request("POST", "/api/admin/users", adminTestToken, fmt.Sprintf(`{"name":%q}`, entry.name), nil), 201)
		if err := json.Unmarshal(body, entry.dest); err != nil {
			t.Fatal(err)
		}
	}
	key, aliceToken, err := f.store.CreateKey("Alice app", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, aliceSecond, err := f.store.CreateKey("Alice second", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	bobKey, bobToken, err := f.store.CreateKey("Bob app", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"object":"chat.completion","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":7}}}`)
	})
	f.add("test", u.URL+"/v1", "secret", 1, nil)
	for _, token := range []string{aliceToken, aliceSecond, bobToken} {
		read(t, f.request("POST", "/v1/chat/completions", token, `{"model":"m","messages":[]}`, nil), 200)
	}
	report := usageReport(t, f, "/api/admin/usage?all=true", adminTestToken)
	if report.Totals.Requests != 3 || report.Totals.TotalTokens != 90 || len(report.ByUser) != 2 || len(report.ByKey) != 3 || len(report.ByUpstream) != 1 || report.ByUpstream[0].TotalTokens != 90 {
		t.Fatalf("wrong multi-user totals %+v", report)
	}
	private := usageReport(t, f, "/api/me/usage?all=true&upstream_id=not-an-upstream&user_id="+bob.ID, aliceToken)
	if private.Totals.Requests != 2 || private.Totals.TotalTokens != 60 || len(private.ByUser) != 1 || private.ByUser[0].ID != alice.ID || private.Totals.CachedTokens != 8 || private.Totals.ReasoningTokens != 14 {
		t.Fatalf("query escaped owner scope or lost usage details %+v", private)
	}
	if len(private.DailyByModel) != 1 || private.DailyByModel[0].Model != "m" || private.DailyByModel[0].TotalTokens != 60 {
		t.Fatalf("daily model usage escaped owner scope: %+v", private.DailyByModel)
	}
	if len(private.ByUpstream) != 0 {
		t.Fatal("private usage exposed upstream metadata")
	}
	empty := usageReport(t, f, "/api/me/usage?all=true&key_id="+bobKey.ID, aliceToken)
	if empty.Totals.Requests != 0 || len(empty.ByKey) != 0 || len(empty.DailyByModel) != 0 {
		t.Fatal("cross-owner key filter leaked data")
	}
	for _, path := range []string{"/api/admin/users", "/api/admin/usage", "/api/admin/keys", "/api/admin/upstreams"} {
		read(t, f.request("GET", path, aliceToken, "", nil), 401)
	}
	body := read(t, f.request("GET", "/api/me/keys", aliceToken, "", nil), 200)
	if bytes.Contains(body, []byte(bob.ID)) || bytes.Contains(body, []byte(`"hash"`)) || bytes.Contains(body, []byte(aliceToken)) {
		t.Fatal("private key list leaked metadata")
	}
	body = read(t, f.request("GET", "/api/session", aliceToken, "", nil), 200)
	if !bytes.Contains(body, []byte(`"role":"user"`)) {
		t.Fatal("user session not detected")
	}
	read(t, f.request("POST", "/api/me/keys", aliceToken, `{}`, nil), 405)
	read(t, f.request("GET", "/api/admin/usage?timezone_offset=no", adminTestToken, "", nil), 400)
	read(t, f.request("PUT", "/api/admin/users/"+alice.ID, adminTestToken, `{"name":"Alice","enabled":false}`, nil), 200)
	for _, token := range []string{aliceToken, aliceSecond} {
		read(t, f.request("GET", "/api/me/usage", token, "", nil), 401)
		read(t, f.request("POST", "/v1/chat/completions", token, `{"model":"m"}`, nil), 401)
	}
	read(t, f.request("PUT", "/api/admin/users/"+alice.ID, adminTestToken, `{"name":"Alice renamed","enabled":true}`, nil), 200)
	read(t, f.request("DELETE", "/api/admin/keys/"+key.ID, adminTestToken, "", nil), 200)
	f.restart()
	read(t, f.request("GET", "/api/me", aliceToken, "", nil), 401)
	private = usageReport(t, f, "/api/me/usage?all=true", aliceSecond)
	if private.Totals.Requests != 2 || private.ByUser[0].Name != "Alice renamed" || len(private.ByKey) != 2 {
		t.Fatal("restart/revocation lost usage")
	}
}

func TestResponseRetrievalReconcilesWithoutDoubleCounting(t *testing.T) {
	f := setup(t)
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/responses" {
			io.WriteString(w, `{"object":"response","id":"resp_background","status":"queued","usage":null}`)
			return
		}
		io.WriteString(w, `{"object":"response","id":"resp_background","status":"completed","usage":{"input_tokens":11,"output_tokens":13,"input_tokens_details":{"cached_tokens":5},"output_tokens_details":{"reasoning_tokens":9}}}`)
	})
	upstreamID := f.add("background", u.URL+"/v1", "secret", 1, nil)
	read(t, f.request("POST", "/v1/responses", f.token, `{"model":"m","input":"hello","background":true}`, nil), 200)
	pending := usageReport(t, f, "/api/me/usage?all=true", f.token)
	if pending.Totals.Requests != 1 || pending.Totals.MissingUsageRequests != 1 {
		t.Fatal("queued usage treated as known")
	}
	f.restart()
	for range 3 {
		read(t, f.request("GET", "/v1/responses/resp_background", f.token, "", nil), 200)
	}
	read(t, f.request("POST", "/v1/responses/resp_background/cancel", f.token, `{}`, nil), 200)
	report := usageReport(t, f, "/api/me/usage?all=true", f.token)
	if report.Totals.Requests != 1 || report.Totals.TotalTokens != 24 || report.Totals.MissingUsageRequests != 0 || report.Totals.CachedTokens != 5 || report.Totals.ReasoningTokens != 9 {
		t.Fatalf("response reconciliation incorrect %+v", report)
	}
	admin := usageReport(t, f, "/api/admin/usage?all=true&upstream_id="+upstreamID, adminTestToken)
	if len(admin.ByUpstream) != 1 || admin.ByUpstream[0].Totals != report.Totals {
		t.Fatalf("response reconciliation lost upstream attribution: %+v", admin)
	}
	if f.proxy.Metrics().Tokens != 0 {
		t.Fatal("retrieval inflated session token totals")
	}
}

func TestStreamingUsageInjectionAndUnknownVersusZero(t *testing.T) {
	f := setup(t)
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			StreamOptions map[string]json.RawMessage `json:"stream_options"`
			Metadata      map[string]string          `json:"metadata"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if string(body.StreamOptions["custom_option"]) != "9007199254740993" {
			t.Error("unknown option altered")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if string(body.StreamOptions["include_usage"]) == "true" {
			for range 2 {
				io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0},\"choices\":[]}\n\n")
			}
		} else if string(body.StreamOptions["include_usage"]) != "false" {
			t.Error("include_usage not inserted")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	})
	f.add("stream", u.URL+"/v1", "secret", 1, nil)
	for _, option := range []string{`"custom_option":9007199254740993`, `"custom_option":9007199254740993,"include_usage":false`} {
		read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[],"stream":true,"stream_options":{`+option+`}}`, nil), 200)
	}
	report := usageReport(t, f, "/api/me/usage?all=true", f.token)
	if report.Totals.Requests != 2 || report.Totals.KnownUsageRequests != 1 || report.Totals.MissingUsageRequests != 1 || report.Totals.TotalTokens != 0 {
		t.Fatalf("zero and missing usage conflated %+v", report.Totals)
	}
	for _, body := range []string{`{"model":"m","stream":true,"stream_options":[]}`, `{"model":"m","stream":true,"stream_options":42}`} {
		read(t, f.request("POST", "/v1/chat/completions", f.token, body, nil), 400)
	}
}

func TestUsageStorageFailureStopsNewInference(t *testing.T) {
	f := setup(t)
	var contacted bool
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		gateway.JSON(w, 200, map[string]string{})
	})
	f.add("unused", u.URL+"/v1", "secret", 1, nil)
	f.proxy.Close()
	body := read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"m","messages":[]}`, nil), 503)
	if contacted || !strings.Contains(string(body), "usage_storage_unavailable") {
		t.Fatal("unaccounted inference was sent")
	}
}

func TestAdminUpstreamUsageFilteringAndDeletedHistory(t *testing.T) {
	f := setup(t)
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model == "failed" {
			gateway.Error(w, 400, "bad_request", "invalid fixture request")
			return
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":13,\"completion_tokens\":17}}\n\ndata: [DONE]\n\n")
			return
		}
		io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11}}`)
	})
	a := f.add("Upstream A", u.URL+"/v1", "secret", 1, map[string]string{"model-a": "model-a"})
	b := f.add("Upstream B", u.URL+"/v1", "secret", 1, map[string]string{"model-b": "model-b", "failed": "failed"})
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"model-a","messages":[]}`, nil), 200)
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"model-b","messages":[],"stream":true}`, nil), 200)
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"failed","messages":[]}`, nil), 400)
	report := usageReport(t, f, "/api/admin/usage?all=true", adminTestToken)
	if report.Totals.Requests != 3 || report.Totals.TotalTokens != 48 || len(report.ByUpstream) != 2 {
		t.Fatalf("bad upstream totals: %+v", report)
	}
	filtered := usageReport(t, f, "/api/admin/usage?all=true&upstream_id="+b, adminTestToken)
	if filtered.Totals.Requests != 2 || filtered.Totals.Errors != 1 || filtered.Totals.TotalTokens != 30 || filtered.Totals.MissingUsageRequests != 1 || len(filtered.ByUpstream) != 1 || filtered.ByUpstream[0].Name != "Upstream B" || len(filtered.ByModel) != 2 {
		t.Fatalf("bad upstream filter: %+v", filtered)
	}
	filtered = usageReport(t, f, "/api/admin/usage?all=true&upstream_id="+b+"&model=model-b", adminTestToken)
	if filtered.Totals.Requests != 1 || filtered.Totals.TotalTokens != 30 || len(filtered.ByModel) != 1 {
		t.Fatalf("bad combined filter: %+v", filtered)
	}
	for _, upstream := range f.store.Snapshot().Upstreams {
		if upstream.ID == a {
			upstream.Name, upstream.Enabled = "Renamed A", false
			if _, err := f.store.SaveUpstream(a, config.UpstreamInput{Upstream: upstream}); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.restart()
	filtered = usageReport(t, f, "/api/admin/usage?all=true&upstream_id="+a, adminTestToken)
	if len(filtered.ByUpstream) != 1 || filtered.ByUpstream[0].Name != "Renamed A" || filtered.Totals.TotalTokens != 18 {
		t.Fatalf("rename/disable lost history: %+v", filtered)
	}
	read(t, f.request("DELETE", "/api/admin/upstreams/"+a, adminTestToken, "", nil), 200)
	f.restart()
	filtered = usageReport(t, f, "/api/admin/usage?all=true&upstream_id="+a, adminTestToken)
	if len(filtered.ByUpstream) != 1 || filtered.ByUpstream[0].Name != "Upstream A" || filtered.Totals.Requests != 1 || filtered.Totals.TotalTokens != 18 {
		t.Fatalf("deleted upstream lost its recorded name or usage: %+v", filtered)
	}
	private := read(t, f.request("GET", "/api/me/usage?all=true&upstream_id="+b, f.token, "", nil), 200)
	if bytes.Contains(private, []byte("by_upstream")) || bytes.Contains(private, []byte(b)) || bytes.Contains(private, []byte("Upstream B")) {
		t.Fatalf("private report exposed upstream metadata: %s", private)
	}
}
