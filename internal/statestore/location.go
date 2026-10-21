package statestore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/location"
)

func (s *Store) ListPresets(ctx context.Context, account string) ([]location.Preset, error) {
	if strings.TrimSpace(account) == "" {
		return nil, errors.New("location account is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_key, label, address, latitude, longitude, coordinate_system, created_at, updated_at
		FROM location_presets WHERE account_key=? ORDER BY updated_at DESC, id DESC`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []location.Preset
	for rows.Next() {
		var item location.Preset
		var created, updated int64
		if err := rows.Scan(&item.ID, &item.Account, &item.Label, &item.Address, &item.Latitude, &item.Longitude, &item.CoordinateSystem, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(created, 0).UTC()
		item.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SavePreset(ctx context.Context, account string, preset location.Preset) (location.Preset, error) {
	normalized, err := location.ValidateAndNormalize(preset)
	if err != nil {
		return location.Preset{}, err
	}
	if strings.TrimSpace(account) == "" {
		return location.Preset{}, errors.New("location account is required")
	}
	now := time.Now().UTC()
	normalized.Account = account
	if normalized.ID > 0 {
		result, err := s.db.ExecContext(ctx, `UPDATE location_presets
			SET label=?, address=?, latitude=?, longitude=?, coordinate_system=?, updated_at=?
			WHERE id=? AND account_key=?`,
			normalized.Label, normalized.Address, normalized.Latitude, normalized.Longitude, normalized.CoordinateSystem, now.Unix(), normalized.ID, account)
		if err != nil {
			return location.Preset{}, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return location.Preset{}, errors.New("location preset not found")
		}
		normalized.CreatedAt = now
		normalized.UpdatedAt = now
		return normalized, nil
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO location_presets
		(account_key, label, address, latitude, longitude, coordinate_system, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		account, normalized.Label, normalized.Address, normalized.Latitude, normalized.Longitude, normalized.CoordinateSystem, now.Unix(), now.Unix())
	if err != nil {
		return location.Preset{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return location.Preset{}, err
	}
	normalized.ID = id
	normalized.CreatedAt = now
	normalized.UpdatedAt = now
	return normalized, nil
}

func (s *Store) DeletePreset(ctx context.Context, account string, id int64) error {
	if strings.TrimSpace(account) == "" || id <= 0 {
		return errors.New("location preset id is required")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM location_presets WHERE id=? AND account_key=?`, id, account)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("location preset not found")
	}
	return nil
}

func (s *Store) ListBindings(ctx context.Context, account string) ([]location.Binding, error) {
	if strings.TrimSpace(account) == "" {
		return nil, errors.New("location account is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT account_key, course_id, class_id, schedule_key, preset_id, updated_at
		FROM location_bindings WHERE account_key=? ORDER BY course_id, class_id, schedule_key`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []location.Binding
	for rows.Next() {
		var item location.Binding
		var updated int64
		if err := rows.Scan(&item.Account, &item.CourseID, &item.ClassID, &item.ScheduleKey, &item.PresetID, &updated); err != nil {
			return nil, err
		}
		item.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveBinding(ctx context.Context, account string, binding location.Binding) error {
	binding.Account = strings.TrimSpace(account)
	binding.CourseID = strings.TrimSpace(binding.CourseID)
	binding.ClassID = strings.TrimSpace(binding.ClassID)
	binding.ScheduleKey = strings.TrimSpace(binding.ScheduleKey)
	if binding.Account == "" || binding.CourseID == "" || binding.ClassID == "" || binding.PresetID <= 0 {
		return errors.New("location binding requires account, course_id, class_id and preset_id")
	}
	if len(binding.ScheduleKey) > 128 {
		return errors.New("location schedule_key is too long")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM location_presets WHERE id=? AND account_key=?`, binding.PresetID, account).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("location preset not found")
		}
		return err
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO location_bindings
		(account_key, course_id, class_id, schedule_key, preset_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_key, course_id, class_id, schedule_key) DO UPDATE SET
			preset_id=excluded.preset_id, updated_at=excluded.updated_at`,
		binding.Account, binding.CourseID, binding.ClassID, binding.ScheduleKey, binding.PresetID, now.Unix())
	return err
}

func (s *Store) DeleteBinding(ctx context.Context, account, courseID, classID, scheduleKey string) error {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(courseID) == "" || strings.TrimSpace(classID) == "" {
		return errors.New("location binding requires account, course_id and class_id")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM location_bindings
		WHERE account_key=? AND course_id=? AND class_id=? AND schedule_key=?`, account, courseID, classID, strings.TrimSpace(scheduleKey))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("location binding not found")
	}
	return nil
}

func (s *Store) Resolve(ctx context.Context, account, courseID, classID, scheduleKey string) (*location.Location, error) {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(courseID) == "" || strings.TrimSpace(classID) == "" {
		return nil, errors.New("location resolve requires account, course_id and class_id")
	}
	scheduleKey = strings.TrimSpace(scheduleKey)
	var row struct {
		latitude  float64
		longitude float64
		address   string
	}
	query := `SELECT p.latitude, p.longitude, p.address
		FROM location_bindings b JOIN location_presets p ON p.id=b.preset_id
		WHERE b.account_key=? AND b.course_id=? AND b.class_id=? AND b.schedule_key=?`
	keys := []string{scheduleKey}
	if legacy := legacyScheduleKey(scheduleKey); legacy != "" {
		keys = append(keys, legacy)
	}
	for _, key := range keys {
		err := s.db.QueryRowContext(ctx, query, account, courseID, classID, key).Scan(&row.latitude, &row.longitude, &row.address)
		if err == nil {
			return &location.Location{Latitude: row.latitude, Longitude: row.longitude, Address: row.address}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if scheduleKey == "" {
		return nil, nil
	}
	err := s.db.QueryRowContext(ctx, query, account, courseID, classID, "").Scan(&row.latitude, &row.longitude, &row.address)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &location.Location{Latitude: row.latitude, Longitude: row.longitude, Address: row.address}, nil
}

// legacyScheduleKey maps an auto-generated week-qualified binding key back to
// the pre-week format. Explicit schedule keys never carry the generated -W
// suffix, so this only applies to bindings created by older releases.
func legacyScheduleKey(key string) string {
	i := strings.LastIndex(key, "-W")
	if i <= 0 || i+2 >= len(key) {
		return ""
	}
	suffix := key[i+2:]
	for _, r := range suffix {
		switch {
		case r >= '0' && r <= '9':
		case r == '.' || r == ',' || r == '-':
		default:
			return ""
		}
	}
	return key[:i]
}
