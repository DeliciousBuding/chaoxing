// Package webui serves the private, owner-authenticated management console.
// The SPA and API are deliberately separate from OAuth-protected MCP clients:
// the browser owner gets a scoped cookie session, while agents keep bearer tokens.
package webui

import (
	"bytes"
	"compress/gzip"
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
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/capture"
	"github.com/DeliciousBuding/chaoxing/internal/controlplane"
	"github.com/DeliciousBuding/chaoxing/internal/location"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/signaudit"
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
	sessionCookie      = "__Host-chaoxing_session"
	csrfCookie         = "__Host-chaoxing_csrf"
	sessionTTL         = 8 * time.Hour
	idleTTL            = 30 * time.Minute
	maxBodyBytes       = 128 << 10
	maxSessions        = 32
	maxSessionsPerUser = 6
	loginMinuteIP      = 5
	loginQuarterIP     = 20
)

var (
	errSessionInvalid          = errors.New("session is not active")
	errControlPlaneUnavailable = errors.New("control plane is unavailable")
)

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// cachedAsset holds a deterministic entity tag and a precompressed
// representation for one immutable Vite asset. The original bytes stay in the
// embedded filesystem; only the compressed copy is held in heap memory.
type cachedAsset struct {
	etag       string
	compressed []byte
}

type Execute func(ctx context.Context, account, tool string, arguments map[string]any) (any, error)
type Status func() any
type CaptureList func(ctx context.Context, account, activityKey string, limit int, includePage bool) ([]capture.Activity, error)
type SignAuditList func(ctx context.Context, account string, limit int) ([]signaudit.Record, error)
type ScheduleList func(account string) []ScheduleView

// ScheduleView is the authenticated, read-only projection used by the
// location-binding editor. It never contains coordinates or credentials.
type ScheduleView struct {
	CourseID string   `json:"course_id"`
	ClassID  string   `json:"class_id"`
	Key      string   `json:"key"`
	Days     []string `json:"days"`
	Start    string   `json:"start"`
	End      string   `json:"end"`
	Weeks    string   `json:"weeks,omitempty"`
	Room     string   `json:"room,omitempty"`
}

// AccountConfigView is a sanitized, read-only projection of one upstream
// account's operational policy. Secrets stay in the deployment configuration.
type AccountConfigView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	WatchEnabled    bool   `json:"watch_enabled"`
	AutoSign        bool   `json:"auto_sign"`
	AutoAnswer      bool   `json:"auto_answer"`
	NotifyOnly      bool   `json:"notify_only"`
	LocationEnabled bool   `json:"location_enabled"`
	Classes         int    `json:"classes"`
}

type Capabilities struct {
	ManageUsers    bool     `json:"manage_users"`
	ViewAudit      bool     `json:"view_audit"`
	ManageAccounts bool     `json:"manage_accounts"`
	SensitiveRead  bool     `json:"sensitive_read"`
	ToolScopes     []string `json:"tool_scopes"`
}

type Config struct {
	Version           string
	Issuer            string
	OwnerPasswordHash string
	TrustedProxies    []string
	Accounts          []Account
	Control           *controlplane.Store
	AccountConfigs    func() []AccountConfigView
	Execute           Execute
	Status            Status
	Captures          CaptureList
	SignAudits        SignAuditList
	Schedules         ScheduleList
	Location          location.Service
	Map               *location.MapConfig
	Geocoder          location.Geocoder
}

type webSession struct {
	account     string
	userID      int64
	username    string
	displayName string
	role        string
	authVersion int64
	accounts    []Account
	csrf        string
	created     time.Time
	lastSeen    time.Time
	expires     time.Time
	remoteIP    string
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
	heavy          chan struct{}
	loginSem       chan struct{}
	indexHTML      []byte
	assets         map[string]cachedAsset
	auditFailures  atomic.Uint64
	auditStreak    atomic.Uint64
	auditLastFail  atomic.Int64
}

var allToolScopes = []string{"chaoxing.read", "chaoxing.sign", "chaoxing.work", "chaoxing.answer", "chaoxing.work.submit", "chaoxing.upload", "chaoxing.face"}

func capabilitiesForRole(role string) Capabilities {
	switch role {
	case controlplane.RoleOwner:
		return Capabilities{ManageUsers: true, ViewAudit: true, ManageAccounts: true, SensitiveRead: true, ToolScopes: append([]string(nil), allToolScopes...)}
	case controlplane.RoleAdmin:
		return Capabilities{ManageUsers: true, ViewAudit: true, ManageAccounts: true, SensitiveRead: true, ToolScopes: append([]string(nil), allToolScopes...)}
	case controlplane.RoleOperator:
		return Capabilities{
			SensitiveRead: true,
			ToolScopes:    []string{"chaoxing.read", "chaoxing.sign", "chaoxing.work", "chaoxing.answer", "chaoxing.upload"},
		}
	default:
		return Capabilities{ToolScopes: []string{"chaoxing.read"}}
	}
}

func roleAllowsScope(role, scope string) bool {
	for _, allowed := range capabilitiesForRole(role).ToolScopes {
		if scope == allowed {
			return true
		}
	}
	return false
}

func (s *Server) accountsForRole(role string, grants []string) []Account {
	if role == controlplane.RoleOwner || role == controlplane.RoleAdmin {
		return append([]Account(nil), s.cfg.Accounts...)
	}
	allowed := map[string]bool{}
	for _, key := range grants {
		allowed[key] = true
	}
	out := make([]Account, 0, len(grants))
	for _, account := range s.cfg.Accounts {
		if allowed[account.ID] {
			out = append(out, account)
		}
	}
	return out
}

func firstAccountID(accounts []Account) string {
	if len(accounts) == 0 {
		return ""
	}
	return accounts[0].ID
}

func (s *Server) sessionRole(session webSession) string {
	role := strings.TrimSpace(session.role)
	if role == "" {
		return "anonymous"
	}
	if !controlplane.ValidRole(role) {
		return ""
	}
	return role
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
	assets, err := buildAssetCache()
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg: cfg, issuerHost: issuer.Host, trustedProxies: trusted,
		sessions: map[string]webSession{}, now: time.Now,
		sem: make(chan struct{}, 8), heavy: make(chan struct{}, 2), loginSem: make(chan struct{}, 1),
		indexHTML: index, assets: assets,
	}, nil
}

func buildAssetCache() (map[string]cachedAsset, error) {
	root := distFS()
	out := map[string]cachedAsset{}
	err := fs.WalkDir(root, "assets", func(current string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(root, current)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(content); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		name := strings.TrimPrefix(current, "assets/")
		out[name] = cachedAsset{etag: `"` + hex.EncodeToString(sum[:]) + `"`, compressed: compressed.Bytes()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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
	mux.HandleFunc("GET /map", s.mapFrame)
	mux.Handle("GET /assets/", http.HandlerFunc(s.serveAsset))

	mux.Handle("GET /api/auth/session", s.bounded(s.sessionInfo))
	mux.Handle("POST /api/auth/login", s.bounded(s.login))
	mux.Handle("POST /api/auth/logout", s.bounded(s.logout))
	mux.Handle("GET /api/account", s.bounded(s.authenticated(s.accountInfo)))
	mux.Handle("POST /api/account/select", s.bounded(s.authenticated(s.selectAccount)))
	mux.Handle("GET /api/status", s.bounded(s.authenticated(s.status)))
	mux.Handle("GET /api/captures", s.heavyBounded(s.authenticated(s.captures)))
	mux.Handle("GET /api/sign-audits", s.heavyBounded(s.authenticated(s.signAudits)))
	mux.Handle("GET /api/tools", s.bounded(s.authenticated(s.tools)))
	mux.Handle("POST /api/tools/call", s.heavyBounded(s.authenticated(s.call)))
	mux.Handle("GET /api/admin/users", s.bounded(s.authenticated(s.adminUsers)))
	mux.Handle("POST /api/admin/users", s.bounded(s.authenticated(s.adminCreateUser)))
	mux.Handle("PATCH /api/admin/users/{id}", s.bounded(s.authenticated(s.adminUpdateUser)))
	mux.Handle("GET /api/admin/accounts", s.bounded(s.authenticated(s.adminAccounts)))
	mux.Handle("GET /api/admin/audit", s.bounded(s.authenticated(s.adminAudit)))
	s.registerLocationRoutes(mux)
	s.registerScheduleRoutes(mux)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	asset, ok := s.assets[name]
	if name == "" || !ok || !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", asset.etag)
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if etagMatch(r.Header.Get("If-None-Match"), asset.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if acceptsGzip(r) && len(asset.compressed) > 0 {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(asset.compressed)))
		w.Header().Set("Content-Type", assetContentType(name))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(asset.compressed)
		return
	}
	root := distFS()
	file, err := root.Open("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// ServeContent handles HEAD, range requests and content-type detection for
	// clients that cannot use the precompressed representation.
	http.ServeContent(w, r, name, time.Time{}, seeker)
}

func etagMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, value := range strings.Split(header, ",") {
		if strings.TrimSpace(value) == etag {
			return true
		}
	}
	return false
}

func acceptsGzip(r *http.Request) bool {
	for _, value := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(value), "gzip") {
			return true
		}
	}
	return false
}

func assetContentType(name string) string {
	switch path.Ext(name) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

func (s *Server) bounded(next http.HandlerFunc) http.Handler {
	return s.limited(s.sem, next)
}

// heavyBounded isolates capture queries and upstream tool calls from cheap
// session/status requests. The weak router must still answer a login or a
// dashboard refresh while one large capture or a slow upstream call is live.
func (s *Server) heavyBounded(next http.HandlerFunc) http.Handler {
	return s.limited(s.heavy, next)
}

func (s *Server) limited(limit chan struct{}, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostOK(w, r) {
			return
		}
		select {
		case limit <- struct{}{}:
			defer func() { <-limit }()
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
		id, session, err := s.currentSessionRecord(w, r)
		if err != nil {
			if errors.Is(err, errControlPlaneUnavailable) {
				writeError(w, 503, "CONTROL_PLANE_UNAVAILABLE", "control plane is temporarily unavailable")
				return
			}
			writeError(w, 401, "UNAUTHENTICATED", "login required")
			return
		}
		if r.Method != http.MethodGet && !s.csrfOK(w, r, id) {
			writeError(w, 403, "CSRF_INVALID", "cross-site request rejected")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, authorizedSession{id: id, session: session})))
	}
}

type sessionContextKey struct{}

type authorizedSession struct {
	id      string
	session webSession
}

// requestSession returns the live server-side record, not a zero value for a
// deleted session. A missing session is always a hard authentication failure.
func (s *Server) requestSession(r *http.Request) (webSession, bool) {
	auth, ok := r.Context().Value(sessionContextKey{}).(authorizedSession)
	if !ok || auth.id == "" {
		return webSession{}, false
	}
	s.mu.Lock()
	session, exists := s.sessions[auth.id]
	s.mu.Unlock()
	if !exists {
		return webSession{}, false
	}
	if s.cfg.Control != nil && (session.userID <= 0 || session.authVersion <= 0) {
		return webSession{}, false
	}
	if auth.session.userID != 0 && session.userID != auth.session.userID {
		return webSession{}, false
	}
	return session, true
}

func (s *Server) requestSessionID(r *http.Request) (string, bool) {
	auth, ok := r.Context().Value(sessionContextKey{}).(authorizedSession)
	if !ok || auth.id == "" {
		return "", false
	}
	s.mu.Lock()
	_, exists := s.sessions[auth.id]
	s.mu.Unlock()
	return auth.id, exists
}

func (s *Server) sessionForAudit(r *http.Request) webSession {
	session, _ := s.requestSession(r)
	return session
}

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
	id, _, err := s.currentSessionRecord(w, r)
	return id, err == nil
}

func (s *Server) currentSessionRecord(w http.ResponseWriter, r *http.Request) (string, webSession, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) < 32 || len(cookie.Value) > 128 {
		return "", webSession{}, errSessionInvalid
	}
	id := digest(cookie.Value)
	s.mu.Lock()
	session, ok := s.sessions[id]
	now := s.now()
	if ok {
		if session.remoteIP != s.remoteIP(r) || now.After(session.expires) || now.Sub(session.lastSeen) > idleTTL {
			delete(s.sessions, id)
			ok = false
		}
	}
	s.mu.Unlock()
	if ok && s.cfg.Control != nil {
		if session.userID <= 0 || session.authVersion <= 0 {
			s.mu.Lock()
			delete(s.sessions, id)
			s.mu.Unlock()
			ok = false
		} else {
			valid, validationErr := s.cfg.Control.SessionValid(r.Context(), session.userID, session.authVersion)
			if validationErr != nil {
				return "", webSession{}, fmt.Errorf("%w: %v", errControlPlaneUnavailable, validationErr)
			}
			if !valid {
				s.mu.Lock()
				delete(s.sessions, id)
				s.mu.Unlock()
				ok = false
			}
		}
	}
	if ok {
		s.mu.Lock()
		if current, exists := s.sessions[id]; exists {
			current.lastSeen = now
			s.sessions[id] = current
		} else {
			ok = false
		}
		s.mu.Unlock()
	}
	if !ok {
		// Clear the stale cookie only after all response headers can still be set.
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
		return "", webSession{}, errSessionInvalid
	}
	return id, session, nil
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
	_, session, err := s.currentSessionRecord(w, r)
	if err != nil {
		if errors.Is(err, errControlPlaneUnavailable) {
			writeError(w, 503, "CONTROL_PLANE_UNAVAILABLE", "control plane is temporarily unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"authenticated": false, "csrf": s.csrfValue(w, r), "version": s.cfg.Version})
		return
	}
	payload := s.sessionPayload(session)
	payload["csrf"] = s.csrfValue(w, r)
	payload["version"] = s.cfg.Version
	payload["expires_at"] = session.expires.Format(time.RFC3339)
	writeJSON(w, 200, payload)
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

func (s *Server) recordAudit(r *http.Request, session webSession, category, action, target, account string, success bool, details map[string]any) {
	if s.cfg.Control == nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	role := strings.TrimSpace(session.role)
	if role == "" {
		role = "anonymous"
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		encoded = []byte("{}")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	err = s.cfg.Control.RecordAudit(ctx, controlplane.AuditEvent{
		ActorID: session.userID, ActorUsername: session.username, ActorRole: role,
		AccountKey: account, Category: category, Action: action, Target: target, Success: success,
		IP: s.remoteIP(r), UserAgent: r.UserAgent(), RequestID: randomToken(), Details: encoded,
	})
	if err != nil {
		total := s.auditFailures.Add(1)
		streak := s.auditStreak.Add(1)
		s.auditLastFail.Store(time.Now().Unix())
		if streak == 1 || streak%100 == 0 {
			log.Printf("control-plane audit write failed: total=%d consecutive=%d: %v", total, streak, err)
		}
		return
	}
	s.auditStreak.Store(0)
}

// ControlHealth exposes bounded control-plane write diagnostics for the
// authenticated status page and readiness checks. It contains no audit data.
func (s *Server) ControlHealth() map[string]any {
	last := ""
	if unix := s.auditLastFail.Load(); unix > 0 {
		last = time.Unix(unix, 0).UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"audit_write_failures":             s.auditFailures.Load(),
		"audit_consecutive_write_failures": s.auditStreak.Load(),
		"audit_last_write_failure_at":      last,
	}
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
		Username string `json:"username"`
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
	select {
	case s.loginSem <- struct{}{}:
		defer func() { <-s.loginSem }()
	default:
		w.Header().Set("Retry-After", "2")
		writeError(w, 429, "LOGIN_BUSY", "another login verification is in progress")
		return
	}
	var session webSession
	if s.cfg.Control != nil {
		user, authErr := s.cfg.Control.Authenticate(r.Context(), req.Username, req.Password)
		if authErr != nil {
			if !errors.Is(authErr, controlplane.ErrInvalidCredentials) {
				log.Printf("control-plane authentication failed: %v", authErr)
				writeError(w, 503, "CONTROL_PLANE_UNAVAILABLE", "control plane is temporarily unavailable")
				return
			}
			s.recordAudit(r, webSession{username: strings.TrimSpace(req.Username), role: "anonymous"}, "auth", "auth.login.failed", "", "", false, map[string]any{"reason": "invalid_credentials"})
			writeError(w, 401, "LOGIN_FAILED", "username or password is incorrect")
			return
		}
		accounts := s.accountsForRole(user.Role, user.Accounts)
		session = webSession{
			account: firstAccountID(accounts), userID: user.ID, username: user.Username,
			displayName: user.DisplayName, role: user.Role, authVersion: user.AuthVersion, accounts: accounts,
		}
	} else {
		if !access.VerifyPassword(s.cfg.OwnerPasswordHash, req.Password) {
			writeError(w, 401, "LOGIN_FAILED", "owner password is incorrect")
			return
		}
		accounts := s.accountsForRole(controlplane.RoleOwner, nil)
		session = webSession{account: firstAccountID(accounts), username: "owner", displayName: "Owner", role: controlplane.RoleOwner, accounts: accounts}
	}
	token := randomToken()
	id := digest(token)
	now := s.now()
	s.mu.Lock()
	session.csrf = s.csrfValue(w, r)
	session.created, session.lastSeen = now, now
	session.expires, session.remoteIP = now.Add(sessionTTL), s.remoteIP(r)
	s.pruneSessionsLocked(now)
	if !s.reserveSessionSlotLocked(session.userID, s.sessionRole(session), now) {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "SESSION_LIMIT", "too many active management sessions for this identity")
		return
	}
	s.sessions[id] = session
	s.mu.Unlock()
	s.recordAudit(r, session, "auth", "auth.login", "", session.account, true, nil)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	payload := s.sessionPayload(session)
	payload["expires_at"] = now.Add(sessionTTL).Format(time.RFC3339)
	payload["csrf"] = session.csrf
	payload["version"] = s.cfg.Version
	writeJSON(w, 200, payload)
}

func (s *Server) pruneSessionsLocked(now time.Time) {
	for id, session := range s.sessions {
		if now.After(session.expires) || now.Sub(session.lastSeen) > idleTTL {
			delete(s.sessions, id)
		}
	}
}

func sessionRoleRank(role string) int {
	switch role {
	case controlplane.RoleViewer:
		return 0
	case controlplane.RoleOperator:
		return 1
	case controlplane.RoleAdmin:
		return 2
	case controlplane.RoleOwner:
		return 3
	default:
		return -1
	}
}

// reserveSessionSlotLocked protects the owner from a viewer/operator session
// exhaustion attack. Owners and admins may replace their own oldest session;
// lower roles are capped per identity.
func (s *Server) reserveSessionSlotLocked(userID int64, role string, now time.Time) bool {
	if role == "" {
		return false
	}
	count, oldestID, oldestSeen := 0, "", now
	if userID != 0 {
		for id, session := range s.sessions {
			if session.userID != userID {
				continue
			}
			count++
			if oldestID == "" || session.lastSeen.Before(oldestSeen) {
				oldestID, oldestSeen = id, session.lastSeen
			}
		}
	}
	if count >= maxSessionsPerUser {
		if role != controlplane.RoleOwner && role != controlplane.RoleAdmin {
			return false
		}
		if oldestID != "" {
			delete(s.sessions, oldestID)
		}
	}
	if len(s.sessions) < maxSessions {
		return true
	}
	targetRank := sessionRoleRank(role)
	evictID := ""
	evictRank := targetRank + 1
	evictSeen := now
	for id, session := range s.sessions {
		rank := sessionRoleRank(s.sessionRole(session))
		if rank < 0 || rank > targetRank {
			continue
		}
		if rank < evictRank || (rank == evictRank && session.lastSeen.Before(evictSeen)) {
			evictID, evictRank, evictSeen = id, rank, session.lastSeen
		}
	}
	if evictID == "" {
		return false
	}
	delete(s.sessions, evictID)
	return true
}

func (s *Server) sessionPayload(session webSession) map[string]any {
	role := s.sessionRole(session)
	return map[string]any{
		"authenticated": true,
		"account":       session.account,
		"user": map[string]any{
			"id": session.userID, "username": session.username,
			"display_name": session.displayName, "role": role,
		},
		"capabilities": capabilitiesForRole(role),
	}
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
	kept = append(kept, loginAttempt{at: now, ip: ip})
	if len(kept) > 1024 {
		kept = kept[len(kept)-1024:]
	}
	s.attempts = kept
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
		session := s.sessions[id]
		delete(s.sessions, id)
		s.mu.Unlock()
		s.recordAudit(r, session, "auth", "auth.logout", "", session.account, true, nil)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{"authenticated": false})
}

func (s *Server) accountInfo(w http.ResponseWriter, r *http.Request) {
	id, session, ok := s.authorizedRequestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	accounts, ok := s.accountsForSession(session)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	current := session.account
	if !containsAccount(accounts, current) {
		current = firstAccountID(accounts)
		s.mu.Lock()
		if stored, exists := s.sessions[id]; exists {
			stored.account = current
			s.sessions[id] = stored
		}
		s.mu.Unlock()
	}
	writeJSON(w, 200, map[string]any{"current": current, "accounts": accounts})
}

func (s *Server) selectAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Account string `json:"account"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	id, session, ok := s.authorizedRequestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	accounts, ok := s.accountsForSession(session)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	for _, account := range accounts {
		if account.ID == req.Account {
			s.mu.Lock()
			if stored, exists := s.sessions[id]; exists {
				stored.account = account.ID
				stored.lastSeen = s.now()
				s.sessions[id] = stored
				session = stored
			}
			s.mu.Unlock()
			s.recordAudit(r, session, "account", "account.select", account.ID, account.ID, true, nil)
			writeJSON(w, 200, map[string]any{"current": account.ID})
			return
		}
	}
	s.recordAudit(r, session, "security", "account.select.denied", req.Account, session.account, false, map[string]any{"reason": "account_scope_denied"})
	writeError(w, 403, "ACCOUNT_SCOPE_DENIED", "account is not assigned to this user")
}

func (s *Server) authorizedRequestSession(r *http.Request) (string, webSession, bool) {
	id, ok := s.requestSessionID(r)
	if !ok {
		return "", webSession{}, false
	}
	session, ok := s.requestSession(r)
	if !ok {
		return "", webSession{}, false
	}
	return id, session, true
}

func containsAccount(accounts []Account, id string) bool {
	for _, account := range accounts {
		if account.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) accountsForSession(session webSession) ([]Account, bool) {
	if s.cfg.Control == nil {
		if s.sessionRole(session) != controlplane.RoleOwner {
			return nil, false
		}
		return append([]Account(nil), s.cfg.Accounts...), true
	}
	if session.userID <= 0 || session.authVersion <= 0 {
		return nil, false
	}
	return append([]Account{}, session.accounts...), true
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	role := s.sessionRole(session)
	accounts, allowed := s.accountsForSession(session)
	if !allowed {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	value := map[string]any{"version": s.cfg.Version, "accounts": accounts}
	value["control"] = s.ControlHealth()
	if role != controlplane.RoleViewer && s.cfg.Status != nil {
		if raw := s.cfg.Status(); raw != nil {
			if runtime, ok := raw.(map[string]any); ok {
				allowed := map[string]bool{}
				for _, account := range accounts {
					allowed[account.ID] = true
				}
				if watchers, ok := runtime["watchers"].(map[string]any); ok {
					filtered := map[string]any{}
					for key, item := range watchers {
						if allowed[key] {
							filtered[key] = item
						}
					}
					runtime["watchers"] = filtered
				}
				if tenants, ok := runtime["tenants"].([]string); ok {
					filtered := make([]string, 0, len(tenants))
					for _, tenant := range tenants {
						if allowed[tenant] {
							filtered = append(filtered, tenant)
						}
					}
					runtime["tenants"] = filtered
				}
				value["runtime"] = runtime
			}
		}
	}
	writeJSON(w, 200, value)
}

func (s *Server) captures(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	if !capabilitiesForRole(s.sessionRole(session)).SensitiveRead {
		s.recordAudit(r, session, "security", "captures.denied", "captures", session.account, false, map[string]any{"reason": "permission_denied"})
		writeError(w, 403, "PERMISSION_DENIED", "capture access is not granted to this role")
		return
	}
	account, ok := s.sessionAccount(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, 400, "INVALID_ARGUMENT", "limit must be 1..100")
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
	if accountFilter != "" && accountFilter != account {
		s.recordAudit(r, session, "security", "captures.denied", accountFilter, session.account, false, map[string]any{"reason": "account_scope_denied"})
		writeError(w, 403, "ACCOUNT_SCOPE_DENIED", "captures are scoped to the current account")
		return
	}
	if includePage && activityFilter == "" {
		writeError(w, 400, "INVALID_ARGUMENT", "include_page requires an exact activity_key")
		return
	}
	if s.cfg.Captures == nil {
		writeJSON(w, 200, map[string]any{"items": []map[string]any{}})
		return
	}
	rows, err := s.cfg.Captures(r.Context(), account, activityFilter, limit, includePage)
	if err != nil {
		writeError(w, 502, "CAPTURE_UNAVAILABLE", upstream.SafeFault(err).Message)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"account": row.AccountKey, "activity_key": row.ActivityKey, "course_id": row.CourseID,
			"class_id": row.ClassID, "activity_id": row.ActivityID, "kind": row.Kind, "state": row.State,
			"user_state": row.UserState, "sign_type": row.SignType, "source": row.Source,
			"first_seen_at": row.FirstSeenAt.Format(time.RFC3339), "last_seen_at": row.LastSeenAt.Format(time.RFC3339),
			"update_count": row.UpdateCount, "activity_json": json.RawMessage(row.ActivityJSON),
			"has_page": row.HasPage, "page_error": row.PageError,
		})
		if includePage && len(row.PageJSON) > 0 {
			items[len(items)-1]["page_json"] = json.RawMessage(row.PageJSON)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) signAudits(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	if !capabilitiesForRole(s.sessionRole(session)).SensitiveRead {
		s.recordAudit(r, session, "security", "sign_audits.denied", "sign-audits", session.account, false, map[string]any{"reason": "permission_denied"})
		writeError(w, 403, "PERMISSION_DENIED", "sign audit access is not granted to this role")
		return
	}
	account, ok := s.sessionAccount(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, 400, "INVALID_ARGUMENT", "limit must be 1..100")
			return
		}
		limit = parsed
	}
	if s.cfg.SignAudits == nil {
		writeJSON(w, 200, map[string]any{"items": []signaudit.Record{}})
		return
	}
	rows, err := s.cfg.SignAudits(r.Context(), account, limit)
	if err != nil {
		writeError(w, 502, "SIGN_AUDIT_UNAVAILABLE", upstream.SafeFault(err).Message)
		return
	}
	writeJSON(w, 200, map[string]any{"items": rows})
}

func (s *Server) tools(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	role := s.sessionRole(session)
	items := []map[string]any{}
	for _, d := range operation.All() {
		allowed := true
		for _, scope := range operation.RequiredScopes(d) {
			if !roleAllowsScope(role, scope) {
				allowed = false
				break
			}
		}
		if !allowed {
			continue
		}
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
	definition, ok := operation.Find(req.Tool)
	if !ok {
		writeError(w, 400, "UNKNOWN_TOOL", "operation is not allowlisted")
		return
	}
	session, ok := s.requestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	role := s.sessionRole(session)
	for _, scope := range operation.RequiredScopes(definition) {
		if !roleAllowsScope(role, scope) {
			s.recordAudit(r, session, "security", "tool.denied", definition.Name, session.account, false, map[string]any{"reason": "permission_denied", "scope": scope})
			writeError(w, 403, "PERMISSION_DENIED", "this role cannot call the requested tool")
			return
		}
	}
	accounts, allowed := s.accountsForSession(session)
	if !allowed || !containsAccount(accounts, session.account) {
		s.recordAudit(r, session, "security", "tool.denied", definition.Name, session.account, false, map[string]any{"reason": "account_scope_denied"})
		writeError(w, 403, "ACCOUNT_SCOPE_DENIED", "the selected account is not assigned to this user")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	started := time.Now()
	value, err := s.cfg.Execute(ctx, session.account, req.Tool, req.Arguments)
	if err != nil {
		fault := upstream.SafeFault(err)
		s.recordAudit(r, session, "tool", "tool.call", definition.Name, session.account, false, map[string]any{
			"tool": definition.Name, "error": fault.Code, "duration_ms": time.Since(started).Milliseconds(),
		})
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
	s.recordAudit(r, session, "tool", "tool.call", definition.Name, session.account, true, map[string]any{
		"tool": definition.Name, "duration_ms": time.Since(started).Milliseconds(),
	})
	writeJSON(w, 200, map[string]any{"ok": true, "data": value})
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request, capability string) (webSession, bool) {
	if s.cfg.Control == nil {
		writeError(w, 503, "CONTROL_PLANE_UNAVAILABLE", "control plane is not configured")
		return webSession{}, false
	}
	session, ok := s.requestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return webSession{}, false
	}
	if _, allowed := s.accountsForSession(session); !allowed {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return webSession{}, false
	}
	caps := capabilitiesForRole(s.sessionRole(session))
	allowed := false
	switch capability {
	case "users":
		allowed = caps.ManageUsers
	case "accounts":
		allowed = caps.ManageAccounts
	case "audit":
		allowed = caps.ViewAudit
	}
	if !allowed {
		s.recordAudit(r, session, "security", "admin.denied", capability, session.account, false, nil)
		writeError(w, 403, "PERMISSION_DENIED", "administrator permission is required")
		return webSession{}, false
	}
	return session, true
}

func (s *Server) requireCapability(w http.ResponseWriter, r *http.Request, capability string) bool {
	session, ok := s.requestSession(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return false
	}
	if _, allowed := s.accountsForSession(session); !allowed {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return false
	}
	caps := capabilitiesForRole(s.sessionRole(session))
	allowed := false
	switch capability {
	case "sensitive":
		allowed = caps.SensitiveRead
	case "accounts":
		allowed = caps.ManageAccounts
	}
	if !allowed {
		s.recordAudit(r, session, "security", "permission.denied", capability, session.account, false, nil)
		writeError(w, 403, "PERMISSION_DENIED", "permission is required")
	}
	return allowed
}

func (s *Server) knownAccount(key string) bool {
	for _, account := range s.cfg.Accounts {
		if account.ID == key {
			return true
		}
	}
	return false
}

func (s *Server) validateAccountKeys(keys []string) ([]string, bool) {
	out := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if !s.knownAccount(key) {
			return nil, false
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, true
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "users"); !ok {
		return
	}
	users, err := s.cfg.Control.ListUsers(r.Context())
	if err != nil {
		writeError(w, 500, "CONTROL_PLANE_ERROR", "unable to read platform members")
		return
	}
	writeJSON(w, 200, map[string]any{"items": users})
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAdmin(w, r, "users")
	if !ok {
		return
	}
	var req struct {
		Username    string   `json:"username"`
		DisplayName string   `json:"display_name"`
		Password    string   `json:"password"`
		Role        string   `json:"role"`
		Accounts    []string `json:"accounts"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Role == controlplane.RoleOwner || (req.Role == controlplane.RoleAdmin && s.sessionRole(session) != controlplane.RoleOwner) {
		s.recordAudit(r, session, "security", "member.create.denied", "", "", false, map[string]any{"reason": "role_escalation", "role": req.Role})
		writeError(w, 403, "PERMISSION_DENIED", "only the owner can create administrators")
		return
	}
	if req.Role == controlplane.RoleAdmin || req.Role == controlplane.RoleOwner {
		req.Accounts = nil
	}
	accounts, valid := s.validateAccountKeys(req.Accounts)
	if !valid {
		writeError(w, 400, "INVALID_ACCOUNT", "one or more account keys are unknown")
		return
	}
	user, err := s.cfg.Control.CreateUser(r.Context(), controlplane.CreateUserInput{
		Username: req.Username, DisplayName: req.DisplayName, Password: req.Password,
		Role: req.Role, Accounts: accounts,
	})
	if err != nil {
		if errors.Is(err, controlplane.ErrInvalidUser) {
			writeError(w, 400, "INVALID_MEMBER", "username, password, role or account assignment is invalid")
			return
		}
		writeError(w, 500, "CONTROL_PLANE_ERROR", "unable to create platform member")
		return
	}
	s.recordAudit(r, session, "member", "member.create", user.Username, "", true, map[string]any{
		"username": user.Username, "role": user.Role, "accounts": user.Accounts,
	})
	writeJSON(w, 201, user)
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAdmin(w, r, "users")
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "INVALID_ID", "member id is invalid")
		return
	}
	var req struct {
		DisplayName *string   `json:"display_name"`
		Password    *string   `json:"password"`
		Role        *string   `json:"role"`
		Status      *string   `json:"status"`
		Accounts    *[]string `json:"accounts"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	target, err := s.cfg.Control.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, 404, "MEMBER_NOT_FOUND", "platform member was not found")
		return
	}
	actorRole := s.sessionRole(session)
	if target.Role == controlplane.RoleOwner && actorRole != controlplane.RoleOwner {
		s.recordAudit(r, session, "security", "member.update.denied", target.Username, "", false, map[string]any{"reason": "owner_protected"})
		writeError(w, 403, "PERMISSION_DENIED", "only the owner can modify the owner account")
		return
	}
	if target.Role == controlplane.RoleOwner && req.Password != nil {
		writeError(w, 400, "OWNER_PASSWORD_MANAGED", "the owner password is managed by the deployment configuration")
		return
	}
	if target.Role == controlplane.RoleAdmin && actorRole != controlplane.RoleOwner && target.ID != session.userID {
		s.recordAudit(r, session, "security", "member.update.denied", target.Username, "", false, map[string]any{"reason": "admin_protected"})
		writeError(w, 403, "PERMISSION_DENIED", "only the owner can modify an administrator")
		return
	}
	if id == session.userID && (req.Role != nil || req.Status != nil) {
		s.recordAudit(r, session, "security", "member.update.denied", target.Username, "", false, map[string]any{"reason": "self_role_change"})
		writeError(w, 403, "PERMISSION_DENIED", "you cannot change your own role or status")
		return
	}
	if req.Role != nil {
		if *req.Role == controlplane.RoleOwner || (*req.Role == controlplane.RoleAdmin && actorRole != controlplane.RoleOwner) {
			s.recordAudit(r, session, "security", "member.update.denied", target.Username, "", false, map[string]any{"reason": "role_escalation", "role": *req.Role})
			writeError(w, 403, "PERMISSION_DENIED", "only the owner can grant administrator access")
			return
		}
	}
	if req.Status != nil && !controlplane.ValidStatus(*req.Status) {
		writeError(w, 400, "INVALID_STATUS", "status must be active or disabled")
		return
	}
	if req.Role != nil && !controlplane.ValidRole(*req.Role) {
		writeError(w, 400, "INVALID_ROLE", "role is invalid")
		return
	}
	if req.Accounts != nil {
		accounts, valid := s.validateAccountKeys(*req.Accounts)
		if !valid {
			writeError(w, 400, "INVALID_ACCOUNT", "one or more account keys are unknown")
			return
		}
		req.Accounts = &accounts
	}
	user, err := s.cfg.Control.UpdateUser(r.Context(), id, controlplane.UpdateUserInput{
		DisplayName: req.DisplayName, Password: req.Password, Role: req.Role, Status: req.Status, Accounts: req.Accounts,
		ActorID: session.userID,
	})
	if err != nil {
		if errors.Is(err, controlplane.ErrPermission) {
			s.recordAudit(r, session, "security", "member.update.denied", target.Username, "", false, map[string]any{"reason": "stale_authorization"})
			writeError(w, 403, "PERMISSION_DENIED", "the member or actor changed during the update; retry from fresh state")
			return
		}
		if errors.Is(err, controlplane.ErrInvalidUser) {
			writeError(w, 400, "INVALID_MEMBER", "member update is invalid")
			return
		}
		writeError(w, 500, "CONTROL_PLANE_ERROR", "unable to update platform member")
		return
	}
	changed := []string{}
	if req.DisplayName != nil {
		changed = append(changed, "display_name")
	}
	if req.Password != nil {
		changed = append(changed, "password")
	}
	if req.Role != nil {
		changed = append(changed, "role")
	}
	if req.Status != nil {
		changed = append(changed, "status")
	}
	if req.Accounts != nil {
		changed = append(changed, "accounts")
	}
	s.recordAudit(r, session, "member", "member.update", user.Username, "", true, map[string]any{
		"username": user.Username, "changed": changed, "role": user.Role, "status": user.Status, "accounts": user.Accounts,
	})
	writeJSON(w, 200, user)
}

func (s *Server) adminAccounts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "accounts"); !ok {
		return
	}
	byID := map[string]AccountConfigView{}
	if s.cfg.AccountConfigs != nil {
		for _, item := range s.cfg.AccountConfigs() {
			byID[item.ID] = item
		}
	}
	users, err := s.cfg.Control.ListUsers(r.Context())
	if err != nil {
		writeError(w, 500, "CONTROL_PLANE_ERROR", "unable to read account grants")
		return
	}
	members := map[string]int{}
	for _, user := range users {
		if user.Role == controlplane.RoleOwner || user.Role == controlplane.RoleAdmin {
			continue
		}
		for _, key := range user.Accounts {
			members[key]++
		}
	}
	items := make([]map[string]any, 0, len(s.cfg.Accounts))
	for _, account := range s.cfg.Accounts {
		view, exists := byID[account.ID]
		if !exists {
			view = AccountConfigView{ID: account.ID, Name: account.Name}
		}
		items = append(items, map[string]any{
			"id": view.ID, "name": view.Name, "watch_enabled": view.WatchEnabled,
			"auto_sign": view.AutoSign, "auto_answer": view.AutoAnswer, "notify_only": view.NotifyOnly,
			"location_enabled": view.LocationEnabled, "classes": view.Classes, "explicit_members": members[account.ID],
		})
	}
	writeJSON(w, 200, map[string]any{"items": items, "issuer": s.cfg.Issuer, "version": s.cfg.Version})
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "audit"); !ok {
		return
	}
	limit, offset := 50, 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 && parsed <= 100000 {
			offset = parsed
		}
	}
	filter := controlplane.AuditFilter{
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		Account:  strings.TrimSpace(r.URL.Query().Get("account")),
		Actor:    strings.TrimSpace(r.URL.Query().Get("actor")),
		Limit:    limit, Offset: offset,
	}
	if len(filter.Category) > 24 || len(filter.Account) > 32 || len(filter.Actor) > 32 {
		writeError(w, 400, "INVALID_FILTER", "audit filter is too long")
		return
	}
	items, total, err := s.cfg.Control.ListAudit(r.Context(), filter)
	if err != nil {
		writeError(w, 500, "CONTROL_PLANE_ERROR", "unable to read audit log")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(w, r) || !s.sameOrigin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
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
	payload, err := json.Marshal(value)
	if err != nil {
		status = 500
		payload = []byte(`{"ok":false,"error":{"code":"ENCODE_FAILED","message":"response could not be encoded"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(payload, '\n'))
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed while generating a management token")
	}
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
