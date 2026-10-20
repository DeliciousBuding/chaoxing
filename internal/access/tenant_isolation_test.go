package access

import (
	"net/url"
	"testing"
)

func TestTenantRejectsRootTokensAndRefreshWithoutRevokingRoot(t *testing.T) {
	_, root := setup(t, config(t))
	f := authorizeFlow(t, root)
	w := form(root, "/oauth/token", tokenForm(f))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	tokens := bodyMap(t, w)
	access, refresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	cfg := config(t)
	cfg.Issuer = "https://mcp.example.com/t/example"
	_, tenant := setup(t, cfg)
	if authenticated(root, access) != 204 {
		t.Fatal("root token invalid")
	}
	if authenticated(tenant, access) != 401 {
		t.Fatal("root token crossed tenant boundary")
	}
	if w := form(tenant, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}); w.Code != 400 {
		t.Fatal("root refresh crossed tenant boundary", w.Code)
	}
	if authenticated(root, access) != 204 {
		t.Fatal("tenant rejection affected root token")
	}
	if w := form(root, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}); w.Code != 200 {
		t.Fatal("tenant rejection revoked root refresh", w.Code)
	}
}
