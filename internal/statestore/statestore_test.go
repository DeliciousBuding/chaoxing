package statestore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestProxyHealthRoundTrip(t *testing.T) {
	s, path := open(t)
	if m, err := s.ProxyHealth(); err != nil || len(m) != 0 {
		t.Fatal(m, err)
	}
	cool := time.Now().Add(time.Minute).Truncate(time.Second)
	if err := s.SetProxyHealth("p1", ProxyHealth{Failures: 3, CoolUntil: cool}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	m, err := s2.ProxyHealth()
	if err != nil {
		t.Fatal(err)
	}
	if h := m["p1"]; h.Failures != 3 || !h.CoolUntil.Equal(cool) {
		t.Fatalf("health not persisted: %+v", h)
	}
	if err := s2.SetProxyHealth("p1", ProxyHealth{}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s2.ProxyHealth(); len(m) != 0 {
		t.Fatal("healthy endpoint row should be deleted")
	}
	// Permission bits only apply on POSIX; Windows relies on directory ACLs.
	if info, err := os.Stat(path); (err != nil || info.Mode().Perm() != 0600) && runtime.GOOS != "windows" {
		t.Fatalf("file mode: %v %v", info.Mode(), err)
	}
}

func TestNewerSchemaRejected(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("expected newer schema to be rejected")
	}
}
