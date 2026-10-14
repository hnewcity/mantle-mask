package backup

import (
	"bytes"
	"testing"
)

func TestEncryptedBackupAuthentication(t *testing.T) {
	password := "migration-password-123"
	plain := []byte(`{"api_key":"secret-upstream-key","hash":"client-key-hash"}`)
	first, err := Encrypt(plain, password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encrypt(plain, password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte("secret-upstream-key")) {
		t.Fatal("backup leaks plaintext or reuses randomness")
	}
	decoded, err := Decrypt(first, password)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("round trip: %s %v", decoded, err)
	}
	if _, err := Decrypt(first, "wrong-password-123"); err == nil {
		t.Fatal("wrong password accepted")
	}
	for _, index := range []int{0, len(magic), len(first) - 1} {
		corrupt := append([]byte(nil), first...)
		corrupt[index] ^= 1
		if _, err := Decrypt(corrupt, password); err == nil {
			t.Fatalf("corruption accepted at %d", index)
		}
	}
	if _, err := Decrypt(first[:10], password); err == nil {
		t.Fatal("truncated backup accepted")
	}
	if _, err := Encrypt(plain, "short"); err == nil {
		t.Fatal("short password accepted")
	}
}
