package usage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

func reportFixture(tb testing.TB, count int) (*Store, config.State) {
	tb.Helper()
	s, err := Open(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	tx, err := s.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO requests
  (id,started_at,user_id,user_name,key_id,key_name,model,path,upstream_id,upstream_name,
   status,attempts,completed,pending_sync,input_tokens,output_tokens,cached_tokens,reasoning_tokens,usage_known)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tb.Fatal(err)
	}
	defer stmt.Close()
	models := []string{"alias", "flat", "unknown", "free"}
	upstreams := []string{"east", "west", "deleted"}
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC).UnixMilli()
	for i := 0; i < count; i++ {
		// Include exact threshold boundaries, cache > input, missing/zero usage,
		// failed/in-flight/pending requests, renamed entities and unknown routes.
		input := int64([]int{0, 1000, 272000, 272001, 400000}[i%5])
		cached := input * 9 / 10
		if i%17 == 0 {
			cached = input + 100
		}
		output, known, completed, pending, attempts, status := int64(i%1000), 1, 1, 0, 1, 200
		if i%7 == 0 {
			known = 0
			input, output, cached = 0, 0, 0
		}
		if i%11 == 0 {
			completed = 0
		}
		if i%13 == 0 {
			pending = 1
		}
		if i%19 == 0 {
			attempts = 0
		}
		if i%23 == 0 {
			status = 500
		}
		user, key := fmt.Sprintf("u%d", i%7), fmt.Sprintf("k%d", i%19)
		model, upstream := models[i%len(models)], upstreams[i%len(upstreams)]
		if i%41 == 0 {
			user, key, model, upstream = "", "", "", ""
		}
		if i%43 == 0 {
			model = "alias\x00east"
		}
		started := base - int64(i%95)*86400000 + int64(i%24)*3600000 - 1
		if i%47 == 0 {
			started = -int64(i%100000000) - 1
		}
		name := []string{"旧名称", "New", "z", ""}[i%4]
		if _, err = stmt.Exec(fmt.Sprint(i), started, user, name, key, name, model, "/v1/responses", upstream, name,
			status, attempts, completed, pending, input, output, cached, output/2, known); err != nil {
			tb.Fatal(err)
		}
	}
	if err = stmt.Close(); err != nil {
		tb.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	value := func(n float64) *float64 { return &n }
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	state := config.State{
		Upstreams: []config.Upstream{
			{ID: "east", Region: "us-east-1", Models: map[string]string{"alias": "actual"}},
			{ID: "west", Region: "us-west-2", Models: map[string]string{"alias": "actual"}},
		},
		ModelPrices: map[string]config.ModelPrice{
			"flat": {InputUSDPerMillion: 1.25, OutputUSDPerMillion: 5},
			"free": {},
		},
		PriceCatalog: &config.PriceCatalog{UpdatedAt: now, Entries: []config.ReferencePrice{
			{Model: "actual", Region: "us-east-1", Source: "aws", UpdatedAt: now, Price: config.ModelPrice{
				InputUSDPerMillion: 11, OutputUSDPerMillion: 55, CachedInputUSDPerMillion: value(1.1),
				LongContextThreshold: 272000, LongInputUSDPerMillion: value(22), LongOutputUSDPerMillion: value(82.5), LongCachedInputUSDPerMillion: value(2.2),
			}},
			{Model: "actual", Region: "us-west-2", Source: "aws", UpdatedAt: now, Price: config.ModelPrice{
				InputUSDPerMillion: 2.2, OutputUSDPerMillion: 11, CachedInputUSDPerMillion: value(0),
				LongContextThreshold: 272000, LongInputUSDPerMillion: value(4.4), LongOutputUSDPerMillion: value(16.5),
			}},
		}},
	}
	return s, state
}

func sameReport(t *testing.T, got, want Report) {
	t.Helper()
	compareCost := func(a, b *Cost) {
		t.Helper()
		// Grouping order may change floating-point rounding, not accounting.
		if math.Abs(a.EstimatedUSD-b.EstimatedUSD) > 1e-10*math.Max(1, math.Abs(b.EstimatedUSD)) {
			t.Fatalf("cost mismatch: got %+v, want %+v", *a, *b)
		}
		a.EstimatedUSD, b.EstimatedUSD = 0, 0
	}
	compareCost(&got.Cost, &want.Cost)
	for _, pair := range []struct{ a, b []Group }{{got.ByUser, want.ByUser}, {got.ByKey, want.ByKey}, {got.ByModel, want.ByModel}, {got.ByUpstream, want.ByUpstream}, {got.Daily, want.Daily}} {
		if len(pair.a) != len(pair.b) {
			t.Fatalf("group count mismatch: %d != %d", len(pair.a), len(pair.b))
		}
		for i := range pair.a {
			compareCost(&pair.a[i].Cost, &pair.b[i].Cost)
		}
	}
	if len(got.DailyByModel) != len(want.DailyByModel) {
		t.Fatal("daily/model count mismatch")
	}
	for i := range got.DailyByModel {
		compareCost(&got.DailyByModel[i].Cost, &want.DailyByModel[i].Cost)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report mismatch:\ngot: %+v\nwant: %+v", got, want)
	}
}

func TestSinglePassReportMatchesLegacy(t *testing.T) {
	for _, count := range []int{0, 2500} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, prices := reportFixture(t, count)
			for _, query := range []string{
				"all=true", "all=true&timezone_offset=480", "all=true&timezone_offset=-840", "all=true&timezone_offset=840",
				"all=true&user_id=u1", "all=true&key_id=k2", "all=true&upstream_id=west", "all=true&model=alias",
				"all=true&user_id=u1&key_id=k2&model=alias&upstream_id=east", "all=true&model=missing",
				"from=2026-10-01&to=2026-10-16&timezone_offset=480", "from=1969-12-31&to=1970-01-01&timezone_offset=-30",
			} {
				for _, priced := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/priced=%t", query, priced), func(t *testing.T) {
						values, _ := url.ParseQuery(query)
						filter, err := ParseFilter(values, time.Now())
						if err != nil {
							t.Fatal(err)
						}
						state := config.State{}
						if priced {
							state = prices
						}
						want, err := s.legacyReportWithPricing(context.Background(), filter, state)
						if err != nil {
							t.Fatal(err)
						}
						got, err := s.ReportWithPricing(context.Background(), filter, state)
						if err != nil {
							t.Fatal(err)
						}
						sameReport(t, got, want)
					})
				}
			}
		})
	}
}

func TestReportCancellationReleasesReader(t *testing.T) {
	s, state := reportFixture(t, 500)
	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := s.ReportWithPricing(ctx, Filter{All: true}, state)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.ReportWithPricing(ctx, Filter{All: true}, state); err != nil {
		t.Fatal(err)
	}
	if stats := s.reader.Stats(); stats.InUse != 0 {
		t.Fatalf("leaked readers: %+v", stats)
	}
}

func BenchmarkUsageReport(b *testing.B) {
	for _, count := range []int{20000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s, state := reportFixture(b, count)
			for _, legacy := range []bool{true, false} {
				name := "single_pass"
				if legacy {
					name = "v0.3.0"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						var r Report
						var err error
						if legacy {
							r, err = s.legacyReportWithPricing(context.Background(), Filter{All: true, TimezoneOffset: 480}, state)
						} else {
							r, err = s.ReportWithPricing(context.Background(), Filter{All: true, TimezoneOffset: 480}, state)
						}
						if err != nil {
							b.Fatal(err)
						}
						if r.Totals.Requests != int64(count) {
							b.Fatal("incorrect request count")
						}
					}
				})
			}
		})
	}
}

func TestReportRejectsTokenOverflow(t *testing.T) {
	for _, column := range []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens"} {
		t.Run(column, func(t *testing.T) {
			s, state := reportFixture(t, 2)
			if _, err := s.db.Exec("UPDATE requests SET "+column+"=?", int64(math.MaxInt64)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReportWithPricing(context.Background(), Filter{All: true}, state); err == nil {
				t.Fatal("overflowing token totals accepted")
			}
			if stats := s.reader.Stats(); stats.InUse != 0 {
				t.Fatalf("overflow leaked a reader: %+v", stats)
			}
		})
	}
}
