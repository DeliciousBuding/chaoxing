package upstream

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type recordingTransport struct {
	calls int
}

func (r *recordingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestPacedTransportAddsRandomizedInterval(t *testing.T) {
	base := &recordingTransport{}
	p := newPacedTransport(base, 500*time.Millisecond, 0).(*pacedTransport)
	now := time.Unix(1000, 0)
	var slept []time.Duration
	p.now = func() time.Time { return now }
	p.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		now = now.Add(d)
		return nil
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
	for range 3 {
		resp, err := p.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if base.calls != 3 || len(slept) != 2 || slept[0] != 500*time.Millisecond || slept[1] != 500*time.Millisecond {
		t.Fatalf("calls=%d slept=%v", base.calls, slept)
	}
}

func TestPacedTransportAppliesJitterBounds(t *testing.T) {
	base := &recordingTransport{}
	p := newPacedTransport(base, 100*time.Millisecond, 40).(*pacedTransport)
	now := time.Unix(2000, 0)
	var slept []time.Duration
	p.now = func() time.Time { return now }
	p.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		now = now.Add(d)
		return nil
	}
	p.random = func() float64 { return 0.25 }
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
	for range 2 {
		resp, err := p.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if len(slept) != 1 || slept[0] != 110*time.Millisecond {
		t.Fatalf("slept=%v", slept)
	}
}

func TestPacedTransportHonorsContextCancellation(t *testing.T) {
	base := &recordingTransport{}
	p := newPacedTransport(base, time.Minute, 0).(*pacedTransport)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	p.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	first, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
	if _, err := p.RoundTrip(first); err != nil {
		t.Fatal(err)
	}
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
	if _, err := p.RoundTrip(req); err == nil {
		t.Fatal("cancellation ignored")
	}
}
