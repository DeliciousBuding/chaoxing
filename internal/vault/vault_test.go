package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSealedCredentials(t *testing.T) {
	d := t.TempDir()
	p, k := filepath.Join(d, "c"), filepath.Join(d, "k")
	c := Credentials{"test-user", "test-secret"}
	if e := Seal(p, k, c); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if bytes.Contains(b, []byte(c.Password)) || bytes.Contains(b, []byte(c.Phone)) {
		t.Fatal("plaintext persisted")
	}
	got, e := Open(p, k)
	if e != nil || got != c {
		t.Fatalf("roundtrip: %v", e)
	}
	b[len(b)-1] ^= 1
	os.WriteFile(p, b, 0600)
	if _, e = Open(p, k); e == nil {
		t.Fatal("tamper accepted")
	}
}

func TestExistingKeyMustRemainPrivate(t *testing.T) {
	d := t.TempDir()
	keyPath := filepath.Join(d, "k")
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{1}, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenData(filepath.Join(d, "session"), keyPath); err == nil {
		t.Fatal("world-readable encryption key was accepted")
	}
}
func TestSessionPurposeIsolation(t *testing.T) {
	d := t.TempDir()
	p, k := filepath.Join(d, "s"), filepath.Join(d, "k")
	if e := SealData(p, k, []byte("test cookie")); e != nil {
		t.Fatal(e)
	}
	b, e := OpenData(p, k)
	if e != nil || string(b) != "test cookie" {
		t.Fatal(e)
	}
	if _, e := Open(p, k); e == nil {
		t.Fatal("session accepted as credentials")
	}
}
