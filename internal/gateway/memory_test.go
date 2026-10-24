package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

func BenchmarkAffinityPut(b *testing.B) {
	for _, size := range []int{1000, 20000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s, err := openAffinity(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			binding := Binding{UpstreamID: "up_benchmark", Fingerprint: config.Hash("fingerprint"), Model: "model", Expires: time.Now().Add(time.Hour)}
			// Seed in memory: going through put would itself be O(n²) to set up.
			for i := range size {
				s.bindings[config.Hash(fmt.Sprint("seed", i))] = binding
			}
			if err := config.AtomicJSON(s.path, s.bindings); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			i := 0
			for b.Loop() {
				i++
				if err := s.put(config.Hash(fmt.Sprint("new", i)), binding); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkProxyLargeStreamingBody measures the gateway-side copies of a large
// agent-style chat request, including stream_options insertion and mapping.
func BenchmarkProxyLargeStreamingBody(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	dir := b.TempDir()
	store, err := config.Open(dir, "")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := store.SaveUpstream("", config.UpstreamInput{
		Upstream:    config.Upstream{Name: "bench", BaseURL: upstream.URL, Weight: 1, Enabled: true, AuthMode: "api_key", Models: map[string]string{"alias": "actual-model"}},
		Credentials: config.Credentials{APIKey: "secret"},
	}); err != nil {
		b.Fatal(err)
	}
	g, err := New(store, dir, usage.Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer g.Close()
	content := bytes.Repeat([]byte("lorem ipsum "), (1<<20)/12)
	body := []byte(`{"model":"alias","stream":true,"messages":[{"role":"user","content":"` + string(content) + `"}]}`)
	st := store.RoutingSnapshot()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		g.ProxyWithState(rec, req, Principal{ID: "key", UserID: "user"}, st)
		if rec.Code != 200 {
			b.Fatal(rec.Code, rec.Body.String())
		}
	}
}
