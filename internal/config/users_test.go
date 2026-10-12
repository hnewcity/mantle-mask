package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyKeysMigrateAndUserLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	key, token, err := s.CreateKey("legacy")
	if err != nil {
		t.Fatal(err)
	}
	legacy := s.Snapshot()
	legacy.Version, legacy.Users, legacy.APIKeys[0].UserID = 1, nil, ""
	if err := AtomicJSON(filepath.Join(dir, "config.json"), legacy); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	migrated, ok := s.Authenticate(token)
	if !ok || migrated.ID != key.ID || migrated.UserID != DefaultUserID {
		t.Fatal("legacy token changed during migration")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	var disk State
	if json.Unmarshal(raw, &disk) != nil || disk.Version != 2 || len(disk.Users) != 1 {
		t.Fatal("migration not persisted")
	}
	u, err := s.SaveUser("", UserInput{Name: "Alice", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	k, token, err := s.CreateKey("work", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := s.CreateKey("second", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUser(u.ID, UserInput{Name: u.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{token, second} {
		if _, ok := s.Authenticate(token); ok {
			t.Fatal("disabled user authenticated")
		}
	}
	if _, _, err := s.CreateKey("invalid", u.ID); err == nil {
		t.Fatal("issued key for disabled user")
	}
	if _, err := s.SaveUser(u.ID, UserInput{Name: u.Name, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Authenticate(token); !ok {
		t.Fatal("user re-enable did not restore keys")
	}
	if err := s.DeleteKey(k.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Authenticate(token); ok {
		t.Fatal("revoked key authenticated")
	}
	if len(s.Keys(u.ID, false)) != 1 || len(s.Keys(u.ID, true)) != 2 {
		t.Fatal("revocation lost attribution metadata")
	}
	for _, k := range s.Keys("", true) {
		if k.Hash != "" {
			t.Fatal("key listing exposes hash")
		}
	}
}
