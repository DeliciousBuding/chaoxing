package answer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// OpenAIResponsesProvider uses the official OpenAI Go SDK and the Responses
// API. It deliberately performs one bounded, stateless call per question: the
// surrounding Engine already owns caching, normalization, confidence policy
// and provider fallback. An interactive agent loop would add latency and cost
// without improving the current structured-question contract.
type OpenAIResponsesProvider struct {
	NameLabel string
	Client    openai.Client
	Model     string
}

func NewOpenAIResponses(baseURL, apiKey, model string, timeout time.Duration) *OpenAIResponsesProvider {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	// An explicit (even empty) key and base URL override ambient OPENAI_API_KEY
	// and OPENAI_BASE_URL values, keeping this provider deterministic.
	client := openai.NewClient(
		option.WithBaseURL(strings.TrimSpace(baseURL)),
		option.WithAPIKey(strings.TrimSpace(apiKey)),
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
		option.WithRequestTimeout(timeout),
		option.WithMaxRetries(0),
	)
	return &OpenAIResponsesProvider{NameLabel: "responses", Client: client, Model: strings.TrimSpace(model)}
}

func (p *OpenAIResponsesProvider) Name() string {
	if p.NameLabel != "" {
		return p.NameLabel
	}
	return "responses"
}

func (p *OpenAIResponsesProvider) Query(ctx context.Context, q Question) (string, float64, error) {
	if p.Model == "" {
		return "", 0, fmt.Errorf("responses provider is not configured")
	}
	result, err := p.Client.Responses.New(ctx, responses.ResponseNewParams{
		Instructions: openai.String(llmPrompt(q.Type)),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(llmUserPrompt(q)),
		},
		Model: p.Model,
		Store: openai.Bool(false),
	})
	if err != nil {
		return "", 0, fmt.Errorf("responses provider call failed")
	}
	if result == nil {
		return "", 0, fmt.Errorf("responses provider returned no response")
	}
	values, err := parseAnswerJSON(result.OutputText())
	if err != nil {
		return "", 0, err
	}
	return strings.Join(values, "\n"), 0.88, nil
}
