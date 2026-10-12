package operation

import (
	"context"
	"testing"
)

func TestExecuteValidationAndTrace(t *testing.T) {
	calls := 0
	handler := func(context.Context, string, map[string]any) (any, error) {
		calls++
		return map[string]any{"complete": false, "items": []any{}}, nil
	}
	if _, e := Execute(context.Background(), "assignments.list", map[string]any{"url": "forbidden"}, handler); e == nil || calls != 0 {
		t.Fatal(e, calls)
	}
	v, e := Execute(context.Background(), "assignments.list", nil, handler)
	if e != nil {
		t.Fatal(e)
	}
	m := v.(map[string]any)["meta"].(map[string]any)
	if len(m["trace_id"].(string)) != 24 || calls != 1 {
		t.Fatal(m, calls)
	}
}
