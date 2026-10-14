package config

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestModelPriceValidation(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1), 1000001} {
		for _, price := range []ModelPrice{{InputUSDPerMillion: value}, {OutputUSDPerMillion: value}, {CachedInputUSDPerMillion: &value}} {
			if ValidateModelPrices(map[string]ModelPrice{"model": price}) == nil {
				t.Fatalf("accepted invalid price: %+v", price)
			}
		}
	}
	for _, model := range []string{"", " model", "model ", strings.Repeat("a", 257)} {
		if ValidateModelPrices(map[string]ModelPrice{model: {}}) == nil {
			t.Fatalf("accepted invalid ID %q", model)
		}
	}
	prices := map[string]ModelPrice{}
	for i := range 201 {
		prices[strings.Repeat("a", i+1)] = ModelPrice{}
	}
	if ValidateModelPrices(prices) == nil {
		t.Fatal("accepted more than 200 prices")
	}
	zero := 0.0
	if err := ValidateModelPrices(map[string]ModelPrice{"free": {CachedInputUSDPerMillion: &zero}}); err != nil {
		t.Fatal(err)
	}
}

func TestModelPricesPersistenceAndBackup(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cached := .1
	prices := map[string]ModelPrice{"alias": {InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: &cached}}
	if err := store.UpdateModelPrices(prices); err != nil {
		t.Fatal(err)
	}
	cached = 99
	delete(prices, "alias")
	snapshot := store.Snapshot()
	if snapshot.ModelPrices["alias"].InputUSDPerMillion != 1 || *snapshot.ModelPrices["alias"].CachedInputUSDPerMillion != .1 {
		t.Fatal("caller mutated persisted prices")
	}
	*snapshot.ModelPrices["alias"].CachedInputUSDPerMillion = 99
	reopened, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if *reopened.Snapshot().ModelPrices["alias"].CachedInputUSDPerMillion != .1 {
		t.Fatal("lost prices after reopen")
	}
	portable, err := reopened.Export()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := reopened.PrepareImport(portable)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ModelPrices["alias"].OutputUSDPerMillion != 2 {
		t.Fatal("lost prices in backup")
	}
	portable.State.ModelPrices["alias"] = ModelPrice{InputUSDPerMillion: -1}
	if _, err := reopened.PrepareImport(portable); err == nil {
		t.Fatal("accepted invalid backup prices")
	}
	if err := reopened.UpdateModelPrices(nil); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().ModelPrices) != 0 {
		t.Fatal("could not clear pricing")
	}
}

func TestModelPriceJSONRequiresExplicitRegularRates(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"input_usd_per_million":0}`, `{"input_usd_per_million":null,"output_usd_per_million":1}`, `{"input_usd_per_million":0,"output_usd_per_million":0,"typo":1}`} {
		var price ModelPrice
		if err := json.Unmarshal([]byte(raw), &price); err == nil {
			t.Fatalf("accepted incomplete or invalid price %s", raw)
		}
	}
	var price ModelPrice
	if err := json.Unmarshal([]byte(`{"input_usd_per_million":0,"output_usd_per_million":0}`), &price); err != nil {
		t.Fatal(err)
	}
	if price.CachedInputUSDPerMillion != nil {
		t.Fatal("missing cache price must inherit input")
	}
}

func TestReferenceCatalogPersistenceBackupAndManualIsolation(t *testing.T) {
	store, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateModelPrices(map[string]ModelPrice{"alias": {InputUSDPerMillion: 7, OutputUSDPerMillion: 8}}); err != nil {
		t.Fatal(err)
	}
	catalog := PriceCatalog{UpdatedAt: time.Now(), Entries: []ReferencePrice{{Model: "actual", Region: "us-west-2", Source: "aws", UpdatedAt: time.Now(), Price: ModelPrice{InputUSDPerMillion: 1, OutputUSDPerMillion: 2}}}}
	if err := store.UpdatePriceCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Entries[0].Price.InputUSDPerMillion = 99
	if store.Snapshot().ModelPrices["alias"].InputUSDPerMillion != 7 || store.Snapshot().PriceCatalog.Entries[0].Price.InputUSDPerMillion != 1 {
		t.Fatal("catalog synchronization changed overrides or leaked ownership")
	}
	portable, err := store.Export()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareImport(portable)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.PriceCatalog.Entries[0].Price.OutputUSDPerMillion != 2 {
		t.Fatal("backup lost reference prices")
	}
	prepared.PriceCatalog.Entries[0].Source = "untrusted"
	if ValidatePriceCatalog(prepared.PriceCatalog) == nil {
		t.Fatal("invalid catalog source accepted")
	}
	threshold := 1000.0
	if ValidateModelPrices(map[string]ModelPrice{"m": {LongContextThreshold: 200000, LongInputUSDPerMillion: &threshold}}) == nil {
		t.Fatal("incomplete tier accepted")
	}
}
