package operation

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestValidationAcrossNumericTransports(t *testing.T) {
	d, _ := Find("assignments.list")
	for _, v := range []any{json.Number("7"), float64(7), int(7)} {
		a := map[string]any{"limit": v}
		if Validate(d, a) != nil || a["limit"] != float64(7) {
			t.Fatal(a)
		}
	}
	for _, a := range []map[string]any{{"limit": 1.5}, {"limit": math.NaN()}, {"limit": json.Number("1e999")}, {"status": "finished"}, {"class_id": "x"}, {"url": "https://example.com"}, {"course": "bad\x01"}} {
		if Validate(d, a) == nil {
			t.Fatal(a)
		}
	}
}
func TestResultSeparatesCoveragePaginationAndState(t *testing.T) {
	r, e := Result("list_assignments", map[string]any{"complete": true, "has_more": true, "coverage": "selected_class", "items": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	m := r["meta"].(map[string]any)
	if m["status"] != "complete" || m["pagination"].(map[string]any)["has_more"] != true {
		t.Fatal(r)
	}
	r, e = Result("list_assignments", map[string]any{"items": []any{}})
	if e != nil || r["meta"].(map[string]any)["status"] != "partial" {
		t.Fatal(r, e)
	}
}
func TestNoticeRawInputIsExplicitlyBoolean(t *testing.T) {
	d, _ := Find("notices.list")
	for _, valid := range []any{true, false} {
		if err := Validate(d, map[string]any{"include_raw": valid}); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []any{"true", 1, json.Number("1")} {
		if err := Validate(d, map[string]any{"include_raw": invalid}); err == nil {
			t.Fatal("non-boolean include_raw accepted", invalid)
		}
	}
}

func TestSignArgumentsAcceptBoundedNumbers(t *testing.T) {
	d, _ := Find("sign_activity")
	valid := map[string]any{"course_id": "course-1", "class_id": "class-1", "activity_id": "activity-1", "latitude": json.Number("31.123456"), "longitude": json.Number("121.123456")}
	if err := Validate(d, valid); err != nil || valid["latitude"] != 31.123456 {
		t.Fatal(valid, err)
	}
	for _, invalid := range []map[string]any{
		{"course_id": "course-1", "class_id": "class-1", "activity_id": "activity-1", "latitude": json.Number("91")},
		{"course_id": "course-1", "class_id": "class-1", "activity_id": "activity-1", "longitude": json.Number("-181")},
		{"course_id": "course-1", "class_id": "class-1", "activity_id": "activity-1", "latitude": json.Number("NaN")},
	} {
		if Validate(d, invalid) == nil {
			t.Fatal(invalid)
		}
	}
}

func TestWorkContractsUseDedicatedScopeAndBoundedAnswer(t *testing.T) {
	d, ok := Find("workdrafts.upsert")
	if !ok || RequiredScope(d) != "chaoxing.work" {
		t.Fatal(d, ok)
	}
	answer := strings.Repeat("a", 4096)
	valid := map[string]any{"course_id": "course-1", "class_id": "class-1", "job_id": "work-1", "question_id": "q1", "answer": answer}
	if err := Validate(d, valid); err != nil {
		t.Fatal(err)
	}
	valid["answer"] = strings.Repeat("a", 4097)
	if err := Validate(d, valid); err == nil {
		t.Fatal("oversized answer accepted")
	}
	for _, name := range []string{"list_work_drafts", "delete_work_draft", "export_work_draft"} {
		d, ok := Find(name)
		if !ok || RequiredScope(d) != "chaoxing.work" {
			t.Fatal(name, d, ok)
		}
	}
}

func TestAssignmentQuestionsContractIsReadOnlyAndBridgeable(t *testing.T) {
	d, ok := Find("get_assignment_questions")
	if !ok || RequiredScope(d) != "chaoxing.read" {
		t.Fatal(d, ok)
	}
	if err := Validate(d, map[string]any{"id": "course-1:class-1:42"}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(d, map[string]any{}); err == nil {
		t.Fatal("missing assignment id accepted")
	}
	assignment := map[string]any{"id": "course-1:class-1:42", "course_id": "course-1", "class_id": "class-1", "title": "Synthetic", "course": "Synthetic course", "status": "未交", "submission_state": "pending", "availability_state": "unknown", "raw_status": "未交"}
	job := map[string]any{"id": "42", "course_id": "course-1", "class_id": "class-1", "knowledge_id": "", "knowledge": "course assignment", "title": "Synthetic", "type": "work", "is_passed": false}
	question := map[string]any{"id": "q1", "title": "Pick one", "type": "single", "answer_type": "0", "options": []any{"A Alpha", "B Beta"}}
	out, e := Result("get_assignment_questions", map[string]any{"assignment": assignment, "job": job, "job_id": "42", "questions": []any{question}, "form_data": map[string]any{}, "complete": true, "coverage": "assignment_task_redirect_work_page"})
	if e != nil || out["meta"].(map[string]any)["status"] != "complete" {
		t.Fatal(out, e)
	}
}

func TestActivityPageContractIsReadOnlyEvidenceCollection(t *testing.T) {
	d, ok := Find("get_activity_page")
	if !ok || RequiredScope(d) != "chaoxing.read" {
		t.Fatal(d, ok)
	}
	valid := map[string]any{"course_id": "course-1", "class_id": "class-1", "activity_id": "9301"}
	if err := Validate(d, valid); err != nil {
		t.Fatal(err)
	}
	if err := Validate(d, map[string]any{"course_id": "course-1"}); err == nil {
		t.Fatal("incomplete activity identity accepted")
	}
	activity := map[string]any{"id": "9301", "course_id": "course-1", "class_id": "class-1", "course": "Synthetic", "title": "Practice", "kind": "practice", "state": "active", "user_state": "unsigned", "raw_type": 42, "raw_status": 1, "raw_user_status": 0, "signable": false, "answerable": false}
	page := map[string]any{"activity": activity, "entry_url": "https://mobilelearn.chaoxing.com/page", "final_url": "https://mobilelearn.chaoxing.com/page", "title": "Practice", "text": "evidence", "iframes": []any{}, "scripts": []any{}, "links": []any{}, "forms": []any{}, "body_sha256": strings.Repeat("a", 64)}
	out, e := Result("get_activity_page", map[string]any{"activity_page": page, "complete": true, "coverage": "single_activity_page_structure"})
	if e != nil || out["meta"].(map[string]any)["status"] != "complete" {
		t.Fatal(out, e)
	}
}

func TestRegistryCatalogAndNoUnsupportedArguments(t *testing.T) {
	ids := map[string]bool{}
	for _, d := range All() {
		if ids[d.ID] {
			t.Fatal(d.ID)
		}
		ids[d.ID] = true
		if d.InputSchema() == nil || d.OutputSchema() == nil {
			t.Fatal(d)
		}
	}
	d, _ := Find("list_materials")
	if _, ok := d.Fields["folder_id"]; ok {
		t.Fatal("unsupported folder advertised")
	}
}

func TestStudyDigestContract(t *testing.T) {
	d, ok := Find("study.digest")
	if !ok || d.Name != "get_study_digest" {
		t.Fatal(d)
	}
	valid := map[string]any{"course_id": "course-1", "class_id": "class-1", "limit": 20, "notice_limit": 5, "exam_limit": 10}
	if Validate(d, valid) != nil {
		t.Fatal(valid)
	}
	for _, invalid := range []map[string]any{{"notice_limit": 0}, {"notice_limit": 21}, {"exam_limit": 0}, {"exam_limit": 21}, {"status": "pending"}, {"cursor": "opaque"}} {
		if Validate(d, invalid) == nil {
			t.Fatal("invalid digest arguments accepted", invalid)
		}
	}
	out, e := Result("get_study_digest", map[string]any{"items": []any{}, "counts": map[string]any{}, "sources": map[string]any{}, "complete": false})
	if e != nil || out["meta"].(map[string]any)["status"] != "partial" {
		t.Fatal(out, e)
	}
	caps := Capabilities()
	if caps["contract_version"] != "15" {
		t.Fatal(caps["contract_version"])
	}
	unsupported, ok := caps["unsupported_domains"].([]string)
	if !ok {
		t.Fatalf("unsupported_domains has type %T", caps["unsupported_domains"])
	}
	for _, domain := range []string{"classroom_practice_submission"} {
		found := false
		for _, actual := range unsupported {
			if actual == domain {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("unsupported_domains missing %s: %#v", domain, unsupported)
		}
	}
}
func TestRuntimeRejectsMalformedMetadata(t *testing.T) {
	for _, v := range []map[string]any{{"warnings": "not an array"}, {"items": "not an array"}, {"meta": map[string]any{"status": "complete", "coverage": map[string]any{"complete": "yes", "scope": "example"}}}} {
		if _, e := Result("list_assignments", v); e == nil {
			t.Fatal("invalid output accepted", v)
		}
	}
}
