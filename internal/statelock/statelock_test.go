package statelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireRejectsSecondOwnerAndReleases(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrInUse) {
		t.Fatalf("second owner: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".chaoxing.lock"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock metadata=%v err=%v", info, err)
	}
}

func TestAcquireRejectsPublicDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir); err == nil {
		t.Fatal("public state directory accepted")
	}
}
