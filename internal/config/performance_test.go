package config

import (
	"fmt"
	"testing"
	"time"
)

func benchmarkState() State {
	now := time.Now().UTC()
	rate := 0.25
	st := State{Version: 2, Users: []User{{ID: DefaultUserID, Enabled: true}}, PriceCatalog: &PriceCatalog{UpdatedAt: now}}
	for i := range 8 {
		st.Upstreams = append(st.Upstreams, Upstream{ID: fmt.Sprint(i), Models: map[string]string{"model": "upstream-model"}})
	}
	for i := range 1000 {
		st.APIKeys = append(st.APIKeys, APIKey{ID: fmt.Sprint(i), Hash: Hash(fmt.Sprint(i)), UserID: DefaultUserID})
	}
	for i := range 1000 {
		st.PriceCatalog.Entries = append(st.PriceCatalog.Entries, ReferencePrice{Model: fmt.Sprint(i), Region: "us-east-1", Source: "aws", UpdatedAt: now, Price: ModelPrice{InputUSDPerMillion: 1, OutputUSDPerMillion: 5, CachedInputUSDPerMillion: &rate}})
	}
	return st
}

func BenchmarkConfigSnapshot(b *testing.B) {
	s := &Store{state: benchmarkState()}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.Snapshot()
	}
}

func BenchmarkAuthenticateSnapshot(b *testing.B) {
	s := &Store{state: benchmarkState()}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, _, ok := s.AuthenticateSnapshot("999"); !ok {
			b.Fatal("authentication failed")
		}
	}
}
