package access

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPendingDCRReclaimsOldestNotApproved(t *testing.T) {
	cfg := config(t)
	s, h := setup(t, cfg)
	now := time.Now()
	approved := randomToken()
	oldest := randomToken()
	s.mu.Lock()
	s.state.Clients[approved] = client{ID: approved, Redirects: []string{"https://client.example.com/cb"}, Approved: true, Created: now.Add(-time.Hour).Unix(), Expires: now.Add(24 * time.Hour).Unix()}
	s.state.Clients[oldest] = client{ID: oldest, Redirects: []string{"https://client.example.com/cb"}, Created: now.Add(-5 * time.Minute).Unix(), Expires: now.Add(5 * time.Minute).Unix()}
	for range maxClients - 2 {
		id := randomToken()
		s.state.Clients[id] = client{ID: id, Redirects: []string{"https://client.example.com/cb"}, Created: now.Unix(), Expires: now.Add(10 * time.Minute).Unix()}
	}
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	w := request(h, "POST", "/oauth/register", `{"client_name":"Example","redirect_uris":["https://client.example.com/cb"]}`, "application/json")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := bodyMap(t, w)["client_id"].(string)
	s.mu.Lock()
	if len(s.state.Clients) != maxClients {
		t.Fatal("unbounded clients")
	}
	if _, ok := s.state.Clients[oldest]; ok {
		t.Fatal("oldest pending not reclaimed")
	}
	if !s.state.Clients[approved].Approved {
		t.Fatal("approved client evicted")
	}
	c := s.state.Clients[id]
	if c.Approved || c.Expires-c.Created != int64(clientTTL.Seconds()) {
		t.Fatal("pending TTL", c)
	}
	s.mu.Unlock()
	s, _ = setup(t, cfg)
	if s.state.Clients[id].Approved || !s.state.Clients[approved].Approved {
		t.Fatal("approval flags lost on restart")
	}
	s.mu.Lock()
	c = s.state.Clients[id]
	c.Created = time.Now().Add(-clientTTL - time.Minute).Unix()
	c.Expires = c.Created + int64(clientTTL.Seconds())
	s.state.Clients[id] = c
	s.pruneLocked()
	if _, ok := s.state.Clients[id]; ok {
		t.Fatal("pending TTL not reclaimed")
	}
	s.mu.Unlock()
}

func TestRegisterAdmissionDoesNotStarveAuthorize(t *testing.T) {
	s, h := setup(t, config(t))
	s.registerSem <- struct{}{}
	s.registerSem <- struct{}{}
	defer func() {
		<-s.registerSem
		<-s.registerSem
	}()

	w := request(h, "POST", "/oauth/register", `{"redirect_uris":["https://client.example.com/cb"]}`, "application/json")
	if w.Code != 503 {
		t.Fatalf("register admission status=%d", w.Code)
	}
	// Authorization still reaches its own validation instead of sharing the
	// saturated registration bucket.
	w = request(h, "GET", "/oauth/authorize", "", "")
	if w.Code != 400 {
		t.Fatalf("authorize admission status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDCRPendingClientsAreBoundedPerNetwork(t *testing.T) {
	_, h := setup(t, config(t))
	for index := 0; index < 4; index++ {
		w := request(h, "POST", "/oauth/register", `{"redirect_uris":["https://client.example.com/cb"]}`, "application/json")
		if w.Code != 201 {
			t.Fatalf("registration %d status=%d body=%s", index, w.Code, w.Body.String())
		}
	}
	w := request(h, "POST", "/oauth/register", `{"redirect_uris":["https://client.example.com/cb"]}`, "application/json")
	if w.Code != 429 {
		t.Fatalf("per-network pending cap status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestConsentPromotesAndRefreshRenewsDCR(t *testing.T) {
	s, h := setup(t, config(t))
	f := authorizeFlow(t, h)
	c := s.state.Clients[f.id]
	if !c.Approved || c.Expires-c.Created != int64((90*24*time.Hour).Seconds()) {
		t.Fatal("consent did not promote client")
	}
	w := form(h, "/oauth/token", tokenForm(f))
	refresh := bodyMap(t, w)["refresh_token"].(string)
	s.mu.Lock()
	c = s.state.Clients[f.id]
	c.Created -= 3600
	c.Expires -= 3600
	s.state.Clients[f.id] = c
	s.mu.Unlock()
	w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.state.Clients[f.id].Expires <= c.Expires {
		t.Fatal("approved registration not renewed")
	}
}

func TestRefreshHundredsConstantStateAndProofReplay(t *testing.T) {
	cfg := config(t)
	s, h := setup(t, cfg)
	f := authorizeFlow(t, h)
	w := form(h, "/oauth/token", tokenForm(f))
	m := bodyMap(t, w)
	old := m["refresh_token"].(string)
	refresh := old
	access := m["access_token"].(string)
	family, _, ok := s.verifyRefresh(old)
	if !ok {
		t.Fatal("initial proof rejected")
	}
	originalExpiry := s.state.Refresh[family].Expires
	for i := 0; i < 500; i++ {
		// The endpoint limiter is tested separately; exercise rotation mechanics.
		s.mu.Lock()
		s.budget = 60
		s.tokenBudget = 30
		s.mu.Unlock()
		w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
		if w.Code != 200 {
			t.Fatal(i, w.Code, w.Body.String())
		}
		m = bodyMap(t, w)
		refresh = m["refresh_token"].(string)
		access = m["access_token"].(string)
		if len(s.state.Refresh) != 1 || len(s.state.Access) != 1 {
			t.Fatal("rotation state grew", i, len(s.state.Refresh), len(s.state.Access))
		}
		if s.state.Refresh[family].Expires != originalExpiry {
			t.Fatal("family TTL extended")
		}
	}
	s, h = setup(t, cfg)
	if authenticated(h, access) != 204 {
		t.Fatal("current access lost on restart")
	}
	// Knowing a family and counter is not authority: changing the random nonce
	// or sequence without its MAC must not revoke that family's current tokens.
	parts := strings.Split(old, ".")
	parts[3] = randomToken()
	forged := strings.Join(parts, ".")
	for _, raw := range []string{forged, "rt1." + family + ".0." + randomToken() + "." + randomToken(), old + ".junk"} {
		w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {raw}})
		if w.Code != 400 || authenticated(h, access) != 204 {
			t.Fatal("forged proof revoked live family")
		}
	}
	w = form(h, "/oauth/revoke", url.Values{"token": {forged}})
	if w.Code != 200 || authenticated(h, access) != 204 {
		t.Fatal("forged revoke revoked live family")
	}
	w = form(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}})
	if w.Code != 400 || authenticated(h, access) != 401 {
		t.Fatal("old issued proof not revoked after restart")
	}
	if len(s.state.Refresh) != 0 || len(s.state.Access) != 0 {
		t.Fatal("revoked family retained state")
	}
	_, h = setup(t, cfg)
	if authenticated(h, access) != 401 {
		t.Fatal("revocation did not persist")
	}
}

func TestConsentShowsReceivingCallbackAndClientID(t *testing.T) {
	s, h := setup(t, config(t))
	w := request(h, "POST", "/oauth/register", `{"client_name":"Trusted reader <script>fake</script>","redirect_uris":["https://receiver.example.com/callback"]}`, "application/json")
	id := bodyMap(t, w)["client_id"].(string)
	owner := randomToken()
	s.mu.Lock()
	s.sessions[digest(owner)] = session{expires: time.Now().Add(time.Hour).Unix()}
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(strings.Repeat("v", 43)))
	q := url.Values{"client_id": {id}, "response_type": {"code"}, "redirect_uri": {"https://receiver.example.com/callback"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	w = request(h, "GET", "/oauth/authorize?"+q.Encode(), "", "", &http.Cookie{Name: "__Host-chaoxing-owner", Value: owner})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, visible := range []string{"Self-reported client name (not verified)", "Client ID: " + id, "Callback (receives authorization): https://receiver.example.com/callback"} {
		if !strings.Contains(body, visible) {
			t.Fatal("missing visible identity", visible)
		}
	}
	if strings.Contains(body, "<script>fake</script>") {
		t.Fatal("client name not escaped")
	}
}

func TestRefreshKeyAndLegacyStateFailClosed(t *testing.T) {
	cfg := config(t)
	s, _ := setup(t, cfg)
	keyPath := filepath.Join(cfg.StateDir, "oauth-refresh.key")
	info, err := os.Stat(keyPath)
	if err != nil || info.Size() != 32 {
		t.Fatal("key missing")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
	stateBytes, _ := json.Marshal(s.state)
	legacy := strings.Replace(string(stateBytes), `"version":2`, `"version":1`, 1)
	if err := atomicWrite(s.path, []byte(legacy)); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("legacy state silently migrated")
	}
	unchanged, _ := os.ReadFile(s.path)
	if string(unchanged) != legacy {
		t.Fatal("legacy state overwritten")
	}
	if err := atomicWrite(s.path, stateBytes); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(keyPath)
	if err := atomicWrite(keyPath, randomBytes(32)); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("replacement proof key accepted")
	}
	if err := atomicWrite(keyPath, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("missing proof key regenerated")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("missing key recreated")
	}
}

func TestPrivateStateUnderPublicTempParent(t *testing.T) {
	cfg := config(t)
	if err := os.Chmod(filepath.Dir(cfg.StateDir), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatal("state child is not private", info.Mode())
	}
}

func TestNonPrivateStateDirectoryRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode checks")
	}
	cfg := config(t)
	if err := os.Mkdir(cfg.StateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg.StateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("non-private state directory accepted")
	}
}
