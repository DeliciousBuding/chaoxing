package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

func jsonValue(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCatalogConvergesWithOperations(t *testing.T) {
	list := result(t, rpc(New(nil), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))["result"].(map[string]any)["tools"].([]any)
	definitions := operation.All()
	if len(list) != len(definitions) {
		t.Fatalf("got %d tools, want %d", len(list), len(definitions))
	}
	for i, d := range definitions {
		got := list[i].(map[string]any)
		if got["name"] != d.Name || got["title"] != d.Title || got["description"] != d.Description {
			t.Fatal(got)
		}
		if !reflect.DeepEqual(got["inputSchema"], jsonValue(t, d.InputSchema())) ||
			!reflect.DeepEqual(got["outputSchema"], jsonValue(t, d.OutputSchema())) {
			t.Fatalf("schema drift: %s", d.Name)
		}
		wantAnnotations := map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
		if !reflect.DeepEqual(got["annotations"], wantAnnotations) {
			t.Fatal(got)
		}
		security := jsonValue(t, []map[string]any{{"type": "oauth2", "scopes": []string{"chaoxing.read"}}})
		if !reflect.DeepEqual(got["securitySchemes"], security) ||
			!reflect.DeepEqual(got["_meta"].(map[string]any)["securitySchemes"], security) {
			t.Fatal(got)
		}
	}
}

func TestSharedValidationAndNewTools(t *testing.T) {
	valid := []struct {
		name string
		args map[string]any
	}{
		{"list_assignments", map[string]any{"course_id": "course-1", "class_id": "class-1", "status": "unknown", "limit": 7}},
		{"list_notices", map[string]any{"limit": 3, "cursor": "opaque-next", "include_raw": true}},
		{"get_study_overview", map[string]any{"course_id": "course-1"}},
		{"list_action_items", map[string]any{"course": "Synthetic course"}},
		{"get_study_digest", map[string]any{"course_id": "course-1", "class_id": "class-1", "limit": 20, "notice_limit": 5, "exam_limit": 10}},
		{"get_capabilities", map[string]any{}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := operation.Find(tc.name)
			want := jsonValue(t, tc.args).(map[string]any)
			if err := operation.Validate(d, want); err != nil {
				t.Fatal(err)
			}
			called := false
			h := New(func(_ context.Context, name string, args map[string]any) (any, error) {
				called = true
				if name != tc.name || !reflect.DeepEqual(args, want) {
					t.Fatalf("%s: %#v", name, args)
				}
				return map[string]any{}, nil
			})
			params, _ := json.Marshal(map[string]any{"name": tc.name, "arguments": tc.args})
			got := result(t, rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+string(params)+`}`))
			if !called || got["error"] != nil {
				t.Fatal(got)
			}
		})
	}
	calls := 0
	h := New(func(context.Context, string, map[string]any) (any, error) { calls++; return nil, nil })
	for _, params := range []string{
		`{"name":"list_assignments","arguments":{"class_id":"class-1"}}`,
		`{"name":"list_assignments","arguments":{"status":"graded"}}`,
		`{"name":"list_notices","arguments":{"limit":101}}`,
		`{"name":"list_notices","arguments":{"cursor":"bad\tvalue"}}`,
		`{"name":"list_materials","arguments":{"course_id":"c","class_id":"k","cpi":"p","folder_id":"f"}}`,
		`{"name":"get_study_overview","arguments":{"offset":1}}`,
		`{"name":"get_study_digest","arguments":{"notice_limit":21}}`,
		`{"name":"courses.list","arguments":{}}`,
	} {
		got := result(t, rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+params+`}`))
		if got["error"] == nil {
			t.Fatal(params, got)
		}
	}
	if calls != 0 {
		t.Fatal("invalid arguments reached upstream")
	}
}

func TestResultsPreserveLegacyAndAddMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  any
		status string
	}{
		{"list_assignments", map[string]any{"complete": false, "coverage": "global_feed_only", "items": []any{}, "warnings": []string{"Partial source"}, "fetched_at": "2026-10-13T00:00:00Z", "has_more": true, "offset": 7, "total_in_feed": 15}, "partial"},
		{"list_courses", map[string]any{"courses": []any{}}, "complete"},
		{"get_study_overview", map[string]any{"complete": false, "coverage_details": map[string]any{"queried_courses": 1}, "meta": map[string]any{"custom": "preserved"}}, "partial"},
		{"list_exams", []any{}, "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := New(func(context.Context, string, map[string]any) (any, error) { return tc.value, nil })
			got := result(t, rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tc.name+`"}}`))["result"].(map[string]any)
			structured := got["structuredContent"].(map[string]any)
			meta := structured["meta"].(map[string]any)
			if meta["status"] != tc.status || meta["coverage"] == nil || meta["freshness"] == nil || meta["warnings"] == nil {
				t.Fatal(meta)
			}
			raw := jsonValue(t, tc.value)
			if legacy, ok := raw.(map[string]any); ok {
				for key, v := range legacy {
					if key == "meta" {
						continue
					}
					if !reflect.DeepEqual(structured[key], v) {
						t.Fatalf("lost legacy %s", key)
					}
				}
			} else if !reflect.DeepEqual(structured["result"], raw) {
				t.Fatal(structured)
			}
			content := got["content"].([]any)[0].(map[string]any)["text"].(string)
			var textResult any
			if err := json.Unmarshal([]byte(content), &textResult); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(textResult, structured) {
				t.Fatal("text and structured results differ")
			}
			if tc.name == "list_assignments" {
				if meta["coverage"].(map[string]any)["complete"] != false || meta["pagination"].(map[string]any)["has_more"] != true {
					t.Fatal(meta)
				}
			}
			if tc.name == "get_study_overview" && meta["custom"] != "preserved" {
				t.Fatal(meta)
			}
		})
	}
}

func TestTypedErrorsAreSafeAndUnknownErrorsRedacted(t *testing.T) {
	const secret = "synthetic-secret https://private.example/path"
	for _, code := range []string{"AUTH_REQUIRED", "AUTH_COOLDOWN", "CREDENTIALS_UNAVAILABLE", "INVALID_ARGUMENT", "AMBIGUOUS_COURSE", "NOT_FOUND", "COURSE_ACCESS", "UNSUPPORTED", "UPSTREAM_SCHEMA", "UPSTREAM_UNAVAILABLE", "UPSTREAM_HTTP", "RESPONSE_TOO_LARGE", "STATE_WRITE_FAILED", "INVALID_URL", "INVALID_TOOL"} {
		t.Run(code, func(t *testing.T) {
			err := fmt.Errorf("wrapped %s: %w", secret, &upstream.Fault{Code: code, Message: secret, Category: secret, Hint: secret, Retryable: true})
			h := New(func(context.Context, string, map[string]any) (any, error) {
				return nil, err
			})
			w := rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_courses"}}`)
			got := result(t, w)["result"].(map[string]any)
			safe := got["structuredContent"].(map[string]any)["error"].(map[string]any)
			if got["isError"] != true || safe["code"] != code || safe["message"] == "" || strings.Contains(w.Body.String(), secret) {
				t.Fatal(w.Body.String())
			}
			if !reflect.DeepEqual(safe, jsonValue(t, upstream.SafeFault(err))) {
				t.Fatal("MCP error differs from shared SafeFault", safe)
			}
			content := got["content"].([]any)[0].(map[string]any)["text"].(string)
			var textError any
			if err := json.Unmarshal([]byte(content), &textError); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(textError, safe) {
				t.Fatal("text and structured errors differ")
			}
		})
	}
	for _, err := range []error{errors.New(secret), &upstream.Fault{Code: secret, Message: secret}, fmt.Errorf("AUTH_REQUIRED: %s", secret)} {
		h := New(func(context.Context, string, map[string]any) (any, error) { return nil, err })
		w := rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_courses"}}`)
		got := result(t, w)["result"].(map[string]any)
		safe := got["structuredContent"].(map[string]any)["error"].(map[string]any)
		if got["isError"] != true || safe["code"] != "INTERNAL" || safe["message"] != "Operation failed." || safe["category"] != "internal" || safe["retryable"] != false || strings.Contains(w.Body.String(), secret) {
			t.Fatal(w.Body.String())
		}
		if !reflect.DeepEqual(safe, jsonValue(t, upstream.SafeFault(err))) {
			t.Fatal("unknown error differs from shared SafeFault", safe)
		}
	}
}

func TestUnavailableResultsRemainRedacted(t *testing.T) {
	for _, value := range []any{make(chan int), map[string]any{"data": strings.Repeat("x", maxResult)}} {
		h := New(func(context.Context, string, map[string]any) (any, error) { return value, nil })
		got := result(t, rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_courses"}}`))
		if got["error"].(map[string]any)["code"] != float64(-32603) {
			t.Fatal(got)
		}
	}
}

func TestLegacyProtocolVersionsRemainSupported(t *testing.T) {
	for _, version := range []string{"2025-03-26", "2025-06-18", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			h := NewWithVersion(nil, "synthetic-version")
			w := rpc(h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+version+`"}}`)
			got := result(t, w)["result"].(map[string]any)
			if got["protocolVersion"] != version || got["serverInfo"].(map[string]any)["version"] != "synthetic-version" {
				t.Fatal(got)
			}
		})
	}
}
