package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPortableBackupWithDifferentExternalMasterKeys(t *testing.T) {
	sourceDir, targetDir := t.TempDir(), t.TempDir()
	sourceMaster := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	targetMaster := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	source, err := Open(sourceDir, sourceMaster)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := source.CreateKey("original")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []UpstreamInput{
		{Upstream: Upstream{Name: "api", BaseURL: "https://example.test/v1", AuthMode: "api_key", Weight: 1, Enabled: true}, Credentials: Credentials{APIKey: "source-api-key"}},
		{Upstream: Upstream{Name: "role", BaseURL: "https://example.test/v1", AuthMode: "sigv4", Region: "us-west-2", Profile: "external-profile", Weight: 2, Enabled: false}},
	} {
		if _, err := source.SaveUpstream("", input); err != nil {
			t.Fatal(err)
		}
	}
	exported, err := source.Export()
	if err != nil {
		t.Fatal(err)
	}
	target, err := Open(targetDir, targetMaster)
	if err != nil {
		t.Fatal(err)
	}
	state, err := target.PrepareImport(exported)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Restore(state); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(targetDir, targetMaster)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := reopened.Export()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported, actual) {
		t.Fatal("portable state differs")
	}
	if _, ok := reopened.Authenticate(token); !ok {
		t.Fatal("original API key rejected")
	}
	if _, err := os.Stat(filepath.Join(targetDir, "master.key")); !os.IsNotExist(err) {
		t.Fatal("restore should not replace external master key")
	}
	if _, err := Open(targetDir, sourceMaster); err == nil {
		t.Fatal("source master key still decrypts destination")
	}
}
