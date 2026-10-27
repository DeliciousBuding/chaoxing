package watch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

type assignmentSource struct {
	fakeSource
	rows      []upstream.Assignment
	complete  bool
	err       error
	signer    *fakeSigner
	seenSigns int
	calls     map[string]int
}

func (s *assignmentSource) ListClassAssignments(_ context.Context, _, classID string) ([]upstream.Assignment, bool, error) {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[classID]++
	if s.signer != nil {
		s.seenSigns = len(s.signer.requests)
	}
	return s.rows, s.complete, s.err
}

func classroomSchedule() []Schedule {
	return []Schedule{{Days: []string{"Tue"}, Start: "10:00", End: "11:40"}}
}

func TestClassroomAssignmentsPollEveryTickAndKeepAcceptedState(t *testing.T) {
	src := &assignmentSource{complete: true, rows: []upstream.Assignment{assignment("old", "pending")}}
	snd := &fakeSender{}
	path := filepath.Join(t.TempDir(), "state.json")
	r := assignmentRunner(t, src, snd, path)
	r.cfg.Interval = 5 * time.Second
	r.cfg.Classes[0].Schedule = classroomSchedule()
	now := time.Date(2026, 10, 27, 10, 30, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	src.rows = append(src.rows, assignment("quiz", "pending"))
	now = now.Add(5 * time.Second)
	snd.fail = true
	r.Tick(context.Background())
	if len(snd.sent) != 1 || snd.sent[0].Priority != 9 || r.assignments["c:k"].Items["quiz"].Notified {
		t.Fatal("in-class quiz not urgent or failed send suppressed retry", snd.sent)
	}
	snd.fail = false
	now = now.Add(5 * time.Second)
	r.Tick(context.Background())
	if src.calls["k"] != 3 || len(snd.sent) != 2 || !r.assignments["c:k"].Items["quiz"].Notified {
		t.Fatal("in-class assignments did not poll/retry at five-second cadence")
	}
	r = assignmentRunner(t, src, snd, path)
	r.cfg.Classes[0].Schedule = classroomSchedule()
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	if len(snd.sent) != 2 {
		t.Fatal("restart replayed accepted quiz or historical baseline")
	}
	now = now.Add(2 * time.Hour)
	src.rows = append(src.rows, assignment("after-class", "pending"))
	r.Tick(context.Background())
	if len(snd.sent) != 3 || snd.sent[2].Priority != 6 {
		t.Fatal("outside-class work received classroom urgency", snd.sent)
	}
	now = now.Add(5 * time.Second)
	r.Tick(context.Background())
	if src.calls["k"] != 5 {
		t.Fatal("idle assignments no longer bounded to five minutes")
	}
}

func TestClassroomAssignmentPollingDoesNotStarveOtherClasses(t *testing.T) {
	src := &assignmentSource{complete: true}
	r := assignmentRunner(t, src, &fakeSender{}, "")
	r.cfg.Classes = []Class{
		{CourseID: "c", ClassID: "first", Schedule: classroomSchedule()},
		{CourseID: "c", ClassID: "second", Schedule: classroomSchedule()},
		{CourseID: "c", ClassID: "idle", Schedule: []Schedule{{Days: []string{"Wed"}, Start: "10:00", End: "11:40"}}},
	}
	now := time.Date(2026, 10, 27, 10, 30, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	now = now.Add(5 * time.Second)
	r.Tick(context.Background())
	if src.calls["first"] != 2 || src.calls["second"] != 2 || src.calls["idle"] != 1 {
		t.Fatalf("active/idle rotation: %v", src.calls)
	}
}

func assignment(id, state string) upstream.Assignment {
	return upstream.Assignment{ID: id, CourseID: "c", ClassID: "k", Course: "Synthetic", Title: "Synthetic work", SubmissionState: state, AvailabilityState: "unknown"}
}

func assignmentRunner(t *testing.T, source *assignmentSource, sender *fakeSender, path string) *Runner {
	t.Helper()
	r, err := New(Config{Interval: time.Minute, Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: path}, source, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAssignmentBaselineNewWorkAndRestart(t *testing.T) {
	src := &assignmentSource{rows: []upstream.Assignment{assignment("old", "pending")}, complete: true}
	snd := &fakeSender{}
	path := filepath.Join(t.TempDir(), "watch.json")
	r := assignmentRunner(t, src, snd, path)
	now := time.Now()
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	if len(snd.sent) != 0 {
		t.Fatal("initial baseline flooded old work")
	}
	src.rows = append(src.rows, assignment("new", "pending"))
	now = now.Add(6 * time.Minute)
	r.Tick(context.Background())
	if len(snd.sent) != 1 || snd.sent[0].Priority != 6 {
		t.Fatal(snd.sent)
	}
	r = assignmentRunner(t, src, snd, path)
	r.Tick(context.Background())
	if len(snd.sent) != 1 {
		t.Fatal("restart duplicated accepted assignment")
	}
}

func TestAssignmentIncompleteFeedAndRetry(t *testing.T) {
	src := &assignmentSource{rows: []upstream.Assignment{assignment("old", "pending")}, complete: false}
	snd := &fakeSender{}
	r := assignmentRunner(t, src, snd, filepath.Join(t.TempDir(), "state.json"))
	now := time.Now()
	r.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		r.Tick(context.Background())
		now = now.Add(6 * time.Minute)
	}
	if len(r.assignments) != 0 || !r.Metrics().FeedDegraded {
		t.Fatal("partial established baseline or stayed healthy")
	}
	src.complete = true
	r.Tick(context.Background())
	if r.Metrics().FeedDegraded || len(snd.sent) != 0 {
		t.Fatal("feed did not recover or flooded baseline")
	}
	src.rows = append(src.rows, assignment("new", "pending"), assignment("unknown", "unknown"), assignment("done", "submitted"))
	snd.fail = true
	now = now.Add(6 * time.Minute)
	r.Tick(context.Background())
	if r.assignments["c:k"].Items["new"].Notified {
		t.Fatal("failed send marked accepted")
	}
	snd.fail = false
	now = now.Add(6 * time.Minute)
	r.Tick(context.Background())
	if len(snd.sent) != 2 || !r.assignments["c:k"].Items["new"].Notified {
		t.Fatal("pending assignment did not retry")
	}
	now = now.Add(6 * time.Minute)
	r.Tick(context.Background())
	if len(snd.sent) != 2 {
		t.Fatal("duplicate accepted assignment")
	}
	for i := 0; i < 3; i++ {
		src.err = errors.New("synthetic")
		now = now.Add(6 * time.Minute)
		r.Tick(context.Background())
	}
	if !r.Metrics().FeedDegraded {
		t.Fatal("failed feed not degraded")
	}
}

func TestAssignmentsRunAfterSigning(t *testing.T) {
	sign := &fakeSigner{}
	src := &assignmentSource{fakeSource: fakeSource{items: []upstream.Activity{signIn("c", "k", "sign", true)}}, complete: true, signer: sign}
	r, err := New(Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, src, &fakeSender{}, sign)
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	if src.seenSigns != 1 {
		t.Fatal("assignment scan preceded signing")
	}
}

func TestUnknownAssignmentBecomesActionableAndClosedIsSilent(t *testing.T) {
	src := &assignmentSource{complete: true, rows: []upstream.Assignment{assignment("u", "unknown")}}
	snd := &fakeSender{}
	r := assignmentRunner(t, src, snd, "")
	now := time.Now()
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	closed := assignment("closed", "pending")
	closed.AvailabilityState = "closed"
	src.rows = []upstream.Assignment{assignment("u", "pending"), closed, assignment("done", "submitted")}
	now = now.Add(6 * time.Minute)
	r.Tick(context.Background())
	if len(snd.sent) != 1 || !r.assignments["c:k"].Items["u"].Notified || r.assignments["c:k"].Items["closed"].Notified {
		t.Fatal("unexpected pending/closed policy")
	}
}

func TestAcceptedAssignmentCanDisappearOrLoseStatusWithoutDuplicate(t *testing.T) {
	src := &assignmentSource{complete: true}
	snd := &fakeSender{}
	r := assignmentRunner(t, src, snd, "")
	now := time.Now()
	r.now = func() time.Time { return now }
	r.Tick(context.Background())
	for _, rows := range [][]upstream.Assignment{{assignment("new", "pending")}, {}, {assignment("new", "unknown")}, {assignment("new", "pending")}} {
		src.rows = rows
		now = now.Add(6 * time.Minute)
		r.Tick(context.Background())
	}
	if len(snd.sent) != 1 {
		t.Fatal("accepted work rang again after feed changes")
	}
}
