package usage

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

func assertCost(t *testing.T, got Cost, amount float64, priced, unpriced int64) {
	t.Helper()
	if math.Abs(got.EstimatedUSD-amount) > 1e-12 || got.PricedRequests != priced || got.UnpricedRequests != unpriced {
		t.Fatalf("cost = %+v, want USD %.9f, priced %d, unpriced %d", got, amount, priced, unpriced)
	}
}

func TestCostEstimatesAcrossDimensionsAndFilters(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 9, 17, 0, 0, 0, time.UTC)
	add := func(id, model, user, key, upstream string, counters *Counters, status int) {
		t.Helper()
		r := Request{ID: id, Model: model, UserID: user, UserName: user, KeyID: key, KeyName: key, StartedAt: now, Attempts: 1, UpstreamID: upstream, UpstreamName: upstream, Status: status}
		if err := store.Begin(r); err != nil {
			t.Fatal(err)
		}
		if err := store.Attempt(r); err != nil {
			t.Fatal(err)
		}
		if counters != nil {
			if err := store.Tokens(id, *counters); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Finish(r); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "model-a", "alice", "k1", "up-a", &Counters{InputTokens: 1000000, OutputTokens: 500000, CachedTokens: 250000, ReasoningTokens: 400000}, 200)
	// A returned usage object is counted even on an error; retries do not multiply it.
	add("b", "model-b", "bob", "k2", "up-b", &Counters{InputTokens: 1000000, OutputTokens: 100000, CachedTokens: 100000}, 500)
	add("unknown", "unknown", "alice", "k1", "up-a", &Counters{InputTokens: 10}, 200)
	add("zero", "model-a", "alice", "k1", "up-a", &Counters{}, 200)
	add("missing", "model-a", "alice", "k1", "up-a", nil, 200)
	cached := .1
	prices := map[string]config.ModelPrice{"model-a": {InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: &cached}, "model-b": {InputUSDPerMillion: 3, OutputUSDPerMillion: 4}}
	report, err := store.ReportWithPrices(context.Background(), reportFilter(t, "all=true&timezone_offset=480"), prices)
	if err != nil {
		t.Fatal(err)
	}
	// A = .75 + .025 + 1 = 1.775; B = 3 + .4 = 3.4 (cached price omitted).
	assertCost(t, report.Cost, 5.175, 3, 1)
	for _, groups := range [][]Group{report.ByUser, report.ByKey, report.ByModel, report.ByUpstream, report.Daily} {
		var sum Cost
		for _, group := range groups {
			sum.EstimatedUSD += group.Cost.EstimatedUSD
			sum.PricedRequests += group.Cost.PricedRequests
			sum.UnpricedRequests += group.Cost.UnpricedRequests
		}
		assertCost(t, sum, 5.175, 3, 1)
	}
	var daily Cost
	for _, row := range report.DailyByModel {
		daily.EstimatedUSD += row.Cost.EstimatedUSD
		daily.PricedRequests += row.Cost.PricedRequests
		daily.UnpricedRequests += row.Cost.UnpricedRequests
	}
	assertCost(t, daily, 5.175, 3, 1)
	for _, query := range []string{"all=true&user_id=alice", "all=true&key_id=k1", "all=true&upstream_id=up-a", "from=2026-10-10&to=2026-10-10&timezone_offset=480&model=model-a"} {
		filtered, err := store.ReportWithPrices(context.Background(), reportFilter(t, query), prices)
		if err != nil {
			t.Fatal(err)
		}
		unknown := int64(1)
		if query == "from=2026-10-10&to=2026-10-10&timezone_offset=480&model=model-a" {
			unknown = 0
		}
		assertCost(t, filtered.Cost, 1.775, 2, unknown)
	}
	noPrices, err := store.Report(context.Background(), reportFilter(t, "all=true"))
	if err != nil {
		t.Fatal(err)
	}
	assertCost(t, noPrices.Cost, 0, 0, 4)
	zero := 0.0
	prices["model-a"] = config.ModelPrice{InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: &zero}
	updated, err := store.ReportWithPrices(context.Background(), reportFilter(t, "all=true"), prices)
	if err != nil {
		t.Fatal(err)
	}
	assertCost(t, updated.Cost, 5.15, 3, 1)
}

func TestCostPriceIDsAreParametersAndReconciliationDoesNotDoubleCount(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := "quoted' UNION SELECT model FROM requests--"
	request := Request{ID: "r", Model: model, StartedAt: time.Now(), Attempts: 1, Status: 200}
	for _, err := range []error{store.Begin(request), store.Attempt(request), store.Bind("scope", "r"), store.Finish(request)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if err := store.Reconcile("scope", Counters{InputTokens: 100, CachedTokens: 1000, OutputTokens: 200}); err != nil {
			t.Fatal(err)
		}
	}
	freeCache := 0.0
	prices := map[string]config.ModelPrice{model: {InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: &freeCache}}
	report, err := store.ReportWithPrices(context.Background(), reportFilter(t, "all=true"), prices)
	if err != nil {
		t.Fatal(err)
	}
	assertCost(t, report.Cost, .0004, 1, 0)
	prices[model] = config.ModelPrice{}
	report, err = store.ReportWithPrices(context.Background(), reportFilter(t, "all=true"), prices)
	if err != nil {
		t.Fatal(err)
	}
	assertCost(t, report.Cost, 0, 1, 0)
}

func TestReferencePricesUseUpstreamRegionAliasesPerRequestTiersAndManualOverrides(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	add := func(id, upstream string, input, output, cached int64) {
		r := Request{ID: id, Model: "alias", UpstreamID: upstream, StartedAt: time.Now(), Status: 200, Attempts: 1}
		for _, err := range []error{store.Begin(r), store.Attempt(r), store.Tokens(id, Counters{InputTokens: input, OutputTokens: output, CachedTokens: cached, ReasoningTokens: output}), store.Finish(r)} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	add("below", "east", 272000, 100, 1000)
	add("above", "east", 272001, 100, 1000)
	add("west", "west", 100, 100, 0)
	add("unknown", "deleted", 100, 100, 0)
	longInput, longOutput, longCache, cached := 2.0, 3.0, .2, .1
	state := config.State{Upstreams: []config.Upstream{{ID: "east", Region: "us-east-1", Models: map[string]string{"alias": "actual"}}, {ID: "west", Region: "us-west-2", Models: map[string]string{"alias": "actual"}}}, PriceCatalog: &config.PriceCatalog{UpdatedAt: time.Now(), Entries: []config.ReferencePrice{
		{Model: "actual", Region: "us-east-1", Source: "litellm", UpdatedAt: time.Now(), Price: config.ModelPrice{InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: &cached, LongContextThreshold: 272000, LongInputUSDPerMillion: &longInput, LongOutputUSDPerMillion: &longOutput, LongCachedInputUSDPerMillion: &longCache}},
		{Model: "actual", Region: "us-west-2", Source: "aws", UpdatedAt: time.Now(), Price: config.ModelPrice{InputUSDPerMillion: 3, OutputUSDPerMillion: 4}},
	}}}
	report, err := store.ReportWithPricing(context.Background(), reportFilter(t, "all=true"), state)
	if err != nil {
		t.Fatal(err)
	}
	// Each request selects its own tier, not the sum of model input tokens.
	amount := (271000.0 + 1000*.1 + 100*2 + 271001*2 + 1000*.2 + 100*3 + 100*3 + 100*4) / 1000000
	assertCost(t, report.Cost, amount, 3, 1)
	state.ModelPrices = map[string]config.ModelPrice{"alias": {InputUSDPerMillion: 0, OutputUSDPerMillion: 1}}
	report, err = store.ReportWithPricing(context.Background(), reportFilter(t, "all=true"), state)
	if err != nil {
		t.Fatal(err)
	}
	assertCost(t, report.Cost, .0004, 4, 0)
}
