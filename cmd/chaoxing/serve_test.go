package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/authz"
	"github.com/DeliciousBuding/chaoxing/internal/controlplane"
	"github.com/DeliciousBuding/chaoxing/internal/egress"
	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/vault"
	"github.com/DeliciousBuding/chaoxing/internal/watch"
)

func TestToolAuditRecorderAttributesMcpClient(t *testing.T) {
	hash, err := access.HashPassword("synthetic-owner-password")
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
	defer store.Close()
	call := auditedToolExecute("tenant-a", func(context.Context, string, map[string]any) (any, error) {
		return map[string]any{"ok": true}, nil
	}, toolAuditRecorder(store))
	ctx := authz.WithPrincipal(context.Background(), "mcp:test-client")
	if _, err := call(ctx, "list_courses", nil); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.ListAudit(context.Background(), controlplane.AuditFilter{Category: "tool", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ActorRole != "mcp" || items[0].AccountKey != "tenant-a" || items[0].Target != "list_courses" || !items[0].Success {
		t.Fatalf("tool audit=%+v", items)
	}
}

func TestValidateTenants(t *testing.T) {
	base := config{StateDir: "/s", KeyPath: "/k"}
	ok := base
	ok.Tenants = []tenantConfig{{ID: "alice-1", StateDir: "/s1", KeyPath: "/k1", OwnerPasswordHash: "h"}}
	if err := validateTenants(ok); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []tenantConfig{
		{ID: "Alice", StateDir: "/s1", KeyPath: "/k1", OwnerPasswordHash: "h"},
		{ID: "-a", StateDir: "/s1", KeyPath: "/k1", OwnerPasswordHash: "h"},
		{ID: strings.Repeat("a", 33), StateDir: "/s1", KeyPath: "/k1", OwnerPasswordHash: "h"},
		{ID: "a", StateDir: "/s", KeyPath: "/k1", OwnerPasswordHash: "h"},
		{ID: "a", StateDir: "/s1", KeyPath: "", OwnerPasswordHash: "h"},
	} {
		bad := base
		bad.Tenants = []tenantConfig{tc}
		if err := validateTenants(bad); err == nil {
			t.Fatal("accepted", tc)
		}
	}
	dup := base
	dup.Tenants = []tenantConfig{
		{ID: "a", StateDir: "/s1", KeyPath: "/k1", OwnerPasswordHash: "h"},
		{ID: "a", StateDir: "/s2", KeyPath: "/k2", OwnerPasswordHash: "h"},
	}
	if err := validateTenants(dup); err == nil {
		t.Fatal("duplicate tenant accepted")
	}
}

func TestStateStoreIsAlwaysNeededForSignAudit(t *testing.T) {
	if !stateStoreNeeded(config{}) {
		t.Fatal("manual sign-in would bypass the private audit store")
	}
}

func TestWatchSenderRejectsPartialNotificationConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  watchConfig
		want string
	}{
		{"ntfy without topic", watchConfig{NtfyURL: "https://ntfy.example.com"}, "ntfy_topic is missing"},
		{"ntfy topic without url", watchConfig{NtfyTopic: "synthetic-topic"}, "ntfy_url is missing"},
		{"gotify without token", watchConfig{GotifyURL: "https://gotify.example.com"}, "gotify_token is missing"},
		{"gotify token without url", watchConfig{GotifyToken: "synthetic-token"}, "gotify_url is missing"},
		{"no channel", watchConfig{}, "no feishu_webhook"},
	} {
		_, err := watchSender(tc.cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestWatchSenderBuildsIndependentChannels(t *testing.T) {
	sender, err := watchSender(watchConfig{
		FeishuWebhook: "https://example.com/feishu-webhook",
		NtfyURL:       "https://ntfy.example.com",
		NtfyTopic:     "synthetic-topic",
		GotifyURL:     "https://gotify.example.com",
		GotifyToken:   "synthetic-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	multi, ok := sender.(notify.Multi)
	if !ok || len(multi) != 3 {
		t.Fatalf("sender has type %T with length %d", sender, len(multi))
	}
}

func TestWatchCoreConfigPreservesAutomationFlags(t *testing.T) {
	cfg, err := watchCoreConfig(watchConfig{
		AutoSign: true, AutoAnswer: true, IntervalSeconds: 60,
		Classes: []watch.Class{{CourseID: "c", ClassID: "k"}},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoSign || !cfg.AutoAnswer {
		t.Fatalf("automation flags were dropped: %+v", cfg)
	}
}

func tenantFixture(t *testing.T) (config, *upstream.Client, *access.Server) {
	t.Helper()
	d := t.TempDir()
	c := config{
		StateDir: filepath.Join(d, "default"),
		KeyPath:  filepath.Join(d, "key"),
		Issuer:   "https://mcp.example.com",
		Tenants: []tenantConfig{{
			ID:       "roommate",
			StateDir: filepath.Join(d, "t-roommate"),
			KeyPath:  filepath.Join(d, "t-key"),
		}},
	}
	if err := vault.Seal(filepath.Join(c.StateDir, "credentials.sealed"), c.KeyPath, vault.Credentials{Phone: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	hash, err := access.HashPassword("owner-password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	c.OwnerPasswordHash = hash
	c.Tenants[0].OwnerPasswordHash = hash
	x, err := upstream.New(upstream.Config{
		CredentialsPath: filepath.Join(c.StateDir, "credentials.sealed"),
		KeyPath:         c.KeyPath,
		SessionPath:     filepath.Join(c.StateDir, "session.sealed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := access.New(access.Config{Issuer: c.Issuer, StateDir: filepath.Join(c.StateDir, "oauth"), OwnerPasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	return c, x, a
}

func TestTenantIsolationAndDiscovery(t *testing.T) {
	c, x, a := tenantFixture(t)
	pool, err := egress.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	execute, err := operationExecutor(x, c.StateDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux, ids, _, err := publicMux(c, x, a, pool, nil, nil, nil, execute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "roommate" {
		t.Fatal(ids)
	}
	for _, path := range []string{
		"/.well-known/oauth-authorization-server/t/roommate",
		"/.well-known/oauth-protected-resource/t/roommate/mcp",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "https://mcp.example.com"+path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "https://mcp.example.com/t/roommate") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	// Tenant metadata advertises the tenant issuer.
	r := httptest.NewRequest("GET", "https://mcp.example.com/t/roommate/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://mcp.example.com/t/roommate") {
		t.Fatal(w.Code, w.Body.String())
	}
	// Tenant MCP requires auth and does not leak into the root account.
	r = httptest.NewRequest("POST", "https://mcp.example.com/t/roommate/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_courses"}}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("tenant call bypassed OAuth", w.Code)
	}
	// Anonymous discovery still works per tenant.
	r = httptest.NewRequest("POST", "https://mcp.example.com/t/roommate/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("tenant discovery blocked", w.Code)
	}
	// Unknown tenant is not routed.
	r = httptest.NewRequest("GET", "https://mcp.example.com/t/unknown/.well-known/oauth-authorization-server", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("unknown tenant served")
	}
	// Tenant OAuth state is separate from root state.
	r = httptest.NewRequest("POST", "https://mcp.example.com/t/roommate/oauth/register", strings.NewReader(`{"redirect_uris":["https://client.example.com/cb"]}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 && w.Code != 201 {
		t.Fatal("tenant register failed", w.Code, w.Body.String())
	}
}

func TestWatcherStaleBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if watcherStale(now, now.Add(-time.Minute), time.Time{}, 0) {
		t.Fatal("fresh process flagged stale before its first tick")
	}
	if watcherStale(now, now.Add(-time.Hour), now.Add(-14*time.Minute), 10) {
		t.Fatal("recent tick flagged stale")
	}
	if !watcherStale(now, now.Add(-time.Hour), now.Add(-16*time.Minute), 10) {
		t.Fatal("wedged loop not flagged stale")
	}
	if !watcherStale(now, now.Add(-time.Hour), time.Time{}, 0) {
		t.Fatal("watcher that never ticked not flagged stale")
	}
	if !watcherStale(now, now.Add(-time.Hour), now.Add(-time.Hour), 1) {
		t.Fatal("watcher with only a started tick not flagged stale")
	}
	if watcherStale(now, now.Add(time.Minute), now.Add(time.Minute), 1) {
		t.Fatal("clock skew flagged stale")
	}
}
