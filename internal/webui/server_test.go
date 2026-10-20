package webui

import (
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
	if csp == "" || w.Header().Get("X-Frame-Options") != "DENY" || strings.Contains(csp, "unsafe-inline") {
		t.Fatal("security headers missing")
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
}

func TestWebUIServesEmbeddedAssetWithImmutableCache(t *testing.T) {
	_, mux := newTestServer(t)
	w := do(t, mux, "GET", "/", "")
	match := regexp.MustCompile(`src="/assets/([^"]+\.js)"`).FindStringSubmatch(w.Body.String())
	if match == nil {
		t.Fatal("built JavaScript asset missing from index")
	}
	w = do(t, mux, "GET", "/assets/"+match[1], "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatal(w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if cache := w.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatal("hashed asset is not cached immutably", cache)
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
		s.attempts = append(s.attempts, loginAttempt{at: now.Add(-time.Duration(i) * time.Second)})
	}
	if s.allowLogin() {
		t.Fatal("eleventh login attempt inside one minute was allowed")
	}
}

func TestWebUICapturePageEvidenceIsOptIn(t *testing.T) {
	s, mux := newTestServer(t)
	seen := time.Now().UTC().Truncate(time.Second)
	s.cfg.Captures = func(context.Context, int) ([]capture.Activity, error) {
		return []capture.Activity{{
			AccountKey: "root", ActivityKey: "course:class:activity", CourseID: "course", ClassID: "class",
			ActivityID: "activity", Kind: "practice", State: "active", UserState: "unsigned", SignType: "unknown",
			Source: "class", FirstSeenAt: seen, LastSeenAt: seen, ActivityJSON: []byte(`{"id":"activity"}`),
			PageJSON: []byte(`{"forms":[{"action":"/practice/submit"}]}`),
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
