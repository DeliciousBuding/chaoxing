package egress

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeStore struct{ m map[string]Health }

func (f *fakeStore) ProxyHealth() (map[string]Health, error) { return f.m, nil }
func (f *fakeStore) SetProxyHealth(k string, h Health) error {
	if h.Failures <= 0 && h.CoolUntil.IsZero() {
		delete(f.m, k)
		return nil
	}
	f.m[k] = h
	return nil
}

type fakeAssignmentStore struct {
	fakeStore
	assigned map[string]string
}

func (f *fakeAssignmentStore) ProxyAssignments() (map[string]string, error) { return f.assigned, nil }
func (f *fakeAssignmentStore) SetProxyAssignment(account, endpoint string) error {
	if f.assigned == nil {
		f.assigned = map[string]string{}
	}
	f.assigned[account] = endpoint
	return nil
}
func (f *fakeAssignmentStore) DeleteProxyAssignment(account string) error {
	delete(f.assigned, account)
	return nil
}

// socks5Server is a minimal no-auth CONNECT server for tests.
func socks5Server(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				if _, err := br.ReadByte(); err != nil { // version
					return
				}
				n, err := br.ReadByte()
				if err != nil {
					return
				}
				if _, err := io.ReadFull(br, make([]byte, int(n))); err != nil {
					return
				}
				if _, err := c.Write([]byte{5, 0}); err != nil {
					return
				}
				head := make([]byte, 4)
				if _, err := io.ReadFull(br, head); err != nil || head[1] != 1 {
					return
				}
				var host string
				switch head[3] {
				case 1:
					b := make([]byte, 4)
					if _, err := io.ReadFull(br, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case 3:
					l, err := br.ReadByte()
					if err != nil {
						return
					}
					b := make([]byte, int(l))
					if _, err := io.ReadFull(br, b); err != nil {
						return
					}
					host = string(b)
				default:
					return
				}
				pb := make([]byte, 2)
				if _, err := io.ReadFull(br, pb); err != nil {
					return
				}
				up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb)))))
				if err != nil {
					c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
					return
				}
				go io.Copy(up, br)
				io.Copy(c, up)
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestDirectOnlyRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	p, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: p}).Get(srv.URL)
	if err != nil || resp.StatusCode != 204 {
		t.Fatal(resp, err)
	}
	resp.Body.Close()
	if s := p.Status(); len(s) != 1 || s[0].Kind != "direct" {
		t.Fatal(s)
	}
}

func TestSOCKS5RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "via-socks") }))
	defer srv.Close()
	p, err := New([]Endpoint{{URL: "socks5://" + socks5Server(t)}}, &fakeStore{m: map[string]Health{}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // direct and socks5 both serve; rotation covers both
		resp, err := (&http.Client{Transport: p}).Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	for _, s := range p.Status() {
		if s.Failures != 0 || s.Cooldown {
			t.Fatal(s)
		}
	}
}

func TestFailureCooldownAndPersist(t *testing.T) {
	fs := &fakeStore{m: map[string]Health{}}
	// Closed port: dials fail fast.
	p, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}}, fs)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return time.Unix(1000, 0) }
	req, _ := http.NewRequest("GET", "http://example.invalid/", nil)
	// First request picks direct or socks5 depending on rotation; force socks5 selection.
	p.mu.Lock()
	p.next = 1
	p.mu.Unlock()
	if _, err := p.RoundTrip(req); err == nil {
		t.Fatal("expected transport failure")
	}
	st := p.Status()
	if st[1].Failures != 1 || !st[1].Cooldown {
		t.Fatal(st)
	}
	var persisted Health
	for k, h := range fs.m {
		if strings.HasPrefix(k, "socks5:") {
			persisted = h
		}
	}
	if persisted.Failures != 1 {
		t.Fatal("health not persisted")
	}
	// Cooling endpoint is skipped; direct answers instead.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	resp, err := p.RoundTrip(req)
	_ = resp
	if err == nil {
		resp.Body.Close()
		t.Fatal("example.invalid must not resolve")
	}
	req2, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err = p.RoundTrip(req2)
	if err != nil || resp.StatusCode != 204 {
		t.Fatal("direct fallback failed", err)
	}
	resp.Body.Close()
	// Cooldown expiry: advance clock beyond 30s, socks5 retried.
	p.now = func() time.Time { return time.Unix(2000, 0) }
	p.mu.Lock()
	p.next = 1
	p.mu.Unlock()
	if e := p.pick(context.Background()); e.key == directKey {
		t.Fatal("cooling endpoint should be retried after expiry")
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New([]Endpoint{{URL: "http://x:1"}}, nil); err == nil {
		t.Fatal("non-socks5 scheme accepted")
	}
	if _, err := New([]Endpoint{{URL: "socks5://h:1", Bind: "not-an-ip"}}, nil); err == nil {
		t.Fatal("invalid bind accepted")
	}
	eps := make([]Endpoint, maxEndpoints+1)
	for i := range eps {
		eps[i] = Endpoint{URL: "socks5://h:1"}
	}
	if _, err := New(eps, nil); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatal(err)
	}
	// Duplicate endpoints rejected.
	if _, err := New([]Endpoint{{URL: "socks5://a:1"}, {URL: "socks5://a:1"}}, nil); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := New([]Endpoint{{URL: "socks5://a:1"}, {URL: "socks5://b:2"}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRestoredHealthFromStore(t *testing.T) {
	fs := &fakeStore{m: map[string]Health{}}
	p1, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}}, fs)
	if err != nil {
		t.Fatal(err)
	}
	p1.failEndpoint(p1.list[1])
	p2, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}}, fs)
	if err != nil {
		t.Fatal(err)
	}
	if s := p2.Status(); s[1].Failures != 1 || !s[1].Cooldown {
		t.Fatal("health not restored", s)
	}
}

func TestAccountLabeledEgressIsSticky(t *testing.T) {
	store := &fakeAssignmentStore{fakeStore{m: map[string]Health{}}, map[string]string{}}
	p, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}, {URL: "socks5://127.0.0.1:2"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAccount(context.Background(), "root")
	first := p.pick(ctx)
	for range 5 {
		if next := p.pick(ctx); next != first {
			t.Fatalf("account egress changed: %s -> %s", first.key, next.key)
		}
	}
	if store.assigned["root"] != first.key {
		t.Fatal(store.assigned)
	}
}

func TestStickyAssignmentIsRestored(t *testing.T) {
	store := &fakeAssignmentStore{fakeStore{m: map[string]Health{}}, map[string]string{}}
	p1, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}, {URL: "socks5://127.0.0.1:2"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	first := p1.pick(WithAccount(context.Background(), "root"))
	p2, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}, {URL: "socks5://127.0.0.1:2"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if second := p2.pick(WithAccount(context.Background(), "root")); second.key != first.key {
		t.Fatalf("assignment not restored: %s != %s", first.key, second.key)
	}
}

func TestStickyAssignmentUsesDirectOnlyWhileCooling(t *testing.T) {
	store := &fakeAssignmentStore{fakeStore{m: map[string]Health{}}, map[string]string{}}
	p, err := New([]Endpoint{{URL: "socks5://127.0.0.1:1"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAccount(context.Background(), "root")
	if selected := p.pick(ctx); selected.key == directKey {
		t.Fatal("sticky account was bound to direct")
	}
	assigned := store.assigned["root"]
	p.failEndpoint(p.list[1])
	if selected := p.pick(ctx); selected.key != directKey {
		t.Fatal("cooling endpoint was reused")
	}
	if store.assigned["root"] != assigned {
		t.Fatal("cooling changed the persistent assignment")
	}
	p.heal(p.list[1])
	if selected := p.pick(ctx); selected.key != assigned {
		t.Fatalf("assignment not restored after cooldown: %s != %s", selected.key, assigned)
	}
}
