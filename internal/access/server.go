package access

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/authz"
)

// Config contains only this service's public issuer, private state location,
// and salted owner hash. Owner authentication is independent of upstream login.
type Config struct {
	Issuer, StateDir, OwnerPasswordHash string
	EnableCIMD                          bool
}
type session struct{ expires int64 }
type grant struct {
	client, redirect, challenge, state, session, resource string
	scope                                                 string
	expires                                               int64
}

type loginNonce struct {
	expires int64
	ip      string
}

// Server must have one process owning its StateDir. Codes, login sessions and
// pending consents are volatile; clients and hashed bearer tokens survive restart.
type Server struct {
	cfg            Config
	path           string
	mu             sync.Mutex
	state          diskState
	committed      []byte
	refreshKey     []byte
	sessions       map[string]session
	logins         map[string]loginNonce
	pending, codes map[string]grant
	cimd           map[string]client
	fetch          func(context.Context, string) (client, error)
	sem            chan struct{}
	registerSem    chan struct{}
	tokenSem       chan struct{}
	loginSem       chan struct{}
	budget         float64
	budgetAt       time.Time
	registerBudget float64
	registerAt     time.Time
	tokenBudget    float64
	tokenAt        time.Time
	loginCount     int
	loginWindow    time.Time
	loginAttempts  map[string][]int64
	clientNetworks map[string]string
	pendingByNet   map[string]int
	registerTries  map[string][]int64
}

func New(cfg Config) (*Server, error) {
	cfg.Issuer = strings.TrimRight(cfg.Issuer, "/")
	if !validIssuer(cfg.Issuer) {
		return nil, errors.New("issuer must be an explicit HTTPS origin")
	}
	var err error
	if _, _, _, err = parsePasswordHash(cfg.OwnerPasswordHash); err != nil {
		return nil, err
	}
	if cfg.StateDir == "" {
		return nil, errors.New("state directory is required")
	}
	if err = os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(cfg.StateDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("state directory must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("state directory must have mode 0700")
	}
	s := &Server{cfg: cfg, path: filepath.Join(cfg.StateDir, "oauth-state.json"), sessions: map[string]session{}, logins: map[string]loginNonce{}, pending: map[string]grant{}, codes: map[string]grant{}, cimd: map[string]client{}, fetch: fetchCIMD, sem: make(chan struct{}, 4), registerSem: make(chan struct{}, 2), tokenSem: make(chan struct{}, 4), loginSem: make(chan struct{}, 1), budget: 60, budgetAt: time.Now(), registerBudget: 10, registerAt: time.Now(), tokenBudget: 30, tokenAt: time.Now(), loginAttempts: map[string][]int64{}, clientNetworks: map[string]string{}, pendingByNet: map[string]int{}, registerTries: map[string][]int64{}}
	s.state, err = loadState(s.path)
	if err != nil {
		return nil, err
	}
	if s.state.Issuer != "" && s.state.Issuer != cfg.Issuer {
		return nil, errors.New("state issuer mismatch")
	}
	s.refreshKey, err = loadRefreshKey(filepath.Join(cfg.StateDir, "oauth-refresh.key"), s.state.RefreshKeyID != "")
	if err != nil {
		return nil, err
	}
	keyID := digest(string(s.refreshKey))
	if s.state.RefreshKeyID != "" && s.state.RefreshKeyID != keyID {
		return nil, errors.New("OAuth refresh proof key does not match state")
	}
	s.state.RefreshKeyID = keyID
	s.state.Issuer = cfg.Issuer
	s.committed, _ = json.Marshal(s.state)
	s.pruneLocked()
	if err = s.saveLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func validIssuer(raw string) bool {
	u, err := url.Parse(raw)
	// Path is allowed only as a tenant prefix such as /t/<id>; query and fragment stay forbidden.
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && validIssuerPath(u.Path) && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.ContainsAny(raw, "\r\n\t\\\"'<> ;*{}")
}

func validIssuerPath(p string) bool {
	if p == "" {
		return true
	}
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.HasSuffix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if seg == "" {
			return false
		}
		for _, r := range seg {
			if !('a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// Register mounts discovery, OAuth, and server-rendered login/consent routes.
func (s *Server) Register(mux *http.ServeMux) {
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		mux.HandleFunc("GET "+p, s.resourceMetadata)
	}
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/mcp", "/.well-known/openid-configuration", "/.well-known/openid-configuration/mcp"} {
		mux.HandleFunc("GET "+p, s.serverMetadata)
	}
	for _, p := range []string{"GET /oauth/authorize", "GET /oauth/login", "POST /oauth/login", "POST /oauth/consent"} {
		mux.Handle(p, s.bounded(http.HandlerFunc(s.route)))
	}
	mux.Handle("POST /oauth/register", s.registerBounded(http.HandlerFunc(s.route)))
	for _, p := range []string{"POST /oauth/token", "POST /oauth/revoke"} {
		mux.Handle(p, s.tokenBounded(http.HandlerFunc(s.route)))
	}
}

// RegisterPathDiscovery mounts RFC 8414/9728 path-insertion discovery at the
// origin mux. OAuth handlers and protected resources stay under the issuer.
func (s *Server) RegisterPathDiscovery(mux *http.ServeMux) {
	u, err := url.Parse(s.cfg.Issuer)
	if err != nil || u.Path == "" {
		return
	}
	for _, prefix := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		mux.HandleFunc("GET "+prefix+u.Path, s.serverMetadata)
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource"+u.Path+"/mcp", s.resourceMetadata)
}
func (s *Server) bounded(next http.Handler) http.Handler {
	return s.boundedWith(next, s.sem, &s.budget, &s.budgetAt, 60)
}

func (s *Server) tokenBounded(next http.Handler) http.Handler {
	return s.boundedWith(next, s.tokenSem, &s.tokenBudget, &s.tokenAt, 30)
}

func (s *Server) registerBounded(next http.Handler) http.Handler {
	return s.boundedWith(next, s.registerSem, &s.registerBudget, &s.registerAt, 10)
}

func (s *Server) boundedWith(next http.Handler, sem chan struct{}, budget *float64, budgetAt *time.Time, max float64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			oauthError(w, 503, "temporarily_unavailable")
			return
		}
		s.mu.Lock()
		now := time.Now()
		*budget += now.Sub(*budgetAt).Seconds()
		if *budget > max {
			*budget = max
		}
		*budgetAt = now
		ok := *budget >= 1
		if ok {
			*budget = *budget - 1
		}
		s.mu.Unlock()
		if !ok {
			w.Header().Set("Retry-After", "1")
			oauthError(w, 429, "temporarily_unavailable")
			return
		}
		if len(r.URL.RawQuery) > 8192 {
			oauthError(w, 400, "invalid_request")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		// Chromium serializes a same-origin form POST's Origin as "null" under
		// no-referrer. Keep the origin for local forms, but suppress cross-origin
		// referrers so authorization query parameters never reach callbacks.
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
func (s *Server) resourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"resource": s.cfg.Issuer + "/mcp", "authorization_servers": []string{s.cfg.Issuer}, "scopes_supported": []string{scopeRead, scopeSign, scopeWork, scopeAnswer, scopeWorkSubmit, scopeUpload, scopeFace}, "bearer_methods_supported": []string{"header"}})
}
func (s *Server) serverMetadata(w http.ResponseWriter, _ *http.Request) {
	i := s.cfg.Issuer
	writeJSON(w, 200, map[string]any{"issuer": i, "authorization_endpoint": i + "/oauth/authorize", "token_endpoint": i + "/oauth/token", "registration_endpoint": i + "/oauth/register", "revocation_endpoint": i + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{scopeRead, scopeSign, scopeWork, scopeAnswer, scopeWorkSubmit, scopeUpload, scopeFace}, "client_id_metadata_document_supported": s.cfg.EnableCIMD, "authorization_response_iss_parameter_supported": true})
}
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/register":
		s.registerClient(w, r)
	case "/oauth/authorize":
		s.authorize(w, r)
	case "/oauth/login":
		s.login(w, r)
	case "/oauth/consent":
		s.consent(w, r)
	case "/oauth/token":
		s.exchange(w, r)
	case "/oauth/revoke":
		s.revoke(w, r)
	}
}
func decodeJSON(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func (s *Server) registerClient(w http.ResponseWriter, r *http.Request) {
	network := oauthClientNetwork(r)
	var req struct {
		Name      string   `json:"client_name"`
		Redirects []string `json:"redirect_uris"`
		Auth      string   `json:"token_endpoint_auth_method"`
		Scope     string   `json:"scope"`
		Grants    []string `json:"grant_types"`
		Responses []string `json:"response_types"`
	}
	if decodeJSON(r, &req) != nil || len(req.Name) > 120 || len(req.Redirects) == 0 || len(req.Redirects) > 8 || (req.Auth != "" && req.Auth != "none") {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	scope, scopeOK := normalizeScope(req.Scope)
	if !scopeOK {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	for _, g := range req.Grants {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, 400, "invalid_client_metadata")
			return
		}
	}
	for _, v := range req.Responses {
		if v != "code" {
			oauthError(w, 400, "invalid_client_metadata")
			return
		}
	}
	for _, uri := range req.Redirects {
		if !validRedirect(uri) {
			oauthError(w, 400, "invalid_redirect_uri")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	now := time.Now()
	if !s.registerAllowedLocked(network, now) {
		w.Header().Set("Retry-After", "60")
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	if len(s.state.Clients) >= maxClients {
		oldest := ""
		var created int64
		for id, c := range s.state.Clients {
			if !c.Approved && (oldest == "" || c.Created < created || (c.Created == created && id < oldest)) {
				oldest = id
				created = c.Created
			}
		}
		if oldest == "" {
			oauthError(w, 503, "temporarily_unavailable")
			return
		}
		delete(s.state.Clients, oldest)
		s.forgetClientNetworkLocked(oldest)
	}
	// A connector caches its public registration before the owner logs in.
	// Registration is not authorization: keep it usable across setup retries;
	// the bounded pool reclaims unapproved entries first when capacity is full.
	c := client{ID: randomToken(), Name: req.Name, Redirects: req.Redirects, Scope: scope, Expires: now.Add(clientTTL).Unix(), Created: now.Unix()}
	s.state.Clients[c.ID] = c
	s.rememberClientNetworkLocked(c.ID, network)
	if s.saveLocked() != nil {
		oauthError(w, 503, "server_error")
		return
	}
	writeJSON(w, 201, map[string]any{"client_id": c.ID, "client_name": c.Name, "redirect_uris": c.Redirects, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "scope": scope, "client_id_issued_at": time.Now().Unix()})
}
func (s *Server) resolveClient(ctx context.Context, id string) (client, bool) {
	s.mu.Lock()
	s.pruneLocked()
	c, ok := s.state.Clients[id]
	if !ok {
		c, ok = s.cimd[id]
		ok = ok && c.Expires > time.Now().Unix()
	}
	s.mu.Unlock()
	if ok {
		return c, true
	}
	if !s.cfg.EnableCIMD || !validCIMD(id) {
		return client{}, false
	}
	c, err := s.fetch(ctx, id)
	if err != nil {
		return client{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.cimd {
		if v.Expires <= time.Now().Unix() {
			delete(s.cimd, k)
		}
	}
	if len(s.cimd) >= maxClients {
		return client{}, false
	}
	s.cimd[id] = c
	return c, true
}
func challengeOK(raw string) bool {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(b) == 32 && len(raw) == 43
}
func verifierOK(raw, challenge string) bool {
	if len(raw) < 43 || len(raw) > 128 {
		return false
	}
	for _, c := range raw {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	b := sha256.Sum256([]byte(raw))
	got := base64.RawURLEncoding.EncodeToString(b[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}
func (s *Server) sessionID(r *http.Request) string {
	c, err := r.Cookie("__Host-chaoxing-owner")
	if err != nil || len(c.Value) > 128 {
		return ""
	}
	id := digest(c.Value)
	v, ok := s.sessions[id]
	if !ok || v.expires <= time.Now().Unix() {
		return ""
	}
	return id
}
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope, scopeOK := normalizeScope(q.Get("scope"))
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !challengeOK(q.Get("code_challenge")) || !scopeOK || (q.Get("resource") != "" && q.Get("resource") != s.cfg.Issuer+"/mcp") || len(q.Get("state")) > 1024 {
		oauthError(w, 400, "invalid_request")
		return
	}
	c, ok := s.resolveClient(r.Context(), q.Get("client_id"))
	if !ok {
		oauthError(w, 400, "invalid_client")
		return
	}
	allowedScope := c.Scope
	if allowedScope == "" {
		allowedScope = scopeRead
	}
	if !scopeSubset(scope, allowedScope) {
		oauthError(w, 400, "invalid_scope")
		return
	}
	redirect := q.Get("redirect_uri")
	matched := false
	for _, v := range c.Redirects {
		if v == redirect {
			matched = true
		}
	}
	if !matched {
		oauthError(w, 400, "invalid_redirect_uri")
		return
	}
	resource := q.Get("resource")
	if resource == "" {
		resource = s.cfg.Issuer + "/mcp"
	}
	s.mu.Lock()
	s.pruneLocked()
	sid := s.sessionID(r)
	if sid == "" {
		s.mu.Unlock()
		http.Redirect(w, r, "/oauth/login?next="+url.QueryEscape(r.URL.RequestURI()), 302)
		return
	}
	if len(s.pending) >= maxPending {
		s.mu.Unlock()
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	nonce := randomToken()
	s.pending[digest(nonce)] = grant{client: c.ID, redirect: redirect, challenge: q.Get("code_challenge"), state: q.Get("state"), session: sid, resource: resource, scope: scope, expires: time.Now().Add(10 * time.Minute).Unix()}
	s.mu.Unlock()
	// Always ask: no approval memory or silent first-client authorization.
	s.page(w, pageData{Consent: true, Name: c.Name, ClientID: c.ID, Nonce: nonce, Callback: redirect, Scopes: scope, HighRisk: highRiskScope(scope)})
}

type pageData struct {
	Consent                               bool
	HighRisk                              bool
	Name, ClientID, Nonce, Next, Callback string
	Scopes                                string
}

var pageTemplate = template.Must(template.New("owner").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>MCP authorization</title><h1>{{if .Consent}}Authorize access{{else}}Owner login{{end}}</h1>{{if .Consent}}<p>Self-reported client name (not verified): {{.Name}}</p><p>Client ID: {{.ClientID}}</p><p>Callback (receives authorization): {{.Callback}}</p><p>Permissions: {{.Scopes}}. chaoxing.sign can submit class sign-in; chaoxing.work can edit local answer drafts; chaoxing.answer can generate answers; chaoxing.work.submit can save or submit coursework; chaoxing.upload can upload files; chaoxing.face can enroll and use a face template.</p>{{if .HighRisk}}<p><strong>High-risk permissions requested.</strong> Only approve if you recognize this exact callback URL and intend to grant write or biometric access.</p>{{end}}<form method="post" action="/oauth/consent"><input type="hidden" name="consent_token" value="{{.Nonce}}"><button name="decision" value="approve">Approve</button><button name="decision" value="deny">Deny</button></form>{{else}}<form method="post" action="/oauth/login"><input type="hidden" name="csrf" value="{{.Nonce}}"><input type="hidden" name="next" value="{{.Next}}"><label>Owner password <input name="password" type="password" autocomplete="current-password" maxlength="1024" required></label><button>Log in</button></form>{{end}}</html>`))

func highRiskScope(scope string) bool {
	for _, item := range strings.Fields(scope) {
		switch item {
		case scopeSign, scopeWork, scopeAnswer, scopeWorkSubmit, scopeUpload, scopeFace:
			return true
		}
	}
	return false
}

func (s *Server) page(w http.ResponseWriter, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Consent is persisted by this server and the callback redirect is issued
	// with a server-side 303, so the form never needs an external form-action.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	_ = pageTemplate.Execute(w, d)
}
func safeNext(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.IsAbs() || u.Host != "" || u.Path != "/oauth/authorize" || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\\") {
		return ""
	}
	return u.RequestURI()
}
func (s *Server) browserOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return (origin == "" || origin == s.cfg.Issuer) && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

// oauthClientNetwork returns a stable coarse network for the edge client. The
// service sees Cloudflare through loopback; trust its overwritten forwarding
// headers only in that case. Direct callers never get to choose their bucket
// via a spoofed header.
func oauthClientNetwork(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	remote, err := netip.ParseAddr(host)
	if err != nil || !remote.IsValid() {
		return "unknown"
	}
	remote = remote.Unmap()
	if remote.IsLoopback() {
		if candidate, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil && candidate.IsValid() {
			return maskClientNetwork(candidate.Unmap())
		}
		values := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(values) - 1; i >= 0; i-- {
			candidate, err := netip.ParseAddr(strings.TrimSpace(values[i]))
			if err != nil || !candidate.IsValid() || candidate.IsLoopback() {
				continue
			}
			return maskClientNetwork(candidate.Unmap())
		}
	}
	return maskClientNetwork(remote)
}

func maskClientNetwork(addr netip.Addr) string {
	bits := 64
	if addr.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(addr, bits).Masked().String()
}

func (s *Server) loginAllowedLocked(ip string, now time.Time) bool {
	if s.loginWindow.IsZero() || now.Sub(s.loginWindow) >= time.Minute {
		s.loginWindow = now
		s.loginCount = 0
	}
	cutoff := now.Add(-15 * time.Minute).Unix()
	recent := s.loginAttempts[ip]
	kept := recent[:0]
	perMinute, perQuarter := 0, 0
	for _, at := range recent {
		if at <= cutoff {
			continue
		}
		kept = append(kept, at)
		perQuarter++
		if at > now.Add(-time.Minute).Unix() {
			perMinute++
		}
	}
	s.loginAttempts[ip] = kept
	return s.loginCount < maxLoginGlobal && perMinute < maxLoginPerMin && perQuarter < maxLoginPer15m
}

func (s *Server) recordLoginFailureLocked(ip string, now time.Time) {
	if s.loginWindow.IsZero() || now.Sub(s.loginWindow) >= time.Minute {
		s.loginWindow = now
		s.loginCount = 0
	}
	s.loginCount++
	s.loginAttempts[ip] = append(s.loginAttempts[ip], now.Unix())
	if len(s.loginAttempts) > 1024 {
		for key := range s.loginAttempts {
			if key != ip {
				delete(s.loginAttempts, key)
				break
			}
		}
	}
}

func (s *Server) clearLoginFailuresLocked(ip string) {
	delete(s.loginAttempts, ip)
}

func (s *Server) registerAllowedLocked(network string, now time.Time) bool {
	cutoff := now.Add(-time.Hour).Unix()
	kept := s.registerTries[network][:0]
	perMinute := 0
	for _, at := range s.registerTries[network] {
		if at <= cutoff {
			continue
		}
		kept = append(kept, at)
		if at > now.Add(-time.Minute).Unix() {
			perMinute++
		}
	}
	if perMinute >= 4 || len(kept) >= 20 || s.pendingByNet[network] >= 4 {
		s.registerTries[network] = kept
		return false
	}
	s.registerTries[network] = append(kept, now.Unix())
	if len(s.registerTries) > 4096 {
		for key := range s.registerTries {
			if key != network {
				delete(s.registerTries, key)
				break
			}
		}
	}
	return true
}

func (s *Server) rememberClientNetworkLocked(clientID, network string) {
	s.clientNetworks[clientID] = network
	s.pendingByNet[network]++
}

func (s *Server) forgetClientNetworkLocked(clientID string) {
	network, ok := s.clientNetworks[clientID]
	if !ok {
		return
	}
	delete(s.clientNetworks, clientID)
	if s.pendingByNet[network] <= 1 {
		delete(s.pendingByNet, network)
		return
	}
	s.pendingByNet[network]--
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		next := safeNext(r.URL.Query().Get("next"))
		if next == "" {
			oauthError(w, 400, "invalid_request")
			return
		}
		s.mu.Lock()
		s.pruneLocked()
		now := time.Now()
		ip := oauthClientNetwork(r)
		pendingForIP := 0
		for _, entry := range s.logins {
			if entry.ip == ip && entry.expires > now.Unix() {
				pendingForIP++
			}
		}
		if len(s.logins) >= maxPending || pendingForIP >= maxLoginPending {
			s.mu.Unlock()
			w.Header().Set("Retry-After", "30")
			oauthError(w, 429, "temporarily_unavailable")
			return
		}
		nonce := randomToken()
		s.logins[digest(nonce)] = loginNonce{expires: now.Add(loginNonceTTL).Unix(), ip: ip}
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "__Host-chaoxing-login", Value: nonce, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(loginNonceTTL.Seconds())})
		s.page(w, pageData{Nonce: nonce, Next: next})
		return
	}
	if !s.browserOK(r) || r.ParseForm() != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	nonce := r.PostForm.Get("csrf")
	ck, err := r.Cookie("__Host-chaoxing-login")
	if err != nil || next == "" || len(nonce) > 128 || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(nonce)) != 1 {
		oauthError(w, 403, "access_denied")
		return
	}
	s.mu.Lock()
	s.pruneLocked()
	now := time.Now()
	nonceKey := digest(nonce)
	entry, ok := s.logins[nonceKey]
	delete(s.logins, nonceKey)
	s.mu.Unlock()
	if !ok || entry.expires <= now.Unix() {
		w.Header().Set("Retry-After", "60")
		oauthError(w, 429, "access_denied")
		return
	}
	s.mu.Lock()
	ip := oauthClientNetwork(r)
	limited := !s.loginAllowedLocked(ip, now)
	s.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		oauthError(w, 429, "access_denied")
		return
	}
	select {
	case s.loginSem <- struct{}{}:
		defer func() { <-s.loginSem }()
	default:
		w.Header().Set("Retry-After", "2")
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	if !verifyPassword(s.cfg.OwnerPasswordHash, r.PostForm.Get("password")) {
		s.mu.Lock()
		s.recordLoginFailureLocked(ip, time.Now())
		s.mu.Unlock()
		oauthError(w, 403, "access_denied")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLoginFailuresLocked(ip)
	if len(s.sessions) >= maxPending {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	raw := randomToken()
	s.sessions[digest(raw)] = session{expires: time.Now().Add(8 * time.Hour).Unix()}
	http.SetCookie(w, &http.Cookie{Name: "__Host-chaoxing-owner", Value: raw, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 8 * 3600})
	http.Redirect(w, r, next, 303)
}
func (s *Server) callback(g grant, key, value string) string {
	u, _ := url.Parse(g.redirect)
	q := u.Query()
	q.Set(key, value)
	q.Set("iss", s.cfg.Issuer)
	if g.state != "" {
		q.Set("state", g.state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func (s *Server) consent(w http.ResponseWriter, r *http.Request) {
	if !s.browserOK(r) || r.ParseForm() != nil {
		oauthError(w, 403, "access_denied")
		return
	}
	decision := r.PostForm.Get("decision")
	if decision != "approve" && decision != "deny" {
		oauthError(w, 400, "invalid_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	key := digest(r.PostForm.Get("consent_token"))
	g, ok := s.pending[key]
	if !ok || s.sessionID(r) == "" || s.sessionID(r) != g.session {
		oauthError(w, 403, "access_denied")
		return
	}
	if decision == "deny" {
		delete(s.pending, key)
		http.Redirect(w, r, s.callback(g, "error", "access_denied"), 303)
		return
	}
	if len(s.codes) >= maxPending {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if c, exists := s.state.Clients[g.client]; exists {
		matched := false
		for _, uri := range c.Redirects {
			if uri == g.redirect {
				matched = true
			}
		}
		if !matched {
			oauthError(w, 400, "invalid_client")
			return
		}
		c.Approved = true
		c.Created = time.Now().Unix()
		c.Expires = time.Now().Add(90 * 24 * time.Hour).Unix()
		s.state.Clients[c.ID] = c
		if s.saveLocked() != nil {
			oauthError(w, 503, "server_error")
			return
		}
		s.forgetClientNetworkLocked(c.ID)
	} else if !validCIMD(g.client) {
		oauthError(w, 400, "invalid_client")
		return
	}
	delete(s.pending, key)
	raw := randomToken()
	g.expires = time.Now().Add(time.Minute).Unix()
	s.codes[digest(raw)] = g
	http.Redirect(w, r, s.callback(g, "code", raw), 303)
}

type tokenRequest struct {
	GrantType string `json:"grant_type"`
	Code      string `json:"code"`
	Redirect  string `json:"redirect_uri"`
	Client    string `json:"client_id"`
	Verifier  string `json:"code_verifier"`
	Refresh   string `json:"refresh_token"`
	Scope     string `json:"scope"`
	Resource  string `json:"resource"`
}

func parseToken(r *http.Request) (tokenRequest, error) {
	var q tokenRequest
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		err := decodeJSON(r, &q)
		return q, err
	}
	err := r.ParseForm()
	f := r.PostForm
	q = tokenRequest{f.Get("grant_type"), f.Get("code"), f.Get("redirect_uri"), f.Get("client_id"), f.Get("code_verifier"), f.Get("refresh_token"), f.Get("scope"), f.Get("resource")}
	return q, err
}
func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	q, err := parseToken(r)
	requestedScope := ""
	scopeOK := true
	if strings.TrimSpace(q.Scope) != "" {
		requestedScope, scopeOK = normalizeScope(q.Scope)
	}
	if err != nil || !scopeOK || (q.Resource != "" && q.Resource != s.cfg.Issuer+"/mcp") {
		oauthError(w, 400, "invalid_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	var cid, family string
	var counter uint64
	resource := q.Resource
	if resource == "" {
		resource = s.cfg.Issuer + "/mcp"
	}
	expiry := time.Now().Add(refreshTTL).Unix()
	scope := requestedScope
	switch q.GrantType {
	case "authorization_code":
		g, ok := s.codes[digest(q.Code)]
		if !ok || (q.Client != "" && q.Client != g.client) || q.Redirect != g.redirect || (q.Resource != "" && q.Resource != g.resource) || !verifierOK(q.Verifier, g.challenge) {
			oauthError(w, 400, "invalid_grant")
			return
		}
		delete(s.codes, digest(q.Code))
		cid = g.client
		resource = g.resource
		if scope == "" {
			scope = g.scope
		} else if !scopeSubset(scope, g.scope) {
			oauthError(w, 400, "invalid_scope")
			return
		}
		family = randomToken()
	case "refresh_token":
		var verified bool
		family, counter, verified = s.verifyRefresh(q.Refresh)
		t, ok := s.state.Refresh[family]
		if !verified || !ok || (q.Client != "" && q.Client != t.Client) {
			oauthError(w, 400, "invalid_grant")
			return
		}
		if counter < t.Counter || (counter == t.Counter && digest(q.Refresh) != t.Digest) {
			s.revokeFamilyLocked(t.Family)
			if s.saveLocked() != nil {
				oauthError(w, 503, "server_error")
				return
			}
			oauthError(w, 400, "invalid_grant")
			return
		}
		if counter != t.Counter || counter == ^uint64(0) {
			oauthError(w, 400, "invalid_grant")
			return
		}
		counter++
		if c, exists := s.state.Clients[t.Client]; exists && c.Approved {
			c.Created = time.Now().Unix()
			c.Expires = time.Now().Add(90 * 24 * time.Hour).Unix()
			s.state.Clients[c.ID] = c
		}
		delete(s.state.Access, t.AccessID)
		delete(s.state.Refresh, family)
		cid = t.Client
		family = t.Family
		allowedScope := t.Scope
		if allowedScope == "" {
			allowedScope = scopeRead
		}
		if scope == "" {
			scope = allowedScope
		} else if !scopeSubset(scope, allowedScope) {
			oauthError(w, 400, "invalid_scope")
			return
		}
		if t.Resource != "" {
			resource = t.Resource
		}
		expiry = t.Expires
	default:
		oauthError(w, 400, "unsupported_grant_type")
		return
	}
	if len(s.state.Access) >= maxTokens || len(s.state.Refresh) >= maxTokens {
		s.state = emptyState()
		_ = json.Unmarshal(s.committed, &s.state)
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	a, refresh := randomToken(), s.signRefresh(family, counter)
	s.state.Access[digest(a)] = token{Client: cid, Family: family, Resource: resource, Scope: scope, Expires: time.Now().Add(accessTTL).Unix(), Created: time.Now().Unix()}
	s.state.Refresh[family] = token{Client: cid, Family: family, Resource: resource, Scope: scope, Expires: expiry, Created: time.Now().Unix(), Digest: digest(refresh), Counter: counter, AccessID: digest(a)}
	if s.saveLocked() != nil {
		oauthError(w, 503, "server_error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, 200, map[string]any{"access_token": a, "refresh_token": refresh, "token_type": "Bearer", "expires_in": int(accessTTL.Seconds()), "scope": scope})
}
func (s *Server) revokeFamilyLocked(family string) {
	if t, ok := s.state.Refresh[family]; ok {
		delete(s.state.Access, t.AccessID)
		delete(s.state.Refresh, family)
	}
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := r.PostForm.Get("token")
	family, counter, verified := s.verifyRefresh(raw)
	t, ok := s.state.Refresh[family]
	ok = verified && ok && counter <= t.Counter
	if !ok {
		t, ok = s.state.Access[digest(raw)]
	}
	if ok {
		s.revokeFamilyLocked(t.Family)
		if s.saveLocked() != nil {
			oauthError(w, 503, "server_error")
			return
		}
	}
	w.WriteHeader(200)
}

// Protect rejects every request without a live, locally issued token and
// carries its granted scopes into the tool adapter.
// Tokens are opaque; only SHA-256 identifiers are persisted. No static bypass.
func (s *Server) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.cfg.Issuer {
			oauthError(w, 403, "access_denied")
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		ok := false
		scopes := ""
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && len(parts[1]) <= 128 {
			s.mu.Lock()
			t, found := s.state.Access[digest(parts[1])]
			ok = found && t.Expires > time.Now().Unix() && (t.Resource == "" || t.Resource == s.cfg.Issuer+"/mcp")
			if ok {
				scopes = t.Scope
				if scopes == "" {
					scopes = scopeRead
				}
			}
			s.mu.Unlock()
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+s.cfg.Issuer+`/.well-known/oauth-protected-resource/mcp"`)
			oauthError(w, 401, "invalid_token")
			return
		}
		next.ServeHTTP(w, r.WithContext(authz.WithScopes(r.Context(), scopes)))
	})
}

// ProtectMCP permits anonymous protocol discovery while keeping every actual
// tool invocation behind the OAuth token. The request body is restored
// before either path so the MCP transport still performs its own validation.
func (s *Server) ProtectMCP(next http.Handler) http.Handler {
	protected := s.Protect(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			protected.ServeHTTP(w, r)
			return
		}
		// Authenticated calls go straight to bearer verification. Only small
		// anonymous discovery envelopes are buffered before authentication, so
		// an unauthenticated 64 KiB request cannot consume the MCP budget.
		if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			protected.ServeHTTP(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			oauthError(w, 413, "invalid_request")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var request struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &request) == nil {
			switch request.Method {
			case "initialize", "ping", "tools/list", "server/discover":
				next.ServeHTTP(w, r)
				return
			}
		}
		protected.ServeHTTP(w, r)
	})
}
