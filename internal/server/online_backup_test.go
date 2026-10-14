package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/backup"
	"github.com/hnewcity/mantle-mask/internal/gateway"
)

func TestOnlineMigrationPreservesLiveStreamsAndDeduplicatesCatchUp(t *testing.T) {
	source := setup(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	mock := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"still live\"}}]}\n\n")
			w.(http.Flusher).Flush()
			close(started)
			<-release
			io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
		}
	})
	source.add("live", mock.URL+"/v1", "fixture-key", 1, nil)
	done := make(chan []byte, 1)
	go func() {
		resp := source.request("POST", "/v1/chat/completions", source.token, `{"model":"test","messages":[],"stream":true}`, nil)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		done <- body
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not start")
	}
	initial := exportFile(t, source)
	plain, err := backup.Decrypt(initial, backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := gateway.DecodeBackup(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Usage.Requests) != 1 || snapshot.Usage.Requests[0].Completed {
		t.Fatal("online snapshot must retain the in-flight state")
	}
	// Importing the source's own snapshot must not delete or overwrite its live row.
	restoreFile(t, source, initial, backupPassword, "RESTORE", adminTestToken, 200)
	active := usageReport(t, source, "/api/admin/usage?all=true", adminTestToken)
	if active.Totals.InFlightRequests != 1 || active.Totals.PendingSyncRequests != 0 {
		t.Fatalf("active local request was reclassified: %#v", active.Totals)
	}
	read(t, source.request("POST", "/v1/chat/completions", source.token, `{"model":"test","messages":[]}`, nil), 200)
	select {
	case <-done:
		t.Fatal("backup/restore unexpectedly closed the stream")
	default:
	}

	target := setup(t)
	restoreFile(t, target, initial, backupPassword, "RESTORE", adminTestToken, 200)
	pending := usageReport(t, target, "/api/admin/usage?all=true", adminTestToken)
	if pending.Totals.PendingSyncRequests != 1 || pending.Totals.InFlightRequests != 0 || pending.Totals.Errors != 0 {
		t.Fatalf("remote in-flight accounting is misleading: %#v", pending.Totals)
	}
	target.restart()
	pending = usageReport(t, target, "/api/admin/usage?all=true", adminTestToken)
	if pending.Totals.PendingSyncRequests != 1 || pending.Totals.Errors != 0 {
		t.Fatal("restart falsely marked the source stream as failed")
	}
	read(t, target.request("POST", "/v1/chat/completions", source.token, `{"model":"test","messages":[]}`, nil), 200)
	settings := target.store.Snapshot().Settings
	settings.MaxRetries = 5
	if err := target.store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	_, newToken, err := target.store.CreateKey("created after migration")
	if err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	select {
	case body := <-done:
		if !bytes.Contains(body, []byte("[DONE]")) || bytes.Contains(body, []byte("usage_persistence_failed")) {
			t.Fatalf("stream was interrupted: %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish")
	}
	// The client may read the last bytes just before the handler commits Finish.
	deadline := time.Now().Add(5 * time.Second)
	for usageReport(t, source, "/api/admin/usage?all=true", adminTestToken).Totals.InFlightRequests != 0 {
		if time.Now().After(deadline) {
			t.Fatal("source request did not finish accounting")
		}
		time.Sleep(time.Millisecond)
	}
	tail := exportFile(t, source)
	for _, file := range [][]byte{tail, tail, initial} {
		restoreFile(t, target, file, backupPassword, "RESTORE", adminTestToken, 200, "usage")
	}
	merged := usageReport(t, target, "/api/admin/usage?all=true", adminTestToken)
	if merged.Totals.Requests != 3 || merged.Totals.TotalTokens != 22 || merged.Totals.PendingSyncRequests != 0 || merged.Totals.InFlightRequests != 0 {
		t.Fatalf("catch-up lost/duplicated usage: %#v", merged.Totals)
	}
	read(t, target.request("GET", "/api/me", newToken, "", nil), 200)
	if target.store.Snapshot().Settings.MaxRetries != 5 {
		t.Fatal("usage-only import changed configuration")
	}
	target.restart()
	if usageReport(t, target, "/api/admin/usage?all=true", adminTestToken).Totals.TotalTokens != 22 {
		t.Fatal("merged usage not durable")
	}
}

func TestAcceptedRequestKeepsItsConfigurationAfterRestore(t *testing.T) {
	f := setup(t)
	mock := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer old-credential" {
			t.Error("accepted request lost original credentials")
		}
		io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3}}`)
	})
	f.add("old route", mock.URL+"/v1", "old-credential", 1, nil)
	key, state, ok := f.store.AuthenticateSnapshot(f.token)
	if !ok {
		t.Fatal("initial authorization failed")
	}
	empty := setup(t)
	archive := exportFile(t, empty)
	restoreFile(t, f, archive, backupPassword, "RESTORE", adminTestToken, 200)
	if _, ok := f.store.Authenticate(f.token); ok {
		t.Fatal("new requests must use the new configuration")
	}
	request, _ := http.NewRequest("POST", "http://gateway/v1/chat/completions", bytes.NewBufferString(`{"model":"test","messages":[]}`))
	response := httptest.NewRecorder()
	f.proxy.ProxyWithState(response, request, gateway.Principal{ID: key.ID, Name: key.Name, UserID: key.UserID}, state)
	if response.Code != 200 {
		t.Fatalf("old authorized request failed: %s", response.Body.String())
	}
	report := usageReport(t, f, "/api/admin/usage?all=true", adminTestToken)
	if report.Totals.TotalTokens != 5 {
		t.Fatal("accepted request lost usage")
	}
}

func TestConfigurationSnapshotCannotBeMutatedByRestore(t *testing.T) {
	f := setup(t)
	_, before, ok := f.store.AuthenticateSnapshot(f.token)
	if !ok {
		t.Fatal("auth failed")
	}
	exported, err := f.proxy.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	exported.Config.State.Settings.MaxRetries = 5
	prepared, err := f.proxy.PrepareBackup(exported)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.proxy.RestoreBackup(prepared); err != nil {
		t.Fatal(err)
	}
	if before.Settings.MaxRetries == 5 {
		t.Fatal("captured configuration was mutated")
	}
}
