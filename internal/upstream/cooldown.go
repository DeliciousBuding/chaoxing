package upstream

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/persist"
)

type endpointCooldown struct {
	Until    time.Time `json:"until"`
	Failures int       `json:"failures"`
	Source   string    `json:"source,omitempty"`
}

type cooldownState struct {
	Version   int                         `json:"version"`
	Endpoints map[string]endpointCooldown `json:"endpoints"`
}

func (c *Client) cooldownPath() string {
	if c.config.SessionPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.config.SessionPath), "upstream-cooldowns.json")
}

func (c *Client) loadCooldowns() error {
	path := c.cooldownPath()
	if path == "" {
		return nil
	}
	if err := evidenceRegular(path); err != nil {
		return fault("STATE_INVALID", "cooldown state must be a regular file")
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Size() > 64<<10 {
		return fault("STATE_READ_FAILED", "cooldown state unavailable")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fault("STATE_READ_FAILED", "cooldown state unavailable")
	}
	var state cooldownState
	if json.Unmarshal(b, &state) != nil || state.Version != 1 || len(state.Endpoints) > 128 {
		return fault("STATE_INVALID", "invalid cooldown state")
	}
	for key, entry := range state.Endpoints {
		u, err := url.Parse("https://" + key)
		if err != nil || !safeURL(u) || u.Host+u.Path != key || entry.Failures < 1 || entry.Failures > 5 || entry.Until.IsZero() {
			return fault("STATE_INVALID", "invalid cooldown endpoint")
		}
	}
	c.cooldowns = state.Endpoints
	return nil
}

// The query gate serializes account operations; this lock also protects
// cooldown state when called directly by transport tests.
func (c *Client) saveCooldownsLocked() error {
	path := c.cooldownPath()
	if path == "" {
		return nil
	}
	if err := evidenceRegular(path); err != nil {
		return fault("STATE_WRITE_FAILED", "cooldown state unavailable")
	}
	b, err := json.Marshal(cooldownState{Version: 1, Endpoints: c.cooldowns})
	if err != nil || persist.Write(path, b, 0600) != nil {
		return fault("STATE_WRITE_FAILED", "cannot persist cooldown")
	}
	return nil
}

// Account/endpoint scoped, not course scoped. A different class or adapter
// cannot bypass a throttle, and expiry permits one normal request, not a retry loop.
func (c *Client) endpointDeferred(u *url.URL) error {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	state := c.cooldowns[u.Host+u.Path]
	if remaining := time.Until(state.Until); remaining > 0 {
		return &Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: int(math.Ceil(remaining.Seconds())), RetryAfterSource: state.Source}
	}
	return nil
}

func retryAfterSeconds(value string, now time.Time) int {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 && seconds <= 86400 {
		return seconds
	}
	if until, err := http.ParseTime(value); err == nil {
		seconds := int(math.Ceil(until.Sub(now).Seconds()))
		if seconds > 0 && seconds <= 86400 {
			return seconds
		}
	}
	return 0
}

func (c *Client) deferEndpoint(u *url.URL, retryAfter string) error {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	if c.cooldowns == nil {
		c.cooldowns = make(map[string]endpointCooldown)
	}
	k := u.Host + u.Path
	state := c.cooldowns[k]
	now := time.Now()
	state.Failures = min(state.Failures+1, 5)
	// No published wait means a bounded recovery probe, not an invented
	// five-minute platform ban. Repeated refusals still escalate durably.
	seconds := [...]int{60, 120, 300, 900, 3600}[state.Failures-1]
	state.Source = "local_backoff"
	if serverSeconds := retryAfterSeconds(retryAfter, now); serverSeconds >= seconds {
		seconds = serverSeconds
		state.Source = "upstream"
	}
	state.Until = now.Add(time.Duration(seconds) * time.Second)
	c.cooldowns[k] = state
	if err := c.saveCooldownsLocked(); err != nil {
		return err
	}
	return &Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: seconds, RetryAfterSource: state.Source}
}

// Only a real accepted response resets rejection history; merely waiting or
// restarting does not prove that the upstream has lifted its restriction.
func (c *Client) endpointSucceeded(u *url.URL, body []byte) error {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Result) > 0 {
		result := strings.TrimSpace(string(envelope.Result))
		if result != "1" && result != "true" {
			return nil
		}
	}
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	k := u.Host + u.Path
	if _, ok := c.cooldowns[k]; !ok {
		return nil
	}
	delete(c.cooldowns, k)
	return c.saveCooldownsLocked()
}
