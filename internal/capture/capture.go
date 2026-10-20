// Package capture defines the private, sanitized production observation model
// used to reconstruct still-unsupported classroom H5 protocols.
package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const MaxPageJSONBytes = 256 * 1024

// Activity is one deduplicated activity observation. ActivityJSON and PageJSON
// contain only normalized, sanitized projections; raw HTTP bodies and hidden
// form values are never stored.
type Activity struct {
	AccountKey   string
	ActivityKey  string
	CourseID     string
	ClassID      string
	ActivityID   string
	Kind         string
	State        string
	UserState    string
	SignType     string
	Source       string
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	UpdateCount  int
	ActivityJSON json.RawMessage
	PageJSON     json.RawMessage
	PageError    string
}

type SaveResult struct {
	HadPage bool
}

type Store interface {
	SaveActivityCapture(ctx context.Context, item Activity) (SaveResult, error)
	SaveActivityPage(ctx context.Context, accountKey, activityKey string, pageJSON []byte, pageError string) error
	PruneActivityCaptures(ctx context.Context, cutoff time.Time) error
}

func Key(courseID, classID, activityID string) string {
	return courseID + ":" + classID + ":" + activityID
}

func (a Activity) Validate() error {
	for k, v := range map[string]string{
		"account":  a.AccountKey,
		"activity": a.ActivityKey,
		"course":   a.CourseID,
		"class":    a.ClassID,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("activity capture requires %s key", k)
		}
	}
	if a.FirstSeenAt.IsZero() || a.LastSeenAt.IsZero() || a.LastSeenAt.Before(a.FirstSeenAt) {
		return errors.New("activity capture timestamps are invalid")
	}
	if len(a.ActivityJSON) == 0 || !json.Valid(a.ActivityJSON) {
		return errors.New("activity capture payload is not valid JSON")
	}
	if len(a.ActivityJSON) > 64*1024 || len(a.PageJSON) > MaxPageJSONBytes {
		return errors.New("activity capture payload exceeds size limit")
	}
	return nil
}
