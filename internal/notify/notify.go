// Package notify delivers operator-configured outbound alerts. It never
// carries credentials and is only used by the optional activity watcher.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Message struct {
	Title string
	Body  string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

type Multi []Sender

func (m Multi) Send(ctx context.Context, msg Message) error {
	var errs []error
	for _, s := range m {
		if err := s.Send(ctx, msg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type Feishu struct {
	Webhook string
	Client  *http.Client
}

func (f Feishu) Send(ctx context.Context, m Message) error {
	if strings.TrimSpace(f.Webhook) == "" {
		return errors.New("feishu webhook is not configured")
	}
	payload := map[string]any{"msg_type": "text", "content": map[string]any{"text": m.Title + "\n" + m.Body}}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Webhook, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("feishu webhook returned HTTP %d", resp.StatusCode)
	}
	var ack struct {
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
	}
	if json.Unmarshal(body, &ack) == nil {
		if ack.Code != nil && *ack.Code != 0 {
			return fmt.Errorf("feishu webhook rejected message with code %d", *ack.Code)
		}
		if ack.StatusCode != nil && *ack.StatusCode != 0 {
			return fmt.Errorf("feishu webhook rejected message with status %d", *ack.StatusCode)
		}
	}
	return nil
}

func (f Feishu) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

type Ntfy struct {
	BaseURL string
	Topic   string
	Client  *http.Client
}

func (n Ntfy) Send(ctx context.Context, m Message) error {
	base, err := url.Parse(strings.TrimSpace(n.BaseURL))
	if err != nil || base.Scheme != "https" && base.Scheme != "http" || base.Host == "" {
		return errors.New("ntfy base url is invalid")
	}
	if strings.TrimSpace(n.Topic) == "" {
		return errors.New("ntfy topic is not configured")
	}
	target := strings.TrimRight(base.String(), "/") + "/" + url.PathEscape(n.Topic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", m.Title)
	req.Header.Set("Priority", "5")
	req.Header.Set("Tags", "rotating_light")
	resp, err := n.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("ntfy returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n Ntfy) client() *http.Client {
	if n.Client != nil {
		return n.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}
