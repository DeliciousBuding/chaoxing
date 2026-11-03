package access

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBrowserFormPolicy(t *testing.T) {
	s, h := setup(t, config(t))
	w := request(h, "GET", "/oauth/login?next=%2Foauth%2Fauthorize", "", "")
	if w.Code != 200 || w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("local forms need same-origin referrer policy: status=%d policy=%q", w.Code, w.Header().Get("Referrer-Policy"))
	}
	if got := w.Header().Get("Content-Security-Policy"); got != "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'" {
		t.Fatalf("login must only submit to the issuer: %q", got)
	}
	for _, tc := range []struct {
		origin, site string
		allowed      bool
	}{
		{"https://mcp.example.com", "same-origin", true},
		{"null", "same-origin", false},
		{"https://attacker.example", "cross-site", false},
		{"https://mcp.example.com", "cross-site", false},
	} {
		r := httptest.NewRequest("POST", "https://mcp.example.com/oauth/login", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		if got := s.browserOK(r); got != tc.allowed {
			t.Errorf("origin=%q site=%q allowed=%v, want %v", tc.origin, tc.site, got, tc.allowed)
		}
	}
}

func TestConsentFormPolicyUsesOnlySelectedRegisteredCallback(t *testing.T) {
	for _, tc := range []struct {
		name, callback, origin string
	}{
		{"https", "https://receiver.example.com/callback", "https://receiver.example.com"},
		{"query-and-port", "https://receiver.example.com:8443/callback?next=https%3A%2F%2Funtrusted.example%2F", "https://receiver.example.com:8443"},
		{"loopback", "http://127.0.0.1:49152/callback", "http://127.0.0.1:49152"},
		{"localhost", "http://localhost:49152/callback", "http://localhost:49152"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := setup(t, config(t))
			metadata, err := json.Marshal(map[string]any{"client_name": "Example reader", "redirect_uris": []string{tc.callback, "https://other-registered.example/callback"}})
			if err != nil {
				t.Fatal(err)
			}
			w := request(h, "POST", "/oauth/register", string(metadata), "application/json")
			if w.Code != http.StatusCreated {
				t.Fatal(w.Code, w.Body.String())
			}
			id := bodyMap(t, w)["client_id"].(string)
			owner := randomToken()
			s.sessions[digest(owner)] = session{expires: time.Now().Add(time.Hour).Unix()}
			cookie := &http.Cookie{Name: "__Host-chaoxing-owner", Value: owner}
			sum := sha256.Sum256([]byte(strings.Repeat("v", 43)))
			q := url.Values{"client_id": {id}, "response_type": {"code"}, "redirect_uri": {tc.callback}, "state": {"example-state"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
			w = request(h, "GET", "/oauth/authorize?"+q.Encode(), "", "", cookie)
			want := "default-src 'none'; form-action 'self' " + tc.origin + "; frame-ancestors 'none'; base-uri 'none'"
			if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != want {
				t.Fatalf("consent must allow its 303 callback, not other registered origins or query values: status=%d CSP=%q, want %q", w.Code, w.Header().Get("Content-Security-Policy"), want)
			}
			if !strings.Contains(w.Body.String(), `action="/oauth/consent"`) || w.Header().Get("Referrer-Policy") != "same-origin" {
				t.Fatal("consent form or referrer boundary changed")
			}
			_, tokenText, found := strings.Cut(w.Body.String(), `name="consent_token" value="`)
			if !found {
				t.Fatal("missing consent token")
			}
			nonce, _, found := strings.Cut(tokenText, `"`)
			if !found {
				t.Fatal("malformed consent token field")
			}
			pending := len(s.pending)
			for _, callback := range []string{tc.callback + "extra", "https://unregistered.example/callback", "https://receiver.example.com; form-action *"} {
				q.Set("redirect_uri", callback)
				w = request(h, "GET", "/oauth/authorize?"+q.Encode(), "", "", cookie)
				if w.Code != http.StatusBadRequest || len(s.pending) != pending {
					t.Fatalf("unregistered callback created consent: status=%d pending=%d", w.Code, len(s.pending))
				}
			}
			decision := url.Values{"consent_token": {nonce}, "decision": {"deny"}}
			w = form(h, "/oauth/consent", decision, cookie)
			callback, err := url.Parse(w.Header().Get("Location"))
			if w.Code != http.StatusSeeOther || err != nil || callback.Query().Get("error") != "access_denied" || callback.Query().Get("iss") != s.cfg.Issuer || callback.Query().Get("state") != "example-state" {
				t.Fatal("denial did not return an issuer-bound callback")
			}
			if len(s.pending) != pending-1 || len(s.codes) != 0 || len(s.state.Access) != 0 || s.state.Clients[id].Approved {
				t.Fatal("denial granted authority or retained its consent")
			}
			decision.Set("decision", "approve")
			if w = form(h, "/oauth/consent", decision, cookie); w.Code != http.StatusForbidden {
				t.Fatal("denied consent could be replayed as approval")
			}
		})
	}
}

func TestAuthorizationCodeRejectsMismatchedBindingsAndExpiry(t *testing.T) {
	for _, field := range []string{"client_id", "redirect_uri", "resource", "code_verifier", "expiry"} {
		t.Run(field, func(t *testing.T) {
			s, h := setup(t, config(t))
			f := authorizeFlow(t, h)
			v := tokenForm(f)
			if field == "expiry" {
				g := s.codes[digest(f.code)]
				g.expires = time.Now().Add(-time.Second).Unix()
				s.codes[digest(f.code)] = g
			} else {
				v.Set(field, "mismatched")
			}
			w := form(h, "/oauth/token", v)
			if w.Code != http.StatusBadRequest || len(s.state.Access) != 0 || len(s.state.Refresh) != 0 {
				t.Fatalf("invalid %s exchanged for authority: status=%d", field, w.Code)
			}
			if field != "expiry" {
				if w = form(h, "/oauth/token", tokenForm(f)); w.Code != http.StatusOK {
					t.Fatal("invalid exchange consumed a still-valid authorization code")
				}
			}
		})
	}
}
