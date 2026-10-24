package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/backup"
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
	g, err := New(store, dir, usage.Options{})
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
	g, err = New(store, dir, usage.Options{})
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

func TestSnapshotDoesNotSplitResponseUsageLinkAndAffinity(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(store, dir, usage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := g.usage.Begin(usage.Request{ID: "response-request", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	scope := config.Hash("response-scope")
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- g.usage.BindWith(scope, "response-request", func() error {
			close(entered)
			<-release
			return g.affinity.put(scope, Binding{UpstreamID: "old-upstream", Fingerprint: config.Hash("old"), Expires: time.Now().Add(time.Hour)})
		})
	}()
	<-entered
	exported := make(chan Backup, 1)
	errors := make(chan error, 1)
	go func() { b, err := g.ExportBackup(context.Background()); exported <- b; errors <- err }()
	select {
	case <-exported:
		close(release)
		t.Fatal("snapshot split the routing event")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	snapshot := <-exported
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if snapshot.Usage.Links[scope] != "response-request" || snapshot.Affinity[scope].UpstreamID != "old-upstream" {
		t.Fatal("incoherent response snapshot")
	}
}

func TestOldRequestCooldownCannotDisableRestoredCredentials(t *testing.T) {
	runtime := newRuntime()
	old := config.Upstream{ID: "upstream", BaseURL: "https://example.test/v1", AuthMode: "api_key", Secret: "old", Enabled: true, Weight: 1}
	current := old
	current.Secret = "restored"
	runtime.mark(old, 503, 0, time.Minute, "old upstream failed")
	if _, ok := runtime.choose([]config.Upstream{current}, "", map[string]bool{}, ""); !ok {
		t.Fatal("old request poisoned restored upstream health")
	}
	if _, ok := runtime.choose([]config.Upstream{old}, "", map[string]bool{}, ""); ok {
		t.Fatal("old generation lost its cooldown")
	}
}

func TestStreamedArchiveMatchesMarshaledBackup(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateKey("streamed"); err != nil {
		t.Fatal(err)
	}
	g, err := New(store, dir, usage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for i := range 3 {
		id := fmt.Sprint("request-", i)
		if err := g.usage.Begin(usage.Request{ID: id, StartedAt: time.Now(), UserName: "名字 <&>"}); err != nil {
			t.Fatal(err)
		}
		if err := g.usage.BindWith(config.Hash(id), id, func() error {
			return g.affinity.put(config.Hash(id), Binding{UpstreamID: "up", Fingerprint: config.Hash("f"), Expires: time.Now().Add(time.Hour)})
		}); err != nil {
			t.Fatal(err)
		}
	}
	const password = "streamed-password-123"
	archive, err := g.ExportBackupArchive(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := backup.Decrypt(archive, password)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(plain) {
		t.Fatalf("invalid JSON: %s", plain)
	}
	streamed, err := DecodeBackup(plain)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := g.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	streamed.CreatedAt, direct.CreatedAt = time.Time{}, time.Time{}
	// Compare encodings: decoded times carry no monotonic reading.
	a, _ := json.Marshal(streamed)
	b, _ := json.Marshal(direct)
	if string(a) != string(b) || len(direct.Usage.Requests) != 3 || len(direct.Usage.Links) != 3 {
		t.Fatalf("streamed archive differs:\n%s\n%s", a, b)
	}
}

func TestDecodeBackupStaysStrict(t *testing.T) {
	valid := `{"format":"mantle-mask","version":2,"created_at":"2026-01-01T00:00:00Z","config":{"state":{"version":2},"credentials":{}},"usage":{"requests":[{"id":"r","status":200}],"response_links":{"s":"r"}},"affinity":{}}`
	b, err := DecodeBackup([]byte(valid))
	if err != nil || len(b.Usage.Requests) != 1 || b.Usage.Requests[0].Status != 200 || b.Usage.Links["s"] != "r" || b.Affinity == nil {
		t.Fatalf("valid backup: %+v %v", b, err)
	}
	// encoding/json matches field names case-insensitively.
	if b, err := DecodeBackup([]byte(strings.Replace(valid, `"usage"`, `"Usage"`, 1))); err != nil || len(b.Usage.Requests) != 1 {
		t.Fatalf("case-insensitive field: %v", err)
	}
	for _, invalid := range []string{
		strings.Replace(valid, `"format"`, `"extra":1,"format"`, 1),
		strings.Replace(valid, `"status":200`, `"status":200,"prompt":"x"`, 1),
		strings.Replace(valid, `"response_links"`, `"links_extra":{},"response_links"`, 1),
		strings.Replace(valid, `"requests":[`, `"requests":{`, 1),
		valid + `{}`,
		valid[:len(valid)-20],
	} {
		if _, err := DecodeBackup([]byte(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}
