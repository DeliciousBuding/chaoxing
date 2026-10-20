package classroomagent

import (
	"context"
	"errors"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/answer"
)

type stubProvider struct {
	text string
	conf float64
}

func (p *stubProvider) Name() string { return "stub" }
func (p *stubProvider) Query(context.Context, answer.Question) (string, float64, error) {
	return p.text, p.conf, nil
}

type stubQuestions struct {
	items []answer.Question
	err   error
}

func (s *stubQuestions) ClassroomQuestions(context.Context, Activity) ([]answer.Question, error) {
	return s.items, s.err
}

func classroomActivity() Activity {
	return Activity{ID: "9001", CourseID: "c1", ClassID: "k1", Course: "Synthetic",
		Title: "Synthetic quiz", Kind: "answer", State: "active", UserState: "unsigned", Answerable: true}
}

func TestDecideScopeGate(t *testing.T) {
	for _, kind := range []string{"homework", "practice", "signin", "activity"} {
		agent, err := New(Policy{Enabled: true}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		a := classroomActivity()
		a.Kind = kind
		a.Answerable = false
		d := agent.Decide(context.Background(), a)
		if d.Action != ActionSkip || d.Reason != "NON_CLASSROOM_SCOPE" {
			t.Fatalf("kind %s: got %+v", kind, d)
		}
	}
}

func TestDecideClaimOnlyWithoutQuestions(t *testing.T) {
	agent, _ := New(Policy{Enabled: true}, nil, nil)
	d := agent.Decide(context.Background(), classroomActivity())
	if d.Action != ActionClaim || d.Reason != "CLAIM_ONLY_NO_QUESTIONS" {
		t.Fatalf("got %+v", d)
	}
}

func TestDecideNotifyOnlyKeepsClaimOff(t *testing.T) {
	agent, _ := New(Policy{Enabled: true, NotifyOnly: true}, nil, nil)
	d := agent.Decide(context.Background(), classroomActivity())
	if d.Action != ActionNotify || !d.NeedsReview {
		t.Fatalf("got %+v", d)
	}
}

func TestDecideStates(t *testing.T) {
	agent, _ := New(Policy{Enabled: true}, nil, nil)
	finished := classroomActivity()
	finished.State, finished.Answerable = "finished", false
	if d := agent.Decide(context.Background(), finished); d.Reason != "ACTIVITY_NOT_ACTIVE" {
		t.Fatalf("finished: %+v", d)
	}
	answered := classroomActivity()
	answered.UserState, answered.Answerable = "signed", false
	if d := agent.Decide(context.Background(), answered); d.Reason != "ALREADY_ANSWERED" {
		t.Fatalf("answered: %+v", d)
	}
	odd := classroomActivity()
	odd.UserState, odd.Answerable = "expired", false
	if d := agent.Decide(context.Background(), odd); d.Reason != "NOT_CLAIMABLE" {
		t.Fatalf("odd: %+v", d)
	}
}

func TestDecideAnswers(t *testing.T) {
	engine, err := answer.NewEngine([]answer.Provider{&stubProvider{text: "B", conf: 0.9}}, "", 0.82)
	if err != nil {
		t.Fatal(err)
	}
	questions := &stubQuestions{items: []answer.Question{{ID: "q1", Title: "1+1", Type: "single", Options: []string{"A.1", "B.2"}}}}
	agent, _ := New(Policy{Enabled: true}, engine, questions)
	d := agent.Decide(context.Background(), classroomActivity())
	if d.Action != ActionClaim || d.Reason != "ANSWERS_READY" || len(d.Answers) != 1 || d.Answers[0].Text != "B" {
		t.Fatalf("got %+v", d)
	}

	agentNotify, _ := New(Policy{Enabled: true, NotifyOnly: true}, engine, questions)
	d = agentNotify.Decide(context.Background(), classroomActivity())
	if d.Action != ActionNotify || d.Reason != "REVIEW_REQUIRED" || len(d.Answers) != 1 {
		t.Fatalf("notify_only: %+v", d)
	}
}

func TestDecideRejectsLowConfidence(t *testing.T) {
	engine, _ := answer.NewEngine([]answer.Provider{&stubProvider{text: "B", conf: 0.78}}, "", 0.75)
	questions := &stubQuestions{items: []answer.Question{{ID: "q1", Title: "1+1", Type: "single", Options: []string{"A.1", "B.2"}}}}
	agent, _ := New(Policy{Enabled: true}, engine, questions)
	d := agent.Decide(context.Background(), classroomActivity())
	if d.Action != ActionNotify || d.Reason != "LOW_CONFIDENCE" {
		t.Fatalf("got %+v", d)
	}
}

func TestDecideQuestionFailures(t *testing.T) {
	agent, _ := New(Policy{Enabled: true, MaxQuestions: 2}, nil, &stubQuestions{err: errors.New("parse failed")})
	if d := agent.Decide(context.Background(), classroomActivity()); d.Reason != "ANSWER_GENERATION_FAILED" {
		t.Fatalf("parse error: %+v", d)
	}
	agent, _ = New(Policy{Enabled: true, MaxQuestions: 2}, nil, &stubQuestions{items: []answer.Question{{ID: "q"}, {ID: "q"}, {ID: "q"}}})
	if d := agent.Decide(context.Background(), classroomActivity()); d.Reason != "TOO_MANY_QUESTIONS" {
		t.Fatalf("too many: %+v", d)
	}
	agent, _ = New(Policy{Enabled: true}, nil, &stubQuestions{items: []answer.Question{{ID: "q"}}})
	if d := agent.Decide(context.Background(), classroomActivity()); d.Reason != "ANSWER_ENGINE_UNAVAILABLE" {
		t.Fatalf("no engine: %+v", d)
	}
}

func TestDisabledAgentNotifies(t *testing.T) {
	agent, _ := New(Policy{}, nil, nil)
	if agent.Enabled() {
		t.Fatal("agent should be disabled")
	}
	if d := agent.Decide(context.Background(), classroomActivity()); d.Action != ActionNotify || d.Reason != "AGENT_DISABLED" {
		t.Fatalf("got %+v", d)
	}
}
