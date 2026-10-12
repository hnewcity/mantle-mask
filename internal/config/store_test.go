package config

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSecretsAreEncryptedAndKeysAreHashed(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	secret := "sensitive-bedrock-token-for-test"
	id, err := store.SaveUpstream("", UpstreamInput{
		Upstream:    Upstream{Name: "test", BaseURL: "https://bedrock-mantle.us-east-1.api.aws", AuthMode: "api_key", Weight: 1, Enabled: true},
		Credentials: Credentials{APIKey: secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateKey("application")
	if err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disk, []byte(secret)) || bytes.Contains(disk, []byte(token)) {
		t.Fatal("plaintext secret on disk")
	}
	reopened, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := reopened.Credentials(reopened.Snapshot().Upstreams[0])
	if err != nil || credentials.APIKey != secret {
		t.Fatalf("decrypt: %v", err)
	}
	if _, ok := reopened.Authenticate(token); !ok {
		t.Fatal("key lost on restart")
	}
	view := reopened.Views()[0]
	if view.Secret != "" || !view.HasCredentials || view.BaseURL != "https://bedrock-mantle.us-east-1.api.aws/v1" {
		t.Fatalf("invalid view: %+v", view)
	}
	input := UpstreamInput{Upstream: view.Upstream}
	input.Name = "edited"
	if _, err := reopened.SaveUpstream(id, input); err != nil {
		t.Fatal(err)
	}
	credentials, err = reopened.Credentials(reopened.Snapshot().Upstreams[0])
	if err != nil || credentials.APIKey != secret {
		t.Fatal("blank secret did not preserve credentials")
	}
}

func TestConcurrentPersistenceAndRevocation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, _, err := s.CreateKey("test"); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	reopened, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().APIKeys) != 20 {
		t.Fatal("concurrent updates were lost")
	}
	key, token, err := s.CreateKey("revoke")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Authenticate(token); ok {
		t.Fatal("revoked key authenticated")
	}
}

func TestRejectInsecureAndMalformedUpstreams(t *testing.T) {
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"file:///tmp/a", "http://example.com/v1", "https://user:pass@example.com/v1", "https://example.com/v1?foo=bar", "https://example.com/v1/../secret", "https://example.com/v1#frag"} {
		t.Run(base, func(t *testing.T) {
			_, err := s.SaveUpstream("", UpstreamInput{Upstream: Upstream{Name: "bad", BaseURL: base, Weight: 1, AuthMode: "api_key"}, Credentials: Credentials{APIKey: "key"}})
			if err == nil {
				t.Fatal("invalid URL accepted")
			}
		})
	}
}

func TestMissingOrWrongMasterKeyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveUpstream("", UpstreamInput{Upstream: Upstream{Name: "a", BaseURL: "http://localhost:1111/v1", Weight: 1, AuthMode: "api_key"}, Credentials: Credentials{APIKey: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="); err == nil {
		t.Fatal("wrong key accepted")
	}
	if err := os.Remove(filepath.Join(dir, "master.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, ""); err == nil {
		t.Fatal("missing key silently regenerated")
	}
}
