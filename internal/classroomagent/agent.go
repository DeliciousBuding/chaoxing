// Package classroomagent owns the bounded decision process for classroom
// answer activities. It never talks to Chaoxing directly: the watcher keeps
// execution, notification and pacing. The agent only decides.
//
// Design contract:
//   - Scope gate: only classroom "answer" activities are eligible. Homework
//     and practice are explicitly out of scope and are never auto-submitted.
//   - Planner is deterministic rules, not an LLM loop.
//   - The LLM is a single-call generator per question, followed by
//     normalization and a confidence gate.
//   - Every ambiguous outcome degrades to "notify the operator".
package classroomagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeliciousBuding/chaoxing/internal/answer"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

// Activity is the read-only projection the agent is allowed to see.
type Activity struct {
	ID         string
	CourseID   string
	ClassID    string
	Course     string
	Title      string
	Kind       string
	State      string
	UserState  string
	Answerable bool
}

// FromUpstream converts one verified feed activity.
func FromUpstream(a upstream.Activity) Activity {
	return Activity{
		ID: a.ID, CourseID: a.CourseID, ClassID: a.ClassID, Course: a.Course,
		Title: a.Title, Kind: a.Kind, State: a.State, UserState: a.UserState,
		Answerable: a.Answerable,
	}
}

// QuestionSource supplies parsed classroom questions when a future protocol
// reconstruction provides them. A nil source is valid and keeps the agent in
// claim-only mode; it must never invent questions from prose.
type QuestionSource interface {
	ClassroomQuestions(ctx context.Context, a Activity) ([]answer.Question, error)
}

// Policy bounds the agent. Defaults favor notification over action.
type Policy struct {
	Enabled       bool
	NotifyOnly    bool
	MinConfidence float64
	MaxQuestions  int
}

// Decision is a machine-checkable planner result.
type Decision struct {
	Action        string          // claim, notify or skip
	Reason        string          // stable reason code for state and alerts
	Detail        string          // human-readable explanation
	NeedsReview   bool            // operator attention required
	Answers       []answer.Answer // generated answers, if any
	QuestionCount int
}

const (
	ActionClaim  = "claim"
	ActionNotify = "notify"
	ActionSkip   = "skip"
)

var reasonText = map[string]string{
	"NON_CLASSROOM_SCOPE":       "不是课堂答题活动，自动处理被拒绝",
	"ACTIVITY_NOT_ACTIVE":       "活动已结束或未开始",
	"ALREADY_ANSWERED":          "该账号已参与过此活动",
	"NOT_CLAIMABLE":             "活动当前状态不可参与",
	"AGENT_DISABLED":            "课堂答题代理未启用",
	"CLAIM_ONLY_NO_QUESTIONS":   "课堂协议暂无题目解析，仅执行安全抢答领取",
	"TOO_MANY_QUESTIONS":        "题目数量超过安全上限，转人工",
	"ANSWER_ENGINE_UNAVAILABLE": "答题引擎未配置或不可用",
	"ANSWER_GENERATION_FAILED":  "存在无法生成答案的题目，转人工",
	"LOW_CONFIDENCE":            "存在低于置信度阈值的答案，转人工",
	"REVIEW_REQUIRED":           "答案已生成，notify_only 模式下等待人工确认",
	"ANSWERS_READY":             "答案全部通过校验，执行课堂领取",
}

// Agent is stateless apart from its policy and provider chain.
type Agent struct {
	policy    Policy
	engine    *answer.Engine
	questions QuestionSource
}

func New(policy Policy, engine *answer.Engine, questions QuestionSource) (*Agent, error) {
	if !policy.Enabled {
		return &Agent{policy: policy}, nil
	}
	if policy.MinConfidence <= 0 || policy.MinConfidence > 1 {
		policy.MinConfidence = 0.82
	}
	if policy.MaxQuestions <= 0 {
		policy.MaxQuestions = 20
	}
	if policy.MaxQuestions > 100 {
		policy.MaxQuestions = 100
	}
	return &Agent{policy: policy, engine: engine, questions: questions}, nil
}

func (a *Agent) Enabled() bool { return a != nil && a.policy.Enabled }

// Decide evaluates one activity through the full gate chain. It performs no
// upstream writes; LLM calls happen only when questions are available.
func (a *Agent) Decide(ctx context.Context, activity Activity) Decision {
	switch {
	case activity.Kind != "answer":
		return skip("NON_CLASSROOM_SCOPE")
	case activity.State != "active":
		return skip("ACTIVITY_NOT_ACTIVE")
	case activity.UserState == "signed":
		return skip("ALREADY_ANSWERED")
	case !activity.Answerable:
		return skip("NOT_CLAIMABLE")
	}
	if a == nil || !a.policy.Enabled {
		return notify("AGENT_DISABLED")
	}
	if a.questions == nil {
		if a.policy.NotifyOnly {
			return notify("CLAIM_ONLY_NO_QUESTIONS")
		}
		return Decision{Action: ActionClaim, Reason: "CLAIM_ONLY_NO_QUESTIONS", Detail: reasonText["CLAIM_ONLY_NO_QUESTIONS"]}
	}
	questions, err := a.questions.ClassroomQuestions(ctx, activity)
	if err != nil || len(questions) == 0 {
		return notifyWith("ANSWER_GENERATION_FAILED", fmt.Sprintf("题目解析不可用：%v", safeDetail(err)))
	}
	if len(questions) > a.policy.MaxQuestions {
		return notifyWith("TOO_MANY_QUESTIONS", fmt.Sprintf("题目数 %d 超过上限 %d", len(questions), a.policy.MaxQuestions))
	}
	if a.engine == nil || !a.engine.Enabled() {
		return notify("ANSWER_ENGINE_UNAVAILABLE")
	}
	answers := make([]answer.Answer, 0, len(questions))
	for _, q := range questions {
		generated, err := a.engine.Answer(ctx, q)
		if err != nil {
			return notifyWith("ANSWER_GENERATION_FAILED", fmt.Sprintf("题目 %s 生成失败：%v", short(q.ID), safeDetail(err)))
		}
		if generated.Confidence < a.policy.MinConfidence {
			return notifyWith("LOW_CONFIDENCE", fmt.Sprintf("题目 %s 置信度 %.2f 低于阈值 %.2f", short(q.ID), generated.Confidence, a.policy.MinConfidence))
		}
		answers = append(answers, generated)
	}
	if a.policy.NotifyOnly {
		d := notify("REVIEW_REQUIRED")
		d.Answers, d.QuestionCount = answers, len(questions)
		return d
	}
	return Decision{Action: ActionClaim, Reason: "ANSWERS_READY", Detail: reasonText["ANSWERS_READY"], Answers: answers, QuestionCount: len(questions)}
}

func skip(reason string) Decision {
	return Decision{Action: ActionSkip, Reason: reason, Detail: reasonText[reason]}
}

func notify(reason string) Decision {
	return Decision{Action: ActionNotify, Reason: reason, Detail: reasonText[reason], NeedsReview: true}
}

func notifyWith(reason, detail string) Decision {
	d := notify(reason)
	d.Detail = detail
	return d
}

func short(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 24 {
		return id[:24] + "…"
	}
	return id
}

func safeDetail(err error) string {
	if err == nil {
		return "unknown"
	}
	text := err.Error()
	if len(text) > 160 {
		return text[:160]
	}
	return text
}
