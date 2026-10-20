package main

import (
	"strings"
	"testing"
)

func TestResponsesProviderConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		item answerProviderConfig
		want string
	}{
		{
			name: "requires model",
			item: answerProviderConfig{Type: "responses", BaseURL: "https://api.example.com/v1", APIKey: "key"},
			want: "requires model",
		},
		{
			name: "requires api key",
			item: answerProviderConfig{Type: "responses", BaseURL: "https://api.example.com/v1", Model: "gpt-test"},
			want: "requires an api_key source",
		},
		{
			name: "rejects chat-completions endpoint",
			item: answerProviderConfig{Type: "responses", BaseURL: "https://api.example.com/v1/chat/completions", APIKey: "key", Model: "gpt-test"},
			want: "API root",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := answerConfig{Enabled: true, Providers: []answerProviderConfig{tc.item}}
			_, err := cfg.engine(t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestResponsesProviderConfigBuildsEngine(t *testing.T) {
	cfg := answerConfig{
		Enabled:       true,
		MinConfidence: 0.8,
		Providers: []answerProviderConfig{{
			Type:    "responses",
			BaseURL: "https://api.example.com/v1",
			APIKey:  "key",
			Model:   "gpt-test",
		}},
	}
	engine, err := cfg.engine(t.TempDir())
	if err != nil || !engine.Enabled() {
		t.Fatalf("engine=%v err=%v", engine, err)
	}
}
