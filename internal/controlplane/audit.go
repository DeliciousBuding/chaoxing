package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

type AuditEvent struct {
	ID            int64           `json:"id"`
	CreatedAt     time.Time       `json:"created_at"`
	ActorID       int64           `json:"actor_id"`
	ActorUsername string          `json:"actor_username"`
	ActorRole     string          `json:"actor_role"`
	AccountKey    string          `json:"account_key,omitempty"`
	Category      string          `json:"category"`
	Action        string          `json:"action"`
	Target        string          `json:"target,omitempty"`
	Success       bool            `json:"success"`
	IP            string          `json:"ip,omitempty"`
	UserAgent     string          `json:"user_agent,omitempty"`
	RequestID     string          `json:"request_id,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
}

type AuditFilter struct {
	Category string
	Account  string
	Actor    string
	Limit    int
	Offset   int
}

func sanitizeAuditText(value string, max int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > max {
		runes := []rune(value)
		value = string(runes[:max])
	}
	return value
}

func validCategory(category string) bool {
	switch category {
	case "auth", "member", "account", "tool", "config", "security":
		return true
	default:
		return false
	}
}

func (s *Store) RecordAudit(ctx context.Context, event AuditEvent) error {
	if !validCategory(event.Category) || strings.TrimSpace(event.Action) == "" {
		return ErrInvalidUser
	}
	details := strings.TrimSpace(string(event.Details))
	if details == "" {
		details = "{}"
	}
	if len(details) > 4096 || !json.Valid([]byte(details)) {
		return ErrInvalidUser
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events
		(created_at, actor_id, actor_username, actor_role, account_key, category, action, target, success, ip, user_agent, request_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nowUnix(), event.ActorID, sanitizeAuditText(event.ActorUsername, 32), sanitizeAuditText(event.ActorRole, 16),
		sanitizeAuditText(event.AccountKey, 32), event.Category, sanitizeAuditText(event.Action, 64),
		sanitizeAuditText(event.Target, 128), event.Success, sanitizeAuditText(event.IP, 64),
		sanitizeAuditText(event.UserAgent, 256), sanitizeAuditText(event.RequestID, 64), details)
	return err
}

func (s *Store) ListAudit(ctx context.Context, filter AuditFilter) ([]AuditEvent, int64, error) {
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	where := []string{"1=1"}
	args := []any{}
	if filter.Category != "" && validCategory(filter.Category) {
		where = append(where, "category=?")
		args = append(args, filter.Category)
	}
	if filter.Account != "" {
		where = append(where, "account_key=?")
		args = append(args, sanitizeAuditText(filter.Account, 32))
	}
	if filter.Actor != "" {
		where = append(where, "actor_username=?")
		args = append(args, sanitizeAuditText(strings.ToLower(filter.Actor), 32))
	}
	clause := strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, actor_id, actor_username, actor_role,
		account_key, category, action, target, success, ip, user_agent, request_id, details
		FROM audit_events WHERE `+clause+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []AuditEvent{}
	for rows.Next() {
		var (
			item    AuditEvent
			created int64
			success int
			details string
		)
		if err := rows.Scan(&item.ID, &created, &item.ActorID, &item.ActorUsername, &item.ActorRole,
			&item.AccountKey, &item.Category, &item.Action, &item.Target, &success,
			&item.IP, &item.UserAgent, &item.RequestID, &details); err != nil {
			return nil, 0, err
		}
		item.CreatedAt = unixTime(created)
		item.Success = success != 0
		item.Details = json.RawMessage(details)
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) PruneAudit(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM audit_events WHERE created_at < ?`, before.Unix())
	return err
}
