package access

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPassword = "test-owner-password-only"

var hashOnce sync.Once
var testHash string

func config(t *testing.T) Config {
	t.Helper()
	hashOnce.Do(func() {
		var err error
		testHash, err = HashPassword(testPassword)
		if err != nil {
			panic(err)
		}
	})
	// TempDir's parent permissions depend on the runner. New creates this private
	// child with 0700, exercising the production permission boundary unchanged.
	return Config{Issuer: "https://mcp.example.com", StateDir: filepath.Join(t.TempDir(), "oauth"), OwnerPasswordHash: testHash}
}
func setup(t *testing.T, cfg Config) (*Server, *http.ServeMux) {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	mux.Handle("/mcp", s.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })))
	return s, mux
}
func request(h http.Handler, method, path, body, contentType string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://mcp.example.com"+path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func bodyMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return m
}
func form(h http.Handler, path string, v url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return request(h, "POST", path, v.Encode(), "application/x-www-form-urlencoded", cookies...)
}

type flow struct {
	id, path, verifier, code string
	owner                    *http.Cookie
}

func authorizeFlow(t *testing.T, h http.Handler) flow {
	return authorizeFlowScopes(t, h, scopeRead)
}

func authorizeFlowScopes(t *testing.T, h http.Handler, scopes string) flow {
	t.Helper()
	w := request(h, "POST", "/oauth/register", `{"client_name":"Example reader","redirect_uris":["https://client.example.com/callback"],"scope":"`+scopes+`"}`, "application/json")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	f := flow{id: bodyMap(t, w)["client_id"].(string), verifier: strings.Repeat("v", 43)}
	sum := sha256.Sum256([]byte(f.verifier))
	q := url.Values{"client_id": {f.id}, "response_type": {"code"}, "redirect_uri": {"https://client.example.com/callback"}, "scope": {scopes}, "state": {"example-state"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "resource": {"https://mcp.example.com/mcp"}}
	f.path = "/oauth/authorize?" + q.Encode()
	w = request(h, "GET", f.path, "", "")
	if w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	loginPath := w.Header().Get("Location")
	w = request(h, "GET", loginPath, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("no login cookie")
	}
	nonce := cookies[0].Value
	w = form(h, "/oauth/login", url.Values{"csrf": {nonce}, "password": {testPassword}, "next": {f.path}}, cookies[0])
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-chaoxing-owner" {
			f.owner = c
		}
	}
	if f.owner == nil {
		t.Fatal("missing owner session")
	}
	w = request(h, "GET", f.path, "", "", f.owner)
	if w.Code != 200 || !strings.Contains(w.Body.String(), scopes) {
		t.Fatal(w.Code, w.Body.String())
	}
	text := w.Body.String()
	prefix := `name="consent_token" value="`
	start := strings.Index(text, prefix)
	if start < 0 {
		t.Fatal(text)
	}
	raw := strings.SplitN(text[start+len(prefix):], `"`, 2)[0]
	w = form(h, "/oauth/consent", url.Values{"consent_token": {raw}, "decision": {"approve"}}, f.owner)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("state") != "example-state" {
		t.Fatal("lost state")
	}
	if u.Query().Get("iss") != "https://mcp.example.com" {
		t.Fatal("authorization response missing issuer")
	}
	f.code = u.Query().Get("code")
	if f.code == "" {
		t.Fatal("no code")
	}
	if w = form(h, "/oauth/consent", url.Values{"consent_token": {raw}, "decision": {"approve"}}, f.owner); w.Code != 403 {
		t.Fatal("consent replay accepted")
	}
	return f
}
func tokenForm(f flow) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "code": {f.code}, "code_verifier": {f.verifier}, "redirect_uri": {"https://client.example.com/callback"}}
}
func authenticated(h http.Handler, raw string) int {
	r := httptest.NewRequest("POST", "https://mcp.example.com/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}
func TestOAuthFlowRestartRefresh(t *testing.T) {
	cfg := config(t)
	_, h := setup(t, cfg)
	w := request(h, "POST", "/mcp", "", "")
	if w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), "/oauth-protected-resource/mcp") {
		t.Fatal("missing challenge")
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/mcp", "/.well-known/openid-configuration", "/.well-known/openid-configuration/mcp"} {
		w = request(h, "GET", p, "", "")
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal(p, w.Code)
		}
	}
	f := authorizeFlow(t, h)
	v := tokenForm(f)
	v.Set("code_verifier", strings.Repeat("x", 43))
	if w = form(h, "/oauth/token", v); w.Code != 400 {
		t.Fatal("wrong PKCE accepted")
	}
	w = form(h, "/oauth/token", tokenForm(f))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	tokens := bodyMap(t, w)
	access, refresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	if authenticated(h, access) != 204 {
		t.Fatal("access rejected")
	}
	if w = form(h, "/oauth/token", tokenForm(f)); w.Code != 400 {
		t.Fatal("code replay accepted")
	}
	_, h = setup(t, cfg)
	if authenticated(h, access) != 204 {
		t.Fatal("access lost on restart")
	}
	w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {f.id}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	next := bodyMap(t, w)
	newAccess, newRefresh := next["access_token"].(string), next["refresh_token"].(string)
	if newRefresh == refresh {
		t.Fatal("refresh did not rotate")
	}
	// Signed old-token proofs survive restart; replay revokes the entire family.
	_, h = setup(t, cfg)
	w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	if w.Code != 400 {
		t.Fatal("refresh replay accepted")
	}
	if authenticated(h, newAccess) != 401 {
		t.Fatal("replayed family not revoked")
	}
	if w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {newRefresh}}); w.Code != 400 {
		t.Fatal("family refresh remained live")
	}
	b, err := os.ReadFile(cfg.StateDir + "/oauth-state.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{testPassword, testHash, access, refresh, newAccess, newRefresh} {
		if strings.Contains(string(b), raw) {
			t.Fatal("raw credential persisted")
		}
	}
	info, _ := os.Stat(cfg.StateDir + "/oauth-state.json")
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("wrong mode", info.Mode())
	}
}

func TestSignScopeIsGrantedAndNarrowedOnRefresh(t *testing.T) {
	s, h := setup(t, config(t))
	f := authorizeFlowScopes(t, h, scopeRead+" "+scopeSign)
	w := form(h, "/oauth/token", tokenForm(f))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	tokens := bodyMap(t, w)
	if tokens["scope"] != scopeRead+" "+scopeSign {
		t.Fatal(tokens)
	}
	access := tokens["access_token"].(string)
	s.mu.Lock()
	stored := s.state.Access[digest(access)]
	s.mu.Unlock()
	if stored.Scope != scopeRead+" "+scopeSign {
		t.Fatalf("%+v", stored)
	}
	w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}, "scope": {scopeRead}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	narrowed := bodyMap(t, w)
	if narrowed["scope"] != scopeRead {
		t.Fatal(narrowed)
	}
}

func TestReadRegistrationCannotRequestSignScope(t *testing.T) {
	_, h := setup(t, config(t))
	f := authorizeFlow(t, h)
	q := url.Values{"client_id": {f.id}, "response_type": {"code"}, "redirect_uri": {"https://client.example.com/callback"}, "scope": {scopeSign}, "code_challenge_method": {"S256"}, "code_challenge": {strings.Repeat("c", 43)}}
	w := request(h, "GET", "/oauth/authorize?"+q.Encode(), "", "", f.owner)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestRefreshConcurrentSingleWinner(t *testing.T) {
	_, h := setup(t, config(t))
	f := authorizeFlow(t, h)
	w := form(h, "/oauth/token", tokenForm(f))
	refresh := bodyMap(t, w)["refresh_token"].(string)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}).Code
		}()
	}
	wg.Wait()
	close(codes)
	success := 0
	for c := range codes {
		if c == 200 {
			success++
		} else if c != 400 {
			t.Fatal(c)
		}
	}
	if success != 1 {
		t.Fatal("rotation winners", success)
	}
}
func TestFailClosedAndBounds(t *testing.T) {
	cfg := config(t)
	s, h := setup(t, cfg)
	for _, body := range []string{`{"redirect_uris":["https://*.example.com/cb"]}`, `{"redirect_uris":["https://client.example.com/cb"],"scope":"chaoxing.write"}`, `{"redirect_uris":["https://client.example.com/cb"],"token_endpoint_auth_method":"client_secret_basic"}`} {
		if w := request(h, "POST", "/oauth/register", body, "application/json"); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "POST", "/oauth/register", strings.Repeat("x", 17<<10), "application/json"); w.Code != 400 {
		t.Fatal("oversized body accepted")
	}
	s.mu.Lock()
	s.state.Access[digest("expired")] = token{Expires: time.Now().Add(-time.Second).Unix()}
	s.pending[digest("expired")] = grant{expires: 1}
	s.pruneLocked()
	if len(s.pending) != 0 {
		t.Fatal("pending not pruned")
	}
	s.mu.Unlock()
	if authenticated(h, "expired") != 401 {
		t.Fatal("expired token accepted")
	}
	if safeNext("/oauth/../api/admin") != "" || safeNext("//evil.example.com/oauth/authorize") != "" {
		t.Fatal("unsafe next accepted")
	}
	cfg.OwnerPasswordHash = "plaintext"
	if _, err := New(cfg); err == nil {
		t.Fatal("plaintext owner config accepted")
	}
}
func TestPersistenceFailureRollsBack(t *testing.T) {
	s, _ := setup(t, config(t))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Access["new"] = token{Expires: time.Now().Add(time.Hour).Unix()}
	s.path = s.cfg.StateDir + "/missing/state.json"
	if s.saveLocked() == nil {
		t.Fatal("expected persistence failure")
	}
	if _, ok := s.state.Access["new"]; ok {
		t.Fatal("failed write retained in memory")
	}
}
func TestCIMDGuards(t *testing.T) {
	for _, raw := range []string{"https://evil.example.com/client.json", "https://chatgpt.com:8443/client.json", "https://user@chatgpt.com/client.json", "http://chatgpt.com/client.json", "https://chatgpt.com.evil.example.com/client.json"} {
		if validCIMD(raw) {
			t.Fatal(raw)
		}
	}
	if !validCIMD("https://chatgpt.com/client.json") {
		t.Fatal("valid CIMD rejected")
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "::1", "fc00::1", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "2002:7f00:1::", "192.0.2.1", "224.0.0.1"} {
		if publicIP(net.ParseIP(raw)) {
			t.Fatal(raw)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
}
func TestJSONTokenAndRevoke(t *testing.T) {
	_, h := setup(t, config(t))
	f := authorizeFlow(t, h)
	b, _ := json.Marshal(map[string]string{"grant_type": "authorization_code", "code": f.code, "code_verifier": f.verifier, "redirect_uri": "https://client.example.com/callback"})
	w := request(h, "POST", "/oauth/token", string(b), "application/json")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	m := bodyMap(t, w)
	a := m["access_token"].(string)
	w = form(h, "/oauth/revoke", url.Values{"token": {m["refresh_token"].(string)}})
	if w.Code != 200 || authenticated(h, a) != 401 {
		t.Fatal("revoke failed")
	}
}

func TestPasswordHash(t *testing.T) {
	hashOnce.Do(func() { testHash, _ = HashPassword(testPassword) })
	if !verifyPassword(testHash, testPassword) || verifyPassword(testHash, "wrong") {
		t.Fatal("password verification")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestCorruptState(t *testing.T) {
	cfg := config(t)
	s, _ := setup(t, cfg)
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(f, `{"version":1}`)
	_ = f.Close()
	if _, err = New(cfg); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestMalformedStateFailsClosed(t *testing.T) {
	cfg := config(t)
	s, _ := setup(t, cfg)
	valid, _ := json.Marshal(s.state)
	cases := map[string]string{
		"missing issuer":   `{"version":1,"clients":{},"access":{},"refresh":{}}`,
		"duplicate key":    `{"version":1,"version":1,"issuer":"https://mcp.example.com","clients":{},"access":{},"refresh":{}}`,
		"unknown field":    string(valid[:len(valid)-1]) + `,"unexpected":true}`,
		"unknown version":  strings.Replace(string(valid), `"version":2`, `"version":3`, 1),
		"null collection":  strings.Replace(string(valid), `"clients":{}`, `"clients":null`, 1),
		"invalid token":    strings.Replace(string(valid), `"access":{}`, `"access":{"not-a-hash":{"client":"x","family":"y","expires":9999999999}}`, 1),
		"invalid redirect": strings.Replace(string(valid), `"clients":{}`, `"clients":{"`+strings.Repeat("A", 43)+`":{"client_id":"`+strings.Repeat("A", 43)+`","redirect_uris":["https://*.example.com/cb"],"expires":9999999999}}`, 1),
		"trailing data":    string(valid) + ` {}`,
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if err := atomicWrite(s.path, []byte(b)); err != nil {
				t.Fatal(err)
			}
			if _, err := New(cfg); err == nil {
				t.Fatal("malformed snapshot accepted")
			}
		})
	}
}

func TestIssuerBindingAndVolatileCodes(t *testing.T) {
	cfg := config(t)
	_, h := setup(t, cfg)
	f := authorizeFlow(t, h)
	_, h = setup(t, cfg)
	if w := form(h, "/oauth/token", tokenForm(f)); w.Code != 400 {
		t.Fatal("code survived restart")
	}
	cfg.Issuer = "https://other.example.com"
	if _, err := New(cfg); err == nil {
		t.Fatal("issuer change reused old state")
	}
}

func TestClientAndTokenCapacity(t *testing.T) {
	s, h := setup(t, config(t))
	s.mu.Lock()
	for range maxClients {
		id := randomToken()
		s.state.Clients[id] = client{ID: id, Redirects: []string{"https://client.example.com/cb"}, Expires: time.Now().Add(time.Hour).Unix(), Approved: true}
	}
	s.mu.Unlock()
	w := request(h, "POST", "/oauth/register", `{"redirect_uris":["https://client.example.com/cb"]}`, "application/json")
	if w.Code != 503 {
		t.Fatal("client capacity ignored")
	}
}

func TestConsentSessionBindingAndDecision(t *testing.T) {
	s, h := setup(t, config(t))
	s.mu.Lock()
	owner := randomToken()
	s.sessions[digest(owner)] = session{expires: time.Now().Add(time.Hour).Unix()}
	nonce := randomToken()
	s.pending[digest(nonce)] = grant{client: randomToken(), redirect: "https://client.example.com/cb", session: "different-session", expires: time.Now().Add(time.Minute).Unix()}
	s.mu.Unlock()
	cookie := &http.Cookie{Name: "__Host-chaoxing-owner", Value: owner}
	v := url.Values{"consent_token": {nonce}, "decision": {"approve"}}
	if w := form(h, "/oauth/consent", v, cookie); w.Code != 403 {
		t.Fatal("cross-session consent accepted")
	}
	v.Set("decision", "anything")
	if w := form(h, "/oauth/consent", v, cookie); w.Code != 400 {
		t.Fatal("invalid decision approved")
	}
}

func TestMCPDiscoveryIsAnonymousButCallsRemainProtected(t *testing.T) {
	s, err := New(config(t))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	mux.Handle("/mcp", s.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })))
	for _, method := range []string{"initialize", "ping", "tools/list", "server/discover"} {
		w := request(mux, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"`+method+`"}`, "application/json")
		if w.Code != 204 {
			t.Fatalf("discovery method %s was protected: %d", method, w.Code)
		}
	}
	for _, method := range []string{"tools/call", "unknown"} {
		w := request(mux, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"`+method+`"}`, "application/json")
		if w.Code != 401 {
			t.Fatalf("method %s bypassed OAuth: %d", method, w.Code)
		}
	}
	f := authorizeFlow(t, mux)
	w := form(mux, "/oauth/token", tokenForm(f))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	accessToken := bodyMap(t, w)["access_token"].(string)
	r := httptest.NewRequest("POST", "https://mcp.example.com/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+accessToken)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("authenticated call rejected", w.Code)
	}
}
