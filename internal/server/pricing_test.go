package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

func TestModelPricingAdminConfigurationAndPrivateEstimates(t *testing.T) {
	f := setup(t)
	read(t, f.request("GET", "/api/admin/usage/pricing", f.token, "", nil), 401)
	read(t, f.request("PUT", "/api/admin/usage/pricing", f.token, `{}`, nil), 401)
	for _, invalid := range []string{`null`, `{"m":{}}`, `{"m":{"input_usd_per_million":0}}`, `{"m":{"input_usd_per_million":null,"output_usd_per_million":1}}`, `{"":{"input_usd_per_million":1}}`, `{"m":{"input_usd_per_million":-1}}`, `{"m":{"output_usd_per_million":1e100}}`, `{"m":{"cached_input_usd_per_million":-1}}`} {
		read(t, f.request("PUT", "/api/admin/usage/pricing", adminTestToken, invalid, nil), 400)
	}
	u := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"object":"chat.completion","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":7}}}`)
	})
	upstreamID := f.add("test", u.URL+"/v1", "secret", 1, map[string]string{"alias": "actual-model"})
	for range 2 {
		read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"alias","messages":[]}`, nil), 200)
	}
	read(t, f.request("PUT", "/api/admin/usage/pricing", adminTestToken, `{"alias":{"input_usd_per_million":1,"output_usd_per_million":2,"cached_input_usd_per_million":0.25}}`, nil), 200)
	// Another user's estimate must never leak into the authenticated user's cost.
	other, err := f.store.SaveUser("", config.UserInput{Name: "Other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := f.store.CreateKey("Other", other.ID)
	if err != nil {
		t.Fatal(err)
	}
	read(t, f.request("POST", "/v1/chat/completions", otherToken, `{"model":"alias","messages":[]}`, nil), 200)
	private := usageReport(t, f, "/api/me/usage?all=true&user_id="+other.ID, f.token)
	if math.Abs(private.Cost.EstimatedUSD-.000094) > 1e-12 || private.Cost.PricedRequests != 2 || len(private.ByUpstream) != 0 {
		t.Fatalf("incorrect private cost %+v", private)
	}
	report := usageReport(t, f, "/api/admin/usage?all=true&upstream_id="+upstreamID, adminTestToken)
	if math.Abs(report.Cost.EstimatedUSD-.000141) > 1e-12 || report.Cost.PricedRequests != 3 {
		t.Fatalf("incorrect admin cost %+v", report.Cost)
	}
	f.restart()
	var prices map[string]config.ModelPrice
	if err := json.Unmarshal(read(t, f.request("GET", "/api/admin/usage/pricing", adminTestToken, "", nil), 200), &prices); err != nil {
		t.Fatal(err)
	}
	if prices["alias"].OutputUSDPerMillion != 2 {
		t.Fatal("lost prices across restart")
	}
	read(t, f.request("PUT", "/api/admin/usage/pricing", adminTestToken, `{}`, nil), 200)
	report = usageReport(t, f, "/api/admin/usage?all=true", adminTestToken)
	if report.Cost.PricedRequests != 0 || report.Cost.UnpricedRequests != 3 {
		t.Fatal("cleared price treated as free")
	}
}

func TestReferencePriceSyncAdminScopeOverridesCacheAndFailure(t *testing.T) {
	f := setup(t)
	upstream := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":4}}}`)
	})
	f.add("reference", upstream.URL+"/v1", "test-only", 1, map[string]string{"alias": "actual"})
	read(t, f.request("POST", "/v1/chat/completions", f.token, `{"model":"alias","messages":[]}`, nil), 200)
	server := f.server.Config.Handler.(*Server)
	cached := .25
	catalog := config.PriceCatalog{UpdatedAt: time.Now(), Entries: []config.ReferencePrice{{Model: "actual", Region: "us-east-1", Source: "aws", UpdatedAt: time.Now(), Price: config.ModelPrice{InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: &cached}}}, Sources: []config.PriceSourceStatus{{Source: "aws", Region: "us-east-1", Success: true, Models: 1}}, Warnings: []string{}}
	server.syncPrices = func(ctx context.Context, old *config.PriceCatalog, regions []string) (config.PriceCatalog, error) {
		if len(regions) != 1 || regions[0] != "us-east-1" {
			t.Errorf("wrong configured pricing regions %v", regions)
		}
		return catalog, nil
	}
	read(t, f.request("GET", "/api/admin/usage/pricing/reference", f.token, "", nil), 401)
	read(t, f.request("POST", "/api/admin/usage/pricing/sync", f.token, `{}`, nil), 401)
	read(t, f.request("PUT", "/api/admin/usage/pricing", adminTestToken, `{"alias":{"input_usd_per_million":7,"output_usd_per_million":8}}`, nil), 200)
	read(t, f.request("POST", "/api/admin/usage/pricing/sync", adminTestToken, `{}`, nil), 200)
	if f.store.Snapshot().ModelPrices["alias"].InputUSDPerMillion != 7 {
		t.Fatal("sync overwrote manual prices")
	}
	report := usageReport(t, f, "/api/me/usage?all=true", f.token)
	if math.Abs(report.Cost.EstimatedUSD-.00023) > 1e-12 {
		t.Fatalf("manual override lost %+v", report.Cost)
	}
	read(t, f.request("PUT", "/api/admin/usage/pricing", adminTestToken, `{}`, nil), 200)
	report = usageReport(t, f, "/api/me/usage?all=true", f.token)
	if math.Abs(report.Cost.EstimatedUSD-.000047) > 1e-12 {
		t.Fatalf("region/alias reference pricing lost %+v", report.Cost)
	}
	server.pricingSyncMu.Lock()
	read(t, f.request("POST", "/api/admin/usage/pricing/sync", adminTestToken, `{}`, nil), 409)
	server.pricingSyncMu.Unlock()
	server.syncPrices = func(context.Context, *config.PriceCatalog, []string) (config.PriceCatalog, error) {
		return config.PriceCatalog{}, errors.New("fixture offline")
	}
	read(t, f.request("POST", "/api/admin/usage/pricing/sync", adminTestToken, `{}`, nil), 502)
	if len(f.store.Snapshot().PriceCatalog.Entries) != 1 {
		t.Fatal("failed sync cleared cached prices")
	}
	f.restart()
	var restored config.PriceCatalog
	if err := json.Unmarshal(read(t, f.request("GET", "/api/admin/usage/pricing/reference", adminTestToken, "", nil), 200), &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Entries) != 1 {
		t.Fatal("restart lost cached reference prices")
	}
}
