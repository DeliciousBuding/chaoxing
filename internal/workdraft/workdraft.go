// Package workdraft owns local, operator-authored answer drafts. It never
// submits coursework upstream; export only materializes request payloads.
package workdraft

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/answer"
	"github.com/DeliciousBuding/chaoxing/internal/persist"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

const maxStateBytes = 2 << 20

type QuestionSource interface {
	GetWorkQuestions(ctx context.Context, courseID, classID, jobID string) (upstream.WorkQuestionSet, error)
}

type Item struct {
	QuestionID string    `json:"question_id"`
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	Answer     string    `json:"answer"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Work struct {
	CourseID  string          `json:"course_id"`
	ClassID   string          `json:"class_id"`
	JobID     string          `json:"job_id"`
	Title     string          `json:"title"`
	Questions map[string]Item `json:"questions"`
}

type stateFile struct {
	Version int             `json:"version"`
	Works   map[string]Work `json:"works"`
}

type Store struct {
	path     string
	source   QuestionSource
	answerer *answer.Engine
	mu       sync.Mutex
	works    map[string]Work
}

func Open(path string, source QuestionSource) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("work draft state path is required")
	}
	if source == nil {
		return nil, errors.New("work draft question source is required")
	}
	s := &Store{path: path, source: source, works: map[string]Work{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > maxStateBytes {
		return nil, errors.New("work draft state exceeds limit")
	}
	var state stateFile
	if json.Unmarshal(b, &state) != nil || state.Version != 1 || state.Works == nil {
		return nil, errors.New("work draft state is invalid")
	}
	if len(state.Works) > 512 {
		return nil, errors.New("work draft state has too many works")
	}
	s.works = state.Works
	return s, nil
}

func (s *Store) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	switch name {
	case "answer_work":
		return s.answer(ctx, args)
	case "upsert_work_draft":
		return s.upsert(ctx, args)
	case "list_work_drafts":
		return s.list(args)
	case "delete_work_draft":
		return s.delete(args), nil
	case "export_work_draft":
		return s.export(ctx, args)
	default:
		return nil, &upstream.Fault{Code: "INVALID_TOOL", Message: "unknown work draft operation"}
	}
}

func (s *Store) SetAnswerer(engine *answer.Engine) { s.answerer = engine }

func (s *Store) answer(ctx context.Context, args map[string]any) (any, error) {
	set, err := s.source.GetWorkQuestions(ctx, arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))
	if err != nil {
		return nil, err
	}
	if s.answerer == nil || !s.answerer.Enabled() {
		return nil, &upstream.Fault{Code: "UNSUPPORTED", Message: "answer providers are not configured"}
	}
	answers := []answer.Answer{}
	warnings := []string{}
	for _, question := range set.Questions {
		generated, err := s.answerer.Answer(ctx, answer.FromUpstream(question))
		if err != nil {
			warnings = append(warnings, "question "+question.ID+": "+upstream.SafeFault(err).Code)
			continue
		}
		answers = append(answers, generated)
	}
	s.mu.Lock()
	key := workKey(arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))
	work := s.works[key]
	if work.Questions == nil {
		work = Work{CourseID: arg(args, "course_id"), ClassID: arg(args, "class_id"), JobID: arg(args, "job_id"), Title: set.Job.Title, Questions: map[string]Item{}}
	}
	for _, generated := range answers {
		question, ok := findQuestion(set.Questions, generated.QuestionID)
		if !ok {
			continue
		}
		work.Questions[question.ID] = Item{QuestionID: question.ID, Title: question.Title, Type: question.Type, Answer: generated.Text, UpdatedAt: time.Now().UTC()}
	}
	s.works[key] = work
	saveErr := s.save()
	drafts := make([]Item, 0, len(work.Questions))
	for _, item := range work.Questions {
		drafts = append(drafts, item)
	}
	s.mu.Unlock()
	if saveErr != nil {
		return nil, saveErr
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].QuestionID < drafts[j].QuestionID })
	return map[string]any{
		"answers": answers, "drafts": drafts, "questions": len(set.Questions), "answered": len(answers),
		"complete": len(answers) == len(set.Questions), "coverage": "configured_answer_providers; not submitted",
		"warnings": warnings, "fetched_at": time.Now().UTC(),
	}, nil
}

func (s *Store) upsert(ctx context.Context, args map[string]any) (any, error) {
	set, err := s.source.GetWorkQuestions(ctx, arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))
	if err != nil {
		return nil, err
	}
	question, ok := findQuestion(set.Questions, arg(args, "question_id"))
	if !ok {
		return nil, &upstream.Fault{Code: "NOT_FOUND", Message: "question is not in the current work"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := workKey(arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))
	work := s.works[key]
	if work.Questions == nil {
		work = Work{CourseID: arg(args, "course_id"), ClassID: arg(args, "class_id"), JobID: arg(args, "job_id"), Title: set.Job.Title, Questions: map[string]Item{}}
	}
	if len(work.Questions) >= 256 {
		return nil, errors.New("work draft question limit reached")
	}
	work.Questions[question.ID] = Item{
		QuestionID: question.ID, Title: question.Title, Type: question.Type,
		Answer: arg(args, "answer"), UpdatedAt: time.Now().UTC(),
	}
	s.works[key] = work
	if err := s.save(); err != nil {
		return nil, err
	}
	return map[string]any{"draft": work.Questions[question.ID], "complete": true, "coverage": "local_draft", "fetched_at": time.Now().UTC()}, nil
}

func (s *Store) list(args map[string]any) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	work, ok := s.works[workKey(arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))]
	if !ok {
		return map[string]any{"items": []Item{}, "complete": true, "coverage": "local_draft", "fetched_at": time.Now().UTC()}, nil
	}
	items := make([]Item, 0, len(work.Questions))
	for _, item := range work.Questions {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].QuestionID < items[j].QuestionID })
	return map[string]any{"items": items, "complete": true, "coverage": "local_draft", "fetched_at": time.Now().UTC()}, nil
}

func (s *Store) delete(args map[string]any) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := workKey(arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))
	work, ok := s.works[key]
	if !ok {
		return map[string]any{"deleted": false, "complete": true, "coverage": "local_draft", "fetched_at": time.Now().UTC()}
	}
	delete(work.Questions, arg(args, "question_id"))
	if len(work.Questions) == 0 {
		delete(s.works, key)
	} else {
		s.works[key] = work
	}
	if err := s.save(); err != nil {
		return map[string]any{"deleted": false, "error": "STATE_WRITE_FAILED", "complete": false, "coverage": "local_draft", "fetched_at": time.Now().UTC()}
	}
	return map[string]any{"deleted": true, "complete": true, "coverage": "local_draft", "fetched_at": time.Now().UTC()}
}

func (s *Store) export(ctx context.Context, args map[string]any) (any, error) {
	set, err := s.source.GetWorkQuestions(ctx, arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	work := s.works[workKey(arg(args, "course_id"), arg(args, "class_id"), arg(args, "job_id"))]
	questions := make(map[string]Item, len(work.Questions))
	for id, item := range work.Questions {
		questions[id] = item
	}
	s.mu.Unlock()
	save := map[string]string{}
	for key, value := range set.FormData {
		save[key] = value
	}
	save["pyFlag"] = "1"
	submit := map[string]string{}
	for key, value := range save {
		submit[key] = value
	}
	submit["pyFlag"] = ""
	answered := 0
	for _, question := range set.Questions {
		draft, ok := questions[question.ID]
		if !ok {
			continue
		}
		answerKey, typeKey := "answer"+question.ID, "answertype"+question.ID
		save[answerKey], save[typeKey] = draft.Answer, question.AnswerType
		submit[answerKey], submit[typeKey] = draft.Answer, question.AnswerType
		answered++
	}
	return map[string]any{
		"payloads":  map[string]map[string]string{"save": save, "submit": submit},
		"questions": len(set.Questions), "answered": answered, "complete": true,
		"coverage": "local_draft_export; not submitted", "fetched_at": time.Now().UTC(),
	}, nil
}

func (s *Store) Payloads(ctx context.Context, args map[string]any) (save map[string]string, submit map[string]string, questions int, answered int, err error) {
	value, err := s.export(ctx, args)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	result, _ := value.(map[string]any)
	payloads, _ := result["payloads"].(map[string]map[string]string)
	return payloads["save"], payloads["submit"], result["questions"].(int), result["answered"].(int), nil
}

func (s *Store) save() error {
	b, err := json.Marshal(stateFile{Version: 1, Works: s.works})
	if err != nil || len(b) > maxStateBytes {
		return errors.New("work draft state exceeds limit")
	}
	return persist.Write(s.path, b, 0o600)
}

func findQuestion(rows []upstream.WorkQuestion, id string) (upstream.WorkQuestion, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return upstream.WorkQuestion{}, false
}

func workKey(courseID, classID, jobID string) string {
	return courseID + ":" + classID + ":" + jobID
}

func arg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}
