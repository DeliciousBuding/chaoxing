package statestore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSpecialFilenamePragmasAndConcurrentAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health # & %.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mode string
	var checkpoint int
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatal(mode, err)
	}
	if err := s.db.QueryRow("PRAGMA wal_autocheckpoint").Scan(&checkpoint); err != nil || checkpoint != 256 {
		t.Fatal(checkpoint, err)
	}
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			key := fmt.Sprintf("endpoint-%d", worker)
			for range 30 {
				if err := s.SetProxyHealth(key, ProxyHealth{Failures: 1, CoolUntil: time.Now().Add(time.Minute)}); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.ProxyHealth(); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	var result string
	if err := s.db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatal(result, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.ProxyHealth()
	if err != nil || len(rows) != 8 {
		t.Fatal(len(rows), err)
	}
}

func TestMigrationFailureRollsBackSchemaVersion(t *testing.T) {
	s, _ := open(t)
	if _, err := s.db.Exec("PRAGMA user_version=0; DROP TABLE proxy_health; CREATE VIEW proxy_health AS SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err == nil {
		t.Fatal("conflicting schema accepted")
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatal(version, err)
	}
}

func TestDatabaseSymlinkRejected(t *testing.T) {
	s, path := open(t)
	s.Close()
	alias := filepath.Join(filepath.Dir(path), "alias.db")
	if err := os.Symlink(path, alias); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Open(alias); err == nil {
		t.Fatal("database symlink accepted")
	}
}

func BenchmarkProxyHealthWAL(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "health.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	h := ProxyHealth{Failures: 1, CoolUntil: time.Unix(2000000000, 0)}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := s.SetProxyHealth("endpoint", h); err != nil {
				b.Error(err)
			}
		}
	})
}
