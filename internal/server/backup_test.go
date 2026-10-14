package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hnewcity/mantle-mask/internal/backup"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
)

const backupPassword = "test-backup-password-123"

func exportFile(t *testing.T, f *fixture) []byte {
	t.Helper()
	response := f.request("POST", "/api/admin/backup/export", adminTestToken, `{"password":"`+backupPassword+`"}`, nil)
	if response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(response.Header.Get("Content-Disposition"), ".mmbak") {
		t.Fatal("missing private download headers")
	}
	return read(t, response, 200)
}

func restoreFile(t *testing.T, f *fixture, data []byte, password, confirm, token string, status int, modes ...string) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "backup.mmbak")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteField("password", password); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteField("confirm", confirm); err != nil {
		t.Fatal(err)
	}
	if len(modes) > 0 {
		if err = writer.WriteField("mode", modes[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return read(t, f.request("POST", "/api/admin/backup/restore", token, body.String(), map[string]string{"Content-Type": writer.FormDataContentType()}), status)
}

func TestCompleteBackupMigrationPreservesKeysCredentialsUsageAndAffinity(t *testing.T) {
	source := setup(t)
	mock := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret-for-backup" {
			t.Errorf("upstream credentials lost")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_backup","object":"response","status":"completed","model":"actual-model","usage":{"input_tokens":27,"output_tokens":13,"input_tokens_details":{"cached_tokens":7},"output_tokens_details":{"reasoning_tokens":5}},"output":[]}`)
	})
	source.add("Original upstream", mock.URL+"/v1", "upstream-secret-for-backup", 3, map[string]string{"alias": "actual-model"})
	_, err := source.store.SaveUpstream("", config.UpstreamInput{Upstream: config.Upstream{Name: "disabled SigV4", BaseURL: "https://example.test/v1", Region: "us-east-1", AuthMode: "sigv4", Weight: 2, Enabled: false}, Credentials: config.Credentials{AccessKeyID: "access-id", SecretAccessKey: "secret-access-key", SessionToken: "session-token"}})
	if err != nil {
		t.Fatal(err)
	}
	user, err := source.store.SaveUser("", config.UserInput{Name: "Disabled user", Note: "retain this note", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, disabledToken, err := source.store.CreateKey("disabled user key", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.store.SaveUser(user.ID, config.UserInput{Name: user.Name, Note: user.Note, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	revoked, revokedToken, err := source.store.CreateKey("revoked key")
	if err != nil {
		t.Fatal(err)
	}
	if err = source.store.DeleteKey(revoked.ID); err != nil {
		t.Fatal(err)
	}
	settings := source.store.Snapshot().Settings
	settings.MaxRetries = 4
	settings.LogCapacity = 321
	if err = source.store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	read(t, source.request("POST", "/v1/responses", source.token, `{"model":"alias","input":"test"}`, nil), 200)
	originalReport := usageReport(t, source, "/api/admin/usage?all=true", adminTestToken)
	if originalReport.Totals.TotalTokens != 40 {
		t.Fatalf("fixture usage missing: %#v", originalReport.Totals)
	}
	archive := exportFile(t, source)
	for _, secret := range []string{source.token, "upstream-secret-for-backup", "secret-access-key", backupPassword, adminTestToken} {
		if bytes.Contains(archive, []byte(secret)) {
			t.Fatal("backup leaked a plaintext secret")
		}
	}
	target := setup(t)
	oldTargetToken := target.token
	restoreFile(t, target, archive, backupPassword, "RESTORE", adminTestToken, 200)
	before, err := source.proxy.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after, err := target.proxy.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Config, after.Config) || !reflect.DeepEqual(before.Usage, after.Usage) || !reflect.DeepEqual(before.Affinity, after.Affinity) {
		t.Fatal("migration changed persistent state")
	}
	if source.store.Snapshot().Upstreams[0].Secret == target.store.Snapshot().Upstreams[0].Secret {
		t.Fatal("upstream credentials were not re-encrypted")
	}
	migratedReport := usageReport(t, target, "/api/admin/usage?all=true", adminTestToken)
	if !reflect.DeepEqual(originalReport, migratedReport) {
		t.Fatalf("usage changed: before=%#v after=%#v", originalReport, migratedReport)
	}
	for _, token := range []string{oldTargetToken, disabledToken, revokedToken} {
		read(t, target.request("GET", "/api/me", token, "", nil), 401)
	}
	read(t, target.request("GET", "/api/me", source.token, "", nil), 200)
	read(t, target.request("GET", "/v1/responses/resp_backup", source.token, "", nil), 200)
	target.restart()
	read(t, target.request("GET", "/api/admin/session", adminTestToken, "", nil), 200)
	read(t, target.request("GET", "/api/me", source.token, "", nil), 200)
	read(t, target.request("GET", "/v1/responses/resp_backup", source.token, "", nil), 200)
	if !reflect.DeepEqual(originalReport, usageReport(t, target, "/api/admin/usage?all=true", adminTestToken)) {
		t.Fatal("restart or response retrieval changed historical usage")
	}
}

func TestBackupValidationAndAuthorizationNeverMutateData(t *testing.T) {
	f := setup(t)
	archive := exportFile(t, f)
	initial, err := f.proxy.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", f.token} {
		read(t, f.request("POST", "/api/admin/backup/export", token, `{"password":"`+backupPassword+`"}`, nil), 401)
		restoreFile(t, f, archive, backupPassword, "RESTORE", token, 401)
	}
	read(t, f.request("POST", "/api/admin/backup/export", adminTestToken, `{"password":"short"}`, nil), 400)
	restoreFile(t, f, archive, "wrong-password-long", "RESTORE", adminTestToken, 400)
	restoreFile(t, f, archive, backupPassword, "", adminTestToken, 400)
	corrupt := append([]byte{}, archive...)
	corrupt[len(corrupt)-1] ^= 1
	restoreFile(t, f, corrupt, backupPassword, "RESTORE", adminTestToken, 400)
	restoreFile(t, f, archive[:12], backupPassword, "RESTORE", adminTestToken, 400)
	cases := []func(*gateway.Backup){
		func(b *gateway.Backup) { b.Version = 99 },
		func(b *gateway.Backup) { b.Usage.Requests = nil },
		func(b *gateway.Backup) { b.Config.State.APIKeys[0].Hash = "invalid" },
		func(b *gateway.Backup) { b.Config.State.Users = append(b.Config.State.Users, b.Config.State.Users[0]) },
		func(b *gateway.Backup) { b.Affinity = nil },
	}
	plain, err := backup.Decrypt(archive, backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range cases {
		snapshot, err := gateway.DecodeBackup(plain)
		if err != nil {
			t.Fatal(err)
		}
		change(&snapshot)
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		invalid, err := backup.Encrypt(data, backupPassword)
		if err != nil {
			t.Fatal(err)
		}
		restoreFile(t, f, invalid, backupPassword, "RESTORE", adminTestToken, 400)
	}
	after, err := f.proxy.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial.Config, after.Config) || !reflect.DeepEqual(initial.Usage, after.Usage) || !reflect.DeepEqual(initial.Affinity, after.Affinity) {
		t.Fatal("rejected restore changed data")
	}
}

func TestRestoreWriteFailureKeepsTrafficAndResumesOnRestart(t *testing.T) {
	f := setup(t)
	archive := exportFile(t, f)
	blocked := filepath.Join(f.dir, "affinity.json")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	restoreFile(t, f, archive, backupPassword, "RESTORE", adminTestToken, 500)
	read(t, f.request("GET", "/api/admin/settings", adminTestToken, "", nil), 200)
	read(t, f.request("GET", "/api/me", f.token, "", nil), 200)
	read(t, f.request("POST", "/api/admin/backup/export", adminTestToken, `{"password":"`+backupPassword+`"}`, nil), 409)
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	f.restart()
	read(t, f.request("GET", "/api/me", f.token, "", nil), 200)
	exportFile(t, f)
}

func TestConcurrentBackupReturnsConflict(t *testing.T) {
	f := setup(t)
	handler := New(f.store, f.proxy, adminTestToken, "")
	handler.backupMu.Lock()
	defer handler.backupMu.Unlock()
	request := httptest.NewRequest("POST", "/api/admin/backup/export", strings.NewReader(`{"password":"`+backupPassword+`"}`))
	request.Header.Set("Authorization", "Bearer "+adminTestToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != 409 {
		t.Fatalf("want 409 got %d", recorder.Code)
	}
}
