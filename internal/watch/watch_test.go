package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/capture"
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

type fakeGroupSource struct {
	fakeSource
	groupItems []upstream.Activity
	groupCalls int
}

func (f *fakeGroupSource) ListGroupActivities(context.Context) ([]upstream.Activity, error) {
	f.groupCalls++
	return f.groupItems, nil
}

type fakeSigner struct {
	requests []upstream.SignRequest
	result   upstream.SignResult
	err      error
}

type fakeAnswerer struct {
	requests [][3]string
	result   upstream.SignResult
	err      error
}

func (f *fakeAnswerer) AnswerActivity(_ context.Context, courseID, classID, activityID string) (upstream.SignResult, error) {
	f.requests = append(f.requests, [3]string{courseID, classID, activityID})
	return f.result, f.err
}

func (f *fakeSigner) SignActivity(_ context.Context, req upstream.SignRequest) (upstream.SignResult, error) {
	f.requests = append(f.requests, req)
	return f.result, f.err
}

type fakeSender struct {
	fail bool
	sent []notify.Message
}

type fakeCaptureStore struct {
	saved  []capture.Activity
	pages  []string
	pruned int
	had    map[string]bool
}

func (f *fakeCaptureStore) SaveActivityCapture(_ context.Context, item capture.Activity) (capture.SaveResult, error) {
	f.saved = append(f.saved, item)
	if f.had == nil {
		f.had = map[string]bool{}
	}
	return capture.SaveResult{HadPage: f.had[item.ActivityKey]}, nil
}

func (f *fakeCaptureStore) SaveActivityPage(_ context.Context, _, activityKey string, _ []byte, _ string) error {
	if f.had == nil {
		f.had = map[string]bool{}
	}
	f.had[activityKey] = true
	f.pages = append(f.pages, activityKey)
	return nil
}

func (f *fakeCaptureStore) PruneActivityCaptures(context.Context, time.Time) error {
	f.pruned++
	return nil
}

type fakePageSource struct {
	calls int
}

func (f *fakePageSource) ActivityPage(context.Context, map[string]any) (any, error) {
	f.calls++
	return map[string]any{"forms": []map[string]any{{"action": "/synthetic"}}}, nil
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

func answerActivity(course, class, id string) upstream.Activity {
	start := time.Now()
	return upstream.Activity{ID: id, CourseID: course, ClassID: class, Course: "Synthetic", Title: "Synthetic rapid answer", Kind: "answer", State: "active", UserState: "unsigned", Answerable: true, StartsAt: &start}
}

func TestWatchAutoAnswersOnce(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{answerActivity("c", "k", "answer-1")}}
	snd := &fakeSender{}
	answerer := &fakeAnswerer{result: upstream.SignResult{Method: "answer", State: "answered", Verified: true}}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3, AutoAnswer: true,
		Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json",
	}, src, snd, nil, answerer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		r.Tick(context.Background())
		now = now.Add(time.Minute)
	}
	if len(answerer.requests) != 1 || answerer.requests[0] != [3]string{"c", "k", "answer-1"} {
		t.Fatalf("%+v", answerer.requests)
	}
	if len(snd.sent) != 1 || snd.sent[0].Title != "✅ 学习通自动抢答成功" {
		t.Fatalf("%+v", snd.sent)
	}
	if r.state[key(src.items[0])].Status != "answered" {
		t.Fatalf("%+v", r.state)
	}
}

func TestWatchAutoAnswerRequiresSource(t *testing.T) {
	_, err := New(Config{Interval: time.Minute, AutoAnswer: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, &fakeSource{}, &fakeSender{}, nil)
	if err == nil || !strings.Contains(err.Error(), "auto answer") {
		t.Fatal(err)
	}
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

func TestWatchMergesGroupCardAndUsesPresetSignCode(t *testing.T) {
	src := &fakeGroupSource{}
	item := signIn("c", "k", "group-1", true)
	item.SignType = "password"
	item.Source, item.GroupName = "group", "Synthetic course group"
	src.groupItems = []upstream.Activity{item}
	snd := &fakeSender{}
	signer := &fakeSigner{result: upstream.SignResult{Method: "password", State: "signed", Verified: true}}
	r, err := New(Config{
		Interval: time.Minute, AutoSign: true,
		Classes:   []Class{{CourseID: "c", ClassID: "k", SignCode: &SignCode{Code: "1234"}}},
		StatePath: t.TempDir() + "/state.json",
	}, src, snd, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if src.groupCalls != 1 || len(signer.requests) != 1 {
		t.Fatalf("groupCalls=%d requests=%d", src.groupCalls, len(signer.requests))
	}
	if signer.requests[0].SignCode != "1234" {
		t.Fatalf("%+v", signer.requests[0])
	}
	if len(snd.sent) != 1 || !strings.Contains(snd.sent[0].Body, "Synthetic course group") {
		t.Fatalf("%+v", snd.sent)
	}
}

func TestWatchCapturesPracticePageOnce(t *testing.T) {
	item := signIn("c", "k", "practice-1", false)
	item.Kind = "practice"
	src := &fakeSource{items: []upstream.Activity{item}}
	store := &fakeCaptureStore{}
	pages := &fakePageSource{}
	r, err := New(Config{
		Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json",
		Capture: true, CaptureAccount: "root", CaptureRetention: 24 * time.Hour,
		CaptureActivityPages: true, CaptureStore: store, ActivityPageSource: pages,
	}, src, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	now = now.Add(time.Minute)
	r.Tick(context.Background())
	if len(store.saved) != 2 || store.saved[0].Kind != "practice" {
		t.Fatalf("saved=%d %+v", len(store.saved), store.saved)
	}
	if pages.calls != 1 || len(store.pages) != 1 {
		t.Fatalf("pages=%d saved=%d", pages.calls, len(store.pages))
	}
	if store.pruned != 1 {
		t.Fatalf("pruned=%d", store.pruned)
	}
}

func TestWatchCapturesGroupCards(t *testing.T) {
	item := signIn("c", "k", "group-practice", false)
	item.Kind, item.Source = "practice", "group"
	src := &fakeGroupSource{groupItems: []upstream.Activity{item}}
	store := &fakeCaptureStore{}
	r, err := New(Config{
		Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json",
		Capture: true, CaptureAccount: "tenant", CaptureRetention: 24 * time.Hour, CaptureStore: store,
	}, src, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if len(store.saved) != 1 || store.saved[0].Source != "group" || store.saved[0].AccountKey != "tenant" {
		t.Fatalf("%+v", store.saved)
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

func TestWatchAutoSignFailureUsesHighNotificationPriority(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	signer := &fakeSigner{err: errors.New("synthetic")}
	r, err := New(Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json"}, src, snd, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if len(snd.sent) != 1 || snd.sent[0].Priority != 8 {
		t.Fatalf("%+v", snd.sent)
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
