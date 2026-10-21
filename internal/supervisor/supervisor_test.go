package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
)

type recordingSender struct {
	mu       sync.Mutex
	messages []notify.Message
	err      error
}

func (r *recordingSender) Send(_ context.Context, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.messages = append(r.messages, m)
	return nil
}

func fixedClock(start time.Time) func() time.Time {
	return func() time.Time { return start }
}

func waitForAlert(t *testing.T, s *Supervisor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.WaitForAlert(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFirstStartIsClean(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "supervisor-state.json")
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	sender := &recordingSender{}
	s, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(now), ProcessID: 101})
	if err != nil {
		t.Fatal(err)
	}
	m := s.Metrics()
	if m.ConsecutiveCrashes != 0 || m.PID != 101 || m.StartedAt != now || m.UpdatedAt != now {
		t.Fatal(m)
	}
	if len(sender.messages) != 0 {
		t.Fatal(sender.messages)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
}

func TestAbruptRestartCountsAndAlerts(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "supervisor-state.json")
	base := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	sender := &recordingSender{}
	if _, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(base), ProcessID: 101}); err != nil {
		t.Fatal(err)
	}
	crashAt := base.Add(time.Minute)
	s, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(crashAt), ProcessID: 102})
	if err != nil {
		t.Fatal(err)
	}
	waitForAlert(t, s)
	m := s.Metrics()
	if m.ConsecutiveCrashes != 1 || m.PID != 102 || m.LastCrashAt != crashAt || m.LastRunningAt != base {
		t.Fatal(m)
	}
	if len(sender.messages) != 1 {
		t.Fatal(sender.messages)
	}
	if sender.messages[0].Priority != 9 {
		t.Fatal(sender.messages[0])
	}

	// A clean SIGTERM shutdown breaks the crash chain but retains history.
	if err := s.MarkClean(); err != nil {
		t.Fatal(err)
	}
	cleanAt := base.Add(2 * time.Minute)
	s, err = Start(Config{Path: path, Sender: sender, Now: fixedClock(cleanAt), ProcessID: 103})
	if err != nil {
		t.Fatal(err)
	}
	m = s.Metrics()
	if m.ConsecutiveCrashes != 0 || m.LastCrashAt != crashAt {
		t.Fatal(m)
	}
	if len(sender.messages) != 1 {
		t.Fatal(sender.messages)
	}
}

func TestThirdConsecutiveCrashUsesMaximumPriority(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "supervisor-state.json")
	base := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	sender := &recordingSender{}
	var current *Supervisor
	for i := 0; i < 4; i++ {
		s, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(base.Add(time.Duration(i) * time.Minute)), ProcessID: 110 + i})
		if err != nil {
			t.Fatal(err)
		}
		current = s
	}
	waitForAlert(t, current)
	if len(sender.messages) != 3 {
		t.Fatal(sender.messages)
	}
	last := sender.messages[2]
	if last.Priority != 10 || !strings.Contains(last.Body, "连续异常退出：3") {
		t.Fatal(last)
	}
}

func TestSlowNotifierDoesNotBlockStartup(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "supervisor-state.json")
	base := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	sender := &slowSender{}
	if _, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(base), ProcessID: 140}); err != nil {
		t.Fatal(err)
	}
	s, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(base.Add(time.Minute)), ProcessID: 141})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.WaitForAlert(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.sends != 1 {
		t.Fatal(sender.sends)
	}
}

type slowSender struct {
	mu    sync.Mutex
	sends int
}

func (s *slowSender) Send(ctx context.Context, _ notify.Message) error {
	select {
	case <-time.After(50 * time.Millisecond):
		s.mu.Lock()
		s.sends++
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestNotificationFailureDoesNotBlockStartup(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "supervisor-state.json")
	base := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	sender := &recordingSender{err: errors.New("network unavailable")}
	if _, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(base), ProcessID: 120}); err != nil {
		t.Fatal(err)
	}
	s, err := Start(Config{Path: path, Sender: sender, Now: fixedClock(base.Add(time.Minute)), ProcessID: 121})
	if err != nil {
		t.Fatal(err)
	}
	waitForAlert(t, s)
}

func TestInvalidStateIsRejected(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "supervisor-state.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"status":"running","pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(Config{Path: path, Now: fixedClock(time.Now().UTC()), ProcessID: 130}); err == nil {
		t.Fatal("invalid state accepted")
	}
}
