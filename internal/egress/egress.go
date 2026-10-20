// Package egress implements an optional outbound proxy pool for upstream
// requests. Direct egress is always available and remains the default when no
// proxies are configured. Unlabeled requests use round-robin selection, while
// account-labeled requests keep a stable healthy endpoint. Failures cool an
// endpoint down; there is no background probing.
package egress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/admission"
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

// AssignmentStore optionally persists stable account-to-egress bindings.
type AssignmentStore interface {
	ProxyAssignments() (map[string]string, error)
	SetProxyAssignment(accountKey, endpointKey string) error
	DeleteProxyAssignment(accountKey string) error
}

// Health is the failure state of one endpoint.
type Health struct {
	Failures  int
	CoolUntil time.Time
}

type entry struct {
	persistMu         sync.Mutex
	key               string
	rt                http.RoundTripper
	fail              int
	cool              time.Time
	persistenceFailed bool
	assignmentFailed  bool
}

// Status is the operator-facing state of one endpoint.
type Status struct {
	Key                 string `json:"key"`
	Kind                string `json:"kind"`
	Failures            int    `json:"failures"`
	Cooldown            bool   `json:"cooldown"`
	PersistenceDegraded bool   `json:"persistence_degraded"`
	AssignmentDegraded  bool   `json:"assignment_degraded,omitempty"`
}

// Pool is an http.RoundTripper selecting among configured egress paths.
type Pool struct {
	mu          sync.Mutex
	list        []*entry
	next        int
	db          HealthStore
	assignments AssignmentStore
	assigned    map[string]string
	now         func() time.Time
	requests    *admission.Gate
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
	p := &Pool{db: store, now: time.Now, requests: admission.New(4, 32), assigned: map[string]string{}}
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
		m, err := store.ProxyHealth()
		if err == nil {
			for _, e := range p.list {
				if h, ok := m[e.key]; ok {
					e.fail = h.Failures
					e.cool = h.CoolUntil
				}
			}
		} else {
			for _, e := range p.list {
				e.persistenceFailed = true
			}
		}
	}
	if assignments, ok := store.(AssignmentStore); ok {
		p.assignments = assignments
		if m, err := assignments.ProxyAssignments(); err == nil {
			p.assigned = m
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
	if err := p.requests.Acquire(req.Context()); err != nil {
		return nil, err
	}
	release := true
	defer func() {
		if release {
			p.requests.Release()
		}
	}()
	e := p.pick(req.Context())
	if e == nil {
		return nil, errors.New(errNoEgress)
	}
	resp, err := e.rt.RoundTrip(req)
	if err != nil {
		// RoundTripper errors here are transport-level; response status codes
		// are upstream answers and never count against the egress path.
		if req.Context().Err() == nil {
			p.failEndpoint(e)
		}
		return nil, err
	}
	p.heal(e)
	resp.Body = &limitedBody{ReadCloser: resp.Body, release: p.requests.Release}
	release = false
	return resp, nil
}

// Retain capacity until the response is consumed or closed, not just headers.
type limitedBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.release)
	}
	return n, err
}
func (b *limitedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func (p *Pool) pick(ctx context.Context) *entry {
	if account := Account(ctx); account != "" {
		return p.pickSticky(account)
	}
	return p.pickRoundRobin()
}

func (p *Pool) pickRoundRobin() *entry {
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

func (p *Pool) pickSticky(account string) *entry {
	p.mu.Lock()
	now := p.now()
	if key, ok := p.assigned[account]; ok {
		for _, e := range p.list {
			if e.key != key {
				continue
			}
			if e.cool.IsZero() || !now.Before(e.cool) {
				p.mu.Unlock()
				return e
			}
			p.mu.Unlock()
			return p.list[0] // preserve the binding; use direct only while cooling
		}
		delete(p.assigned, account)
		p.mu.Unlock()
		p.deleteAssignment(account)
		p.mu.Lock()
	}
	candidates := make([]*entry, 0, len(p.list)-1)
	for _, e := range p.list {
		if e.key != directKey && (e.cool.IsZero() || !now.Before(e.cool)) {
			candidates = append(candidates, e)
		}
	}
	var selected *entry
	if len(candidates) > 0 {
		selected = candidates[stableIndex(account, len(candidates))]
		p.assigned[account] = selected.key
		p.mu.Unlock()
		p.setAssignment(account, selected.key)
		return selected
	}
	p.mu.Unlock()
	return p.list[0] // temporary direct fallback; do not bind the account to it
}

func stableIndex(account string, length int) int {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(account))
	return int(sum.Sum32() % uint32(length))
}

func (p *Pool) setAssignment(account, endpoint string) {
	if p.assignments == nil {
		return
	}
	err := p.assignments.SetProxyAssignment(account, endpoint)
	p.mu.Lock()
	for _, e := range p.list {
		if e.key == endpoint {
			e.assignmentFailed = err != nil
			break
		}
	}
	p.mu.Unlock()
}

func (p *Pool) deleteAssignment(account string) {
	if p.assignments == nil {
		return
	}
	_ = p.assignments.DeleteProxyAssignment(account)
}

func (p *Pool) failEndpoint(e *entry) {
	e.persistMu.Lock()
	defer e.persistMu.Unlock()
	p.mu.Lock()
	if e.key == directKey {
		p.mu.Unlock()
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
	h := Health{Failures: e.fail, CoolUntil: e.cool}
	p.mu.Unlock()
	p.persist(e.key, h)
}

func (p *Pool) heal(e *entry) {
	e.persistMu.Lock()
	defer e.persistMu.Unlock()
	p.mu.Lock()
	if e.fail == 0 && e.cool.IsZero() {
		p.mu.Unlock()
		return
	}
	e.fail = 0
	e.cool = time.Time{}
	p.mu.Unlock()
	p.persist(e.key, Health{})
}

func (p *Pool) persist(key string, h Health) {
	if p.db != nil {
		err := p.db.SetProxyHealth(key, h)
		p.mu.Lock()
		for _, e := range p.list {
			if e.key == key {
				e.persistenceFailed = err != nil
				break
			}
		}
		p.mu.Unlock()
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
		out = append(out, Status{Key: e.key, Kind: kind, Failures: e.fail, Cooldown: !e.cool.IsZero() && now.Before(e.cool), PersistenceDegraded: e.persistenceFailed})
		out[len(out)-1].AssignmentDegraded = e.assignmentFailed
	}
	return out
}

type accountContextKey struct{}

// WithAccount labels a request with a private account key. The egress pool uses
// it to keep one account on one healthy endpoint instead of rotating its IP.
func WithAccount(ctx context.Context, account string) context.Context {
	if ctx == nil || !validAccount(account) {
		return ctx
	}
	return context.WithValue(ctx, accountContextKey{}, account)
}

func Account(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(accountContextKey{}).(string)
	if !validAccount(v) {
		return ""
	}
	return v
}

func validAccount(account string) bool {
	if account == "" || len(account) > 64 {
		return false
	}
	for _, r := range account {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
