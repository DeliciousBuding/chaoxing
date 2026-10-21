// Package controlplane stores platform identities, account grants and the
// control-plane audit trail. It never stores upstream credentials, cookies,
// coordinates or sign-in evidence; those stay in each tenant's state store.
package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	_ "modernc.org/sqlite"
)

const (
	RoleOwner    = "owner"
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"

	StatusActive   = "active"
	StatusDisabled = "disabled"

	schemaVersion = 1
)

var (
	ErrNotFound           = errors.New("control-plane record not found")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidUser        = errors.New("invalid user")
)

type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	AuthVersion int64      `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	Accounts    []string   `json:"accounts,omitempty"`
}

func (u User) Active() bool {
	return u.Status == StatusActive
}

func (u User) IsAdmin() bool {
	return u.Role == RoleOwner || u.Role == RoleAdmin
}

func ValidRole(role string) bool {
	switch role {
	case RoleOwner, RoleAdmin, RoleOperator, RoleViewer:
		return true
	default:
		return false
	}
}

func ValidStatus(status string) bool {
	return status == StatusActive || status == StatusDisabled
}

type Store struct {
	db   *sql.DB
	path string
}

// Open creates or opens the control-plane database. bootstrapOwnerHash seeds
// the single owner account only when the database has no users. The upstream
// owner password is never stored separately here.
func Open(path, bootstrapOwnerHash string) (*Store, error) {
	if !access.ValidPasswordHash(bootstrapOwnerHash) {
		return nil, errors.New("bootstrap owner password hash is invalid")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = abs
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("control-plane directory %q unavailable", filepath.Base(dir))
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("control-plane directory permissions must be 0700 (got %04o)", st.Mode().Perm())
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("control-plane database must be a regular file")
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
	u.RawQuery = url.Values{"_pragma": {
		"busy_timeout(1500)", "journal_mode(WAL)", "synchronous(NORMAL)",
		"foreign_keys(1)", "secure_delete(ON)", "cache_size(-1024)", "journal_size_limit(2097152)",
	}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.bootstrapOwner(bootstrapOwnerHash); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return errors.New("control-plane schema is newer than this binary")
	}
	if version == 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(schemaV1); err != nil {
			tx.Rollback()
			return err
		}
		if _, err = tx.Exec(`PRAGMA user_version = 1`); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS admin_users (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  username       TEXT NOT NULL UNIQUE,
  display_name   TEXT NOT NULL,
  password_hash  TEXT NOT NULL,
  role           TEXT NOT NULL CHECK (role IN ('owner','admin','operator','viewer')),
  status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  auth_version   INTEGER NOT NULL DEFAULT 1,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  last_login_at  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS account_grants (
  user_id      INTEGER NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
  account_key  TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (user_id, account_key)
);
CREATE TABLE IF NOT EXISTS audit_events (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at     INTEGER NOT NULL,
  actor_id       INTEGER NOT NULL DEFAULT 0,
  actor_username TEXT NOT NULL DEFAULT '',
  actor_role     TEXT NOT NULL DEFAULT '',
  account_key    TEXT NOT NULL DEFAULT '',
  category       TEXT NOT NULL,
  action         TEXT NOT NULL,
  target         TEXT NOT NULL DEFAULT '',
  success        INTEGER NOT NULL DEFAULT 0,
  ip             TEXT NOT NULL DEFAULT '',
  user_agent     TEXT NOT NULL DEFAULT '',
  request_id     TEXT NOT NULL DEFAULT '',
  details        TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_events(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_events(actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_account ON audit_events(account_key, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_category ON audit_events(category, created_at DESC);
`

func (s *Store) bootstrapOwner(hash string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		now := time.Now().Unix()
		if _, err := s.db.Exec(`INSERT INTO admin_users
			(username, display_name, password_hash, role, status, auth_version, created_at, updated_at)
			VALUES ('owner', 'Owner', ?, 'owner', 'active', 1, ?, ?)`, hash, now, now); err != nil {
			return err
		}
	} else {
		// The deployment config remains the owner-password source of truth. The
		// WebUI deliberately cannot rotate the owner password because the same
		// hash also guards OAuth consent; keeping the two from diverging is more
		// important than allowing an in-browser owner password reset.
		var ownerID int64
		err := s.db.QueryRow(`SELECT id FROM admin_users WHERE username='owner' AND role='owner'`).Scan(&ownerID)
		if err != nil {
			return errors.New("control plane owner record is missing")
		}
		if _, err := s.db.Exec(`UPDATE admin_users SET password_hash=?, auth_version=auth_version+1, updated_at=? WHERE id=?`, hash, nowUnix(), ownerID); err != nil {
			return err
		}
	}
	var owners int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_users WHERE role='owner' AND status='active'`).Scan(&owners); err != nil {
		return err
	}
	if owners == 0 {
		return errors.New("control plane has no active owner")
	}
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (s *Store) userAccounts(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_key FROM account_grants WHERE user_id=? ORDER BY account_key`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}
