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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Title    string
	Body     string
	Priority int
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

// sharedTransport is tuned for home-router deployments. TLS handshakes to
// CDN-fronted push providers intermittently exceed Go's default ten-second
// timer on consumer uplinks, so alarms would otherwise be lost exactly when
// the deployment is already unhealthy.
var sharedTransport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          4,
	MaxIdleConnsPerHost:   2,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   20 * time.Second,
	ResponseHeaderTimeout: 20 * time.Second,
	ExpectContinueTimeout: time.Second,
}

func sharedClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout, Transport: sharedTransport}
}

// Retry re-sends one alert up to Attempts times with linear backoff. It is
// meant for alarm paths (crash, panic) where a missed message is worse than a
// duplicate; ordinary activity notifications keep a single attempt because the
// watcher already retries them on the next tick.
type Retry struct {
	Sender   Sender
	Attempts int
	Delay    time.Duration
}

func (r Retry) Send(ctx context.Context, m Message) error {
	if r.Sender == nil {
		return nil
	}
	attempts := r.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	delay := r.Delay
	if delay <= 0 {
		delay = 2 * time.Second
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(delay * time.Duration(attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		}
		if err := r.Sender.Send(ctx, m); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (m Message) priority() int {
	if m.Priority < 1 {
		return 5
	}
	if m.Priority > 10 {
		return 10
	}
	return m.Priority
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
		return errors.New("feishu webhook transport failed")
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
	return sharedClient(15 * time.Second)
}

type Ntfy struct {
	BaseURL string
	Topic   string
	Client  *http.Client
}

type Gotify struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func (g Gotify) Send(ctx context.Context, m Message) error {
	base, err := url.Parse(strings.TrimSpace(g.BaseURL))
	if err != nil || base.Scheme != "https" && base.Scheme != "http" || base.Host == "" {
		return errors.New("gotify base url is invalid")
	}
	if strings.TrimSpace(g.Token) == "" {
		return errors.New("gotify token is not configured")
	}
	payload := map[string]any{"title": m.Title, "message": m.Body, "priority": m.priority()}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	target := strings.TrimRight(base.String(), "/") + "/message"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", g.Token)
	resp, err := g.client().Do(req)
	if err != nil {
		return errors.New("gotify transport failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("gotify returned HTTP %d", resp.StatusCode)
	}
	var ack struct {
		ID *int `json:"id"`
	}
	if json.Unmarshal(body, &ack) == nil && ack.ID != nil && *ack.ID <= 0 {
		return fmt.Errorf("gotify rejected message")
	}
	return nil
}

func (g Gotify) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return sharedClient(30 * time.Second)
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
	req.Header.Set("Priority", strconv.Itoa(m.priority()))
	req.Header.Set("Tags", "rotating_light")
	resp, err := n.client().Do(req)
	if err != nil {
		return errors.New("ntfy transport failed")
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
	return sharedClient(30 * time.Second)
}
