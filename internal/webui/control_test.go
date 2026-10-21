package webui

import (
	"context"
	"fmt"
	"net/http"
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
