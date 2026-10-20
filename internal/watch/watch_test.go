package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

type fakeSource struct {
	items []upstream.Activity
	calls int
}

func (f *fakeSource) ListClassActivities(context.Context, string, string) ([]upstream.Activity, error) {
	f.calls++
	return f.items, nil
}

type fakeSigner struct {
	requests []upstream.SignRequest
	result   upstream.SignResult
	err      error
}

func (f *fakeSigner) SignActivity(_ context.Context, req upstream.SignRequest) (upstream.SignResult, error) {
	f.requests = append(f.requests, req)
	return f.result, f.err
}

type fakeSender struct {
	fail bool
	sent []notify.Message
}

func (f *fakeSender) Send(_ context.Context, m notify.Message) error {
	f.sent = append(f.sent, m)
	if f.fail {
		return errors.New("synthetic delivery failure")
	}
	return nil
}

func signIn(course, class, id string, signable bool) upstream.Activity {
	start := time.Now()
	return upstream.Activity{ID: id, CourseID: course, ClassID: class, Course: "Synthetic", Title: "Synthetic sign-in", Kind: "signin", State: "active", UserState: "unsigned", SignType: "normal_or_photo", Signable: signable, StartsAt: &start}
}

func TestWatchAlertsThenRepeatsUntilSigned(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r, err := New(Config{Interval: time.Minute, RepeatEvery: 60 * time.Second, MaxRepeats: 3, Window: "07:30-22:30", Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json"}, src, snd, nil)
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

func TestWatchAutoSignsOnceAndUsesPresetLocation(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	signer := &fakeSigner{result: upstream.SignResult{Method: "location", State: "signed", Verified: true}}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3, AutoSign: true,
		Classes:   []Class{{CourseID: "c", ClassID: "k", Location: &Location{Latitude: 31.1, Longitude: 121.2, Address: "Synthetic room"}}},
		StatePath: t.TempDir() + "/state.json",
	}, src, snd, signer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		r.Tick(context.Background())
		now = now.Add(time.Minute)
	}
	if len(signer.requests) != 1 {
		t.Fatalf("sign attempts=%d", len(signer.requests))
	}
	req := signer.requests[0]
	if req.Latitude == nil || req.Longitude == nil || *req.Latitude != 31.1 || *req.Longitude != 121.2 || req.Address != "Synthetic room" {
		t.Fatalf("%+v", req)
	}
	if len(snd.sent) != 1 || snd.sent[0].Title != "✅ 学习通自动签到成功" {
		t.Fatalf("%+v", snd.sent)
	}
}

func TestWatchAutoSignFailureDoesNotRetry(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	signer := &fakeSigner{err: errors.New("synthetic")}
	r, err := New(Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json"}, src, snd, signer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	now = now.Add(time.Minute)
	r.Tick(context.Background())
	if len(signer.requests) != 1 || len(snd.sent) != 2 {
		t.Fatalf("requests=%d sent=%d", len(signer.requests), len(snd.sent))
	}
	if r.state[key(src.items[0])].Code != "INTERNAL" {
		t.Fatalf("%+v", r.state)
	}
}

func TestWatchAutoSignFailureRetriesAlertDelivery(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{fail: true}
	signer := &fakeSigner{err: errors.New("synthetic")}
	r, err := New(Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, src, snd, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if len(signer.requests) != 1 || len(snd.sent) != 1 {
		t.Fatalf("requests=%d sent=%d", len(signer.requests), len(snd.sent))
	}
	retry := &fakeSender{}
	r.sender = retry
	r.Tick(context.Background())
	if len(signer.requests) != 1 || len(retry.sent) != 1 {
		t.Fatalf("requests=%d sent=%d", len(signer.requests), len(retry.sent))
	}
}

func TestWatchAutoSignSkipsManualVerificationTypes(t *testing.T) {
	item := signIn("c", "k", "1", true)
	item.SignType = "qrcode"
	src := &fakeSource{items: []upstream.Activity{item}}
	snd := &fakeSender{}
	signer := &fakeSigner{}
	r, err := New(Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, src, snd, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if len(signer.requests) != 0 || len(snd.sent) != 1 {
		t.Fatalf("requests=%d sent=%d", len(signer.requests), len(snd.sent))
	}
}

func TestWatchPollsOnlyDuringClassSchedule(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	class := Class{CourseID: "c", ClassID: "k", Schedule: []Schedule{{Days: []string{"Tue"}, Start: "08:00", End: "09:40"}}}
	r, err := New(Config{Interval: time.Minute, Classes: []Class{class}}, src, snd, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if src.calls != 0 {
		t.Fatalf("polled outside schedule: %d", src.calls)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 7, 58, 0, 0, shanghai) }
	r.Tick(context.Background())
	if src.calls != 1 {
		t.Fatalf("did not poll before class: %d", src.calls)
	}
}

func TestWatchLeadMinusOneStartsExactlyAtClassTime(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	class := Class{CourseID: "c", ClassID: "k", Schedule: []Schedule{{Days: []string{"Tue"}, Start: "09:00", End: "09:40"}}}
	r, err := New(Config{Interval: time.Minute, Lead: -time.Second, Classes: []Class{class}}, src, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 8, 59, 0, 0, shanghai) }
	r.Tick(context.Background())
	if src.calls != 0 {
		t.Fatalf("polled before start: %d", src.calls)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if src.calls != 1 {
		t.Fatalf("did not poll at start: %d", src.calls)
	}
}

func TestWatchLeadUsesSeconds(t *testing.T) {
	src := &fakeSource{}
	class := Class{CourseID: "c", ClassID: "k", Schedule: []Schedule{{Days: []string{"Tue"}, Start: "09:00", End: "09:40"}}}
	r, err := New(Config{Interval: time.Minute, Lead: 90 * time.Second, Classes: []Class{class}}, src, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 8, 58, 30, 0, shanghai) }
	r.Tick(context.Background())
	if src.calls != 1 {
		t.Fatalf("90-second lead was truncated: %d", src.calls)
	}
}

func TestWatchInvalidStateFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-state.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	signer := &fakeSigner{}
	_, err := New(Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: path}, &fakeSource{}, &fakeSender{}, signer)
	if err == nil || len(signer.requests) != 0 {
		t.Fatal(err, signer.requests)
	}
}

func TestWatchRejectsInvalidJitterAndLead(t *testing.T) {
	class := []Class{{CourseID: "c", ClassID: "k"}}
	if _, err := New(Config{Interval: time.Minute, IntervalJitterPercent: -1, Classes: class}, &fakeSource{}, &fakeSender{}, nil); err == nil {
		t.Fatal("negative jitter accepted")
	}
	if _, err := New(Config{Interval: time.Minute, Lead: -2 * time.Second, Classes: class}, &fakeSource{}, &fakeSender{}, nil); err == nil {
		t.Fatal("invalid lead accepted")
	}
}

func TestWatchCapsRepeatsAndPersistsState(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	statePath := t.TempDir() + "/state.json"
	cfg := Config{Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 2, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: statePath}
	r, err := New(cfg, src, snd, nil)
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
	r2, err := New(cfg, src, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.state) != 1 {
		t.Fatalf("state not reloaded: %+v", r2.state)
	}
}

func TestWindowParsingRejectsGarbage(t *testing.T) {
	if _, err := New(Config{Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}, Window: "evening"}, &fakeSource{}, &fakeSender{}, nil); err == nil {
		t.Fatal("invalid window accepted")
	}
}
