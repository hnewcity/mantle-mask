package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

func awsFixture(region string, input, output float64) []byte {
	products := map[string]any{}
	terms := map[string]any{}
	for _, item := range []struct {
		suffix string
		rate   float64
	}{{"input", input}, {"output", output}} {
		sku := item.suffix
		products[sku] = map[string]any{"attributes": map[string]string{"regionCode": region, "service_tier": "standard", "usagetype": "USW2-openai.gpt-oss-20b-mantle-" + item.suffix + "-tokens-standard"}}
		terms[sku] = map[string]any{"term": map[string]any{"priceDimensions": map[string]any{"dimension": map[string]any{"unit": "1K tokens", "beginRange": "0", "endRange": "Inf", "pricePerUnit": map[string]string{"USD": fmt.Sprint(item.rate / 1000)}}}}}
	}
	data, _ := json.Marshal(map[string]any{"products": products, "terms": map[string]any{"OnDemand": terms}})
	return data
}

func TestAWSParserRequiresCompleteStandardRegionalTokenPrices(t *testing.T) {
	prices, err := parseAWS(awsFixture("us-west-2", .07, .3), "us-west-2")
	if err != nil {
		t.Fatal(err)
	}
	if prices["openai.gpt-oss-20b"].InputUSDPerMillion != .07 || prices["openai.gpt-oss-20b"].OutputUSDPerMillion != .3 {
		t.Fatalf("wrong unit conversion %+v", prices)
	}
	wrong, err := parseAWS(awsFixture("us-west-2", .07, .3), "us-east-1")
	if err != nil || len(wrong) != 0 {
		t.Fatal("region mismatch was priced")
	}
	for _, alter := range []struct{ old, new string }{{"1K tokens", "requests"}, {"Inf", "1000"}, {"standard", "flex"}, {"USD", "EUR"}, {`"output"`, `"missing-output"`}} {
		data := strings.ReplaceAll(string(awsFixture("us-west-2", .07, .3)), alter.old, alter.new)
		prices, err := parseAWS([]byte(data), "us-west-2")
		if err != nil {
			t.Fatal(err)
		}
		// Renaming a SKU in both maps preserves the price, unlike malformed units/ranges.
		if alter.old != `"output"` && len(prices) != 0 {
			t.Fatalf("unsafe AWS prices accepted: %s", alter.new)
		}
	}
	if _, err := parseAWS([]byte(`{"products":{}}`), "us-west-2"); err == nil {
		t.Fatal("incomplete document accepted")
	}
}

func TestCommunityParsersUnitsCacheTiersAndProviderIsolation(t *testing.T) {
	lite, err := parseLiteCatalog([]byte(`{
 "openai/gpt-5.4":{"input_cost_per_token":99,"output_cost_per_token":99},
 "bedrock_mantle/openai.gpt-5.4":{"input_cost_per_token":0.00000275,"output_cost_per_token":0.0000165,"cache_read_input_token_cost":0,
 "input_cost_per_token_above_272k_tokens":0.0000055,"output_cost_per_token_above_272k_tokens":0.00002475,"cache_read_input_token_cost_above_272k_tokens":0.00000055},
 "bedrock_mantle/us-gov-west-1/openai.gpt-5.4":{"input_cost_per_token":0.000003,"output_cost_per_token":0.00002},
 "bedrock_mantle/incomplete":{"output_cost_per_token":0},
 "bedrock_mantle/bad-tier":{"input_cost_per_token":1,"output_cost_per_token":1,"input_cost_per_token_above_128k_tokens":1},
 "bedrock_mantle/free":{"input_cost_per_token":0,"output_cost_per_token":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	commercial := litePrices(lite, "us-west-2")
	price := commercial["openai.gpt-5.4"]
	if !closePrice(price.InputUSDPerMillion, 2.75) || !closePrice(price.OutputUSDPerMillion, 16.5) || price.CachedInputUSDPerMillion == nil || *price.CachedInputUSDPerMillion != 0 || price.LongContextThreshold != 272000 || !closePrice(*price.LongCachedInputUSDPerMillion, .55) {
		t.Fatalf("wrong LiteLLM price %+v", price)
	}
	for _, id := range []string{"incomplete", "bad-tier"} {
		if _, ok := commercial[id]; ok {
			t.Fatal("incomplete pricing converted to zero/base price")
		}
	}
	if _, ok := commercial["free"]; !ok {
		t.Fatal("explicit free prices rejected")
	}
	gov := litePrices(lite, "us-gov-west-1")
	if gov["openai.gpt-5.4"].InputUSDPerMillion != 3 || len(gov) != 1 {
		t.Fatal("commercial price leaked into GovCloud")
	}
	models, err := parseModelsCatalog([]byte(`{"openai":{"models":{"foreign":{"cost":{"input":9,"output":9}}}},"amazon-bedrock":{"models":{"m":{"cost":{"input":1,"output":2,"cache_read":0.1,"context_over_200k":{"input":2,"output":3}}},"unknown":{"cost":{"input":1}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	fallback := modelsPrices(models, "us-east-1")
	if len(fallback) != 1 || fallback["m"].InputUSDPerMillion != 1 || fallback["m"].LongContextThreshold != 200000 {
		t.Fatalf("wrong models.dev parsing %+v", fallback)
	}
}

func TestSyncFallbackFailureCacheNoCredentialsAndRegionValidation(t *testing.T) {
	var failures atomic.Bool
	var leaked atomic.Bool
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			leaked.Store(true)
		}
		if failures.Load() {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/aws/us-west-2":
			w.Write(awsFixture("us-west-2", .07, .3))
		case "/aws/us-east-1":
			w.WriteHeader(503)
		case "/lite":
			fmt.Fprint(w, `{"bedrock_mantle/openai.gpt-oss-20b":{"input_cost_per_token":0.00000007,"output_cost_per_token":0.0000003},"bedrock_mantle/lite-only":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002}}`)
		case "/models":
			fmt.Fprint(w, `{"amazon-bedrock":{"models":{"models-only":{"cost":{"input":3,"output":4}}}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	syncer := &Syncer{client: server.Client(), awsURL: server.URL + "/aws/%s", liteURL: server.URL + "/lite", modelsURL: server.URL + "/models"}
	catalog, err := syncer.Sync(context.Background(), nil, []string{"us-west-2", "us-east-1", "us-west-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Entries) != 6 || len(catalog.Sources) != 4 || len(catalog.Warnings) != 1 || leaked.Load() {
		t.Fatalf("wrong sync result %+v", catalog)
	}
	byID := map[string]config.ReferencePrice{}
	for _, entry := range catalog.Entries {
		byID[entry.Region+"/"+entry.Model] = entry
	}
	if byID["us-west-2/openai.gpt-oss-20b"].Source != "aws" || byID["us-east-1/openai.gpt-oss-20b"].Source != "litellm" || byID["us-west-2/models-only"].Source != "models.dev" {
		t.Fatal("source priority or fallback lost")
	}
	before := calls.Load()
	if _, err := syncer.Sync(context.Background(), nil, []string{"../../private"}); err == nil || calls.Load() != before {
		t.Fatal("invalid region caused a request")
	}
	failures.Store(true)
	if _, err := syncer.Sync(context.Background(), &catalog, []string{"us-west-2"}); err == nil {
		t.Fatal("all failures did not preserve prior catalog")
	}
	// Broken JSON is rejected without interpreting it as an empty/free catalog.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"unfinished":`) }))
	defer broken.Close()
	if _, err := syncer.get(context.Background(), broken.URL); err == nil {
		t.Fatal("malformed JSON accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := syncer.Sync(ctx, &catalog, []string{"us-west-2"}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestTiersSupplementOnlyMatchingOfficialBasePrices(t *testing.T) {
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aws/us-west-2":
			w.Write(awsFixture("us-west-2", 1, 2))
		case "/lite":
			fmt.Fprint(w, `{"bedrock_mantle/openai.gpt-oss-20b":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"cache_read_input_token_cost":0.0000001,"input_cost_per_token_above_272k_tokens":0.000002,"output_cost_per_token_above_272k_tokens":0.000003}}`)
		case "/models":
			fmt.Fprint(w, `{"amazon-bedrock":{"models":{"fallback":{"cost":{"input":0,"output":0}}}}}`)
		}
	}))
	defer server.Close()
	s := &Syncer{client: server.Client(), awsURL: server.URL + "/aws/%s", liteURL: server.URL + "/lite", modelsURL: server.URL + "/models"}
	catalog, err := s.Sync(context.Background(), nil, []string{"us-west-2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog.Entries {
		if entry.Model == "openai.gpt-oss-20b" {
			if entry.Source != "aws+litellm" || entry.Price.LongContextThreshold != 272000 || entry.UpdatedAt.Before(now) {
				t.Fatal("lost verified tier supplement")
			}
		}
	}
}

func TestRedirectAllowlistAndUnsupportedTierSafety(t *testing.T) {
	syncer := New()
	for _, url := range []string{"http://pricing.us-east-1.amazonaws.com/price.json", "https://127.0.0.1/private", "https://example.com/prices"} {
		req, _ := http.NewRequest("GET", url, nil)
		if syncer.client.CheckRedirect(req, nil) == nil {
			t.Fatal("unsafe redirect accepted")
		}
	}
	req, _ := http.NewRequest("GET", "https://models.dev/api.json", nil)
	if err := syncer.client.CheckRedirect(req, nil); err != nil {
		t.Fatal(err)
	}
	lite := map[string]json.RawMessage{"bedrock_mantle/m": json.RawMessage(`{"input_cost_per_token":0.1,"output_cost_per_token":0.2,"input_cost_per_token_above_1m_tokens":0.3}`)}
	models := map[string]json.RawMessage{"m": json.RawMessage(`{"input":1,"output":2}`)}
	if !unsafeTierEntries(lite, models, "us-west-2")["m"] {
		t.Fatal("unsupported tier allowed a flat fallback")
	}
	if _, ok := parsedPrice(json.RawMessage(`{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"cache_creation_input_token_cost_above_1hr":0.000003}`), true); !ok {
		t.Fatal("cache TTL pricing was misclassified as context tiers")
	}
}
