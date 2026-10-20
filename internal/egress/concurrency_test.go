package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/admission"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGlobalCapacityIncludesResponseBody(t *testing.T) {
	p, _ := New(nil, nil)
	p.requests = admission.New(1, 0)
	p.list[0].rt = testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("body"))}, nil
	})
	r, _ := http.NewRequest("GET", "https://example.invalid", nil)
	resp, err := p.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RoundTrip(r); !errors.Is(err, admission.ErrFull) {
		t.Fatal("headers released capacity", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() // EOF + close must release exactly once.
	second, err := p.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	p.list[0].rt = testTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic") })
	if _, err := p.RoundTrip(r); err == nil {
		t.Fatal("expected failure")
	}
	if err := p.requests.Acquire(context.Background()); err != nil {
		t.Fatal("error leaked capacity", err)
	}
	p.requests.Release()
}

type blockedStore struct{ entered, release chan struct{} }

func (s *blockedStore) ProxyHealth() (map[string]Health, error) { return nil, nil }
func (s *blockedStore) SetProxyHealth(string, Health) error {
	close(s.entered)
	<-s.release
	return errors.New("synthetic disk error")
}

func TestSlowPersistenceDoesNotHoldPoolLock(t *testing.T) {
	db := &blockedStore{make(chan struct{}), make(chan struct{})}
	p, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}}, db)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { p.failEndpoint(p.list[1]); close(done) }()
	<-db.entered
	status := make(chan []Status, 1)
	go func() { status <- p.Status() }()
	select {
	case <-status:
	case <-time.After(time.Second):
		t.Error("disk I/O held pool lock")
	}
	if e := p.pick(); e.key != directKey {
		t.Error("cooling proxy picked")
	}
	close(db.release)
	<-done
	if !p.Status()[1].PersistenceDegraded {
		t.Fatal("persistence failure hidden")
	}
}
