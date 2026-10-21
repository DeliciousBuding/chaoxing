package workdraft

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/answer"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

type fakeQuestions struct{}

func (fakeQuestions) GetWorkQuestions(context.Context, string, string, string) (upstream.WorkQuestionSet, error) {
	return upstream.WorkQuestionSet{
		Job: upstream.WorkJob{ID: "work-1", CourseID: "course-1", ClassID: "class-1", Title: "Synthetic work"},
		Questions: []upstream.WorkQuestion{
			{ID: "q1", Title: "Pick one", Type: "single", AnswerType: "0", Options: []string{"A", "B"}},
			{ID: "q2", Title: "Write", Type: "shortanswer", AnswerType: "4"},
		},
		FormData:  map[string]string{"workId": "1", "answerwqbid": "q1,q2,", "pyFlag": ""},
		FetchedAt: time.Now().UTC(),
	}, nil
}

type deterministicAnswerProvider struct{}

func (deterministicAnswerProvider) Name() string { return "test" }
func (deterministicAnswerProvider) Query(_ context.Context, q answer.Question) (string, float64, error) {
	if q.ID == "q1" {
		return "B", 0.99, nil
	}
	return "generated answer", 0.99, nil
}

func TestAnswerWorkPersistsNormalizedProviderAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work-drafts.json")
	store, err := Open(path, fakeQuestions{})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := answer.NewEngine([]answer.Provider{deterministicAnswerProvider{}}, filepath.Join(t.TempDir(), "answer-cache.json"), 0.8)
	if err != nil {
		t.Fatal(err)
	}
	store.SetAnswerer(engine)
	args := map[string]any{"course_id": "course-1", "class_id": "class-1", "job_id": "work-1"}
	out, err := operation.Execute(context.Background(), "answer_work", args, store.Call)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	if result["answered"] != float64(2) || result["questions"] != float64(2) || result["complete"] != true {
		t.Fatalf("%+v", result)
	}
	answers := result["answers"].([]any)
	first := answers[0].(map[string]any)
	if first["answer"] != "B" || first["source"] != "test" || first["confidence"] != 0.99 {
		t.Fatalf("%+v", first)
	}
	list, err := store.Call(context.Background(), "list_work_drafts", args)
	if err != nil {
		t.Fatal(err)
	}
	items := list.(map[string]any)["items"].([]Item)
	if len(items) != 2 {
		t.Fatalf("%+v", items)
	}
}

func TestWorkDraftUpsertListExportDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work-drafts.json")
	store, err := Open(path, fakeQuestions{})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"course_id": "course-1", "class_id": "class-1", "job_id": "work-1"}
	if _, err = store.Call(context.Background(), "upsert_work_draft", withQuestion(args, "q1", "A")); err != nil {
		t.Fatal(err)
	}
	if _, err = operation.Execute(context.Background(), "upsert_work_draft", withQuestion(args, "q2", "answer text"), store.Call); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Call(context.Background(), "upsert_work_draft", withQuestion(args, "q2", "answer text")); err != nil {
		t.Fatal(err)
	}
	list, err := store.Call(context.Background(), "list_work_drafts", args)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.(map[string]any)["items"].([]Item)) != 2 {
		t.Fatalf("%+v", list)
	}
	exported, err := store.Call(context.Background(), "export_work_draft", args)
	if err != nil {
		t.Fatal(err)
	}
	payloads := exported.(map[string]any)["payloads"].(map[string]map[string]string)
	if payloads["save"]["answerq1"] != "A" || payloads["save"]["pyFlag"] != "1" || payloads["submit"]["pyFlag"] != "" {
		t.Fatalf("%+v", payloads)
	}
	if payloads["submit"]["answerq2"] != "answer text" || payloads["submit"]["answertypeq2"] != "4" {
		t.Fatalf("%+v", payloads)
	}
	if _, err = store.Call(context.Background(), "delete_work_draft", withQuestion(args, "q1", "")); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, fakeQuestions{})
	if err != nil {
		t.Fatal(err)
	}
	list, err = reopened.Call(context.Background(), "list_work_drafts", args)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.(map[string]any)["items"].([]Item)) != 1 {
		t.Fatalf("draft state did not persist: %+v", list)
	}
}

func TestWorkDraftRejectsUnknownQuestion(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "work-drafts.json"), fakeQuestions{})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"course_id": "course-1", "class_id": "class-1", "job_id": "work-1"}
	_, err = store.Call(context.Background(), "upsert_work_draft", withQuestion(args, "missing", "A"))
	if err == nil || upstream.SafeFault(err).Code != "NOT_FOUND" {
		t.Fatal(err)
	}
}

func TestWorkDraftConcurrentUpsertAndExportAreSafe(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "work-drafts.json"), fakeQuestions{})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"course_id": "course-1", "class_id": "class-1", "job_id": "work-1"}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := store.Call(context.Background(), "upsert_work_draft", withQuestion(args, "q1", "A")); err != nil {
				t.Errorf("upsert: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := store.Call(context.Background(), "export_work_draft", args); err != nil {
				t.Errorf("export: %v", err)
			}
		}()
	}
	wg.Wait()
}

func withQuestion(args map[string]any, questionID, answer string) map[string]any {
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	out["question_id"], out["answer"] = questionID, answer
	return out
}
