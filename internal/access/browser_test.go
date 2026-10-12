package access

import (
	"net/http/httptest"
	"testing"
)

func TestBrowserFormPolicy(t *testing.T) {
	s, h := setup(t, config(t))
	w := request(h, "GET", "/oauth/login?next=%2Foauth%2Fauthorize", "", "")
	if w.Code != 200 || w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("local forms need same-origin referrer policy: status=%d policy=%q", w.Code, w.Header().Get("Referrer-Policy"))
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
