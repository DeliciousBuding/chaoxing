package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/egress"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/vault"
)

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

func tenantFixture(t *testing.T) (config, *upstream.Client, *access.Server) {
	t.Helper()
	d := t.TempDir()
	c := config{
		StateDir: d,
		KeyPath:  filepath.Join(d, "key"),
		Issuer:   "https://mcp.example.com",
		Tenants: []tenantConfig{{
			ID:       "roommate",
			StateDir: filepath.Join(d, "t-roommate"),
			KeyPath:  filepath.Join(d, "t-key"),
		}},
	}
	if err := vault.Seal(filepath.Join(d, "credentials.sealed"), c.KeyPath, vault.Credentials{Phone: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	hash, err := access.HashPassword("owner-password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	c.OwnerPasswordHash = hash
	c.Tenants[0].OwnerPasswordHash = hash
	x, err := upstream.New(upstream.Config{
		CredentialsPath: filepath.Join(d, "credentials.sealed"),
		KeyPath:         c.KeyPath,
		SessionPath:     filepath.Join(d, "session.sealed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := access.New(access.Config{Issuer: c.Issuer, StateDir: filepath.Join(d, "oauth"), OwnerPasswordHash: hash})
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
	mux, ids, err := publicMux(c, x, a, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "roommate" {
		t.Fatal(ids)
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
