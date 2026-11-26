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

type perClassFlakySource struct {
	errByClass map[string]error
	itemsByCls map[string][]upstream.Activity
	callsByCls map[string]int
	groupItems []upstream.Activity
	groupCalls int
}

func (s *perClassFlakySource) ListClassActivities(_ context.Context, _, classID string) ([]upstream.Activity, error) {
	if s.callsByCls == nil {
		s.callsByCls = map[string]int{}
	}
	s.callsByCls[classID]++
	if err := s.errByClass[classID]; err != nil {
		return nil, err
	}
	return s.itemsByCls[classID], nil
}

func (s *perClassFlakySource) ListGroupActivities(context.Context) ([]upstream.Activity, error) {
	s.groupCalls++
	return s.groupItems, nil
}

func TestIdleScanSkipsCoolingClassAndDowngradesIdleFailureAlert(t *testing.T) {
	src := &perClassFlakySource{
		errByClass: map[string]error{
			"k1": &upstream.Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: 600, RetryAfterSource: "local_backoff"},
		},
	}
	sender := &fakeSender{}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3,
		Window:           "07:30-22:30",
		IdleScanInterval: 3 * time.Minute,
		Classes: []Class{
			{CourseID: "c1", ClassID: "k1", Schedule: []Schedule{{Days: []string{"Wed"}, Start: "09:00", End: "10:00"}}},
			{CourseID: "c2", ClassID: "k2", Schedule: []Schedule{{Days: []string{"Wed"}, Start: "09:00", End: "10:00"}}},
		},
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}, src, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }

	// Tick 1: k1 hits rate limit (600s cooldown), k2 succeeds. Single failure stays quiet.
	r.Tick(context.Background())
	if src.callsByCls["k1"] != 1 || src.callsByCls["k2"] != 1 || len(sender.sent) != 0 {
		t.Fatalf("tick1 calls=%v sent=%+v", src.callsByCls, sender.sent)
	}

	// Tick 2 (3 min later, still inside k1's 600s cooldown): k1 is skipped, k2 still polls.
	now = now.Add(3 * time.Minute)
	r.Tick(context.Background())
	if src.callsByCls["k1"] != 1 || src.callsByCls["k2"] != 2 || len(sender.sent) != 0 {
		t.Fatalf("tick2 should skip cooling k1: calls=%v sent=%+v", src.callsByCls, sender.sent)
	}

	// Tick 3 (after cooldown expires at +11m): k1 probes again, fails a second time -> lower-priority idle warning (priority 5).
	now = now.Add(8 * time.Minute)
	r.Tick(context.Background())
	if src.callsByCls["k1"] != 2 || len(sender.sent) != 1 {
		t.Fatalf("tick3 should warn once on second real failure: calls=%v sent=%+v", src.callsByCls, sender.sent)
	}
	if sender.sent[0].Priority != 5 || sender.sent[0].Title != "⚠️ 学习通课外巡检异常" {
		t.Fatalf("off-schedule alert should be downgraded: %+v", sender.sent[0])
	}
}

func TestIdleGroupProbeVerifiesUnseenCardViaClassFeedWithoutActingOnHistory(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, shanghai)
	end := now.Add(10 * time.Minute)

	liveCard := signIn("c1", "k1", "act-new", true)
	liveCard.EndsAt = &end
	groupNew := liveCard
	groupNew.Source = "group"
	groupNew.EndsAt = nil

	histCard := signIn("c1", "k1", "act-old", false)
	histCard.State = "finished"
	groupOld := histCard
	groupOld.State, groupOld.Signable, groupOld.Source = "active", true, "group"

	src := &perClassFlakySource{
		itemsByCls: map[string][]upstream.Activity{
			"k1": {histCard},
		},
		groupItems: []upstream.Activity{groupOld},
	}
	sender := &fakeSender{}
	signer := &fakeSigner{result: upstream.SignResult{Method: "normal", State: "signed", Submitted: true, Verified: true}}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3,
		Window:            "07:30-22:30",
		GroupScanInterval: 3 * time.Minute,
		IdleScanInterval:  15 * time.Minute,
		AutoSign:          true,
		Classes: []Class{
			{CourseID: "c1", ClassID: "k1", Schedule: []Schedule{{Days: []string{"Wed"}, Start: "09:00", End: "10:00"}}},
		},
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}, src, sender, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return now }

	// Tick 1: initial idle sweep sees histCard (finished), group probe sees groupOld (already in classCards) -> no extra call, no sign.
	r.Tick(context.Background())
	if src.callsByCls["k1"] != 1 || src.groupCalls != 1 || len(signer.requests) != 0 {
		t.Fatalf("tick1 calls=%v groupCalls=%d signs=%d", src.callsByCls, src.groupCalls, len(signer.requests))
	}
	// nextDelay should wake at GroupScanInterval (3m) rather than waiting the full 15m IdleScanInterval.
	if got := r.nextDelay(now); got != 3*time.Minute {
		t.Fatalf("nextDelay=%s want 3m", got)
	}

	// Tick 2 (3 min later, IdleScanInterval 15m not due): group shows only already-known groupOld -> zero activelist calls!
	now = now.Add(3 * time.Minute)
	r.Tick(context.Background())
	if src.callsByCls["k1"] != 1 || src.groupCalls != 2 || len(signer.requests) != 0 {
		t.Fatalf("tick2 should not poll activelist for known history: calls=%v groupCalls=%d signs=%d", src.callsByCls, src.groupCalls, len(signer.requests))
	}

	// Tick 3 (another 3 min later, still before 15m idle sweep): teacher opens an off-schedule sign-in -> group card triggers authoritative class fetch & auto-sign.
	src.itemsByCls["k1"] = []upstream.Activity{liveCard, histCard}
	src.groupItems = []upstream.Activity{groupNew, groupOld}
	now = now.Add(3 * time.Minute)
	r.Tick(context.Background())
	if src.callsByCls["k1"] != 2 || src.groupCalls != 3 || len(signer.requests) != 1 || signer.requests[0].ActivityID != "act-new" {
		t.Fatalf("tick3 should verify & sign unseen group card: calls=%v groupCalls=%d signs=%+v", src.callsByCls, src.groupCalls, signer.requests)
	}
}
