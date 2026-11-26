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

func isSafeCooldownID(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// cooldownKey scopes activelist rate limits per (courseId, classId) when both
// identifiers are present, because Chaoxing enforces a daily per-class quota on
// /v2/apis/active/student/activelist. Other endpoints remain account/endpoint
// scoped.
func cooldownKey(u *url.URL) string {
	base := u.Host + u.Path
	if u.Path == "/v2/apis/active/student/activelist" {
		courseID := strings.TrimSpace(u.Query().Get("courseId"))
		classID := strings.TrimSpace(u.Query().Get("classId"))
		if isSafeCooldownID(courseID) && isSafeCooldownID(classID) {
			return base + "#course=" + courseID + "&class=" + classID
		}
	}
	return base
}

func validCooldownKey(key string) bool {
	base, scope, hasScope := strings.Cut(key, "#")
	u, err := url.Parse("https://" + base)
	if err != nil || !safeURL(u) || u.Host+u.Path != base {
		return false
	}
	if !hasScope {
		return true
	}
	if u.Path != "/v2/apis/active/student/activelist" {
		return false
	}
	vals, err := url.ParseQuery(scope)
	if err != nil || len(vals) != 2 {
		return false
	}
	courseID, classID := vals.Get("course"), vals.Get("class")
	if !isSafeCooldownID(courseID) || !isSafeCooldownID(classID) {
		return false
	}
	return scope == "course="+courseID+"&class="+classID
}

func activityClassURL(courseID, classID string) *url.URL {
	u, _ := url.Parse(activityAPI)
	if isSafeCooldownID(courseID) && isSafeCooldownID(classID) {
		q := url.Values{"courseId": {courseID}, "classId": {classID}}
		u.RawQuery = q.Encode()
	}
	return u
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
		if !validCooldownKey(key) || entry.Failures < 1 || entry.Failures > 5 || entry.Until.IsZero() {
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

// endpointDeferred checks the exact cooldown key (including per-class scope on
// activelist) as well as any broader endpoint-level cooldown. Expiry permits
// one normal request, not a retry loop.
func (c *Client) endpointDeferred(u *url.URL) error {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	base := u.Host + u.Path
	key := cooldownKey(u)
	var bestRemaining time.Duration
	var bestSource string
	for _, k := range []string{key, base} {
		state := c.cooldowns[k]
		if remaining := time.Until(state.Until); remaining > bestRemaining {
			bestRemaining = remaining
			bestSource = state.Source
		}
	}
	if bestRemaining > 0 {
		return &Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: int(math.Ceil(bestRemaining.Seconds())), RetryAfterSource: bestSource}
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
	k := cooldownKey(u)
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
	k := cooldownKey(u)
	base := u.Host + u.Path
	deleted := false
	if _, ok := c.cooldowns[k]; ok {
		delete(c.cooldowns, k)
		deleted = true
	}
	if k != base {
		if state, ok := c.cooldowns[base]; ok && !time.Now().Before(state.Until) {
			delete(c.cooldowns, base)
			deleted = true
		}
	}
	if !deleted {
		return nil
	}
	return c.saveCooldownsLocked()
}
