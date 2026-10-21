package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/DeliciousBuding/chaoxing/internal/access"
)

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)
	accountPattern  = regexp.MustCompile(`^(root|[a-z0-9][a-z0-9-]{0,31})$`)
)

var dummyPasswordHash = sync.OnceValue(func() string {
	hash, _ := access.HashPassword("dummy-password-for-timing")
	return hash
})

type CreateUserInput struct {
	Username    string
	DisplayName string
	Password    string
	Role        string
	Accounts    []string
}

type UpdateUserInput struct {
	DisplayName *string
	Password    *string
	Role        *string
	Status      *string
	Accounts    *[]string
}

func (s *Store) Authenticate(ctx context.Context, username, password string) (User, error) {
	username = normalizeUsername(username)
	if username == "" {
		username = "owner"
	}
	var (
		user                        User
		hash                        string
		created, updated, lastLogin int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, username, display_name, password_hash, role, status,
		auth_version, created_at, updated_at, last_login_at FROM admin_users WHERE username=?`, username).
		Scan(&user.ID, &user.Username, &user.DisplayName, &hash, &user.Role, &user.Status,
			&user.AuthVersion, &created, &updated, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		_ = access.VerifyPassword(dummyPasswordHash(), password)
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	passwordOK := access.VerifyPassword(hash, password)
	if user.Status != StatusActive || !passwordOK {
		return User{}, ErrInvalidCredentials
	}
	now := nowUnix()
	if _, err := s.db.ExecContext(ctx, `UPDATE admin_users SET last_login_at=? WHERE id=?`, now, user.ID); err != nil {
		return User{}, err
	}
	last := unixTime(now)
	user.LastLoginAt = &last
	user.CreatedAt, user.UpdatedAt = unixTime(created), unixTime(updated)
	user.Accounts, err = s.userAccounts(ctx, user.ID)
	return user, err
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.userBy(ctx, `id=?`, id)
}

func (s *Store) userBy(ctx context.Context, where string, value any) (User, error) {
	var (
		user                        User
		created, updated, lastLogin int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, username, display_name, role, status,
		auth_version, created_at, updated_at, last_login_at FROM admin_users WHERE `+where, value).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.Status,
			&user.AuthVersion, &created, &updated, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	user.CreatedAt, user.UpdatedAt = unixTime(created), unixTime(updated)
	if lastLogin > 0 {
		last := unixTime(lastLogin)
		user.LastLoginAt = &last
	}
	user.Accounts, err = s.userAccounts(ctx, user.ID)
	return user, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, display_name, role, status,
		auth_version, created_at, updated_at, last_login_at FROM admin_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	byID := map[int64]int{}
	for rows.Next() {
		var (
			user                        User
			created, updated, lastLogin int64
		)
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.Status,
			&user.AuthVersion, &created, &updated, &lastLogin); err != nil {
			return nil, err
		}
		user.CreatedAt, user.UpdatedAt = unixTime(created), unixTime(updated)
		if lastLogin > 0 {
			last := unixTime(lastLogin)
			user.LastLoginAt = &last
		}
		byID[user.ID] = len(users)
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grants, err := s.db.QueryContext(ctx, `SELECT user_id, account_key FROM account_grants ORDER BY account_key`)
	if err != nil {
		return nil, err
	}
	defer grants.Close()
	for grants.Next() {
		var userID int64
		var key string
		if err := grants.Scan(&userID, &key); err != nil {
			return nil, err
		}
		if index, ok := byID[userID]; ok {
			users[index].Accounts = append(users[index].Accounts, key)
		}
	}
	return users, grants.Err()
}

func validateAccounts(accounts []string) ([]string, error) {
	out := make([]string, 0, len(accounts))
	seen := map[string]bool{}
	for _, account := range accounts {
		account = strings.TrimSpace(account)
		if !accountPattern.MatchString(account) {
			return nil, fmt.Errorf("invalid account key %q", account)
		}
		if seen[account] {
			continue
		}
		seen[account] = true
		out = append(out, account)
	}
	return out, nil
}

func (s *Store) CreateUser(ctx context.Context, input CreateUserInput) (User, error) {
	username := normalizeUsername(input.Username)
	display := strings.TrimSpace(input.DisplayName)
	if display == "" {
		display = username
	}
	if !usernamePattern.MatchString(username) || len(display) > 64 || !ValidRole(input.Role) || input.Role == RoleOwner {
		return User{}, ErrInvalidUser
	}
	accounts, err := validateAccounts(input.Accounts)
	if err != nil {
		return User{}, ErrInvalidUser
	}
	hash, err := access.HashPassword(input.Password)
	if err != nil {
		return User{}, ErrInvalidUser
	}
	now := nowUnix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO admin_users
		(username, display_name, password_hash, role, status, auth_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', 1, ?, ?)`, username, display, hash, input.Role, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrInvalidUser
		}
		return User{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	for _, account := range accounts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_grants(user_id, account_key, created_at) VALUES (?, ?, ?)`, id, account, now); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}

func (s *Store) UpdateUser(ctx context.Context, id int64, input UpdateUserInput) (User, error) {
	if input.DisplayName == nil && input.Password == nil && input.Role == nil && input.Status == nil && input.Accounts == nil {
		return User{}, ErrInvalidUser
	}
	current, err := s.UserByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	if current.Role == RoleOwner {
		if input.Role != nil && *input.Role != RoleOwner {
			return User{}, ErrInvalidUser
		}
		if input.Status != nil && *input.Status != StatusActive {
			return User{}, ErrInvalidUser
		}
	}
	if input.Role != nil && (!ValidRole(*input.Role) || *input.Role == RoleOwner) {
		return User{}, ErrInvalidUser
	}
	if input.Status != nil && !ValidStatus(*input.Status) {
		return User{}, ErrInvalidUser
	}
	var accounts []string
	if input.Accounts != nil {
		accounts, err = validateAccounts(*input.Accounts)
		if err != nil {
			return User{}, ErrInvalidUser
		}
	}
	now := nowUnix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if input.DisplayName != nil {
		display := strings.TrimSpace(*input.DisplayName)
		if display == "" || len(display) > 64 {
			return User{}, ErrInvalidUser
		}
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET display_name=? WHERE id=?`, display, id); err != nil {
			return User{}, err
		}
	}
	if input.Password != nil {
		hash, err := access.HashPassword(*input.Password)
		if err != nil {
			return User{}, ErrInvalidUser
		}
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET password_hash=? WHERE id=?`, hash, id); err != nil {
			return User{}, err
		}
	}
	if input.Role != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET role=? WHERE id=?`, *input.Role, id); err != nil {
			return User{}, err
		}
	}
	if input.Status != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET status=? WHERE id=?`, *input.Status, id); err != nil {
			return User{}, err
		}
	}
	if input.Accounts != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM account_grants WHERE user_id=?`, id); err != nil {
			return User{}, err
		}
		for _, account := range accounts {
			if _, err := tx.ExecContext(ctx, `INSERT INTO account_grants(user_id, account_key, created_at) VALUES (?, ?, ?)`, id, account, now); err != nil {
				return User{}, err
			}
		}
	}
	if input.Password != nil || input.Role != nil || input.Status != nil || input.Accounts != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET auth_version=auth_version+1, updated_at=? WHERE id=?`, now, id); err != nil {
			return User{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET updated_at=? WHERE id=?`, now, id); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}
