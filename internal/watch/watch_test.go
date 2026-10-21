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
	"github.com/DeliciousBuding/chaoxing/internal/classroomagent"
	"github.com/DeliciousBuding/chaoxing/internal/location"
	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

type fakeSource struct {
	items []upstream.Activity
	calls int
	panic bool
}

func (f *fakeSource) ListClassActivities(context.Context, string, string) ([]upstream.Activity, error) {
	f.calls++
	if f.panic {
		panic("synthetic watcher panic")
	}
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

func TestWatchTickRecoversPanicAndResetsCounter(t *testing.T) {
	src := &fakeSource{panic: true}
	snd := &fakeSender{}
	r, err := New(Config{
		Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}},
		StatePath: t.TempDir() + "/state.json",
	}, src, snd, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }

	r.tick(context.Background())

	metrics := r.Metrics()
	if metrics.ConsecutivePanics != 1 || metrics.LastPanicKind != "explicit" || metrics.LastPanicAt.IsZero() {
		t.Fatalf("%+v", metrics)
	}
	if len(snd.sent) != 1 || snd.sent[0].Title != "🚨 学习通 Watcher panic 已恢复" || !strings.Contains(snd.sent[0].Body, "连续 panic：1") {
		t.Fatalf("%+v", snd.sent)
	}

	src.panic = false
	r.tick(context.Background())

	metrics = r.Metrics()
	if metrics.ConsecutivePanics != 0 || len(snd.sent) != 1 {
		t.Fatalf("%+v sent=%d", metrics, len(snd.sent))
	}
}

func TestWatchNextDelayIdlesOutsideClassAndWakesBeforeStart(t *testing.T) {
	r, err := New(Config{
		Interval: time.Minute, Window: "07:30-22:30", Lead: 2 * time.Minute,
		Classes: []Class{{
			CourseID: "c", ClassID: "k",
			Schedule: []Schedule{{Days: []string{"Tue"}, Start: "08:00", End: "09:40"}},
		}},
		StatePath: t.TempDir() + "/state.json",
	}, &fakeSource{}, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.random = func() float64 { return 0.5 }

	if got := r.nextDelay(time.Date(2026, 10, 20, 10, 30, 0, 0, shanghai)); got != 5*time.Minute {
		t.Fatalf("idle delay=%s", got)
	}
	if got := r.nextDelay(time.Date(2026, 10, 20, 23, 0, 0, 0, shanghai)); got != 5*time.Minute {
		t.Fatalf("night delay=%s", got)
	}
	if got := r.nextDelay(time.Date(2026, 10, 20, 7, 27, 0, 0, shanghai)); got != 3*time.Minute {
		t.Fatalf("pre-window delay=%s", got)
	}
	if got := r.nextDelay(time.Date(2026, 10, 20, 7, 58, 0, 0, shanghai)); got != time.Minute {
		t.Fatalf("pre-class delay=%s", got)
	}
}

func TestWatchSkipsGroupFeedWhenNoClassIsActive(t *testing.T) {
	src := &fakeGroupSource{groupItems: []upstream.Activity{signIn("c", "k", "group-1", true)}}
	r, err := New(Config{
		Interval: time.Minute, Window: "07:30-22:30",
		Classes: []Class{{
			CourseID: "c", ClassID: "k",
			Schedule: []Schedule{{Days: []string{"Tue"}, Start: "08:00", End: "09:40"}},
		}},
		StatePath: t.TempDir() + "/state.json",
	}, src, &fakeSender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 10, 30, 0, 0, shanghai) }
	r.Tick(context.Background())

	if src.calls != 0 || src.groupCalls != 0 {
		t.Fatalf("classCalls=%d groupCalls=%d", src.calls, src.groupCalls)
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

func TestWatchClassroomAgentNotifyOnlyNeverClaims(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{answerActivity("c", "k", "answer-9")}}
	snd := &fakeSender{}
	answerer := &fakeAnswerer{result: upstream.SignResult{Method: "answer", State: "answered", Verified: true}}
	agent, err := classroomagent.New(classroomagent.Policy{Enabled: true, NotifyOnly: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3, AutoAnswer: true,
		ClassroomAgent: agent, Classes: []Class{{CourseID: "c", ClassID: "k"}},
		StatePath: t.TempDir() + "/state.json",
	}, src, snd, nil, answerer)
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	if len(answerer.requests) != 0 {
		t.Fatalf("notify_only must not claim: %+v", answerer.requests)
	}
	if len(snd.sent) != 1 || snd.sent[0].Title != "🧠 学习通课堂答题需要确认" {
		t.Fatalf("%+v", snd.sent)
	}
	if e := r.state[key(src.items[0])]; e.Status != "review" || e.Code != "CLAIM_ONLY_NO_QUESTIONS" {
		t.Fatalf("%+v", e)
	}
}

func TestWatchClassroomAgentSkipsHomework(t *testing.T) {
	homework := upstream.Activity{ID: "work-1", CourseID: "c", ClassID: "k", Course: "Synthetic",
		Title: "Synthetic homework", Kind: "homework", State: "active", UserState: "unsigned", Answerable: true}
	src := &fakeSource{items: []upstream.Activity{homework}}
	snd := &fakeSender{}
	answerer := &fakeAnswerer{result: upstream.SignResult{Method: "answer", State: "answered", Verified: true}}
	agent, err := classroomagent.New(classroomagent.Policy{Enabled: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3, AutoAnswer: true,
		ClassroomAgent: agent, Classes: []Class{{CourseID: "c", ClassID: "k"}},
		StatePath: t.TempDir() + "/state.json",
	}, src, snd, nil, answerer)
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	if len(answerer.requests) != 0 {
		t.Fatalf("homework must never be claimed: %+v", answerer.requests)
	}
	if len(snd.sent) != 0 {
		t.Fatalf("scope-gated homework should not alert: %+v", snd.sent)
	}
	if _, exists := r.state[key(homework)]; exists {
		t.Fatal("scope-gated homework must not persist watch state")
	}
}

func TestWatchClassroomAgentClaimsWhenAllowed(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{answerActivity("c", "k", "answer-7")}}
	snd := &fakeSender{}
	answerer := &fakeAnswerer{result: upstream.SignResult{Method: "answer", State: "answered", Verified: true}}
	agent, err := classroomagent.New(classroomagent.Policy{Enabled: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{
		Interval: time.Minute, RepeatEvery: time.Minute, MaxRepeats: 3, AutoAnswer: true,
		ClassroomAgent: agent, Classes: []Class{{CourseID: "c", ClassID: "k"}},
		StatePath: t.TempDir() + "/state.json",
	}, src, snd, nil, answerer)
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	if len(answerer.requests) != 1 || answerer.requests[0] != [3]string{"c", "k", "answer-7"} {
		t.Fatalf("%+v", answerer.requests)
	}
	if len(snd.sent) != 1 || snd.sent[0].Title != "✅ 学习通自动抢答成功" {
		t.Fatalf("%+v", snd.sent)
	}
}

type fakeLocationService struct {
	account  string
	bindings map[string]location.Location
}

func (f *fakeLocationService) Resolve(_ context.Context, account, courseID, classID, scheduleKey string) (*location.Location, error) {
	if account != f.account {
		return nil, nil
	}
	key := courseID + "\x00" + classID + "\x00" + scheduleKey
	if item, ok := f.bindings[key]; ok {
		return &item, nil
	}
	item, ok := f.bindings[courseID+"\x00"+classID+"\x00"]
	if !ok {
		return nil, nil
	}
	return &item, nil
}

func (f *fakeLocationService) ListPresets(context.Context, string) ([]location.Preset, error) {
	return nil, nil
}
func (f *fakeLocationService) SavePreset(context.Context, string, location.Preset) (location.Preset, error) {
	return location.Preset{}, nil
}
func (f *fakeLocationService) DeletePreset(context.Context, string, int64) error { return nil }
func (f *fakeLocationService) ListBindings(context.Context, string) ([]location.Binding, error) {
	return nil, nil
}
func (f *fakeLocationService) SaveBinding(context.Context, string, location.Binding) error {
	return nil
}
func (f *fakeLocationService) DeleteBinding(context.Context, string, string, string, string) error {
	return nil
}

func TestWatchResolverOverridesPresetLocation(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	snd := &fakeSender{}
	signer := &fakeSigner{result: upstream.SignResult{Method: "location", State: "signed", Verified: true}}
	class := Class{
		CourseID: "c", ClassID: "k", Schedule: []Schedule{{Days: []string{"Tue"}, Start: "08:00", End: "09:40"}},
		Location: &Location{Latitude: 31.1, Longitude: 121.2, Address: "legacy config"},
	}
	service := &fakeLocationService{account: "root", bindings: map[string]location.Location{
		"c\x00k\x00Tue-08:00-09:40": {Latitude: 39.90420, Longitude: 116.40740, Address: "Synthetic building"},
	}}
	r, err := New(Config{
		Interval: time.Minute, AutoSign: true, Classes: []Class{class}, StatePath: t.TempDir() + "/state.json",
		LocationResolver: location.NewResolver(service, "root"),
	}, src, snd, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai) }
	r.Tick(context.Background())
	if len(signer.requests) != 1 {
		t.Fatalf("requests=%d", len(signer.requests))
	}
	req := signer.requests[0]
	if req.Latitude == nil || *req.Latitude != 39.90420 || req.Longitude == nil || *req.Longitude != 116.40740 || req.Address != "Synthetic building" {
		t.Fatalf("%+v", req)
	}
}

func TestActiveScheduleKey(t *testing.T) {
	class := Class{Schedule: []Schedule{{Days: []string{"Tue", "Thu"}, Start: "08:00", End: "09:40"}}}
	got := activeScheduleKey(time.Date(2026, 10, 20, 8, 30, 0, 0, shanghai), class)
	if got != "Tue,Thu-08:00-09:40" {
		t.Fatal(got)
	}
	if activeScheduleKey(time.Date(2026, 10, 20, 10, 0, 0, 0, shanghai), class) != "" {
		t.Fatal("returned a key outside the schedule")
	}
}

func TestRunPollsImmediatelyAfterStart(t *testing.T) {
	called := make(chan struct{}, 4)
	source := &countingSource{onCall: called}
	runner, err := New(Config{
		Interval: time.Minute, Classes: []Class{{
			CourseID: "course", ClassID: "class",
			Schedule: []Schedule{{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}, Start: "00:00", End: "23:59"}},
		}}, Window: "00:00-23:59",
	}, source, notifySenderFunc(func(context.Context, notify.Message) error { return nil }), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not poll immediately after start")
	}
	if runner.Metrics().Ticks < 1 {
		t.Fatal("tick counter was not incremented")
	}
}

type countingSource struct{ onCall chan struct{} }

func (c *countingSource) ListClassActivities(context.Context, string, string) ([]upstream.Activity, error) {
	select {
	case c.onCall <- struct{}{}:
	default:
	}
	return nil, nil
}

type notifySenderFunc func(context.Context, notify.Message) error

func (f notifySenderFunc) Send(ctx context.Context, m notify.Message) error { return f(ctx, m) }

func TestAnswerAlertTitleByKind(t *testing.T) {
	cases := map[string]string{
		"practice": "📝 学习通课堂练习提醒",
		"homework": "📝 学习通课堂作业提醒",
		"answer":   "🔔 学习通抢答提醒",
		"":         "🔔 学习通抢答提醒",
	}
	for kind, want := range cases {
		if got := answerAlertTitle(kind); got != want {
			t.Fatalf("%q: got %q want %q", kind, got, want)
		}
	}
}

func TestWindowsIncludeLeadAndMergeContiguousMinutes(t *testing.T) {
	classes := []Class{{
		CourseID: "course", ClassID: "class",
		Schedule: []Schedule{{Days: []string{"Tue"}, Start: "08:00", End: "09:40"}},
	}}
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai) // Tuesday
	until := from.Add(24 * time.Hour)
	got := Windows(classes, 2*time.Minute, from, until)
	if len(got) != 1 {
		t.Fatalf("windows=%v", got)
	}
	if !got[0].Start.Equal(time.Date(2026, 10, 20, 7, 58, 0, 0, shanghai)) {
		t.Fatalf("start=%v", got[0].Start.In(shanghai))
	}
	if !got[0].End.Equal(time.Date(2026, 10, 20, 9, 40, 0, 0, shanghai)) {
		t.Fatalf("end=%v", got[0].End.In(shanghai))
	}
	if got[0].ScheduleKey != "Tue-08:00-09:40" {
		t.Fatal(got[0].ScheduleKey)
	}
}
