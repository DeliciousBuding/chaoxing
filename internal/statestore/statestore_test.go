package statestore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/capture"
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

func TestV1HealthSchemaMigratesToV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schemaV1); err != nil {
		t.Fatal(err)
	}
	cool := time.Now().Add(time.Minute).Truncate(time.Second)
	if _, err = db.Exec("INSERT INTO proxy_health(proxy_key,failures,cool_until) VALUES (?,?,?)", "legacy", 2, cool.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 4 {
		t.Fatal(version, err)
	}
	health, err := s.ProxyHealth()
	if err != nil || health["legacy"].Failures != 2 || !health["legacy"].CoolUntil.Equal(cool) {
		t.Fatal(health, err)
	}
	if _, err := s.ListActivityCaptures(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProxyAssignments(); err != nil {
		t.Fatal(err)
	}
}

func TestActivityCaptureRoundTripAndPrune(t *testing.T) {
	s, _ := open(t)
	now := time.Now().UTC().Truncate(time.Second)
	item := capture.Activity{
		AccountKey: "root", ActivityKey: "c:k:1", CourseID: "c", ClassID: "k", ActivityID: "1",
		Kind: "practice", State: "active", UserState: "unsigned", SignType: "unknown", Source: "class",
		FirstSeenAt: now, LastSeenAt: now, ActivityJSON: []byte(`{"id":"1"}`),
	}
	if _, err := s.SaveActivityCapture(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	item.State = "finished"
	item.LastSeenAt = now.Add(time.Minute)
	if _, err := s.SaveActivityCapture(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveActivityPage(context.Background(), "root", "c:k:1", []byte(`{"forms":[]}`), ""); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListActivityCaptures(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UpdateCount != 2 || rows[0].State != "finished" || len(rows[0].PageJSON) == 0 {
		t.Fatalf("%+v", rows)
	}
	if err := s.PruneActivityCaptures(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rows, err = s.ListActivityCaptures(context.Background(), 10); err != nil || len(rows) != 0 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
}

func TestActivityPageCaptureDoesNotOverwriteEvidence(t *testing.T) {
	s, _ := open(t)
	now := time.Now().UTC().Truncate(time.Second)
	item := capture.Activity{
		AccountKey: "root", ActivityKey: "c:k:1", CourseID: "c", ClassID: "k", ActivityID: "1",
		Kind: "practice", State: "active", UserState: "unsigned", Source: "class",
		FirstSeenAt: now, LastSeenAt: now, ActivityJSON: []byte(`{"id":"1"}`),
	}
	if _, err := s.SaveActivityCapture(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveActivityPage(context.Background(), "root", "c:k:1", []byte(`{"first":true}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveActivityPage(context.Background(), "root", "c:k:1", []byte(`{"second":true}`), ""); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListActivityCaptures(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(rows[0].PageJSON) != `{"first":true}` {
		t.Fatal(string(rows[0].PageJSON))
	}
}

func TestProxyAssignmentRoundTrip(t *testing.T) {
	s, _ := open(t)
	if err := s.SetProxyAssignment("root", "socks5:abc123"); err != nil {
		t.Fatal(err)
	}
	m, err := s.ProxyAssignments()
	if err != nil || m["root"] != "socks5:abc123" {
		t.Fatal(m, err)
	}
	if err := s.DeleteProxyAssignment("root"); err != nil {
		t.Fatal(err)
	}
	if m, err = s.ProxyAssignments(); err != nil || len(m) != 0 {
		t.Fatal(m, err)
	}
}
