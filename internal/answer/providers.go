package answer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type LLMProvider struct {
	NameLabel string
	Endpoint  string
	APIKey    string
	Model     string
	Client    *http.Client
}

func NewLLM(endpoint, apiKey, model string, timeout time.Duration) *LLMProvider {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &LLMProvider{
		NameLabel: "llm", Endpoint: strings.TrimSpace(endpoint), APIKey: strings.TrimSpace(apiKey),
		Model: strings.TrimSpace(model), Client: &http.Client{Timeout: timeout},
	}
}

func (p *LLMProvider) Name() string {
	if p.NameLabel != "" {
		return p.NameLabel
	}
	return "llm"
}

func (p *LLMProvider) Query(ctx context.Context, q Question) (string, float64, error) {
	if p.Endpoint == "" || p.Model == "" {
		return "", 0, fmt.Errorf("llm provider is not configured")
	}
	messages := []map[string]string{
		{"role": "system", "content": llmPrompt(q.Type)},
		{"role": "user", "content": llmUserPrompt(q)},
	}
	body, err := json.Marshal(map[string]any{"model": p.Model, "messages": messages, "stream": false, "temperature": 0})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("llm endpoint invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("llm transport failed")
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(payload) > 1024*1024 || resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("llm response failed")
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &completion) != nil || len(completion.Choices) == 0 {
		return "", 0, fmt.Errorf("llm response invalid")
	}
	values, err := parseAnswerJSON(completion.Choices[0].Message.Content)
	if err != nil {
		return "", 0, err
	}
	return strings.Join(values, "\n"), 0.86, nil
}

type TikuProvider struct {
	NameLabel string
	Endpoint  string
	APIKey    string
	Client    *http.Client
}

func NewTiku(endpoint, apiKey string, timeout time.Duration) *TikuProvider {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &TikuProvider{NameLabel: "tiku", Endpoint: strings.TrimSpace(endpoint), APIKey: strings.TrimSpace(apiKey), Client: &http.Client{Timeout: timeout}}
}

func (p *TikuProvider) Name() string {
	if p.NameLabel != "" {
		return p.NameLabel
	}
	return "tiku"
}

func (p *TikuProvider) Query(ctx context.Context, q Question) (string, float64, error) {
	if p.Endpoint == "" {
		return "", 0, fmt.Errorf("tiku provider is not configured")
	}
	body, err := json.Marshal(map[string]any{"question": q.Title, "type": q.Type, "options": cleanOptions(q.Options)})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("tiku endpoint invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("tiku transport failed")
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(payload) > 1024*1024 || resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("tiku response failed")
	}
	var raw struct {
		Answer struct {
			BestAnswer []string `json:"bestAnswer"`
			Confidence float64  `json:"confidence"`
		} `json:"answer"`
		Data struct {
			Answer     []string `json:"answer"`
			Confidence float64  `json:"confidence"`
		} `json:"data"`
		Confidence float64 `json:"confidence"`
	}
	if json.Unmarshal(payload, &raw) != nil {
		return "", 0, fmt.Errorf("tiku response invalid")
	}
	values := raw.Answer.BestAnswer
	if len(values) == 0 {
		values = raw.Data.Answer
	}
	if len(values) == 0 {
		return "", 0, fmt.Errorf("tiku returned no answer")
	}
	confidence := raw.Answer.Confidence
	if confidence == 0 {
		confidence = raw.Data.Confidence
	}
	if confidence == 0 {
		confidence = raw.Confidence
	}
	if confidence == 0 {
		confidence = 0.9
	}
	return strings.Join(values, "\n"), confidence, nil
}

func llmPrompt(questionType string) string {
	switch questionType {
	case "single":
		return `单选题。只输出JSON：{"Answer":["B"]}，Answer为正确选项字母。`
	case "multiple":
		return `多选题。只输出JSON：{"Answer":["A","C"]}，Answer为所有正确选项字母。`
	case "judgement":
		return `判断题。只输出JSON：{"Answer":["正确"]}或{"Answer":["错误"]}。`
	case "completion":
		return `填空题。只输出JSON：{"Answer":["答案1","答案2"]}，按空顺序输出。`
	default:
		return `答题。只输出JSON：{"Answer":["答案"]}。`
	}
}

func llmUserPrompt(q Question) string {
	var b strings.Builder
	b.WriteString("题目：")
	b.WriteString(q.Title)
	if len(q.Options) > 0 {
		b.WriteString("\n选项：")
		for _, option := range q.Options {
			b.WriteString("\n")
			b.WriteString(option)
		}
	}
	return b.String()
}

func cleanOptions(options []string) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		out = append(out, strings.TrimSpace(option))
	}
	return out
}

func parseAnswerJSON(content string) ([]string, error) {
	text := strings.TrimSpace(strings.Trim(content, "`"))
	if strings.HasPrefix(text, "json\n") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "json\n"))
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("llm answer is not JSON")
	}
	var payload struct {
		Answer      []string `json:"Answer"`
		AnswerLower []string `json:"answer"`
	}
	if json.Unmarshal([]byte(text[start:end+1]), &payload) != nil {
		return nil, fmt.Errorf("llm answer JSON invalid")
	}
	values := payload.Answer
	if len(values) == 0 {
		values = payload.AnswerLower
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("llm answer is empty")
	}
	return values, nil
}
