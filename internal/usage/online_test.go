package usage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPinnedWALSnapshotDoesNotBlockAccounting(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "empty"}[empty], func(t *testing.T) {
			s, err := openForTest(t, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			first := Request{ID: "before", StartedAt: time.Now(), Status: 200}
			if !empty {
				if err := s.Begin(first); err != nil {
					t.Fatal(err)
				}
			}
			view, err := s.PrepareSnapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			if err := view.Pin(context.Background()); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				r := Request{ID: "after", StartedAt: time.Now(), Status: 200}
				err := s.Begin(r)
				if err == nil {
					err = s.Tokens(r.ID, Counters{InputTokens: 99})
				}
				if err == nil {
					err = s.Finish(r)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("snapshot held the writer connection")
			}
			pinned, err := view.Export(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if empty {
				expected = 0
			}
			if len(pinned.Requests) != expected {
				t.Fatal("snapshot included a post-checkpoint request")
			}
			actual, err := s.Export(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(actual.Requests) != expected+1 {
				t.Fatal("live accounting write was lost")
			}
		})
	}
}

func TestLiveMergeNeverOverwritesLocalInFlightAndNeverDoubleCounts(t *testing.T) {
	s, err := openForTest(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	r := Request{ID: "live", StartedAt: time.Now(), UserID: "user", KeyID: "key", Path: "/v1/responses", Attempts: 1, Status: 200}
	if err := s.Begin(r); err != nil {
		t.Fatal(err)
	}
	initial, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	incoming := initial
	incoming.Requests[0].Completed = true
	incoming.Requests[0].InputTokens = 999
	incoming.Requests[0].Status = 200
	if err := s.Merge(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	active, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.Requests[0].Completed || active.Requests[0].InputTokens != 0 {
		t.Fatal("import overwrote a live request")
	}
	if err := s.Tokens(r.ID, Counters{InputTokens: 15, OutputTokens: 7}); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(r); err != nil {
		t.Fatal(err)
	}
	completed, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Merge(ctx, completed); err != nil {
			t.Fatal(err)
		}
	}
	complete, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(complete.Requests) != 1 || complete.Requests[0].InputTokens != 15 {
		t.Fatal("repeat import counted usage twice")
	}
	conflicting := complete
	conflicting.Requests[0].UserID = "other-user"
	if !errors.Is(s.ValidateMerge(ctx, conflicting), ErrIdentityConflict) {
		t.Fatal("conflicting request owner accepted")
	}
}
