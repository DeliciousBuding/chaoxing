// Package statestore provides the embedded SQLite persistence layer used for
// non-authoritative runtime state: egress health, sanitized activity capture
// and private location presets. OAuth state and upstream sessions remain in
// their dedicated sealed stores and are not migrated here.
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

	"github.com/DeliciousBuding/chaoxing/internal/capture"
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

const schemaV2 = `
CREATE TABLE IF NOT EXISTS activity_captures (
  account_key    TEXT NOT NULL,
  activity_key   TEXT NOT NULL,
  course_id      TEXT NOT NULL,
  class_id       TEXT NOT NULL,
  activity_id    TEXT NOT NULL,
  kind           TEXT NOT NULL,
  state          TEXT NOT NULL,
  user_state     TEXT NOT NULL,
  sign_type      TEXT NOT NULL,
  source         TEXT NOT NULL,
  first_seen_at  INTEGER NOT NULL,
  last_seen_at   INTEGER NOT NULL,
  update_count   INTEGER NOT NULL DEFAULT 1,
  activity_json  TEXT NOT NULL,
  page_json      TEXT,
  page_error     TEXT,
  PRIMARY KEY (account_key, activity_key)
);`

const schemaV3 = `
CREATE TABLE IF NOT EXISTS proxy_assignments (
  account_key TEXT PRIMARY KEY,
  proxy_key   TEXT NOT NULL,
  updated_at  INTEGER NOT NULL
);`

const schemaV4 = `
CREATE TABLE IF NOT EXISTS location_presets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_key TEXT NOT NULL,
  label TEXT NOT NULL,
  address TEXT NOT NULL DEFAULT '',
  latitude REAL NOT NULL,
  longitude REAL NOT NULL,
  coordinate_system TEXT NOT NULL DEFAULT 'bd09',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS location_presets_account_idx ON location_presets(account_key, updated_at DESC);

CREATE TABLE IF NOT EXISTS location_bindings (
  account_key TEXT NOT NULL,
  course_id TEXT NOT NULL,
  class_id TEXT NOT NULL,
  schedule_key TEXT NOT NULL DEFAULT '',
  preset_id INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (account_key, course_id, class_id, schedule_key),
  FOREIGN KEY (preset_id) REFERENCES location_presets(id) ON DELETE CASCADE
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
	if version > 4 {
		return fmt.Errorf("state schema version %d newer than supported 4", version)
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err := tx.ExecContext(ctx, schemaV2); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, schemaV3); err != nil {
			return err
		}
	}
	if version < 4 {
		if _, err := tx.ExecContext(ctx, schemaV4); err != nil {
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
	if err := validateActivityCaptureTable(ctx, tx); err != nil {
		return err
	}
	if err := validateProxyAssignmentTable(ctx, tx); err != nil {
		return err
	}
	if err := validateLocationTables(ctx, tx); err != nil {
		return err
	}
	if version < 4 {
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateActivityCaptureTable(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT account_key, activity_key, course_id, class_id, activity_id, kind,
		state, user_state, sign_type, source, first_seen_at, last_seen_at, update_count, activity_json
		FROM activity_captures LIMIT 0`)
	if err != nil {
		return err
	}
	return rows.Close()
}

func validateProxyAssignmentTable(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT account_key, proxy_key, updated_at FROM proxy_assignments LIMIT 0")
	if err != nil {
		return err
	}
	return rows.Close()
}

func validateLocationTables(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, account_key, label, address, latitude, longitude, coordinate_system, created_at, updated_at
		FROM location_presets LIMIT 0`)
	if err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT account_key, course_id, class_id, schedule_key, preset_id, updated_at
		FROM location_bindings LIMIT 0`)
	if err != nil {
		return err
	}
	return rows.Close()
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

// SaveActivityCapture upserts one sanitized activity observation. Existing page
// evidence is preserved when an observation refresh does not carry a new page.
func (s *Store) SaveActivityCapture(_ context.Context, item capture.Activity) (capture.SaveResult, error) {
	if err := item.Validate(); err != nil {
		return capture.SaveResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var hadPage bool
	if err := s.db.QueryRowContext(ctx, `SELECT page_json IS NOT NULL OR page_error IS NOT NULL
		FROM activity_captures WHERE account_key=? AND activity_key=?`, item.AccountKey, item.ActivityKey).Scan(&hadPage); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return capture.SaveResult{}, err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO activity_captures (
		account_key, activity_key, course_id, class_id, activity_id, kind, state, user_state, sign_type, source,
		first_seen_at, last_seen_at, update_count, activity_json, page_json, page_error
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, NULL, NULL)
	ON CONFLICT(account_key, activity_key) DO UPDATE SET
		kind=excluded.kind, state=excluded.state, user_state=excluded.user_state, sign_type=excluded.sign_type,
		source=excluded.source, last_seen_at=excluded.last_seen_at, update_count=activity_captures.update_count+1,
		activity_json=excluded.activity_json`,
		item.AccountKey, item.ActivityKey, item.CourseID, item.ClassID, item.ActivityID, item.Kind, item.State,
		item.UserState, item.SignType, item.Source, item.FirstSeenAt.Unix(), item.LastSeenAt.Unix(), string(item.ActivityJSON))
	if err != nil {
		return capture.SaveResult{}, err
	}
	return capture.SaveResult{HadPage: hadPage}, nil
}

// SaveActivityPage stores sanitized H5 evidence, or a safe capture error, for
// one already-known activity. It never overwrites existing evidence.
func (s *Store) SaveActivityPage(_ context.Context, accountKey, activityKey string, pageJSON []byte, pageError string) error {
	if accountKey == "" || activityKey == "" || (len(pageJSON) == 0 && strings.TrimSpace(pageError) == "") {
		return errors.New("activity page capture requires account, activity and evidence")
	}
	if len(pageJSON) > capture.MaxPageJSONBytes {
		return errors.New("activity page capture exceeds size limit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(ctx, `UPDATE activity_captures
		SET page_json=COALESCE(page_json, ?), page_error=CASE WHEN page_json IS NULL THEN ? ELSE page_error END,
		    last_seen_at=MAX(last_seen_at, ?)
		WHERE account_key=? AND activity_key=?`,
		nullableJSON(pageJSON), strings.TrimSpace(pageError), time.Now().Unix(), accountKey, activityKey)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return errors.New("activity capture not found")
	}
	return nil
}

// ListActivityCaptures returns newest-first sanitized captures for local
// protocol analysis, including retained page evidence when present.
func (s *Store) ListActivityCaptures(ctx context.Context, limit int) ([]capture.Activity, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("capture limit must be between 1 and 1000")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT account_key, activity_key, course_id, class_id, activity_id,
		kind, state, user_state, sign_type, source, first_seen_at, last_seen_at, update_count, activity_json,
		page_json, page_error FROM activity_captures ORDER BY last_seen_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []capture.Activity{}
	for rows.Next() {
		var item capture.Activity
		var first, last int64
		var activityJSON, pageJSON, pageError sql.NullString
		if err := rows.Scan(&item.AccountKey, &item.ActivityKey, &item.CourseID, &item.ClassID, &item.ActivityID,
			&item.Kind, &item.State, &item.UserState, &item.SignType, &item.Source, &first, &last, &item.UpdateCount,
			&activityJSON, &pageJSON, &pageError); err != nil {
			return nil, err
		}
		if activityJSON.Valid {
			item.ActivityJSON = []byte(activityJSON.String)
		}
		item.FirstSeenAt, item.LastSeenAt = time.Unix(first, 0), time.Unix(last, 0)
		if pageJSON.Valid {
			item.PageJSON = []byte(pageJSON.String)
		}
		item.PageError = pageError.String
		out = append(out, item)
	}
	return out, rows.Err()
}

// PruneActivityCaptures removes captures not observed since cutoff.
func (s *Store) PruneActivityCaptures(_ context.Context, cutoff time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, "DELETE FROM activity_captures WHERE last_seen_at < ?", cutoff.Unix())
	return err
}

// ProxyAssignments returns persisted per-account egress bindings.
func (s *Store) ProxyAssignments() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT account_key, proxy_key FROM proxy_assignments")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var account, endpoint string
		if err := rows.Scan(&account, &endpoint); err != nil {
			return nil, err
		}
		out[account] = endpoint
	}
	return out, rows.Err()
}

// SetProxyAssignment persists one account-to-egress binding.
func (s *Store) SetProxyAssignment(accountKey, endpointKey string) error {
	if accountKey == "" || endpointKey == "" {
		return errors.New("proxy assignment requires account and endpoint keys")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO proxy_assignments (account_key, proxy_key, updated_at)
		VALUES (?, ?, ?) ON CONFLICT(account_key) DO UPDATE SET proxy_key=excluded.proxy_key, updated_at=excluded.updated_at`,
		accountKey, endpointKey, time.Now().Unix())
	return err
}

// DeleteProxyAssignment removes an obsolete account binding.
func (s *Store) DeleteProxyAssignment(accountKey string) error {
	if accountKey == "" {
		return errors.New("proxy assignment account required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, "DELETE FROM proxy_assignments WHERE account_key=?", accountKey)
	return err
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
