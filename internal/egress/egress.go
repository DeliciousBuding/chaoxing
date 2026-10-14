// Package egress implements an optional outbound proxy pool for upstream
// requests. Direct egress is always available and remains the default when no
// proxies are configured; SOCKS5 endpoints are tried round-robin with
// failure cooldown. There is no background probing: health is learned only
// from real request outcomes.
package egress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Endpoint is one configured proxy. URL must use the socks5 scheme with an
// optional userinfo for authentication. Bind optionally pins the local
// source address for the dial.
type Endpoint struct {
	URL  string `json:"url"`
	Bind string `json:"bind,omitempty"`
}

// HealthStore persists endpoint health across restarts (statestore adapter).
type HealthStore interface {
	ProxyHealth() (map[string]Health, error)
	SetProxyHealth(key string, h Health) error
}

// Health is the failure state of one endpoint.
type Health struct {
	Failures  int
	CoolUntil time.Time
}

type entry struct {
	key  string
	rt   http.RoundTripper
	fail int
	cool time.Time
}

// Status is the operator-facing state of one endpoint.
type Status struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Failures int    `json:"failures"`
	Cooldown bool   `json:"cooldown"`
}

// Pool is an http.RoundTripper selecting among configured egress paths.
type Pool struct {
	mu   sync.Mutex
	list []*entry
	next int
	db   HealthStore
	now  func() time.Time
}

const (
	maxEndpoints = 16
	baseCooldown = 30 * time.Second
	maxCooldown  = 5 * time.Minute
	maxFailures  = 6
	directKey    = "direct"
	errNoEgress  = "no egress endpoint available"
)

// New builds the pool. An empty endpoints list yields direct-only egress.
func New(endpoints []Endpoint, store HealthStore) (*Pool, error) {
	if len(endpoints) > maxEndpoints {
		return nil, fmt.Errorf("too many egress endpoints (max %d)", maxEndpoints)
	}
	p := &Pool{db: store, now: time.Now}
	p.list = append(p.list, &entry{key: directKey, rt: transport(nil)})
	keys := map[string]bool{directKey: true}
	for _, ep := range endpoints {
		u, err := url.Parse(ep.URL)
		if err != nil || u.Scheme != "socks5" || u.Host == "" {
			return nil, fmt.Errorf("invalid socks5 endpoint: scheme or host missing")
		}
		// Key identifies the endpoint without leaking embedded credentials.
		sum := sha256.Sum256([]byte(ep.URL))
		key := "socks5:" + hex.EncodeToString(sum[:6])
		if keys[key] {
			return nil, errors.New("duplicate egress endpoint")
		}
		keys[key] = true
		rt, err := socksTransport(u, ep.Bind)
		if err != nil {
			return nil, err
		}
		p.list = append(p.list, &entry{key: key, rt: rt})
	}
	if store != nil {
		if m, err := store.ProxyHealth(); err == nil {
			for _, e := range p.list {
				if h, ok := m[e.key]; ok {
					e.fail = h.Failures
					e.cool = h.CoolUntil
				}
			}
		}
	}
	return p, nil
}

func transport(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		DialContext:           dial,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
}

func socksTransport(u *url.URL, bind string) (http.RoundTripper, error) {
	var base proxy.Dialer = &net.Dialer{Timeout: 10 * time.Second}
	if bind != "" {
		ip := net.ParseIP(bind)
		if ip == nil {
			return nil, fmt.Errorf("invalid bind address")
		}
		base = &net.Dialer{Timeout: 10 * time.Second, LocalAddr: &net.TCPAddr{IP: ip}}
	}
	var auth *proxy.Auth
	if u.User != nil {
		pw, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pw}
	}
	d, err := proxy.SOCKS5("tcp", u.Host, auth, base)
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("socks5 dialer lacks context support")
	}
	return transport(cd.DialContext), nil
}

// RoundTrip implements http.RoundTripper with round-robin selection among
// healthy endpoints and failure cooldown learned from transport errors.
func (p *Pool) RoundTrip(req *http.Request) (*http.Response, error) {
	e := p.pick()
	if e == nil {
		return nil, errors.New(errNoEgress)
	}
	resp, err := e.rt.RoundTrip(req)
	if err != nil {
		// RoundTripper errors here are transport-level; response status codes
		// are upstream answers and never count against the egress path.
		p.failEndpoint(e)
		return nil, err
	}
	p.heal(e)
	return resp, nil
}

func (p *Pool) pick() *entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for i := 0; i < len(p.list); i++ {
		e := p.list[(p.next+i)%len(p.list)]
		if e.cool.IsZero() || !now.Before(e.cool) {
			p.next = (p.next + i + 1) % len(p.list)
			return e
		}
	}
	// Everything cooling down: fall back to direct rather than failing.
	return p.list[0]
}

func (p *Pool) failEndpoint(e *entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.key == directKey {
		return // never cool down the direct fallback
	}
	e.fail++
	if e.fail > maxFailures {
		e.fail = maxFailures
	}
	d := baseCooldown << (e.fail - 1)
	if d > maxCooldown {
		d = maxCooldown
	}
	e.cool = p.now().Add(d)
	p.persist(e)
}

func (p *Pool) heal(e *entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.fail == 0 && e.cool.IsZero() {
		return
	}
	e.fail = 0
	e.cool = time.Time{}
	p.persist(e)
}

func (p *Pool) persist(e *entry) {
	if p.db != nil {
		_ = p.db.SetProxyHealth(e.key, Health{Failures: e.fail, CoolUntil: e.cool})
	}
}

// Status reports sanitized endpoint state for the local management plane.
func (p *Pool) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	out := make([]Status, 0, len(p.list))
	for _, e := range p.list {
		kind := "direct"
		if e.key != directKey {
			kind = "socks5"
		}
		out = append(out, Status{Key: e.key, Kind: kind, Failures: e.fail, Cooldown: !e.cool.IsZero() && now.Before(e.cool)})
	}
	return out
}
