package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUpdateUpstreamAPIKeyPreservesConfigurationAndPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.SaveUpstream("", UpstreamInput{
		Upstream: Upstream{Name: "temporary", BaseURL: "https://example.test/openai/v1", Region: "us-west-2",
			AuthMode: "api_key", SigningService: "bedrock", Profile: "profile", Project: "project",
			Weight: 7, Enabled: false, Models: map[string]string{"alias": "model"}},
		Credentials: Credentials{APIKey: "old-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	if err := s.UpdateUpstreamAPIKey(id, "  new-temporary-key\n"); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot()
	oldCredentials, err := s.Credentials(before.Upstreams[0])
	if err != nil || oldCredentials.APIKey != "old-key" {
		t.Fatalf("in-flight snapshot changed: %v", err)
	}
	newCredentials, err := s.Credentials(after.Upstreams[0])
	if err != nil || newCredentials.APIKey != "new-temporary-key" {
		t.Fatalf("new credential not published: %v", err)
	}
	unchanged := s.Snapshot()
	unchanged.Upstreams[0].Secret = before.Upstreams[0].Secret
	if !reflect.DeepEqual(unchanged, before) {
		t.Fatal("rotation changed other configuration")
	}
	disk, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disk, []byte("new-temporary-key")) || bytes.Contains(disk, []byte("old-key")) {
		t.Fatal("plaintext API key on disk")
	}
	reopened, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reopened.Snapshot(), after) {
		t.Fatal("rotation not persisted")
	}

	// A failed disk write must not switch the live credential either.
	s.path = filepath.Join(t.TempDir(), "missing", "config.json")
	if err := s.UpdateUpstreamAPIKey(id, "unsaved-key"); err == nil {
		t.Fatal("write failure ignored")
	}
	if !reflect.DeepEqual(s.Snapshot(), after) {
		t.Fatal("failed rotation changed live state")
	}
}
