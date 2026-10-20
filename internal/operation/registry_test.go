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
	if caps["contract_version"] != "10" {
		t.Fatal(caps["contract_version"])
	}
	unsupported, ok := caps["unsupported_domains"].([]string)
	if !ok {
		t.Fatalf("unsupported_domains has type %T", caps["unsupported_domains"])
	}
	for _, domain := range []string{"photo_signin", "face_signin"} {
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
