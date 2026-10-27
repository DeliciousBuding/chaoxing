package watch

import (
	"context"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

func TestClassroomUrgencyCoversAllPendingActivityKinds(t *testing.T) {
	for _, kind := range []string{"signin", "answer", "practice", "homework", "activity"} {
		t.Run(kind, func(t *testing.T) {
			item := upstream.Activity{ID: "a", CourseID: "c", ClassID: "k", Kind: kind, State: "active", UserState: "unsigned", Signable: kind == "signin", Answerable: kind == "answer"}
			src := &fakeSource{items: []upstream.Activity{item}}
			snd := &fakeSender{}
			r, err := New(Config{Interval: 5 * time.Second, Classes: []Class{{CourseID: "c", ClassID: "k", Schedule: classroomSchedule()}}}, src, snd, nil)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 27, 10, 30, 0, 0, shanghai)
			r.now = func() time.Time { return now }
			r.Tick(context.Background())
			if len(snd.sent) != 1 || snd.sent[0].Priority != 9 {
				t.Fatal("pending classroom activity did not alert urgently", snd.sent)
			}
			r.Tick(context.Background())
			if len(snd.sent) != 1 {
				t.Fatal("five-second poll bypassed notification cooldown")
			}
			item.UserState, item.Signable, item.Answerable = "signed", false, false
			src.items = []upstream.Activity{item}
			now = now.Add(2 * time.Minute)
			r.Tick(context.Background())
			if len(snd.sent) != 1 {
				t.Fatal("completed activity still alerts")
			}
			now = now.Add(2 * time.Hour)
			item.UserState, item.Signable, item.Answerable = "unsigned", kind == "signin", kind == "answer"
			src.items = []upstream.Activity{item}
			r.Tick(context.Background())
			if len(snd.sent) != 1 {
				t.Fatal("out-of-course activity alerted")
			}
			if got := r.activityPriority(item, now, 6); got != 6 {
				t.Fatalf("outside-course urgency=%d", got)
			}
			item.ClassID = "other"
			now = now.Add(-2 * time.Hour)
			if got := r.activityPriority(item, now, 6); got != 6 {
				t.Fatalf("another class inherited urgency=%d", got)
			}
		})
	}
}

func TestClassroomTickCadenceAccountsForWorkWithoutCatchup(t *testing.T) {
	r, err := New(Config{Interval: 5 * time.Second, Classes: []Class{{CourseID: "c", ClassID: "k", Schedule: classroomSchedule()}}}, &fakeSource{}, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.random = func() float64 { return 0.5 }
	start := time.Date(2026, 10, 27, 10, 30, 0, 0, shanghai)
	r.metrics.LastTickStartedAt = start
	if got := r.nextTickDelay(start.Add(2 * time.Second)); got != 3*time.Second {
		t.Fatalf("extra work not deducted: %v", got)
	}
	if got := r.nextTickDelay(start.Add(9 * time.Second)); got != time.Second {
		t.Fatalf("slow tick scheduled catch-up burst: %v", got)
	}
	idle := time.Date(2026, 10, 27, 9, 38, 0, 0, shanghai)
	r.metrics.LastTickStartedAt = idle.Add(-time.Minute)
	if got := r.nextTickDelay(idle); got != 2*time.Minute {
		t.Fatalf("idle wake missed padded class boundary: %v", got)
	}
}
