package answer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type staticProvider struct {
	value string
	err   error
}

func (p staticProvider) Name() string { return "static" }
func (p staticProvider) Query(context.Context, Question) (string, float64, error) {
	return p.value, 0.95, p.err
}

func TestEngineFallsBackAndCaches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.json")
	engine, err := NewEngine([]Provider{staticProvider{err: errors.New("miss")}, staticProvider{value: "B"}}, path, 0.8)
	if err != nil {
		t.Fatal(err)
	}
	q := Question{ID: "q1", Title: "Pick", Type: "single", Options: []string{"A one", "B two"}}
	first, err := engine.Answer(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "B" || first.Source != "static" || first.Confidence != 0.95 {
		t.Fatalf("%+v", first)
	}
	engine.providers = []Provider{staticProvider{err: errors.New("disabled")}}
	second, err := engine.Answer(context.Background(), q)
	if err != nil || second.Text != "B" {
		t.Fatalf("cache miss: %+v err=%v", second, err)
	}
}

func TestNormalizeQuestionTypes(t *testing.T) {
	tests := []struct {
		q    Question
		raw  string
		want string
	}{
		{Question{Type: "single", Options: []string{"A one", "B two"}}, "B two", "B"},
		{Question{Type: "multiple", Options: []string{"A one", "B two", "C three"}}, "C,A,C", "AC"},
		{Question{Type: "judgement"}, "错误", "false"},
		{Question{Type: "completion"}, "one\n\ntwo", "one\ntwo"},
	}
	for _, tc := range tests {
		got, err := Normalize(tc.q, tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("%+v %q: got %q err=%v", tc.q, tc.raw, got, err)
		}
	}
}

func TestLLMProviderParsesOpenAIResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatal("missing key")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"Answer\\\":[\\\"B\\\"]}\\n```\"}}]}"))
	}))
	defer server.Close()
	p := NewLLM(server.URL+"/v1/chat/completions", "key", "test-model", 0)
	value, confidence, err := p.Query(context.Background(), Question{Type: "single"})
	if err != nil || value != "B" || confidence != 0.86 {
		t.Fatalf("value=%q confidence=%v err=%v", value, confidence, err)
	}
}

func TestTikuProviderParsesBestAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/query") {
			t.Fatal(r.URL.Path)
		}
		w.Write([]byte(`{"answer":{"bestAnswer":["A","C"],"confidence":0.91}}`))
	}))
	defer server.Close()
	p := NewTiku(server.URL+"/query", "", 0)
	value, confidence, err := p.Query(context.Background(), Question{Type: "multiple"})
	if err != nil || value != "A\nC" || confidence != 0.91 {
		t.Fatalf("value=%q confidence=%v err=%v", value, confidence, err)
	}
}
