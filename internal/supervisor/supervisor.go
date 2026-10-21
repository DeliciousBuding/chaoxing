// Package supervisor detects process-level crashes across procd restarts.
// It persists only a private, non-secret marker and turns unexpected exits
// into bounded operator notifications.
package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/persist"
)

const (
	stateVersion  = 1
	statusRunning = "running"
	statusClean   = "clean"
)

type stateFile struct {
	Version            int       `json:"version"`
	Status             string    `json:"status"`
	PID                int       `json:"pid"`
	StartedAt          time.Time `json:"started_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ConsecutiveCrashes int       `json:"consecutive_crashes"`
	LastCrashAt        time.Time `json:"last_crash_at,omitempty"`
	LastRunningAt      time.Time `json:"last_running_at,omitempty"`
}

// Metrics is safe for public health output: it contains only process
// identity, timestamps and counters.
type Metrics struct {
	PID                int       `json:"pid"`
	StartedAt          time.Time `json:"started_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ConsecutiveCrashes int       `json:"consecutive_crashes"`
	LastCrashAt        time.Time `json:"last_crash_at,omitempty"`
	LastRunningAt      time.Time `json:"last_running_at,omitempty"`
	LastAlertAt        time.Time `json:"last_alert_at,omitempty"`
	LastAlertError     string    `json:"last_alert_error,omitempty"`
	Degraded           bool      `json:"degraded,omitempty"`
}

type Config struct {
	Path string
	// Sender is optional. Crash counting still works without a notifier.
	Sender        notify.Sender
	Now           func() time.Time
	NotifyTimeout time.Duration
	// AlertRetryDelay overrides the base backoff between alert attempts.
	AlertRetryDelay time.Duration
	// ProcessID is used by tests; zero means the current process.
	ProcessID int
}

type Supervisor struct {
	mu            sync.Mutex
	path          string
	sender        notify.Sender
	now           func() time.Time
	notifyTimeout time.Duration
	state         stateFile
	alertDone     chan struct{}
	degraded      bool
	lastAlertAt   time.Time
	lastAlertErr  string
}

func Start(cfg Config) (*Supervisor, error) {
	if cfg.Path == "" {
		return nil, errors.New("supervisor state path is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	pid := cfg.ProcessID
	if pid == 0 {
		pid = os.Getpid()
	}
	timeout := cfg.NotifyTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	degraded := false
	previous, existed, err := readState(cfg.Path)
	if err != nil {
		// A corrupt or unreadable marker must never stop the daemon; reset the
		// crash history instead and report the degradation through metrics.
		log.Printf("supervisor state reset: %v", err)
		previous, existed, degraded = stateFile{}, false, true
	}
	state := stateFile{
		Version: stateVersion, Status: statusRunning, PID: pid,
		StartedAt: now().UTC(), UpdatedAt: now().UTC(),
	}
	if existed && previous.Status == statusRunning {
		state.ConsecutiveCrashes = previous.ConsecutiveCrashes + 1
		state.LastCrashAt = state.UpdatedAt
		state.LastRunningAt = previous.UpdatedAt
	} else if existed {
		state.LastCrashAt = previous.LastCrashAt
	}
	if err := writeState(cfg.Path, state); err != nil {
		log.Printf("supervisor state persistence degraded: %v", err)
		degraded = true
	}
	sender := cfg.Sender
	if sender != nil {
		delay := cfg.AlertRetryDelay
		if delay <= 0 {
			delay = 2 * time.Second
		}
		sender = notify.Retry{Sender: sender, Attempts: 3, Delay: delay}
	}
	s := &Supervisor{
		path: cfg.Path, sender: sender, now: now, notifyTimeout: timeout, state: state,
		degraded: degraded,
	}
	if state.ConsecutiveCrashes > 0 {
		// Crash alerts are asynchronous: a slow push provider must not delay
		// listener startup or procd's respawn threshold.
		s.alertDone = make(chan struct{})
		go func() {
			defer close(s.alertDone)
			s.sendCrashAlert(state)
		}()
	}
	return s, nil
}

// WaitForAlert waits for the asynchronous crash notification. It is primarily
// a test seam; production callers do not need to block on push delivery.
func (s *Supervisor) WaitForAlert(ctx context.Context) error {
	s.mu.Lock()
	done := s.alertDone
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readState(path string) (stateFile, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return stateFile{}, false, nil
	}
	if err != nil {
		return stateFile{}, false, fmt.Errorf("supervisor state read failed: %w", err)
	}
	var state stateFile
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return stateFile{}, false, errors.New("supervisor state is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return stateFile{}, false, errors.New("supervisor state is invalid")
	}
	if state.Version != stateVersion || state.Status == "" || state.PID <= 0 {
		return stateFile{}, false, errors.New("supervisor state is invalid")
	}
	return state, true, nil
}

func writeState(path string, state stateFile) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return persist.Write(path, b, 0o600)
}

func (s *Supervisor) Metrics() Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Metrics{
		PID: s.state.PID, StartedAt: s.state.StartedAt, UpdatedAt: s.state.UpdatedAt,
		ConsecutiveCrashes: s.state.ConsecutiveCrashes, LastCrashAt: s.state.LastCrashAt,
		LastRunningAt: s.state.LastRunningAt, LastAlertAt: s.lastAlertAt,
		LastAlertError: s.lastAlertErr, Degraded: s.degraded,
	}
}

// MarkClean records a SIGTERM-initiated shutdown. It deliberately breaks the
// consecutive-crash chain while retaining the last crash timestamp.
func (s *Supervisor) MarkClean() error {
	s.mu.Lock()
	now := s.now().UTC()
	s.state.Status = statusClean
	s.state.UpdatedAt = now
	s.state.LastRunningAt = now
	s.state.ConsecutiveCrashes = 0
	state := s.state
	s.mu.Unlock()
	return writeState(s.path, state)
}

func (s *Supervisor) sendCrashAlert(state stateFile) {
	if s.sender == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.notifyTimeout)
	defer cancel()
	priority := 9
	if state.ConsecutiveCrashes >= 3 {
		priority = 10
	}
	message := notify.Message{
		Title: "🚨 学习通进程异常退出后重启",
		Body: fmt.Sprintf(
			"连续异常退出：%d\n上次运行时间：%s\n当前 PID：%d\nprocd 已拉起新进程；若短时间内连续出现，请立即检查日志和部署。",
			state.ConsecutiveCrashes, state.LastRunningAt.Format(time.RFC3339), state.PID,
		),
		Priority: priority,
	}
	if err := s.sender.Send(ctx, message); err != nil {
		log.Printf("supervisor crash alert failed: %v", err)
		s.mu.Lock()
		s.lastAlertErr = "delivery_failed"
		s.mu.Unlock()
		return
	}
	log.Printf("supervisor crash alert delivered (consecutive=%d)", state.ConsecutiveCrashes)
	s.mu.Lock()
	s.lastAlertAt = s.now().UTC()
	s.lastAlertErr = ""
	s.mu.Unlock()
}
