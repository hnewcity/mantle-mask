package config

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSnapshotOwnership(t *testing.T) {
	st := benchmarkState()
	now := time.Now().UTC()
	rate := 2.0
	p := ModelPrice{InputUSDPerMillion: 1, OutputUSDPerMillion: 5, LongContextThreshold: 100,
		CachedInputUSDPerMillion: &rate, LongInputUSDPerMillion: &rate, LongOutputUSDPerMillion: &rate, LongCachedInputUSDPerMillion: &rate}
	st.ModelPrices = map[string]ModelPrice{"alias": p}
	st.APIKeys[0].RevokedAt = &now
	st.PriceCatalog.Entries[0].Price = p
	st.PriceCatalog.Sources = []PriceSourceStatus{{Source: "aws"}}
	st.PriceCatalog.Warnings = []string{"warning"}
	s := &Store{state: st}
	before, _ := json.Marshal(st)
	copy := s.Snapshot()
	after, _ := json.Marshal(copy)
	if string(before) != string(after) {
		t.Fatal("snapshot JSON changed")
	}
	copy.Upstreams[0].Models["model"] = "changed"
	copy.Upstreams[0].Secret = "changed"
	copy.Users[0].Name = "changed"
	copy.APIKeys[0].Name = "changed"
	*copy.APIKeys[0].RevokedAt = time.Time{}
	for _, price := range []ModelPrice{copy.ModelPrices["alias"], copy.PriceCatalog.Entries[0].Price} {
		for _, ptr := range []*float64{price.CachedInputUSDPerMillion, price.LongInputUSDPerMillion, price.LongOutputUSDPerMillion, price.LongCachedInputUSDPerMillion} {
			*ptr = 99
		}
	}
	copy.PriceCatalog.Entries[0].Model = "changed"
	copy.PriceCatalog.Sources[0].Source = "changed"
	copy.PriceCatalog.Warnings[0] = "changed"
	*s.Keys("", true)[0].RevokedAt = time.Time{}
	s.Users()[0].Name = "changed"
	s.Views()[0].Models["model"] = "changed"
	pricing := s.PricingSnapshot()
	*pricing.ModelPrices["alias"].CachedInputUSDPerMillion = 100
	pricing.PriceCatalog.Warnings[0] = "changed"
	pricing.Upstreams[0].Models["model"] = "changed"
	after, _ = json.Marshal(s.Snapshot())
	if string(before) != string(after) {
		t.Fatal("snapshot or view mutated store")
	}
	if !reflect.DeepEqual(cloneState(State{}), State{}) {
		t.Fatal("nil fields not preserved")
	}
}

func TestRoutingSnapshotAuthorizationAndIsolation(t *testing.T) {
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	key, token, err := s.CreateKey("test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transaction(func(st *State) error {
		st.Upstreams = []Upstream{{ID: "u", Models: map[string]string{"alias": "model"}}}
		st.PriceCatalog = &PriceCatalog{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, before, ok := s.AuthenticateRoutingSnapshot(token)
	if !ok || len(before.Users) != 1 || before.Users[0].ID != key.UserID || len(before.Upstreams) != 1 {
		t.Fatal("incomplete routing snapshot")
	}
	if before.APIKeys != nil || before.PriceCatalog != nil || before.ModelPrices != nil {
		t.Fatal("unrelated state copied into routing snapshot")
	}
	before.Upstreams[0].Models["alias"] = "local"
	if s.RoutingSnapshot().Upstreams[0].Models["alias"] != "model" {
		t.Fatal("routing snapshot aliases store")
	}
	if err := s.DeleteKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.AuthenticateRoutingSnapshot(token); ok {
		t.Fatal("revoked key authorized")
	}
	if before.Users[0].ID != key.UserID {
		t.Fatal("prior request snapshot changed")
	}
	if _, _, ok := s.AuthenticateRoutingSnapshot("invalid"); ok {
		t.Fatal("invalid key authorized")
	}
}

func TestRoutingSnapshotConcurrentPublication(t *testing.T) {
	s := &Store{state: State{APIKeys: []APIKey{{Hash: Hash("token"), UserID: "user"}}, Users: []User{{ID: "user", Enabled: true}}}}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				_, st, ok := s.AuthenticateRoutingSnapshot("token")
				if !ok || st.Settings.MaxRetries != len(st.Users[0].Name) {
					t.Error("mixed configuration versions")
				}
			}
		})
	}
	for i := range 200 {
		s.mu.Lock()
		next := cloneState(s.state)
		if i%2 == 0 {
			next.Users[0].Name = "x"
			next.Settings.MaxRetries = 1
		} else {
			next.Users[0].Name = ""
			next.Settings.MaxRetries = 0
		}
		s.state = next
		s.mu.Unlock()
	}
	wg.Wait()
}

func TestRoutingSnapshotTracksRestoreAndDisabledUser(t *testing.T) {
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.CreateKey("key")
	if err != nil {
		t.Fatal(err)
	}
	_, before, ok := s.AuthenticateRoutingSnapshot(token)
	if !ok {
		t.Fatal("initial authorization failed")
	}
	next := s.Snapshot()
	next.Users[0].Enabled = false
	if err := s.RestoreWith(next, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.AuthenticateRoutingSnapshot(token); ok {
		t.Fatal("disabled user authorized after restore")
	}
	if !before.Users[0].Enabled {
		t.Fatal("restore mutated an in-flight snapshot")
	}
	next = s.Snapshot()
	next.Users[0].Enabled, next.Users[0].Name = true, "restored"
	next.APIKeys[0].Hash = Hash("restored-token")
	if err := s.Restore(next); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.AuthenticateRoutingSnapshot(token); ok {
		t.Fatal("old token authorized after restore")
	}
	if _, st, ok := s.AuthenticateRoutingSnapshot("restored-token"); !ok || st.Users[0].Name != "restored" {
		t.Fatal("restored authorization or metadata missing")
	}
}

func BenchmarkAuthenticateRoutingSnapshot(b *testing.B) {
	s := &Store{state: benchmarkState()}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, _, ok := s.AuthenticateRoutingSnapshot("999"); !ok {
			b.Fatal("authentication failed")
		}
	}
}
