package access

import (
	"testing"
	"time"
)

func TestUnapprovedRegistrationSurvivesSetupRetryAndRestart(t *testing.T) {
	cfg := config(t)
	s, h := setup(t, cfg)
	w := request(h, "POST", "/oauth/register", `{"client_name":"Example reader","redirect_uris":["https://client.example.com/callback"]}`, "application/json")
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	id := bodyMap(t, w)["client_id"].(string)
	s.mu.Lock()
	c := s.state.Clients[id]
	c.Created -= int64((20 * time.Minute).Seconds())
	c.Expires -= int64((20 * time.Minute).Seconds())
	s.state.Clients[id] = c
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	s, _ = setup(t, cfg)
	c, ok := s.resolveClient(t.Context(), id)
	if !ok || c.Approved {
		t.Fatal("setup retry must retain a registration, not authorize it")
	}
}
