package answer

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewNewAPI builds a chat-completions provider for NewAPI-style gateways
// (including one-api/new-api deployments). The base URL must be the API root:
// the provider derives /v1/chat/completions itself so misconfigured full
// endpoints are rejected instead of silently doubled.
func NewNewAPI(baseURL, apiKey, model string, timeout time.Duration) (*LLMProvider, error) {
	endpoint, err := NewAPIEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	// Reasoning-first models spend tokens on hidden reasoning before the
	// final answer; keep enough headroom for the JSON contract.
	return &LLMProvider{
		NameLabel: "newapi", Endpoint: endpoint, APIKey: strings.TrimSpace(apiKey),
		Model: strings.TrimSpace(model), MaxTokens: 768,
		Client: &http.Client{Timeout: timeout},
	}, nil
}

// NewAPIEndpoint normalizes an API root into its chat-completions URL.
func NewAPIEndpoint(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("newapi base_url must be an absolute HTTP(S) URL")
	}
	path := strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1")
	if path != "" {
		return "", errors.New("newapi base_url must be the API root, not a full endpoint path")
	}
	u.Path = "/v1/chat/completions"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}
