package answer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAPIEndpoint(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "https://api.example.com", want: "https://api.example.com/v1/chat/completions"},
		{in: "https://api.example.com/", want: "https://api.example.com/v1/chat/completions"},
		{in: "https://api.example.com/v1", want: "https://api.example.com/v1/chat/completions"},
		{in: "https://api.example.com/v1/", want: "https://api.example.com/v1/chat/completions"},
		{in: "https://api.example.com/v1/chat/completions", wantErr: true},
		{in: "https://api.example.com/v1/responses", wantErr: true},
		{in: "ftp://api.example.com", wantErr: true},
		{in: "not a url", wantErr: true},
	}
	for _, tc := range cases {
		got, err := NewAPIEndpoint(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("NewAPIEndpoint(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("NewAPIEndpoint(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestNewNewAPIProviderQuery(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	var gotMaxTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Errorf("decode request failed")
		}
		gotModel, gotMaxTokens = body.Model, body.MaxTokens
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"Answer\":[\"B\"]}"}}]}`))
	}))
	defer server.Close()

	provider, err := NewNewAPI(server.URL, "secret-key", "deepseek-v4.1-flash", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, confidence, err := provider.Query(context.Background(), Question{
		Type: "single", Title: "1+1=?", Options: []string{"A.1", "B.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw != "B" || confidence != 0.86 {
		t.Fatalf("unexpected answer %q confidence %v", raw, confidence)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("authorization header missing")
	}
	if gotModel != "deepseek-v4.1-flash" || gotMaxTokens != 768 {
		t.Fatalf("unexpected model %q max_tokens %d", gotModel, gotMaxTokens)
	}
	if provider.Name() != "newapi" {
		t.Fatalf("unexpected provider name %q", provider.Name())
	}
}

func TestNewNewAPIProviderRejectsBadOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"no json here"}}]}`))
	}))
	defer server.Close()
	provider, err := NewNewAPI(server.URL, "k", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.Query(context.Background(), Question{Type: "single", Title: "q"}); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("expected JSON parse failure, got %v", err)
	}
}
