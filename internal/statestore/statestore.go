// Package statestore provides the embedded SQLite persistence layer used for
// non-authoritative runtime state (egress proxy health). OAuth state and
// upstream sessions remain in their dedicated sealed stores and are not
// migrated here.
package statestore

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ProxyHealth is the persisted failure state of one egress endpoint.
type ProxyHealth struct {
	Failures  int
	CoolUntil time.Time // zero means healthy
}

type Store struct {
	db *sql.DB
}

// Open creates or opens the store at path, applying schema migrations. The
// file is created mode 0600 inside a 0700 directory.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("state directory %q unavailable", filepath.Base(dir))
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection serializes all access and
	// avoids SQLITE_BUSY on the router-class deployments this targets.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS proxy_health (
  proxy_key  TEXT PRIMARY KEY,
  failures   INTEGER NOT NULL DEFAULT 0,
  cool_until INTEGER NOT NULL DEFAULT 0
);`

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("state schema version %d newer than supported 1", version)
	}
	if version == 0 {
		if _, err := s.db.Exec(schemaV1); err != nil {
			return err
		}
		if _, err := s.db.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// ProxyHealth returns persisted health for all known endpoints.
func (s *Store) ProxyHealth() (map[string]ProxyHealth, error) {
	rows, err := s.db.Query("SELECT proxy_key, failures, cool_until FROM proxy_health")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ProxyHealth{}
	for rows.Next() {
		var key string
		var h ProxyHealth
		var cool int64
		if err := rows.Scan(&key, &h.Failures, &cool); err != nil {
			return nil, err
		}
		if cool > 0 {
			h.CoolUntil = time.Unix(cool, 0)
		}
		out[key] = h
	}
	return out, rows.Err()
}

// SetProxyHealth upserts one endpoint's health; failures <= 0 with no
// cooldown deletes the row so healthy state stays compact.
func (s *Store) SetProxyHealth(key string, h ProxyHealth) error {
	if key == "" {
		return errors.New("proxy key required")
	}
	if h.Failures <= 0 && h.CoolUntil.IsZero() {
		_, err := s.db.Exec("DELETE FROM proxy_health WHERE proxy_key = ?", key)
		return err
	}
	var cool int64
	if !h.CoolUntil.IsZero() {
		cool = h.CoolUntil.Unix()
	}
	_, err := s.db.Exec(`INSERT INTO proxy_health (proxy_key, failures, cool_until) VALUES (?, ?, ?)
		ON CONFLICT(proxy_key) DO UPDATE SET failures = excluded.failures, cool_until = excluded.cool_until`, key, h.Failures, cool)
	return err
}
