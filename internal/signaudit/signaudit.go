// Package signaudit defines the private, bounded audit record for one sign-in
// attempt. It is written to the local state store and is never part of the
// public tool contract.
package signaudit

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Record captures where a sign-in was sent from and which location/address was
// submitted. IP values are best-effort observations; failures leave them empty
// rather than blocking the sign-in.
type Record struct {
	AccountKey   string    `json:"account"`
	CourseID     string    `json:"course_id"`
	ClassID      string    `json:"class_id"`
	ActivityID   string    `json:"activity_id"`
	Method       string    `json:"method"`
	State        string    `json:"state"`
	Code         string    `json:"code,omitempty"`
	Verified     bool      `json:"verified"`
	EgressIP     string    `json:"egress_ip,omitempty"`
	EgressSource string    `json:"egress_ip_source,omitempty"`
	Address      string    `json:"address,omitempty"`
	Latitude     *float64  `json:"latitude,omitempty"`
	Longitude    *float64  `json:"longitude,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Store interface {
	SaveSignAudit(ctx context.Context, record Record) error
}

func (r Record) Validate() error {
	for name, value := range map[string]string{
		"account": r.AccountKey, "course": r.CourseID, "class": r.ClassID, "activity": r.ActivityID,
	} {
		if strings.TrimSpace(value) == "" {
			return errors.New("sign audit requires " + name)
		}
	}
	if len(r.Method) > 32 || len(r.State) > 64 || len(r.Code) > 64 || len(r.Address) > 256 || len(r.EgressIP) > 64 || len(r.EgressSource) > 64 {
		return errors.New("sign audit field exceeds limit")
	}
	if r.CreatedAt.IsZero() {
		return errors.New("sign audit timestamp is required")
	}
	return nil
}
