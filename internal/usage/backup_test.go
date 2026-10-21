package usage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/backup"
)

func TestBackupRoundTripAndTransactionalFailure(t *testing.T) {
	ctx := context.Background()
	source, err := openForTest(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	r := Request{ID: "req-original", StartedAt: time.Now().UTC(), UserID: "deleted-user", UserName: "Historical User", KeyID: "deleted-key", KeyName: "Historical Key", Model: "model", Path: "/v1/responses", UpstreamID: "deleted-upstream", UpstreamName: "Historical Upstream", Attempts: 2, Status: 200, DurationMS: 42}
	for _, err := range []error{source.Begin(r), source.Attempt(r), source.Tokens(r.ID, Counters{InputTokens: 100, OutputTokens: 40, CachedTokens: 25, ReasoningTokens: 8}), source.Bind("response-scope", r.ID), source.Finish(r)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	missing := r
	missing.ID = "req-missing"
	missing.Status = 502
	if err := source.Begin(missing); err != nil {
		t.Fatal(err)
	}
	if err := source.Attempt(missing); err != nil {
		t.Fatal(err)
	}
	if err := source.Finish(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := source.export(ctx, 50); !errors.Is(err, backup.ErrTooLarge) {
		t.Fatalf("expected bounded export failure: %v", err)
	}
	original, err := source.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := openForTest(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Restore(ctx, original); err != nil {
		t.Fatal(err)
	}
	restored, err := target.Export(ctx)
	if err != nil || !reflect.DeepEqual(original, restored) {
		t.Fatalf("usage changed: %v", err)
	}
	// A database error halfway through a restore must roll back deleted rows too.
	trigger := `CREATE TRIGGER reject_restore BEFORE INSERT ON requests WHEN NEW.id='reject' BEGIN SELECT RAISE(ABORT,'test failure'); END;`
	if target.sql.mysql {
		trigger = `CREATE TRIGGER reject_restore BEFORE INSERT ON requests FOR EACH ROW
			BEGIN IF NEW.id='reject' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'; END IF; END`
	}
	if _, err := target.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	broken := Backup{Requests: append([]BackupRequest{}, original.Requests...), Links: map[string]string{}}
	broken.Requests = append(broken.Requests, BackupRequest{ID: "reject"})
	if err := target.Restore(ctx, broken); err == nil {
		t.Fatal("expected restore failure")
	}
	restored, err = target.Export(ctx)
	if err != nil || !reflect.DeepEqual(original, restored) {
		t.Fatal("failed restore changed usage")
	}
	if err := target.Reconcile("response-scope", Counters{InputTokens: 120, OutputTokens: 50, CachedTokens: 30, ReasoningTokens: 10}); err != nil {
		t.Fatal(err)
	}
	restored, err = target.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Requests[1].InputTokens != 120 || restored.Requests[1].ReasoningTokens != 10 {
		t.Fatal("response usage link not retained")
	}
}

func TestBackupValidation(t *testing.T) {
	for _, b := range []Backup{
		{},
		{Requests: []BackupRequest{{ID: "same"}, {ID: "same"}}, Links: map[string]string{}},
		{Requests: []BackupRequest{{ID: "x", Counters: Counters{InputTokens: -1}}}, Links: map[string]string{}},
		{Requests: []BackupRequest{}, Links: map[string]string{"scope": "missing"}},
	} {
		if err := b.Validate(); err == nil {
			t.Fatalf("accepted invalid backup: %#v", b)
		}
	}
}
