package upstream

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// pacedTransport enforces a randomized minimum interval between upstream
// requests for one account. Redirects and retries pass through the same
// boundary, so a protocol chain cannot accidentally become a burst.
type pacedTransport struct {
	next   http.RoundTripper
	min    time.Duration
	jitter int
	mu     sync.Mutex
	last   time.Time
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	random func() float64
}

func newPacedTransport(next http.RoundTripper, min time.Duration, jitterPercent int) http.RoundTripper {
	return &pacedTransport{
		next: next, min: min, jitter: jitterPercent,
		now: time.Now, sleep: sleepContext, random: rand.Float64,
	}
}

func (p *pacedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if wait := p.reserve(); wait > 0 {
		if err := p.sleep(req.Context(), wait); err != nil {
			return nil, err
		}
	}
	return p.next.RoundTrip(req)
}

func (p *pacedTransport) reserve() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	interval := p.min
	if p.jitter > 0 {
		spread := time.Duration(float64(p.min) * float64(p.jitter) / 100)
		interval += time.Duration(p.random() * float64(spread))
	}
	if !p.last.IsZero() {
		if elapsed := now.Sub(p.last); elapsed < interval {
			wait := interval - elapsed
			p.last = now.Add(wait)
			return wait
		}
	}
	p.last = now
	return 0
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
