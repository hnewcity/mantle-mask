package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

func TestRestoreJournalResumesAfterFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	_, originalToken, err := store.CreateKey("original")
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(store, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	before, err := g.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before.Config.State.Settings.MaxRetries = 4
	before.Usage.Requests = append(before.Usage.Requests, usage.BackupRequest{ID: "restored-request", Completed: true, Status: 200, Counters: usage.Counters{InputTokens: 123}, UsageKnown: true})
	before.Affinity[config.Hash("scope")] = Binding{UpstreamID: "removed-upstream", Fingerprint: config.Hash("old-upstream"), Model: "model", Expires: time.Now().Add(time.Hour)}
	prepared, err := g.PrepareBackup(before)
	if err != nil {
		t.Fatal(err)
	}
	_, extraToken, err := store.CreateKey("extra")
	if err != nil {
		t.Fatal(err)
	}
	// Force only the last file replacement to fail, after SQLite and config update.
	blocked := filepath.Join(dir, "affinity.json")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := g.RestoreBackup(prepared); err == nil {
		t.Fatal("expected affinity write failure")
	}
	if !g.RestorePending() {
		t.Fatal("partial restore must remain in maintenance")
	}
	journal, err := os.Stat(g.restorePath())
	if err != nil || journal.Mode().Perm() != 0600 {
		t.Fatalf("private recovery journal missing: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	store, err = config.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	g, err = New(store, dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.RestorePending() {
		t.Fatal("recovery did not finish")
	}
	if _, err := os.Stat(g.restorePath()); !os.IsNotExist(err) {
		t.Fatal("completed journal not removed")
	}
	if _, ok := store.Authenticate(originalToken); !ok {
		t.Fatal("original key lost")
	}
	if _, ok := store.Authenticate(extraToken); ok {
		t.Fatal("replacement did not remove destination key")
	}
	if store.Snapshot().Settings.MaxRetries != 4 {
		t.Fatal("settings not recovered")
	}
	snapshot, err := g.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Usage.Requests) != 1 || snapshot.Usage.Requests[0].InputTokens != 123 || len(snapshot.Affinity) != 1 {
		t.Fatal("recovery lost usage or affinity")
	}
}
