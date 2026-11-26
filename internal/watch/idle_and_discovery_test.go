package watch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

// flakySource returns a fixed error until it is cleared, so a test can drive
// consecutive discovery failures and a recovery.
type flakySource struct {
	items []upstream.Activity
	err   error
	calls int
}

func (f *flakySource) ListClassActivities(context.Context, string, string) ([]upstream.Activity, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func TestDiscoveryFailureAlertsOnlyAfterConsecutiveFailures(t *testing.T) {
	src := &flakySource{err: &upstream.Fault{Code: "UPSTREAM_UNAVAILABLE", Message: "synthetic timeout"}}
	sender := &fakeSender{}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3,
		Classes:   []Class{{CourseID: "c", ClassID: "k"}},
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}, src, sender, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A single transient failure must stay silent.
	r.Tick(context.Background())
	if len(sender.sent) != 0 {
		t.Fatalf("first failure must not alert: %+v", sender.sent)
	}

	// The second consecutive failure raises exactly one warning.
	r.Tick(context.Background())
	if len(sender.sent) != 1 || sender.sent[0].Title != "🚨 学习通签到发现异常提醒" {
		t.Fatalf("second failure should alert once: %+v", sender.sent)
	}

	// Further failures inside the same episode stay quiet.
	r.Tick(context.Background())
	if len(sender.sent) != 1 {
		t.Fatalf("repeated failure should not re-alert: %+v", sender.sent)
	}

	// Recovery closes the episode with one message and clears the counter.
	src.err = nil
	r.Tick(context.Background())
	if len(sender.sent) != 2 || sender.sent[1].Title != "✅ 学习通数据源已恢复" {
		t.Fatalf("recovery should be reported once: %+v", sender.sent)
	}
	if _, ok := r.state["feed:activities:c:k"]; ok {
		t.Fatal("failure state should be cleared after recovery")
	}

	// A later single failure starts a fresh episode and stays silent again.
	src.err = &upstream.Fault{Code: "UPSTREAM_UNAVAILABLE", Message: "again"}
	r.Tick(context.Background())
	if len(sender.sent) != 2 {
		t.Fatalf("new episode's first failure must stay silent: %+v", sender.sent)
	}
}

func TestIdleScanPollsOutsideScheduleAndRespectsCadence(t *testing.T) {
	src := &fakeSource{}
	sender := &fakeSender{}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3,
		Window:           "07:30-22:30",
		IdleScanInterval: 3 * time.Minute,
		Classes: []Class{{CourseID: "c", ClassID: "k", Schedule: []Schedule{
			{Days: []string{"Wed"}, Start: "09:00", End: "10:00"},
		}}},
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}, src, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Wednesday 12:00, after the class window and inside the daily window.
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }

	if got := r.nextDelay(now); got != 3*time.Minute {
		t.Fatalf("idle nextDelay = %s, want 3m", got)
	}

	// First idle tick sweeps the watched class even though it is not in session.
	r.Tick(context.Background())
	if src.calls != 1 {
		t.Fatalf("idle scan should poll once, got %d", src.calls)
	}

	// An immediate second tick is not due yet.
	r.Tick(context.Background())
	if src.calls != 1 {
		t.Fatalf("idle cadence respected: got %d calls", src.calls)
	}

	// After the interval elapses the sweep runs again.
	now = now.Add(3 * time.Minute)
	r.Tick(context.Background())
	if src.calls != 2 {
		t.Fatalf("idle scan should resume after the interval, got %d", src.calls)
	}
}

func TestIdleScanDoesNotPollOutsideDailyWindow(t *testing.T) {
	src := &fakeSource{}
	sender := &fakeSender{}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3,
		Window:           "07:30-22:30",
		IdleScanInterval: 3 * time.Minute,
		Classes: []Class{{CourseID: "c", ClassID: "k", Schedule: []Schedule{
			{Days: []string{"Wed"}, Start: "09:00", End: "10:00"},
		}}},
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}, src, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Wednesday 23:30, outside the daily window: no polling at all.
	now := time.Date(2026, 11, 25, 23, 30, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	if src.calls != 0 {
		t.Fatalf("idle scan must not poll outside the daily window, got %d", src.calls)
	}
}
