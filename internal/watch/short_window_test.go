package watch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

func TestEverySignStateNotifiesOnceAcrossRestart(t *testing.T) {
	now := time.Now()
	for _, state := range []string{"active", "finished", "unknown"} {
		for _, user := range []string{"signed", "unsigned", "unknown", "teacher_signed", "leave", "absence"} {
			t.Run(state+"/"+user, func(t *testing.T) {
				item := signIn("c", "k", "a", false)
				item.State, item.UserState = state, user
				source, sender := &fakeSource{items: []upstream.Activity{item}}, &fakeSender{}
				cfg := Config{Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/watch.json"}
				r, err := New(cfg, source, sender, nil)
				if err != nil {
					t.Fatal(err)
				}
				r.now = func() time.Time { return now }
				r.Tick(context.Background())
				if len(sender.sent) != 1 || sender.sent[0].Title != "🔔 学习通签到发现提醒" || !strings.Contains(sender.sent[0].Body, "活动状态："+state) || !strings.Contains(sender.sent[0].Body, "列表考勤状态："+user) {
					t.Fatalf("missing truthful discovery: %+v", sender.sent)
				}
				r, err = New(cfg, source, sender, nil)
				if err != nil {
					t.Fatal(err)
				}
				r.now = func() time.Time { return now.Add(time.Minute) }
				r.Tick(context.Background())
				if len(sender.sent) != 1 {
					t.Fatal("restart repeated discovery")
				}
			})
		}
	}
}

type failingActivitySource struct {
	err   error
	calls int
}

func (s *failingActivitySource) ListClassActivities(context.Context, string, string) ([]upstream.Activity, error) {
	s.calls++
	return nil, s.err
}

func TestDiscoveryFailureWarnsImmediatelyAndDedupesUntilRecovery(t *testing.T) {
	source := &failingActivitySource{err: &upstream.Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: 60, RetryAfterSource: "local_backoff"}}
	sender := &fakeSender{}
	r, err := New(Config{Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, source, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	r.Tick(context.Background())
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Body, "local_backoff") || !strings.Contains(sender.sent[0].Body, "新签到可能暂时无法自动发现") {
		t.Fatalf("alerts=%+v", sender.sent)
	}
	source.err = nil
	r.Tick(context.Background())
	source.err = &upstream.Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: 120}
	r.Tick(context.Background())
	if len(sender.sent) != 2 {
		t.Fatalf("new failure episode not reported: %+v", sender.sent)
	}
}

func TestKnownSignRetriesOutsideDiscoveryWindowAfterRestart(t *testing.T) {
	now := time.Now()
	end := now.Add(5 * time.Minute)
	item := signIn("c", "k", "a", true)
	item.EndsAt = &end
	signer := &fakeSigner{err: &upstream.Fault{Code: "UPSTREAM_UNAVAILABLE"}}
	sender := &fakeSender{}
	cfg := Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/watch.json"}
	r, err := New(cfg, &fakeSource{items: []upstream.Activity{item}}, sender, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	if len(signer.requests) != 1 || r.state[key(item)].Status != "retry" {
		t.Fatal("initial failure not retained")
	}
	cfg.Window = "00:00-00:01"
	source := &failingActivitySource{err: &upstream.Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: 600}}
	signer.err = nil
	signer.result = upstream.SignResult{Method: "normal", State: "signed", Submitted: true, Verified: true}
	r, err = New(cfg, source, sender, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return now.Add(31 * time.Second) }
	r.Tick(context.Background())
	if source.calls != 0 || len(signer.requests) != 2 || !r.state[key(item)].AttendanceVerified {
		t.Fatalf("resume depended on discovery: calls=%d requests=%d entry=%+v", source.calls, len(signer.requests), r.state[key(item)])
	}
}

func TestShortWindowSkipsDelayAndHonorsLongCooldownWithoutResubmit(t *testing.T) {
	now := time.Now()
	end := now.Add(50 * time.Second)
	item := signIn("c", "k", "a", true)
	item.EndsAt = &end
	signer := &fakeSigner{err: &upstream.Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: 300, RetryAfterSource: "upstream"}}
	sender := &fakeSender{}
	r, err := New(Config{Interval: time.Minute, AutoSign: true, AutoSignDelayMin: 25 * time.Second, AutoSignDelayMax: 25 * time.Second, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, &fakeSource{items: []upstream.Activity{item}}, sender, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return now }
	r.wait = func(context.Context, time.Duration) bool {
		t.Fatal("short-window sign slept unnecessarily")
		return false
	}
	r.Tick(context.Background())
	e := r.state[key(item)]
	if e.Status != "blocked_deadline" || !e.RetryAt.IsZero() || len(signer.requests) != 1 {
		t.Fatalf("entry=%+v requests=%d", e, len(signer.requests))
	}
	r.now = func() time.Time { return now.Add(10 * time.Second) }
	r.Tick(context.Background())
	if len(signer.requests) != 1 {
		t.Fatal("deadline bypassed server cooldown")
	}
	if len(sender.sent) < 1 || !strings.Contains(sender.sent[0].Body, "跨过签到截止") {
		t.Fatalf("missing deadline alert: %+v", sender.sent)
	}
}

func TestNotificationWakeDoesNotAccelerateClassPolling(t *testing.T) {
	now := time.Now()
	r, err := New(Config{Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, &fakeSource{}, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.alerts["a"] = pendingAlert{NextAt: now.Add(5 * time.Second)}
	if got := r.notificationDelay(now, time.Minute); got != time.Minute {
		t.Fatalf("maintenance accelerated class polling: %s", got)
	}
	r.cfg.Window = "00:00-00:01"
	if got := r.notificationDelay(now, 5*time.Minute); got != 5*time.Second {
		t.Fatalf("idle delayed notification: %s", got)
	}
}
