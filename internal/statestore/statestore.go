// Package statestore provides the embedded SQLite persistence layer used for
// non-authoritative runtime state (egress proxy health). OAuth state and
// upstream sessions remain in their dedicated sealed stores and are not
// migrated here.
package statestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = abs
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("state directory %q unavailable", filepath.Base(dir))
	}
	// Set permissions before SQLite creates WAL/SHM sidecars; never follow a
	// database symlink or treat filename punctuation as URI parameters.
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular()) {
		return nil, errors.New("state database must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}
	q := url.Values{"_pragma": {"busy_timeout(1000)", "journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)", "wal_autocheckpoint(256)", "journal_size_limit(4194304)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection serializes all access and
	// avoids SQLITE_BUSY on the router-class deployments this targets.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("state schema version %d newer than supported 1", version)
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return err
		}
	}
	// IF NOT EXISTS alone also accepts a conflicting view or malformed table.
	rows, err := tx.QueryContext(ctx, "SELECT proxy_key, failures, cool_until FROM proxy_health LIMIT 0")
	if err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

// ProxyHealth returns persisted health for all known endpoints.
func (s *Store) ProxyHealth() (map[string]ProxyHealth, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT proxy_key, failures, cool_until FROM proxy_health")
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if h.Failures <= 0 && h.CoolUntil.IsZero() {
		_, err := s.db.ExecContext(ctx, "DELETE FROM proxy_health WHERE proxy_key = ?", key)
		return err
	}
	var cool int64
	if !h.CoolUntil.IsZero() {
		cool = h.CoolUntil.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO proxy_health (proxy_key, failures, cool_until) VALUES (?, ?, ?)
		ON CONFLICT(proxy_key) DO UPDATE SET failures = excluded.failures, cool_until = excluded.cool_until`, key, h.Failures, cool)
	return err
}
