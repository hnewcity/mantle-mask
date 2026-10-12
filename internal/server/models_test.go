package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
)

func TestModelDiscoveryUsesV1WithoutChangingInferenceBaseURL(t *testing.T) {
	for _, basePath := range []string{"/v1", "/openai/v1", "/openai/v1/"} {
		for _, authMode := range []string{"api_key", "sigv4"} {
			t.Run(basePath+"/"+authMode, func(t *testing.T) {
				f := setup(t)
				inferencePath := strings.TrimRight(basePath, "/")
				var modelRequests atomic.Int64
				credentials := config.Credentials{
					APIKey: "model-discovery-secret", AccessKeyID: "AKIDMODELS",
					SecretAccessKey: "model-signing-secret", SessionToken: "model-session-token",
				}
				u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("OpenAI-Project") != "proj_models" {
						t.Error("project header was lost")
					}
					if authMode == "api_key" {
						if r.Header.Get("Authorization") != "Bearer "+credentials.APIKey {
							t.Error("upstream bearer key was lost")
						}
					} else {
						// Re-sign the received URL to catch normalization after signing.
						payload, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						signedAt, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
						if err != nil {
							t.Error(err)
						}
						signed := r.Clone(r.Context())
						signed.URL.Scheme, signed.URL.Host = "http", r.Host
						signed.Header.Del("Authorization")
						hash := sha256.Sum256(payload)
						err = v4.NewSigner().SignHTTP(r.Context(), aws.Credentials{
							AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey,
							SessionToken: credentials.SessionToken,
						}, signed, hex.EncodeToString(hash[:]), "bedrock-mantle", "us-east-1", signedAt)
						if err != nil {
							t.Error(err)
						}
						if signed.Header.Get("Authorization") != r.Header.Get("Authorization") {
							t.Error("signature does not match the actual upstream request path")
						}
						if r.Header.Get("X-Amz-Security-Token") != credentials.SessionToken {
							t.Error("session token was lost")
						}
					}
					switch {
					case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
						modelRequests.Add(1)
						gateway.JSON(w, 200, map[string]any{"data": []map[string]string{{"id": "model-from-catalog"}}})
					case r.Method == http.MethodPost && r.URL.Path == inferencePath+"/chat/completions":
						gateway.JSON(w, 200, map[string]any{"object": "chat.completion", "choices": []any{}})
					case r.Method == http.MethodPost && r.URL.Path == inferencePath+"/responses",
						r.Method == http.MethodGet && r.URL.Path == inferencePath+"/responses/resp_catalog":
						gateway.JSON(w, 200, map[string]string{"id": "resp_catalog", "object": "response"})
					default:
						t.Errorf("unexpected upstream route: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				})
				id, err := f.store.SaveUpstream("", config.UpstreamInput{
					Upstream: config.Upstream{
						Name: "catalog", BaseURL: u.URL + basePath, Region: "us-east-1",
						Weight: 1, Enabled: true, AuthMode: authMode, Project: "proj_models",
					},
					Credentials: credentials,
				})
				if err != nil {
					t.Fatal(err)
				}
				// Client discovery uses the canonical catalog URL and retains its cache.
				for range 2 {
					body := read(t, f.request("GET", "/v1/models", f.token, "", nil), 200)
					if !bytes.Contains(body, []byte("model-from-catalog")) {
						t.Fatalf("client discovery lost models: %s", body)
					}
				}
				if modelRequests.Load() != 1 {
					t.Fatal("model cache was bypassed")
				}
				body := read(t, f.request("POST", "/api/admin/upstreams/"+id+"/test", adminTestToken, "", nil), 200)
				var result struct {
					Models []gateway.Model `json:"models"`
				}
				if json.Unmarshal(body, &result) != nil || len(result.Models) != 1 || result.Models[0].ID != "model-from-catalog" {
					t.Fatalf("connection test lost models: %s", body)
				}
				if modelRequests.Load() != 2 {
					t.Fatal("connection test did not refresh the model list")
				}
				read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"model-from-catalog","messages":[]}`, nil), 200)
				read(t, f.request("POST", "/v1/responses", f.token, `{"model":"model-from-catalog","input":"hello"}`, nil), 200)
				read(t, f.request("GET", "/v1/responses/resp_catalog", f.token, "", nil), 200)
				if got := f.store.Snapshot().Upstreams[0].BaseURL; got != u.URL+inferencePath {
					t.Fatalf("configured inference base URL changed: %s", got)
				}
			})
		}
	}
}
