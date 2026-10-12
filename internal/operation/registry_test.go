package operation

import (
	"encoding/json"
	"math"
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

func TestRuntimeRejectsMalformedMetadata(t *testing.T) {
	for _, v := range []map[string]any{{"warnings": "not an array"}, {"items": "not an array"}, {"meta": map[string]any{"status": "complete", "coverage": map[string]any{"complete": "yes", "scope": "example"}}}} {
		if _, e := Result("list_assignments", v); e == nil {
			t.Fatal("invalid output accepted", v)
		}
	}
}
