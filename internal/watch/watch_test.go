package watch

import (
	"context"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

type fakeSource struct {
	items []upstream.Activity
	calls int
}

func (f *fakeSource) Call(context.Context, string, map[string]any) (any, error) {
	f.calls++
	return map[string]any{"items": f.items}, nil
}

type fakeSender struct{ sent []notify.Message }

func (f *fakeSender) Send(_ context.Context, m notify.Message) error {
	f.sent = append(f.sent, m)
	return nil
}

func signIn(course, class, id string, signable bool) upstream.Activity {
	start := time.Now()
	return upstream.Activity{ID: id, CourseID: course, ClassID: class, Course: "Synthetic", Title: "Synthetic sign-in", Kind: "signin", State: "active", UserState: "unsigned", Signable: signable, StartsAt: &start}
}

func TestWatchAlertsThenRepeatsUntilSigned(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r, err := New(Config{Interval: time.Minute, RepeatEvery: 60 * time.Second, MaxRepeats: 3, Window: "07:30-22:30", Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json"}, src, snd)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	if len(snd.sent) != 1 {
		t.Fatalf("sent=%d", len(snd.sent))
	}
	now = now.Add(30 * time.Second)
	r.Tick(context.Background())
	if len(snd.sent) != 1 {
		t.Fatalf("repeated too early: %d", len(snd.sent))
	}
	now = now.Add(31 * time.Second)
	r.Tick(context.Background())
	if len(snd.sent) != 2 {
		t.Fatalf("no repeat: %d", len(snd.sent))
	}
	src.items = []upstream.Activity{signIn("c", "k", "1", false)}
	now = now.Add(2 * time.Minute)
	r.Tick(context.Background())
	if len(snd.sent) != 2 {
		t.Fatalf("alerted after signed: %d", len(snd.sent))
	}
}

func TestWatchRespectsWindow(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	r, err := New(Config{Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3, Window: "08:00-09:00", Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json"}, src, snd)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 23, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if len(snd.sent) != 0 || src.calls != 0 {
		t.Fatalf("polled outside window: sent=%d calls=%d", len(snd.sent), src.calls)
	}
}

func TestWatchCapsRepeatsAndPersistsState(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	statePath := t.TempDir() + "/state.json"
	cfg := Config{Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 2, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: statePath}
	r, err := New(cfg, src, snd)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		r.Tick(context.Background())
		now = now.Add(time.Minute)
	}
	if len(snd.sent) != 2 {
		t.Fatalf("sent=%d", len(snd.sent))
	}
	r2, err := New(cfg, src, &fakeSender{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.state) != 1 {
		t.Fatalf("state not reloaded: %+v", r2.state)
	}
}

func TestWindowParsingRejectsGarbage(t *testing.T) {
	if _, err := New(Config{Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}, Window: "evening"}, &fakeSource{}, &fakeSender{}); err == nil {
		t.Fatal("invalid window accepted")
	}
}
