package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/operation"
)

func rpc(h http.Handler, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://mcp.example.com/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func result(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return m
}
func TestInitializeListCall(t *testing.T) {
	h := New(func(_ context.Context, name string, a map[string]any) (any, error) {
		if name != "list_assignments" || a["limit"] != float64(7) {
			t.Fatal(name, a)
		}
		return map[string]any{"complete": false, "coverage": "global_feed_only", "items": []any{}}, nil
	})
	w := rpc(h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"example","version":"1"}}}`)
	m := result(t, w)
	if m["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatal(m)
	}
	w = rpc(h, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	list := result(t, w)["result"].(map[string]any)["tools"].([]any)
	if len(list) != len(operation.All()) {
		t.Fatal(len(list))
	}
	for _, v := range list {
		tool := v.(map[string]any)
		if tool["annotations"].(map[string]any)["readOnlyHint"] != true || tool["securitySchemes"] == nil {
			t.Fatal(tool)
		}
	}
	w = rpc(h, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_assignments","arguments":{"limit":7}}}`)
	r := result(t, w)["result"].(map[string]any)
	data := r["structuredContent"].(map[string]any)
	if data["complete"] != false || data["coverage"] != "global_feed_only" {
		t.Fatal(r)
	}
	w = rpc(h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if w.Code != 202 || w.Body.Len() != 0 {
		t.Fatal(w.Code)
	}
}
func TestValidationDoesNotCallUpstream(t *testing.T) {
	calls := 0
	h := New(func(context.Context, string, map[string]any) (any, error) { calls++; return nil, nil })
	for _, params := range []string{`{"name":"delete_assignment","arguments":{}}`, `{"name":"get_assignment","arguments":{}}`, `{"name":"get_assignment","arguments":{"id":123}}`, `{"name":"get_schedule","arguments":{"week":31}}`, `{"name":"list_assignments","arguments":{"limit":1.5}}`, `{"name":"list_assignments","arguments":{"offset":-1}}`, `{"name":"list_courses","arguments":{"unexpected":true}}`, `{"name":"list_materials","arguments":{"course_id":"1"}}`, `{"name":"list_courses","arguments":null}`} {
		w := rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+params+`}`)
		if result(t, w)["error"] == nil {
			t.Fatal(params, w.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal("invalid calls reached upstream")
	}
}
func TestTransportBoundsAndErrorRedaction(t *testing.T) {
	h := New(func(context.Context, string, map[string]any) (any, error) {
		return nil, errors.New("example-secret-token")
	})
	w := rpc(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_courses"}}`)
	if strings.Contains(w.Body.String(), "example-secret-token") || !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Body.String())
	}
	w = rpc(h, strings.Repeat("x", maxBody+1))
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`[]`, `{"jsonrpc":"1.0","id":1,"method":"ping"}`, `{"jsonrpc":"2.0","id":{},"method":"ping"}`} {
		if w = rpc(h, body); w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "https://mcp.example.com/mcp", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "https://mcp.example.com/mcp", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestConcurrencyBound(t *testing.T) {
	var active atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	h := New(func(context.Context, string, map[string]any) (any, error) {
		active.Add(1)
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return map[string]any{}, nil
	})
	var wg sync.WaitGroup
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_courses"}}`
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); rpc(h, body) }()
	}
	for range 4 {
		<-entered
	}
	if w := rpc(h, body); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if active.Load() != 4 {
		t.Fatal(active.Load())
	}
	close(release)
	wg.Wait()
}
