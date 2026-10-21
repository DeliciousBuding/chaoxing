package signaudit

import (
	"context"
	"sync/atomic"
)

// Metered wraps a Store and tracks consecutive audit write failures so the
// readiness endpoint can distinguish "sign-in worked, audit silently lost"
// from a healthy process.
type Metered struct {
	inner       Store
	consecutive atomic.Uint64
	lastError   atomic.Value
}

func NewMetered(inner Store) *Metered {
	return &Metered{inner: inner}
}

func (m *Metered) SaveSignAudit(ctx context.Context, record Record) error {
	if m == nil || m.inner == nil {
		return nil
	}
	err := m.inner.SaveSignAudit(ctx, record)
	if err != nil {
		m.consecutive.Add(1)
		m.lastError.Store(err.Error())
		return err
	}
	m.consecutive.Store(0)
	m.lastError.Store("")
	return nil
}

// Health returns the consecutive failure count and the most recent error.
func (m *Metered) Health() (uint64, string) {
	if m == nil {
		return 0, ""
	}
	message, _ := m.lastError.Load().(string)
	return m.consecutive.Load(), message
}
