package webui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/controlplane"
)

func newControlServer(t *testing.T) (*Server, *http.ServeMux, *controlplane.Store) {
	t.Helper()
	hash, err := access.HashPassword("control-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "control")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.Open(filepath.Join(dir, "control.db"), hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server, err := New(Config{
		Version: "test", Issuer: "https://mcp.example.com", OwnerPasswordHash: hash,
		Accounts: []Account{{ID: "tenant", Name: "Tenant"}},
		Control:  store,
		AccountConfigs: func() []AccountConfigView {
			return []AccountConfigView{
				{ID: "root", Name: "默认账号", WatchEnabled: true, AutoSign: true},
				{ID: "tenant", Name: "Tenant", WatchEnabled: true, AutoAnswer: false},
			}
		},
		Execute: func(ctx context.Context, account, tool string, arguments map[string]any) (any, error) {
			return map[string]any{"account": account, "tool": tool}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.Register(mux)
	return server, mux, store
}

func loginAs(t *testing.T, h http.Handler, username, password string) (*http.Cookie, *http.Cookie) {
	t.Helper()
	w := do(t, h, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	if csrf == "" {
		t.Fatal("csrf cookie missing")
	}
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	w = doCSRF(t, h, "POST", "/api/auth/login", body, csrf, csrfCookie)
	if w.Code != 200 {
		t.Fatalf("login %s: %d %s", username, w.Code, w.Body.String())
	}
	session := &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}
	if session.Value == "" {
		t.Fatal("session cookie missing")
	}
	return csrfCookie, session
}

func TestControlPlaneMemberLoginAndScope(t *testing.T) {
	_, mux, store := newControlServer(t)
	csrf, session := loginAs(t, mux, "owner", "control-owner-password")

	w := doCSRF(t, mux, "POST", "/api/admin/users", `{"username":"alice","display_name":"Alice","password":"alice-password-123","role":"operator","accounts":["root"]}`, csrf.Value, csrf, session)
	if w.Code != 201 {
		t.Fatalf("create member: %d %s", w.Code, w.Body.String())
	}

	// The account selector is filtered to explicit grants.
	aliceCSRF, aliceSession := loginAs(t, mux, "alice", "alice-password-123")
	w = do(t, mux, "GET", "/api/account", "", aliceCSRF, aliceSession)
	accountBody := jsonMap(t, w)
	accounts := accountBody["accounts"].([]any)
	if len(accounts) != 1 || accounts[0].(map[string]any)["id"] != "root" {
		t.Fatalf("account scope=%v", accountBody)
	}
	w = doCSRF(t, mux, "POST", "/api/account/select", `{"account":"tenant"}`, aliceCSRF.Value, aliceCSRF, aliceSession)
	if w.Code != 403 {
		t.Fatalf("unauthorized account switch: %d %s", w.Code, w.Body.String())
	}

	// Operator can use sign/answer/read but not work.submit or face.
	w = do(t, mux, "GET", "/api/tools", "", aliceCSRF, aliceSession)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	toolBody := w.Body.String()
	if !strings.Contains(toolBody, `"list_courses"`) || !strings.Contains(toolBody, `"sign_activity"`) {
		t.Fatal("operator tool list missing expected tools")
	}
	if strings.Contains(toolBody, `"submit_work"`) || strings.Contains(toolBody, `"enroll_face"`) {
		t.Fatal("operator received administrator-only tool")
	}

	// Viewer cannot read sensitive audit data or enter the admin API.
	w = doCSRF(t, mux, "POST", "/api/admin/users", `{"username":"viewer","display_name":"Viewer","password":"viewer-password-123","role":"viewer","accounts":["tenant"]}`, csrf.Value, csrf, session)
	if w.Code != 201 {
		t.Fatalf("create viewer: %d %s", w.Code, w.Body.String())
	}
	viewerCSRF, viewerSession := loginAs(t, mux, "viewer", "viewer-password-123")
	w = do(t, mux, "GET", "/api/sign-audits", "", viewerCSRF, viewerSession)
	if w.Code != 403 {
		t.Fatalf("viewer sign audit: %d %s", w.Code, w.Body.String())
	}
	w = do(t, mux, "GET", "/api/admin/users", "", viewerCSRF, viewerSession)
	if w.Code != 403 {
		t.Fatalf("viewer admin API: %d %s", w.Code, w.Body.String())
	}
	w = doCSRF(t, mux, "POST", "/api/tools/call", `{"tool":"answer_activity","arguments":{"course_id":"c","class_id":"k","activity_id":"a"}}`, viewerCSRF.Value, viewerCSRF, viewerSession)
	if w.Code != 403 {
		t.Fatalf("viewer answer tool: %d %s", w.Code, w.Body.String())
	}

	// A grant/role change invalidates existing sessions.
	members, err := store.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var aliceID int64
	for _, user := range members {
		if user.Username == "alice" {
			aliceID = user.ID
		}
	}
	w = doCSRF(t, mux, "PATCH", fmt.Sprintf("/api/admin/users/%d", aliceID), `{"role":"viewer"}`, csrf.Value, csrf, session)
	if w.Code != 200 {
		t.Fatalf("update member: %d %s", w.Code, w.Body.String())
	}
	w = do(t, mux, "GET", "/api/account", "", aliceCSRF, aliceSession)
	if w.Code != 401 {
		t.Fatalf("stale role session survived: %d %s", w.Code, w.Body.String())
	}
}

func TestControlSessionRequiresPositiveIdentity(t *testing.T) {
	s, mux, _ := newControlServer(t)
	now := s.now()
	token := strings.Repeat("c", 64)
	id := digest(token)
	s.sessions[id] = webSession{
		account: "tenant", role: controlplane.RoleOwner, username: "owner",
		created: now, lastSeen: now, expires: now.Add(sessionTTL), remoteIP: "192.0.2.0/24",
	}
	r := httptest.NewRequest("GET", "https://mcp.example.com/api/status", nil)
	r = r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, authorizedSession{id: id}))
	if _, ok := s.requestSession(r); ok {
		t.Fatal("control session without positive user identity was accepted")
	}

	w := do(t, mux, "GET", "/api/status", "", &http.Cookie{Name: sessionCookie, Value: token})
	if w.Code != 401 {
		t.Fatalf("zero-identity session status=%d %s", w.Code, w.Body.String())
	}
}

func TestControlOutagePreservesSessionAndReturns503(t *testing.T) {
	s, mux, store := newControlServer(t)
	csrf, session := loginAs(t, mux, "owner", "control-owner-password")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	id := digest(session.Value)

	w := do(t, mux, "GET", "/api/status", "", csrf, session)
	if w.Code != 503 {
		t.Fatalf("control outage status=%d %s", w.Code, w.Body.String())
	}
	s.mu.Lock()
	_, retained := s.sessions[id]
	s.mu.Unlock()
	if !retained {
		t.Fatal("control-plane outage evicted the browser session")
	}
}

func TestControlPlaneAuditAndConfigSummary(t *testing.T) {
	_, mux, _ := newControlServer(t)
	csrf, session := loginAs(t, mux, "owner", "control-owner-password")
	w := do(t, mux, "GET", "/api/admin/accounts", "", csrf, session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"auto_sign":true`) {
		t.Fatalf("account summary: %d %s", w.Code, w.Body.String())
	}
	w = do(t, mux, "GET", "/api/admin/audit?category=auth", "", csrf, session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"auth.login"`) {
		t.Fatalf("audit list: %d %s", w.Code, w.Body.String())
	}
}

func TestMemberWithNoAccountGrantCanSignIn(t *testing.T) {
	_, mux, _ := newControlServer(t)
	csrf, session := loginAs(t, mux, "owner", "control-owner-password")
	w := doCSRF(t, mux, "POST", "/api/admin/users", `{"username":"viewer","display_name":"Viewer","password":"viewer-password-123","role":"viewer","accounts":[]}`, csrf.Value, csrf, session)
	if w.Code != 201 {
		t.Fatalf("create viewer: %d %s", w.Code, w.Body.String())
	}
	viewerCSRF, viewerSession := loginAs(t, mux, "viewer", "viewer-password-123")
	w = do(t, mux, "GET", "/api/account", "", viewerCSRF, viewerSession)
	if w.Code != 200 {
		t.Fatalf("zero-grant account scope: %d %s", w.Code, w.Body.String())
	}
	body := jsonMap(t, w)
	if accounts, ok := body["accounts"].([]any); !ok || len(accounts) != 0 || body["current"] != "" {
		t.Fatalf("unexpected zero-grant scope: %v", body)
	}
}

func TestLoginFailsClosedWhenControlStoreUnavailable(t *testing.T) {
	_, mux, store := newControlServer(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	if csrf == "" {
		t.Fatal("csrf cookie missing")
	}
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"username":"owner","password":"control-owner-password"}`, csrf, csrfCookie)
	if w.Code != 503 {
		t.Fatalf("login with unavailable store: %d %s", w.Code, w.Body.String())
	}
}

func TestAuditWriteFailureIsObservable(t *testing.T) {
	server, _, store := newControlServer(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/api/admin/users", nil)
	server.recordAudit(request, webSession{username: "owner", role: controlplane.RoleOwner}, "auth", "auth.login.failed", "", "", false, nil)
	health := server.ControlHealth()
	if got, _ := health["audit_write_failures"].(uint64); got != 1 {
		t.Fatalf("audit failure count=%v", health)
	}
	if got, _ := health["audit_consecutive_write_failures"].(uint64); got != 1 {
		t.Fatalf("audit failure streak=%v", health)
	}
	if health["audit_last_write_failure_at"] == "" {
		t.Fatalf("audit failure timestamp missing: %v", health)
	}
}

func TestControlSessionRequiresPositiveUserIdentity(t *testing.T) {
	server, _, _ := newControlServer(t)
	if accounts, ok := server.accountsForSession(webSession{}); ok || len(accounts) != 0 {
		t.Fatalf("zero user session received accounts: %v", accounts)
	}
	if role := server.sessionRole(webSession{}); role == controlplane.RoleOwner {
		t.Fatal("zero role was treated as owner")
	}
}

func TestZeroSessionAuditIsAnonymous(t *testing.T) {
	server, _, store := newControlServer(t)
	request := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/api/admin/users", nil)
	server.recordAudit(request, webSession{}, "security", "test.denied", "", "", false, nil)
	items, _, err := store.ListAudit(context.Background(), controlplane.AuditFilter{Category: "security", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ActorRole != "anonymous" {
		t.Fatalf("audit actor role=%+v", items)
	}
}

func TestOwnerLoginSurvivesViewerSessionExhaustion(t *testing.T) {
	server, mux, _ := newControlServer(t)
	now := server.now()
	server.mu.Lock()
	for i := 0; i < maxSessions; i++ {
		server.sessions[fmt.Sprintf("viewer-%064d", i)] = webSession{
			account: "root", userID: int64(2000 + i), username: fmt.Sprintf("viewer%d", i),
			role: controlplane.RoleViewer, created: now, lastSeen: now,
			expires: now.Add(sessionTTL), remoteIP: "192.0.2.0/24",
		}
	}
	server.mu.Unlock()
	_, session := loginAs(t, mux, "owner", "control-owner-password")
	if session.Value == "" {
		t.Fatal("owner session missing")
	}
	if got := len(server.sessions); got > maxSessions {
		t.Fatalf("session bound exceeded: %d", got)
	}
}
