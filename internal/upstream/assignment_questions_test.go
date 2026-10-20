package upstream

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func assignmentQuestionPage() string {
	return `<html><body><form>
<input name="workId" value="42"><input name="jobid" value="work-42">
<input name="answertypeq1" value="0"><input name="pyFlag" value="">
</form>
<div class="marBom60 questionLi singleQuesId" data="q1">
  <h3 class="mark_name colorDeep">1. <span class="colorShallow">(单选题)</span><span class="qtContent workTextWrap">Pick one</span></h3>
  <div class="stem_answer">
    <div class="clearfix"><span class="num_option">A</span><div class="answer_p">Alpha</div></div>
    <div class="clearfix"><span class="num_option">B</span><div class="answer_p">Beta</div></div>
  </div>
</div></body></html>`
}

func TestAssignmentQuestionsBridgesGlobalWorkAndCachesEvidence(t *testing.T) {
	c := testClient(t)
	c.assignments = []Assignment{{
		ID: "course-1:class-1:42", CourseID: "course-1", ClassID: "class-1", Title: "Synthetic assignment",
		DetailURL: "https://mooc1.chaoxing.com/mooc-ans/mooc2/work/task?courseId=course-1&classId=class-1&cpi=cpi-1&workId=42&answerId=0&enc=enc-1",
	}}
	c.assignmentsAt = time.Now()
	reads := 0
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		reads++
		if r.URL.Path != "/mooc-ans/mooc2/work/task" {
			t.Fatalf("unexpected read %s", r.URL)
		}
		return response(r, assignmentQuestionPage()), nil
	})
	v, e := c.Call(context.Background(), "get_assignment_questions", map[string]any{"id": "course-1:class-1:42"})
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	if out["job_id"] != "42" || len(out["questions"].([]WorkQuestion)) != 1 {
		t.Fatal(out)
	}
	question := out["questions"].([]WorkQuestion)[0]
	if question.Title != "Pick one" || question.Type != "single" || question.AnswerType != "0" || len(question.Options) != 2 {
		t.Fatalf("%+v", question)
	}
	if question.Options[0] != "A Alpha" || question.Options[1] != "B Beta" {
		t.Fatalf("%+v", question.Options)
	}
	form := out["form_data"].(map[string]string)
	if form["workId"] != "42" || form["answerwqbid"] != "q1," || form["pyFlag"] != "" {
		t.Fatal(form)
	}
	if _, ok := form["answertypeq1"]; ok {
		t.Fatal("answer field leaked into submission form")
	}
	set, e := c.GetWorkQuestions(context.Background(), "course-1", "class-1", "42")
	if e != nil || reads != 1 || len(set.Questions) != 1 {
		t.Fatal(set, e, reads)
	}
}

func TestAssignmentQuestionsRejectsReadOnlyPreview(t *testing.T) {
	c := testClient(t)
	c.assignments = []Assignment{{ID: "course-1:class-1:42", CourseID: "course-1", ClassID: "class-1", DetailURL: "https://mooc1.chaoxing.com/mooc-ans/mooc2/work/task?courseId=course-1&classId=class-1&workId=42"}}
	c.assignmentsAt = time.Now()
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		resp := response(r, `<html><body>查看详情</body></html>`)
		resp.Request = r.Clone(r.Context())
		resp.Request.URL.Path = "/mooc-ans/mooc2/work/preview"
		return resp, nil
	})
	_, e := c.AssignmentQuestions(context.Background(), map[string]any{"id": "course-1:class-1:42"})
	if e == nil || SafeFault(e).Code != "WORK_UNAVAILABLE" || !strings.Contains(e.Error(), "read-only") {
		t.Fatal(e)
	}
}
