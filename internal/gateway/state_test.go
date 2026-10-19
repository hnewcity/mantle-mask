package gateway

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRuntimeRingMatchesRetainedLogs(t *testing.T) {
	s := newRuntime()
	rng := rand.New(rand.NewSource(42))
	now := time.Date(2026, 10, 19, 12, 15, 30, 0, time.UTC)
	var retained []RequestLog
	var errors, tokens, latency int64
	capacity := 10
	for i := range 1000 {
		if i%19 == 0 {
			capacity = rng.Intn(25)
		}
		log := RequestLog{ID: fmt.Sprint(i), Time: now.Add(time.Duration(rng.Intn(2500)-2300) * time.Second), Status: 200, DurationMS: int64(i), InputTokens: 3, OutputTokens: 7}
		if i%3 == 0 {
			log.Status = 500
			errors++
		}
		latency += log.DurationMS
		tokens += 10
		retained = append(retained, log)
		if len(retained) > capacity {
			retained = retained[len(retained)-capacity:]
		}
		s.record(log, capacity)
		want := make([]RequestLog, len(retained))
		for j := range retained {
			want[len(retained)-j-1] = retained[j]
		}
		got := s.requestLogs()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order after record %d capacity %d", i, capacity)
		}
		m := s.metricsAt(now)
		if m.Requests != int64(i+1) || m.Errors != errors || m.Tokens != tokens || m.AvgLatencyMS != latency/int64(i+1) {
			t.Fatal("totals changed")
		}
		if !reflect.DeepEqual(m.Recent, want[:min(8, len(want))]) {
			t.Fatal("recent logs changed")
		}
		for _, b := range m.Timeline {
			requests, failures := 0, 0
			for _, l := range retained {
				if !l.Time.Before(b.Time) && l.Time.Before(b.Time.Add(time.Minute)) {
					requests++
					if l.Status >= 400 {
						failures++
					}
				}
			}
			if b.Requests != requests || b.Errors != failures {
				t.Fatalf("timeline mismatch: %+v", b)
			}
		}
		if len(got) > 0 {
			got[0].ID = "mutated"
			if s.requestLogs()[0].ID == "mutated" {
				t.Fatal("logs alias storage")
			}
		}
	}
}

func TestRuntimeTimelineBoundaries(t *testing.T) {
	s := newRuntime()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	start := now.Add(-29 * time.Minute)
	for _, at := range []time.Time{start.Add(-time.Nanosecond), start, now.Add(time.Minute - time.Nanosecond), now.Add(time.Minute)} {
		s.record(RequestLog{Time: at, Status: 500}, 8)
	}
	m := s.metricsAt(now)
	if m.Timeline[0].Requests != 1 || m.Timeline[29].Requests != 1 {
		t.Fatal("incorrect inclusive/exclusive bucket boundary")
	}
}

func TestRuntimeConcurrentReadersAndWriters(t *testing.T) {
	s := newRuntime()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				s.record(RequestLog{Time: time.Now()}, 100)
				s.metrics()
				s.requestLogs()
			}
		})
	}
	wg.Wait()
	if s.metrics().Requests != 400 {
		t.Fatal("lost requests")
	}
}
