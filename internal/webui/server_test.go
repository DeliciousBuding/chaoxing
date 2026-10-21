package webui

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/capture"
)

func newTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	hash, err := access.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Version: "test", Issuer: "https://mcp.example.com", OwnerPasswordHash: hash,
		Accounts: []Account{{ID: "tenant", Name: "tenant"}},
		Execute: func(ctx context.Context, account, tool string, arguments map[string]any) (any, error) {
			return map[string]any{"account": account, "tool": tool, "arguments": arguments}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return s, mux
}

func do(t *testing.T, h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, "https://mcp.example.com"+path, reader)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func doCSRF(t *testing.T, h http.Handler, method, path, body, csrf string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, "https://mcp.example.com"+path, reader)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-Chaoxing-CSRF", csrf)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func jsonMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out
}

func cookieValue(w *httptest.ResponseRecorder, name string) string {
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func TestWebUIRequiresLoginAndCSRF(t *testing.T) {
	_, mux := newTestServer(t)
	if w := do(t, mux, "GET", "/api/status", ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := do(t, mux, "GET", "/api/auth/session", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	csrf := cookieValue(w, csrfCookie)
	if csrf == "" {
		t.Fatal("csrf cookie missing")
	}
	sessionCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	r := httptest.NewRequest("POST", "https://mcp.example.com/api/auth/login", strings.NewReader(`{"password":"wrong password"}`))
	r.AddCookie(sessionCookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Chaoxing-CSRF", csrf)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != 401 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestWebUILoginSessionAndToolCall(t *testing.T) {
	_, mux := newTestServer(t)
	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}

	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	session := &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}
	if session.Value == "" {
		t.Fatal("session cookie missing")
	}

	w = do(t, mux, "GET", "/api/auth/session", "", csrfCookie, session)
	value := jsonMap(t, w)
	if value["authenticated"] != true || value["account"] != "root" {
		t.Fatal(value)
	}

	w = do(t, mux, "POST", "/api/tools/call", `{"tool":"list_courses","arguments":{}}`, csrfCookie, session)
	if w.Code != 403 {
		t.Fatal("missing csrf accepted", w.Code, w.Body.String())
	}
	forgedCookie := &http.Cookie{Name: csrfCookie.Name, Value: strings.Repeat("a", 64)}
	w = doCSRF(t, mux, "POST", "/api/tools/call", `{"tool":"list_courses","arguments":{}}`, strings.Repeat("a", 64), forgedCookie, session)
	if w.Code != 403 {
		t.Fatal("csrf token was not bound to session", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("POST", "https://mcp.example.com/api/tools/call", strings.NewReader(`{"tool":"list_courses","arguments":{"x":1}}`))
	r.AddCookie(csrfCookie)
	r.AddCookie(session)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Chaoxing-CSRF", csrf)
	r.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross-origin accepted", rec.Code, rec.Body.String())
	}

	w = doCSRF(t, mux, "POST", "/api/tools/call", `{"tool":"list_courses","arguments":{"x":1}}`, csrf, csrfCookie, session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	value = jsonMap(t, w)
	data := value["data"].(map[string]any)
	if data["tool"] != "list_courses" || data["account"] != "root" {
		t.Fatal(value)
	}

	w = doCSRF(t, mux, "POST", "/api/account/select", `{"account":"tenant"}`, csrf, csrfCookie, session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = doCSRF(t, mux, "POST", "/api/tools/call", `{"tool":"list_courses","arguments":{}}`, csrf, csrfCookie, session)
	value = jsonMap(t, w)
	data = value["data"].(map[string]any)
	if data["account"] != "tenant" {
		t.Fatal(value)
	}

	w = doCSRF(t, mux, "POST", "/api/auth/logout", `{}`, csrf, csrfCookie, session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = do(t, mux, "GET", "/api/status", "", csrfCookie, session)
	if w.Code != 401 {
		t.Fatal("logout did not invalidate session", w.Code, w.Body.String())
	}
}

func TestWebUIServesEmbeddedBundleWithCSP(t *testing.T) {
	_, mux := newTestServer(t)
	w := do(t, mux, "GET", "/", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "学习通管理平台") {
		t.Fatal(w.Code, w.Body.String())
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing")
	}
	if scriptSrc := regexp.MustCompile(`script-src ([^;]+)`).FindStringSubmatch(csp); scriptSrc == nil || strings.Contains(scriptSrc[1], "unsafe-inline") {
		t.Fatal("script-src must not allow unsafe-inline")
	}
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Fatal("style-src policy missing")
	}
	if strings.Contains(csp, "api.map.baidu.com") || !strings.Contains(csp, "frame-src 'self'") {
		t.Fatal("main document must isolate the third-party map frame")
	}
	if w.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Fatal("COOP header missing")
	}
	match := regexp.MustCompile(`src="/assets/([^"]+\.js)"`).FindStringSubmatch(w.Body.String())
	if match == nil {
		t.Fatal("built JavaScript asset missing from index")
	}
	w = do(t, mux, "GET", "/assets/"+match[1], "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatal(w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	w = do(t, mux, "GET", "/map", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "chaoxing-map") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "api.map.baidu.com") || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatal("map frame isolation headers missing", w.Code, w.Header(), w.Body.String())
	}
}

func TestHeavyRequestsDoNotStarveLightControlPlane(t *testing.T) {
	s, _ := newTestServer(t)
	s.heavy <- struct{}{}
	s.heavy <- struct{}{}
	light := s.bounded(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	heavy := s.heavyBounded(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	if w := do(t, light, "GET", "/", ""); w.Code != 200 {
		t.Fatalf("light request blocked: %d", w.Code)
	}
	if w := do(t, heavy, "GET", "/", ""); w.Code != 503 {
		t.Fatalf("heavy capacity was not bounded: %d", w.Code)
	}
}

func TestWebUIServesEmbeddedAssetWithETagAndGzip(t *testing.T) {
	_, mux := newTestServer(t)
	w := do(t, mux, "GET", "/", "")
	match := regexp.MustCompile(`src="/assets/([^"]+\.js)"`).FindStringSubmatch(w.Body.String())
	if match == nil {
		t.Fatal("built JavaScript asset missing from index")
	}
	name := match[1]
	w = do(t, mux, "GET", "/assets/"+name, "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatal(w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if cache := w.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatal("hashed asset is not cached immutably", cache)
	}
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatal("asset negotiation headers missing", etag, w.Header().Get("Vary"))
	}
	original, err := dist.ReadFile("dist/assets/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Body.Bytes(), original) {
		t.Fatal("identity asset response changed")
	}

	r := httptest.NewRequest("GET", "https://mcp.example.com/assets/"+name, nil)
	r.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != 304 || rec.Body.Len() != 0 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("conditional asset request did not return an empty 304", rec.Code, rec.Body.String())
	}

	r = httptest.NewRequest("GET", "https://mcp.example.com/assets/"+name, nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal(rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, original) {
		t.Fatal("gzip asset did not decode to the original bytes")
	}

	w = do(t, mux, "GET", "/assets/", "")
	if w.Code != 404 {
		t.Fatal("asset directory listing was enabled", w.Code, w.Body.String())
	}
}

func TestWebUISessionLimitIsBounded(t *testing.T) {
	s, mux := newTestServer(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	for i := 0; i < maxSessions; i++ {
		s.sessions[fmt.Sprintf("%064d", i)] = webSession{
			account: "root", csrf: "existing", created: now, lastSeen: now,
			expires: now.Add(sessionTTL), remoteIP: "192.0.2.1",
		}
	}

	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != 429 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestWebUISessionExpiryAndRemoteIPBinding(t *testing.T) {
	s, mux := newTestServer(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }

	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	session := &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}

	now = now.Add(idleTTL + time.Second)
	w = do(t, mux, "GET", "/api/status", "", csrfCookie, session)
	if w.Code != 401 {
		t.Fatal("idle session accepted", w.Code, w.Body.String())
	}

	now = now.Add(time.Second)
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	session = &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}

	r := httptest.NewRequest("GET", "https://mcp.example.com/api/status", nil)
	r.RemoteAddr = "203.0.113.9:12345"
	r.AddCookie(csrfCookie)
	r.AddCookie(session)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != 401 {
		t.Fatal("session cookie was accepted from another remote address", rec.Code, rec.Body.String())
	}

	w = do(t, mux, "GET", "/api/status", "", csrfCookie, session)
	if w.Code != 401 {
		t.Fatal("remote-IP mismatch did not invalidate session", w.Code)
	}

	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	session = &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}
	now = now.Add(sessionTTL + time.Second)
	w = do(t, mux, "GET", "/api/status", "", csrfCookie, session)
	if w.Code != 401 {
		t.Fatal("absolute session TTL not enforced", w.Code)
	}
}

func TestWebUILoginRateLimitWindow(t *testing.T) {
	s, _ := newTestServer(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		s.attempts = append(s.attempts, loginAttempt{at: now.Add(-time.Duration(i) * time.Second), ip: "192.0.2.0/24"})
	}
	if s.allowLogin("192.0.2.0/24") {
		t.Fatal("eleventh login attempt inside one minute was allowed")
	}
}

func TestWebUILoginCapsConcurrentPasswordVerification(t *testing.T) {
	s, mux := newTestServer(t)
	s.loginSem <- struct{}{}
	defer func() { <-s.loginSem }()
	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != http.StatusTooManyRequests {
		t.Fatal("concurrent PBKDF2 was not bounded", w.Code, w.Body.String())
	}
	value := jsonMap(t, w)
	if err, _ := value["error"].(map[string]any); err["code"] != "LOGIN_BUSY" {
		t.Fatal(value)
	}
}

func TestWebUIRejectsInvalidHost(t *testing.T) {
	_, mux := newTestServer(t)
	r := httptest.NewRequest("GET", "https://evil.example.com/api/auth/session", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	value := jsonMap(t, w)
	if err, _ := value["error"].(map[string]any); err["code"] != "INVALID_HOST" {
		t.Fatal(value)
	}
}

func TestWebUIRemoteIPTrustsOnlyLoopbackProxies(t *testing.T) {
	s, _ := newTestServer(t)

	r := httptest.NewRequest("GET", "https://mcp.example.com/api/status", nil)
	r.RemoteAddr = "127.0.0.9:12345"
	r.Header.Set("X-Forwarded-For", "203.0.113.10, 127.0.0.20")
	if got := s.remoteIP(r); got != "203.0.113.0/24" {
		t.Fatalf("trusted proxy client=%q", got)
	}

	r = httptest.NewRequest("GET", "https://mcp.example.com/api/status", nil)
	r.RemoteAddr = "198.51.100.10:12345"
	r.Header.Set("X-Forwarded-For", "203.0.113.10, 127.0.0.20")
	if got := s.remoteIP(r); got != "198.51.100.0/24" {
		t.Fatalf("untrusted direct client=%q", got)
	}
}

func TestWebUISessionSurvivesIPv4AddressChangeInsideNetwork(t *testing.T) {
	s, _ := newTestServer(t)
	now := s.now()
	s.sessions[digest(strings.Repeat("a", 64))] = webSession{
		account: "root", csrf: "synthetic", created: now, lastSeen: now,
		expires: now.Add(sessionTTL), remoteIP: "198.51.100.0/24",
	}
	value := strings.Repeat("a", 64)
	r := httptest.NewRequest("GET", "https://mcp.example.com/api/status", nil)
	r.RemoteAddr = "198.51.100.20:12345"
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
	w := httptest.NewRecorder()
	if _, ok := s.currentSession(w, r); !ok {
		t.Fatal("session was invalidated by another address in the same /24", w.Code, w.Body.String())
	}

	r.RemoteAddr = "203.0.113.20:12345"
	w = httptest.NewRecorder()
	if _, ok := s.currentSession(w, r); ok {
		t.Fatal("session survived another /24 network")
	}
}

func TestWebUICapturePageEvidenceIsOptIn(t *testing.T) {
	s, mux := newTestServer(t)
	seen := time.Now().UTC().Truncate(time.Second)
	s.cfg.Captures = func(_ context.Context, account, activityKey string, limit int, includePage bool) ([]capture.Activity, error) {
		if account != "root" || limit != 10 {
			t.Fatalf("scope=%q limit=%d", account, limit)
		}
		return []capture.Activity{{
			AccountKey: "root", ActivityKey: "course:class:activity", CourseID: "course", ClassID: "class",
			ActivityID: "activity", Kind: "practice", State: "active", UserState: "unsigned", SignType: "unknown",
			Source: "class", FirstSeenAt: seen, LastSeenAt: seen, ActivityJSON: []byte(`{"id":"activity"}`),
			PageJSON: []byte(`{"forms":[{"action":"/practice/submit"}]}`), HasPage: true,
		}}, nil
	}

	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	session := &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}

	w = do(t, mux, "GET", "/api/captures?limit=10", "", csrfCookie, session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	value := jsonMap(t, w)
	items := value["items"].([]any)
	if len(items) != 1 {
		t.Fatal(items)
	}
	if _, exists := items[0].(map[string]any)["page_json"]; exists {
		t.Fatal("page evidence was returned without opt-in")
	}
	if items[0].(map[string]any)["has_page"] != true {
		t.Fatal("has_page metadata missing")
	}
	if w := do(t, mux, "GET", "/api/captures?limit=10&account=tenant", "", csrfCookie, session); w.Code != http.StatusForbidden {
		t.Fatal("cross-account capture query accepted", w.Code, w.Body.String())
	}
	if w := do(t, mux, "GET", "/api/captures?limit=10&include_page=true", "", csrfCookie, session); w.Code != http.StatusBadRequest {
		t.Fatal("unbounded page evidence request accepted", w.Code, w.Body.String())
	}

	w = do(t, mux, "GET", "/api/captures?limit=10&include_page=true&account=root&activity_key=course%3Aclass%3Aactivity", "", csrfCookie, session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	value = jsonMap(t, w)
	items = value["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["page_json"] == nil {
		t.Fatal(value)
	}
}
