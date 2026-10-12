package usage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func reportFilter(t *testing.T, raw string) Filter {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseFilter(q, time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPersistenceReconciliationAndCalendarDates(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{ID: "r1", UserID: "alice", UserName: "Alice", KeyID: "k1", KeyName: "App", Model: "model-a", StartedAt: time.Date(2026, 10, 9, 17, 0, 0, 0, time.UTC)}
	for _, err := range []error{s.Begin(r), s.Attempt(Request{ID: r.ID, Model: r.Model, Attempts: 1, UpstreamID: "up-a", UpstreamName: "Upstream A"}), s.Bind("scope", r.ID)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	c := Counters{InputTokens: 100, OutputTokens: 50, CachedTokens: 20, ReasoningTokens: 30}
	if err := s.Tokens(r.ID, c); err != nil {
		t.Fatal(err)
	}
	// Simulate restart before Finish: preserve reported counters, mark interrupted.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for range 3 {
		if err := s.Reconcile("scope", c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Reconcile("unknown-scope", c); err != nil {
		t.Fatal(err)
	}
	if err := s.Tokens(r.ID, Counters{InputTokens: 90, OutputTokens: 40}); err != nil {
		t.Fatal(err)
	}
	report, err := s.Report(context.Background(), reportFilter(t, "from=2026-10-10&to=2026-10-10&timezone_offset=480"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals != (Totals{Requests: 1, Errors: 1, InputTokens: 100, OutputTokens: 50, TotalTokens: 150, CachedTokens: 20, ReasoningTokens: 30, KnownUsageRequests: 1}) {
		t.Fatalf("incorrect persisted totals: %+v", report.Totals)
	}
	if len(report.ByUpstream) != 1 || report.ByUpstream[0].ID != "up-a" || report.ByUpstream[0].Name != "Upstream A" || report.ByUpstream[0].Totals != report.Totals {
		t.Fatalf("lost upstream attribution after restart: %+v", report.ByUpstream)
	}
	if len(report.Daily) != 1 || report.Daily[0].ID != "2026-10-10" {
		t.Fatalf("wrong local day: %+v", report.Daily)
	}
	report, err = s.Report(context.Background(), reportFilter(t, "from=2026-10-10&to=2026-10-10&timezone_offset=0"))
	if err != nil || report.Totals.Requests != 0 {
		t.Fatalf("UTC filter: %+v %v", report.Totals, err)
	}
	report, err = s.Report(context.Background(), reportFilter(t, "all=true&user_id=other"))
	if err != nil || report.Totals.Requests != 0 {
		t.Fatal("user filter ignored", err)
	}
}

func TestConcurrentRequestsAndMissingVersusZeroUsage(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := Request{ID: fmt.Sprintf("r%d", i), UserID: "u", KeyID: "k", Model: "m", StartedAt: time.Now(), Attempts: 1, Status: 200}
			if err := s.Begin(r); err != nil {
				t.Error(err)
				return
			}
			if i < 10 {
				// Reported zero is known usage, not missing.
				if err := s.Tokens(r.ID, Counters{}); err != nil {
					t.Error(err)
				}
			} else if i >= 20 {
				if err := s.Tokens(r.ID, Counters{InputTokens: 2, OutputTokens: 3}); err != nil {
					t.Error(err)
				}
			}
			if err := s.Finish(r); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	report, err := s.Report(context.Background(), reportFilter(t, "all=true"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Requests != 30 || report.Totals.TotalTokens != 50 || report.Totals.KnownUsageRequests != 20 || report.Totals.MissingUsageRequests != 10 {
		t.Fatalf("concurrent accounting: %+v", report.Totals)
	}
	for _, raw := range []string{"from=invalid", "timezone_offset=bad", "timezone_offset=900", "from=2026-10-11&to=2026-10-10", "all=true&from=2026-10-10", "from=2020-01-01&to=2026-10-10"} {
		q, _ := url.ParseQuery(raw)
		if _, err := ParseFilter(q, time.Now()); err == nil {
			t.Fatalf("accepted invalid filter %s", raw)
		}
	}
}

func TestUpstreamGroupingAndCombinedFilters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	started := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	for i, r := range []Request{
		{ID: "r1", UserID: "alice", KeyID: "k1", Model: "m1", UpstreamID: "up-a", UpstreamName: "Same name", Status: 200, Attempts: 1},
		{ID: "r2", UserID: "alice", KeyID: "k2", Model: "m2", UpstreamID: "up-b", UpstreamName: "Same name", Status: 502, Attempts: 1},
		{ID: "r3", UserID: "bob", KeyID: "k3", Model: "m1", UpstreamID: "up-a", UpstreamName: "Same name", Status: 200, Attempts: 1},
		{ID: "r4", UserID: "alice", KeyID: "k1", Model: "m2", UpstreamID: "up-b", UpstreamName: "Same name", Status: 200, Attempts: 2},
		{ID: "r5", UserID: "alice", KeyID: "k1", Status: 400},
	} {
		r.StartedAt = started
		if err := s.Begin(r); err != nil {
			t.Fatal(err)
		}
		if r.Attempts == 2 {
			if err := s.Attempt(Request{ID: r.ID, Model: r.Model, UpstreamID: "up-a", UpstreamName: "Same name", Attempts: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if r.Attempts > 0 {
			if err := s.Attempt(r); err != nil {
				t.Fatal(err)
			}
		}
		if r.Status == 200 {
			if err := s.Tokens(r.ID, Counters{InputTokens: int64(i + 1), OutputTokens: 2, CachedTokens: 1, ReasoningTokens: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Finish(r); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Report(context.Background(), reportFilter(t, "all=true"))
	if err != nil {
		t.Fatal(err)
	}
	if len(all.ByUpstream) != 3 || all.ByUpstream[0].ID != "" || all.ByUpstream[0].Requests != 1 || all.Totals.Requests != 5 || all.Totals.TotalTokens != 14 {
		t.Fatalf("upstream grouping lost or duplicated requests: %+v", all)
	}
	for _, tc := range []struct {
		query                             string
		requests, tokens, errors, missing int64
	}{
		{"all=true&upstream_id=up-a", 2, 8, 0, 0},
		{"all=true&upstream_id=up-b", 2, 6, 1, 1},
		{"all=true&upstream_id=up-a&user_id=alice", 1, 3, 0, 0},
		{"from=2026-10-10&to=2026-10-10&upstream_id=up-b&user_id=alice&key_id=k1&model=m2", 1, 6, 0, 0},
		{"all=true&upstream_id=up-b&model=m1", 0, 0, 0, 0},
		{"all=true&upstream_id=unknown", 0, 0, 0, 0},
		{"all=true&upstream_id=%27%20OR%201%3D1--", 0, 0, 0, 0},
		{"from=2026-10-09&to=2026-10-09&upstream_id=up-a", 0, 0, 0, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			report, err := s.Report(context.Background(), reportFilter(t, tc.query))
			if err != nil {
				t.Fatal(err)
			}
			if report.Totals.Requests != tc.requests || report.Totals.TotalTokens != tc.tokens || report.Totals.Errors != tc.errors || report.Totals.MissingUsageRequests != tc.missing {
				t.Fatalf("wrong filtered totals: %+v", report)
			}
			if tc.requests == 0 {
				if len(report.ByUpstream) != 0 || len(report.ByModel) != 0 || len(report.ByUser) != 0 || len(report.ByKey) != 0 || len(report.Daily) != 0 {
					t.Fatalf("empty filter leaked groups: %+v", report)
				}
			} else if len(report.ByUpstream) != 1 || report.ByUpstream[0].Totals != report.Totals {
				t.Fatalf("upstream totals disagree: %+v", report)
			}
		})
	}
}

func TestMigrateV1UsagePreservesHistoryAndResponseLinks(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
 CREATE TABLE requests (
  id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
  user_id TEXT NOT NULL, user_name TEXT NOT NULL, key_id TEXT NOT NULL, key_name TEXT NOT NULL,
  model TEXT NOT NULL, path TEXT NOT NULL,
  status INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0, completed INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_tokens INTEGER NOT NULL DEFAULT 0, reasoning_tokens INTEGER NOT NULL DEFAULT 0,
  usage_known INTEGER NOT NULL DEFAULT 0
 );
 CREATE TABLE response_links (scope TEXT PRIMARY KEY, request_id TEXT NOT NULL REFERENCES requests(id));
 INSERT INTO requests (id, started_at, user_id, user_name, key_id, key_name, model, path, attempts, input_tokens, output_tokens, usage_known)
 VALUES ('legacy', 1791619200000, 'alice', 'Alice', 'k1', 'App', 'm1', '/v1/responses', 1, 10, 20, 1);
 INSERT INTO response_links (scope, request_id) VALUES ('legacy-scope', 'legacy');
 PRAGMA user_version=1;
 `)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Opening twice covers the one-time migration and reopening an upgraded DB.
	for range 2 {
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var version int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
			t.Fatalf("schema version %d: %v", version, err)
		}
		if err := s.Reconcile("legacy-scope", Counters{InputTokens: 12, OutputTokens: 24}); err != nil {
			t.Fatal(err)
		}
		report, err := s.Report(context.Background(), reportFilter(t, "all=true"))
		if err != nil {
			t.Fatal(err)
		}
		if len(report.ByUpstream) != 1 || report.ByUpstream[0].ID != "" || report.ByUpstream[0].Name != "" || report.ByUpstream[0].TotalTokens != 36 || report.Totals.Requests != 1 || report.Totals.Errors != 1 {
			t.Fatalf("migration changed legacy attribution or usage: %+v", report)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Request{ID: "new", StartedAt: time.Now(), UpstreamID: "up-new", UpstreamName: "New upstream", Attempts: 1, Status: 200}
	for _, err := range []error{s.Begin(r), s.Attempt(r), s.Tokens(r.ID, Counters{InputTokens: 3, OutputTokens: 4}), s.Finish(r)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := s.Report(context.Background(), reportFilter(t, "all=true&upstream_id=up-new"))
	if err != nil || len(report.ByUpstream) != 1 || report.Totals.Requests != 1 || report.Totals.TotalTokens != 7 {
		t.Fatalf("cannot write after migration: %+v %v", report, err)
	}
}

func TestDailyModelsUseCalendarAndAllFilters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Date(2026, 10, 9, 17, 0, 0, 0, time.UTC)
	for i, entry := range []struct {
		model, user, key, upstream string
		offset                     time.Duration
		known                      bool
	}{
		{"a", "alice", "k1", "up-a", 0, true},
		{"a", "alice", "k1", "up-a", time.Hour, true},
		{"b", "alice", "k2", "up-b", 0, true},
		{"a", "bob", "k3", "up-a", 0, true},
		{"a", "alice", "k1", "up-a", 24 * time.Hour, true},
		{"", "alice", "k1", "up-a", 0, false},
	} {
		r := Request{ID: fmt.Sprint(i), Model: entry.model, UserID: entry.user, KeyID: entry.key,
			UpstreamID: entry.upstream, StartedAt: base.Add(entry.offset), Attempts: 1, Status: 200}
		if err := s.Begin(r); err != nil {
			t.Fatal(err)
		}
		if err := s.Attempt(r); err != nil {
			t.Fatal(err)
		}
		if entry.known {
			if err := s.Tokens(r.ID, Counters{InputTokens: 10, OutputTokens: 3, CachedTokens: 4, ReasoningTokens: 2}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Finish(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query            string
		groups           int
		requests, tokens int64
	}{
		{"all=true&timezone_offset=480", 4, 6, 65},
		{"from=2026-10-10&to=2026-10-10&timezone_offset=480&user_id=alice", 3, 4, 39},
		{"from=2026-10-10&to=2026-10-10&timezone_offset=480&user_id=alice&key_id=k1&upstream_id=up-a&model=a", 1, 2, 26},
		{"from=2026-10-10&to=2026-10-10&timezone_offset=0", 1, 1, 13},
		{"all=true&user_id=bob&key_id=k1", 0, 0, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			report, err := s.Report(context.Background(), reportFilter(t, tc.query))
			if err != nil {
				t.Fatal(err)
			}
			if len(report.DailyByModel) != tc.groups {
				t.Fatalf("unexpected groups: %+v", report.DailyByModel)
			}
			var requests, tokens int64
			daily := map[string]int64{}
			for _, row := range report.DailyByModel {
				requests += row.Requests
				tokens += row.TotalTokens
				daily[row.Date] += row.TotalTokens
				if row.Model == "" && (row.MissingUsageRequests != 1 || row.KnownUsageRequests != 0) {
					t.Fatalf("unknown usage lost: %+v", row)
				}
			}
			if requests != tc.requests || tokens != tc.tokens || tokens != report.Totals.TotalTokens {
				t.Fatalf("incorrect daily model totals: %+v", report)
			}
			for _, row := range report.Daily {
				if daily[row.ID] != row.TotalTokens {
					t.Fatalf("calendar mismatch: %+v", report)
				}
			}
		})
	}
}
