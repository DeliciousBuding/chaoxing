package upstream

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCooldownEscalatesAcrossIdleAndRestart(t *testing.T) {
	u, _ := url.Parse(activityAPI)
	c := testClient(t)
	for _, want := range []int{300, 600, 1200, 2400, 3600, 3600} {
		got := SafeFault(c.deferEndpoint(u, ""))
		if got.Code != "UPSTREAM_RATE_LIMITED" || got.RetryAfterSeconds != want {
			t.Fatalf("got %+v want %d", got, want)
		}
		restarted, err := New(c.config)
		if err != nil {
			t.Fatal(err)
		}
		if got := SafeFault(restarted.endpointDeferred(u)); got.Code != "UPSTREAM_RATE_LIMITED" || got.RetryAfterSeconds < want-1 {
			t.Fatalf("restart bypassed cooldown: %+v", got)
		}
		// Expiry and a long idle period permit a request but must not reset history.
		state := restarted.cooldowns[u.Host+u.Path]
		state.Until = time.Now().Add(-time.Hour)
		restarted.cooldowns[u.Host+u.Path] = state
		if err := restarted.endpointDeferred(u); err != nil {
			t.Fatal(err)
		}
		c = restarted
	}
	if err := c.endpointSucceeded(u, []byte(`{"result":0,"errorMsg":"synthetic rejection"}`)); err != nil {
		t.Fatal(err)
	}
	if c.cooldowns[u.Host+u.Path].Failures != 5 {
		t.Fatal("business rejection reset history")
	}
	if err := c.endpointSucceeded(u, []byte(`{"result":1,"data":{}}`)); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(c.config)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.cooldowns) != 0 || SafeFault(restarted.deferEndpoint(u, "")).RetryAfterSeconds != 300 {
		t.Fatal("real success did not reset durable history")
	}
}

func TestRetryAfterDeltaAndDate(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"7200", 7200},
		{now.Add(2 * time.Hour).Format(http.TimeFormat), 7200},
		{"-1", 0}, {"0", 0}, {"invalid", 0}, {"86401", 0},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		if got := retryAfterSeconds(tc.value, now); got != tc.want {
			t.Fatalf("%q = %d want %d", tc.value, got, tc.want)
		}
	}
}

func TestRawReadAndWriteShareDurableCooldown(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(map[bool]string{false: "read", true: "write"}[write], func(t *testing.T) {
			calls := 0
			c := testClient(t)
			c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
				calls++
				res := response(r, `{"result":0,"errorMsg":"请勿频繁操作"}`)
				res.StatusCode = http.StatusTooManyRequests
				res.Header.Set("Retry-After", "7200")
				return res, nil
			})
			for i := 0; i < 2; i++ {
				var err error
				if write {
					_, _, err = c.rawWrite(context.Background(), http.MethodPost, activityAPI+"?classId=b", strings.NewReader(""), "text/plain")
				} else {
					_, _, err = c.raw(context.Background(), http.MethodGet, activityAPI+"?classId=a", nil)
				}
				f := SafeFault(err)
				if f.Code != "UPSTREAM_RATE_LIMITED" || f.RetryAfterSeconds < 7199 {
					t.Fatalf("%+v", f)
				}
			}
			if calls != 1 {
				t.Fatalf("cooldown bypassed: %d", calls)
			}
			// Changing the class, request method or frontend does not change the key.
			_, _, err := c.raw(context.Background(), http.MethodGet, activityAPI+"?classId=other", nil)
			if SafeFault(err).Code != "UPSTREAM_RATE_LIMITED" || calls != 1 {
				t.Fatal("different class bypassed cooldown")
			}
		})
	}
}

func TestCooldownRejectsCorruptAndSymlinkState(t *testing.T) {
	c := testClient(t)
	path := c.cooldownPath()
	for _, body := range []string{`{`, `{"version":99}`, `{"version":1,"endpoints":{"evil.example/path":{"failures":1,"until":"2026-01-01T00:00:00Z"}}}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(c.config); SafeFault(err).Code != "STATE_INVALID" {
			t.Fatalf("invalid state accepted: %v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(c.config.KeyPath, path); err != nil {
		t.Skip("symlink unavailable")
	}
	if _, err := New(c.config); SafeFault(err).Code != "STATE_INVALID" {
		t.Fatal("symlink state accepted")
	}
	u, _ := url.Parse(activityAPI)
	if err := c.deferEndpoint(u, ""); SafeFault(err).Code != "STATE_WRITE_FAILED" {
		t.Fatal("symlink write accepted")
	}
	if c.endpointDeferred(u) == nil {
		t.Fatal("failed persistence erased in-memory cooldown")
	}
}
