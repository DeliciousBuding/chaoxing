// Package webui serves the private, owner-authenticated management console.
// The SPA and API are deliberately separate from OAuth-protected MCP clients:
// the browser owner gets a scoped cookie session, while agents keep bearer tokens.
package webui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/capture"
	"github.com/DeliciousBuding/chaoxing/internal/location"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

//go:embed all:dist
var dist embed.FS

func distFS() fs.FS {
	root, err := fs.Sub(dist, "dist")
	if err != nil {
		// The embedded pattern is compile-time fixed, so this cannot fail in a
		// correctly built binary. Keep it explicit rather than silently serving
		// the package root under /assets.
		panic(fmt.Sprintf("webui: invalid embedded bundle: %v", err))
	}
	return root
}

const (
	sessionCookie  = "__Host-chaoxing_session"
	csrfCookie     = "__Host-chaoxing_csrf"
	sessionTTL     = 8 * time.Hour
	idleTTL        = 30 * time.Minute
	maxBodyBytes   = 128 << 10
	maxSessions    = 32
	loginMinuteIP  = 5
	loginQuarterIP = 20
)

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Execute func(ctx context.Context, account, tool string, arguments map[string]any) (any, error)
type Status func() any
type CaptureList func(ctx context.Context, limit int) ([]capture.Activity, error)

type Config struct {
	Version           string
	Issuer            string
	OwnerPasswordHash string
	TrustedProxies    []string
	Accounts          []Account
	Execute           Execute
	Status            Status
	Captures          CaptureList
	Location          location.Service
	Map               *location.MapConfig
	Geocoder          location.Geocoder
}

type webSession struct {
	account  string
	csrf     string
	created  time.Time
	lastSeen time.Time
	expires  time.Time
	remoteIP string
}

type loginAttempt struct {
	at time.Time
	ip string
}

type Server struct {
	cfg            Config
	issuerHost     string
	trustedProxies []netip.Prefix
	mu             sync.Mutex
	sessions       map[string]webSession
	attempts       []loginAttempt
	now            func() time.Time
	sem            chan struct{}
	indexHTML      []byte
}

func New(cfg Config) (*Server, error) {
	if strings.TrimSpace(cfg.Version) == "" {
		cfg.Version = "dev"
	}
	if cfg.Issuer == "" {
		return nil, errors.New("webui issuer is required")
	}
	issuer, err := url.Parse(cfg.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.Path != "" || issuer.RawQuery != "" || issuer.Fragment != "" {
		return nil, errors.New("webui issuer must be an HTTPS origin")
	}
	trusted, err := parseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	if cfg.OwnerPasswordHash == "" {
		return nil, errors.New("webui owner password hash is required")
	}
	if cfg.Execute == nil {
		return nil, errors.New("webui executor is required")
	}
	seen := map[string]bool{}
	accounts := []Account{{ID: "root", Name: "默认账号"}}
	for _, a := range cfg.Accounts {
		if a.ID == "" || a.ID == "root" || seen[a.ID] {
			return nil, errors.New("webui account ids must be unique and cannot replace root")
		}
		if a.Name == "" {
			a.Name = a.ID
		}
		seen[a.ID] = true
		accounts = append(accounts, a)
	}
	cfg.Accounts = accounts
	index, err := dist.ReadFile("dist/index.html")
	if err != nil {
		return nil, errors.New("webui bundle is missing; run npm --prefix web run build")
	}
	return &Server{
		cfg: cfg, issuerHost: issuer.Host, trustedProxies: trusted,
		sessions: map[string]webSession{}, now: time.Now,
		sem: make(chan struct{}, 8), indexHTML: index,
	}, nil
}

func parseTrustedProxies(values []string) ([]netip.Prefix, error) {
	if len(values) == 0 {
		return []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("::1/128"),
		}, nil
	}
	out := make([]netip.Prefix, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || !prefix.IsValid() {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q", value)
		}
		prefix = prefix.Masked()
		if seen[prefix.String()] {
			return nil, fmt.Errorf("duplicate trusted proxy CIDR %q", value)
		}
		seen[prefix.String()] = true
		out = append(out, prefix)
	}
	return out, nil
}

// Register mounts the management UI and its JSON API. It does not alter OAuth
// or MCP routes and should be mounted at the public origin root.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /app", s.index)
	mux.HandleFunc("GET /app/{path...}", s.index)
	mux.Handle("GET /assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/assets/") == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		http.FileServerFS(distFS()).ServeHTTP(w, r)
	}))

	mux.Handle("GET /api/auth/session", s.bounded(s.sessionInfo))
	mux.Handle("POST /api/auth/login", s.bounded(s.login))
	mux.Handle("POST /api/auth/logout", s.bounded(s.logout))
	mux.Handle("GET /api/account", s.bounded(s.authenticated(s.accountInfo)))
	mux.Handle("POST /api/account/select", s.bounded(s.authenticated(s.selectAccount)))
	mux.Handle("GET /api/status", s.bounded(s.authenticated(s.status)))
	mux.Handle("GET /api/captures", s.bounded(s.authenticated(s.captures)))
	mux.Handle("GET /api/tools", s.bounded(s.authenticated(s.tools)))
	mux.Handle("POST /api/tools/call", s.bounded(s.authenticated(s.call)))
	s.registerLocationRoutes(mux)
}

func (s *Server) bounded(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostOK(w, r) {
			return
		}
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			writeError(w, 503, "TEMPORARILY_UNAVAILABLE", "management UI is busy")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Vary", "Cookie")
		next(w, r)
	})
}

func (s *Server) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !s.sameOrigin(w, r) {
			return
		}
		id, ok := s.currentSession(w, r)
		if !ok {
			writeError(w, 401, "UNAUTHENTICATED", "login required")
			return
		}
		if r.Method != http.MethodGet && !s.csrfOK(w, r, id) {
			writeError(w, 403, "CSRF_INVALID", "cross-site request rejected")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionIDKey{}, id)))
	}
}

type sessionIDKey struct{}

func (s *Server) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if origin == s.cfg.Issuer {
		return true
	}
	writeError(w, 403, "CROSS_ORIGIN", "origin is not the configured issuer")
	return false
}

func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) < 32 || len(cookie.Value) > 128 {
		return "", false
	}
	id := digest(cookie.Value)
	s.mu.Lock()
	session, ok := s.sessions[id]
	now := s.now()
	if ok {
		if session.remoteIP != s.remoteIP(r) || now.After(session.expires) || now.Sub(session.lastSeen) > idleTTL {
			delete(s.sessions, id)
			ok = false
		} else {
			session.lastSeen = now
			s.sessions[id] = session
		}
	}
	s.mu.Unlock()
	if !ok {
		// Clear the stale cookie only after all response headers can still be set.
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	}
	return id, ok
}

func (s *Server) csrfOK(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	cookie, err := r.Cookie(csrfCookie)
	header := strings.TrimSpace(r.Header.Get("X-Chaoxing-CSRF"))
	if err != nil || cookie.Value == "" || len(header) != len(cookie.Value) || subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
		return false
	}
	if sessionID != "" {
		s.mu.Lock()
		session, ok := s.sessions[sessionID]
		s.mu.Unlock()
		return ok && session.csrf == header
	}
	return true
}

func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request) {
	s.ensureCSRF(w, r)
	id, ok := s.currentSession(w, r)
	if !ok {
		writeJSON(w, 200, map[string]any{"authenticated": false, "csrf": s.csrfValue(w, r), "version": s.cfg.Version})
		return
	}
	s.mu.Lock()
	session := s.sessions[id]
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"authenticated": true, "account": session.account, "csrf": s.csrfValue(w, r), "version": s.cfg.Version, "expires_at": session.expires.Format(time.RFC3339)})
}

func (s *Server) ensureCSRF(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(csrfCookie); err == nil {
		return
	}
	token := randomToken()
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: token, Path: "/", MaxAge: int((12 * time.Hour).Seconds()), HttpOnly: false, Secure: true, SameSite: http.SameSiteStrictMode})
}

func (s *Server) csrfValue(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	// ensureCSRF just set a token, but the request's Cookie map predates it.
	for _, cookie := range w.Header().Values("Set-Cookie") {
		if strings.HasPrefix(cookie, csrfCookie+"=") {
			value := strings.SplitN(cookie, ";", 2)[0]
			return strings.TrimPrefix(value, csrfCookie+"=")
		}
	}
	return ""
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.ensureCSRF(w, r)
	if !s.sameOrigin(w, r) {
		return
	}
	if !s.csrfOK(w, r, "") {
		writeError(w, 403, "CSRF_INVALID", "cross-site request rejected")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ip := s.remoteIP(r)
	if !s.allowLogin(ip) {
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "LOGIN_RATE_LIMITED", "too many login attempts")
		return
	}
	if !access.VerifyPassword(s.cfg.OwnerPasswordHash, req.Password) {
		writeError(w, 401, "LOGIN_FAILED", "owner password is incorrect")
		return
	}
	token := randomToken()
	id := digest(token)
	now := s.now()
	s.mu.Lock()
	for oldID, session := range s.sessions {
		if now.After(session.expires) || now.Sub(session.lastSeen) > idleTTL {
			delete(s.sessions, oldID)
		}
	}
	if len(s.sessions) >= maxSessions {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "SESSION_LIMIT", "too many active management sessions")
		return
	}
	s.sessions[id] = webSession{account: "root", csrf: s.csrfValue(w, r), created: now, lastSeen: now, expires: now.Add(sessionTTL), remoteIP: s.remoteIP(r)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{"authenticated": true, "account": "root", "expires_at": now.Add(sessionTTL).Format(time.RFC3339)})
}

func (s *Server) allowLogin(ip string) bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]loginAttempt, 0, len(s.attempts)+1)
	perMinute, perQuarter := 0, 0
	for _, attempt := range s.attempts {
		if now.Sub(attempt.at) > 15*time.Minute {
			continue
		}
		kept = append(kept, attempt)
		if attempt.ip == ip {
			perQuarter++
			if now.Sub(attempt.at) <= time.Minute {
				perMinute++
			}
		}
	}
	s.attempts = append(kept, loginAttempt{at: now, ip: ip})
	return perMinute < loginMinuteIP && perQuarter < loginQuarterIP
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentSession(w, r)
	if !s.sameOrigin(w, r) || !s.csrfOK(w, r, id) {
		writeError(w, 403, "CSRF_INVALID", "cross-site request rejected")
		return
	}
	if ok {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{"authenticated": false})
}

func (s *Server) accountInfo(w http.ResponseWriter, r *http.Request) {
	id, _ := r.Context().Value(sessionIDKey{}).(string)
	s.mu.Lock()
	session := s.sessions[id]
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"current": session.account, "accounts": s.cfg.Accounts})
}

func (s *Server) selectAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Account string `json:"account"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	for _, account := range s.cfg.Accounts {
		if account.ID == req.Account {
			id, _ := r.Context().Value(sessionIDKey{}).(string)
			s.mu.Lock()
			if session, ok := s.sessions[id]; ok {
				session.account = account.ID
				session.lastSeen = s.now()
				s.sessions[id] = session
			}
			s.mu.Unlock()
			writeJSON(w, 200, map[string]any{"current": account.ID})
			return
		}
	}
	writeError(w, 400, "INVALID_ACCOUNT", "unknown management account")
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	value := map[string]any{"version": s.cfg.Version, "accounts": s.cfg.Accounts}
	if s.cfg.Status != nil {
		if v := s.cfg.Status(); v != nil {
			value["runtime"] = v
		}
	}
	writeJSON(w, 200, value)
}

func (s *Server) captures(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, 400, "INVALID_ARGUMENT", "limit must be 1..200")
			return
		}
		limit = parsed
	}
	includePage := false
	if raw := strings.TrimSpace(r.URL.Query().Get("include_page")); raw != "" {
		switch strings.ToLower(raw) {
		case "true", "1":
			includePage = true
		case "false", "0":
			includePage = false
		default:
			writeError(w, 400, "INVALID_ARGUMENT", "include_page must be true or false")
			return
		}
	}
	accountFilter := strings.TrimSpace(r.URL.Query().Get("account"))
	activityFilter := strings.TrimSpace(r.URL.Query().Get("activity_key"))
	if len(accountFilter) > 256 || len(activityFilter) > 512 {
		writeError(w, 400, "INVALID_ARGUMENT", "capture filters are too long")
		return
	}
	if s.cfg.Captures == nil {
		writeJSON(w, 200, map[string]any{"items": []map[string]any{}})
		return
	}
	rows, err := s.cfg.Captures(r.Context(), limit)
	if err != nil {
		writeError(w, 502, "CAPTURE_UNAVAILABLE", upstream.SafeFault(err).Message)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if (accountFilter != "" && row.AccountKey != accountFilter) ||
			(activityFilter != "" && row.ActivityKey != activityFilter) {
			continue
		}
		items = append(items, map[string]any{
			"account": row.AccountKey, "activity_key": row.ActivityKey, "course_id": row.CourseID,
			"class_id": row.ClassID, "activity_id": row.ActivityID, "kind": row.Kind, "state": row.State,
			"user_state": row.UserState, "sign_type": row.SignType, "source": row.Source,
			"first_seen_at": row.FirstSeenAt.Format(time.RFC3339), "last_seen_at": row.LastSeenAt.Format(time.RFC3339),
			"update_count": row.UpdateCount, "activity_json": json.RawMessage(row.ActivityJSON),
			"has_page": len(row.PageJSON) > 0, "page_error": row.PageError,
		})
		if includePage && len(row.PageJSON) > 0 {
			items[len(items)-1]["page_json"] = json.RawMessage(row.PageJSON)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) tools(w http.ResponseWriter, _ *http.Request) {
	items := []map[string]any{}
	for _, d := range operation.All() {
		fields := map[string]any{}
		names := make([]string, 0, len(d.Fields))
		for name := range d.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			f := d.Fields[name]
			fields[name] = map[string]any{"type": f.Type, "description": f.Description, "required": f.Required, "min": f.Min, "max": f.Max, "enum": f.Enum, "max_length": f.MaxLength}
		}
		items = append(items, map[string]any{"id": d.ID, "name": d.Name, "title": d.Title, "description": d.Description, "scope": operation.RequiredScope(d), "fields": fields})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) call(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if _, ok := operation.Find(req.Tool); !ok {
		writeError(w, 400, "UNKNOWN_TOOL", "operation is not allowlisted")
		return
	}
	id, _ := r.Context().Value(sessionIDKey{}).(string)
	s.mu.Lock()
	session := s.sessions[id]
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	value, err := s.cfg.Execute(ctx, session.account, req.Tool, req.Arguments)
	if err != nil {
		fault := upstream.SafeFault(err)
		status := 502
		if fault.Code == "INVALID_ARGUMENT" || fault.Code == "UNKNOWN_TOOL" {
			status = 400
		} else if fault.Code == "RATE_LIMITED" {
			status = 429
			w.Header().Set("Retry-After", "1")
		}
		writeError(w, status, fault.Code, fault.Message)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "data": value})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(w, r) || !s.sameOrigin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://api.map.baidu.com https://mapopen.baidu.com; style-src 'self' 'unsafe-inline'; img-src 'self' data: https://map.baidu.com https://*.bdimg.com https://*.bdstatic.com; font-src 'self'; connect-src 'self' https://api.map.baidu.com https://mapopen.baidu.com; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.WriteHeader(200)
	_, _ = w.Write(s.indexHTML)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.ContentLength > maxBodyBytes {
		writeError(w, 413, "BODY_TOO_LARGE", "request body exceeds 128 KiB")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(v); err != nil {
		writeError(w, 400, "INVALID_JSON", "request body is not valid JSON")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, 400, "INVALID_JSON", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Server) hostOK(w http.ResponseWriter, r *http.Request) bool {
	if strings.EqualFold(r.Host, s.issuerHost) {
		return true
	}
	writeError(w, 403, "INVALID_HOST", "request host is not the configured origin")
	return false
}

// remoteIP returns a stable client network, not the immediate reverse-proxy
// address. IPv4 sessions bind to /24 and IPv6 to /64 so mobile roaming does
// not log the owner out while cross-network cookie theft remains bounded.
func (s *Server) remoteIP(r *http.Request) string {
	remote, ok := requestIP(r.RemoteAddr)
	if !ok {
		return "unknown"
	}
	if !trustedProxy(s.trustedProxies, remote) {
		return clientNetwork(remote)
	}
	values := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(values) - 1; i >= 0; i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(values[i]))
		if err != nil || !candidate.IsValid() {
			continue
		}
		candidate = candidate.Unmap()
		if trustedProxy(s.trustedProxies, candidate) {
			continue
		}
		return clientNetwork(candidate)
	}
	return clientNetwork(remote)
}

func requestIP(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.IsValid() {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func trustedProxy(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func clientNetwork(addr netip.Addr) string {
	bits := 64
	if addr.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(addr, bits).Masked().String()
}
