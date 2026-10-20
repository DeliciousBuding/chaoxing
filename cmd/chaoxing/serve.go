package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/egress"
	"github.com/DeliciousBuding/chaoxing/internal/mcphttp"
	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/statestore"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/watch"
	"github.com/DeliciousBuding/chaoxing/internal/workdraft"
)

func ownerHash(s string) (string, error) { return access.HashPassword(s) }

// healthAdapter bridges the SQLite store to the egress health interface.
type healthAdapter struct{ s *statestore.Store }

func (a healthAdapter) ProxyHealth() (map[string]egress.Health, error) {
	m, err := a.s.ProxyHealth()
	if err != nil {
		return nil, err
	}
	out := make(map[string]egress.Health, len(m))
	for k, h := range m {
		out[k] = egress.Health{Failures: h.Failures, CoolUntil: h.CoolUntil}
	}
	return out, nil
}
func (a healthAdapter) SetProxyHealth(key string, h egress.Health) error {
	return a.s.SetProxyHealth(key, statestore.ProxyHealth{Failures: h.Failures, CoolUntil: h.CoolUntil})
}

var tenantID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

const maxTenants = 32

// tenantRuntime isolates one account: its own OAuth state, sealed credentials,
// session and upstream client. The client is created lazily so unused tenants
// cost nothing on memory-constrained hosts.
type tenantRuntime struct {
	cfg       tenantConfig
	pool      *egress.Pool
	once      sync.Once
	client    *upstream.Client
	err       error
	watch     *watch.Runner
	draftOnce sync.Once
	drafts    *workdraft.Store
	draftErr  error
}

func (t *tenantRuntime) upstreamClient() (*upstream.Client, error) {
	t.once.Do(func() {
		cfg := t.cfg.upstreamConfig()
		cfg.Transport = t.pool
		t.client, t.err = upstream.New(cfg)
	})
	return t.client, t.err
}

func (t *tenantRuntime) workDrafts() (*workdraft.Store, error) {
	t.draftOnce.Do(func() {
		client, err := t.upstreamClient()
		if err != nil {
			t.draftErr = err
			return
		}
		t.drafts, t.draftErr = workdraft.Open(filepath.Join(t.cfg.StateDir, "work-drafts.json"), client)
	})
	return t.drafts, t.draftErr
}

func isWorkDraftOperation(name string) bool {
	switch name {
	case "upsert_work_draft", "list_work_drafts", "delete_work_draft", "export_work_draft":
		return true
	default:
		return false
	}
}

func operationExecutor(client *upstream.Client, stateDir string) (func(context.Context, string, map[string]any) (any, error), error) {
	drafts, err := workdraft.Open(filepath.Join(stateDir, "work-drafts.json"), client)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, name string, args map[string]any) (any, error) {
		if isWorkDraftOperation(name) {
			return operation.Execute(ctx, name, args, drafts.Call)
		}
		return operation.Execute(ctx, name, args, client.Call)
	}, nil
}

func validateTenants(c config) error {
	if len(c.Tenants) > maxTenants {
		return errors.New("too many tenants (max 32)")
	}
	seen := map[string]bool{}
	states := []string{c.StateDir}
	keys := []string{c.KeyPath}
	for _, tc := range c.Tenants {
		if !tenantID.MatchString(tc.ID) {
			return errors.New("invalid tenant id " + tc.ID)
		}
		if seen[tc.ID] {
			return errors.New("duplicate tenant id " + tc.ID)
		}
		seen[tc.ID] = true
		if tc.StateDir == "" || tc.KeyPath == "" || tc.OwnerPasswordHash == "" {
			return errors.New("tenant " + tc.ID + " requires state_dir, key_path and owner_password_hash")
		}
		if tc.StateDir == c.StateDir || tc.KeyPath == c.KeyPath {
			return errors.New("tenant " + tc.ID + " shares state with the default account")
		}
		states = append(states, tc.StateDir)
		keys = append(keys, tc.KeyPath)
	}
	return validateTenantPaths(states, keys)
}

// publicMux mounts the root account and all configured tenants. It returns the
// mux and the ordered tenant ids for the management status endpoint.
func publicMux(c config, x *upstream.Client, a *access.Server, pool *egress.Pool, rootExecute func(context.Context, string, map[string]any) (any, error)) (*http.ServeMux, []string, []*tenantRuntime, error) {
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/mcp", a.ProtectMCP(mcphttp.NewWithVersion(rootExecute, version)))
	// Additional tenants mount a complete isolated OAuth + MCP stack under
	// /t/<id>/. Discovery metadata lives at /t/<id>/.well-known/...; clients
	// requiring RFC 8414 path-insertion discovery must configure endpoints
	// explicitly for tenant issuers.
	tenantIDs := []string{}
	runtimes := []*tenantRuntime{}
	for _, tc := range c.Tenants {
		tr := &tenantRuntime{cfg: tc, pool: pool}
		ta, e := access.New(access.Config{Issuer: c.Issuer + "/t/" + tc.ID, StateDir: filepath.Join(tc.StateDir, "oauth"), OwnerPasswordHash: tc.OwnerPasswordHash, EnableCIMD: os.Getenv("CHAOXING_ENABLE_CIMD") == "true"})
		if e != nil {
			return nil, nil, nil, e
		}
		tm := http.NewServeMux()
		ta.Register(tm)
		ta.RegisterPathDiscovery(mux)
		runtimes = append(runtimes, tr)
		texec := func(ctx context.Context, name string, args map[string]any) (any, error) {
			cl, e := tr.upstreamClient()
			if e != nil {
				return nil, e
			}
			if isWorkDraftOperation(name) {
				drafts, e := tr.workDrafts()
				if e != nil {
					return nil, e
				}
				return operation.Execute(ctx, name, args, drafts.Call)
			}
			return operation.Execute(ctx, name, args, cl.Call)
		}
		tm.Handle("/mcp", ta.ProtectMCP(mcphttp.NewWithVersion(texec, version)))
		mux.Handle("/t/"+tc.ID+"/", http.StripPrefix("/t/"+tc.ID, tm))
		if tc.Watch != nil && tc.Watch.Enabled {
			cl, e := tr.upstreamClient()
			if e != nil {
				return nil, nil, nil, e
			}
			tr.watch, e = newWatcher(*tc.Watch, tc.StateDir, cl)
			if e != nil {
				return nil, nil, nil, e
			}
		}
		tenantIDs = append(tenantIDs, tc.ID)
	}
	return mux, tenantIDs, runtimes, nil
}

func newWatcher(c watchConfig, stateDir string, client *upstream.Client) (*watch.Runner, error) {
	sender, err := watchSender(c)
	if err != nil {
		return nil, err
	}
	interval := time.Duration(c.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	return watch.New(watch.Config{
		Interval:              interval,
		IntervalJitterPercent: c.IntervalJitterPercent,
		RepeatEvery:           time.Duration(c.RepeatSeconds) * time.Second,
		MaxRepeats:            c.MaxRepeats,
		Window:                c.Window,
		Lead:                  time.Duration(c.LeadSeconds) * time.Second,
		AutoSign:              c.AutoSign,
		Classes:               c.Classes,
		StatePath:             filepath.Join(stateDir, "watch-state.json"),
	}, client, sender, client)
}

func serve() error {
	c, e := load()
	if e != nil {
		return e
	}
	if !loopback(c.Listen) || !loopback(c.LocalListen) || c.Listen == c.LocalListen {
		return errors.New("public and management listeners must be distinct loopback addresses")
	}
	if e = validateTenants(c); e != nil {
		return e
	}
	// Egress pool: direct by default, SOCKS5 endpoints when configured. Pool
	// health persists in SQLite only when proxies exist, keeping the default
	// deployment free of the database file.
	var store *statestore.Store
	var health egress.HealthStore
	if len(c.Proxies) > 0 {
		store, e = statestore.Open(filepath.Join(c.StateDir, "chaoxing.db"))
		if e != nil {
			return e
		}
		defer store.Close()
		health = healthAdapter{store}
	}
	pool, e := egress.New(c.Proxies, health)
	if e != nil {
		return e
	}
	rootConfig := c.upstreamConfig()
	rootConfig.Transport = pool
	x, e := upstream.New(rootConfig)
	if e != nil {
		return e
	}
	rootExecute, e := operationExecutor(x, c.StateDir)
	if e != nil {
		return e
	}
	var watcher *watch.Runner
	if c.Watch != nil && c.Watch.Enabled {
		watcher, e = newWatcher(*c.Watch, c.StateDir, x)
		if e != nil {
			return e
		}
	}
	a, e := access.New(access.Config{Issuer: c.Issuer, StateDir: filepath.Join(c.StateDir, "oauth"), OwnerPasswordHash: c.OwnerPasswordHash, EnableCIMD: os.Getenv("CHAOXING_ENABLE_CIMD") == "true"})
	if e != nil {
		return e
	}
	mux, tenantIDs, tenants, e := publicMux(c, x, a, pool, rootExecute)
	if e != nil {
		return e
	}
	execute := func(ctx context.Context, name string, args map[string]any) (any, error) {
		return rootExecute(ctx, name, args)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Chaoxing MCP</title><h1>Chaoxing MCP</h1><p>Connect your MCP client to /mcp using OAuth. This service does not provide public course data.</p></html>`))
	})
	local := http.NewServeMux()
	localAuth := func(w http.ResponseWriter, r *http.Request) bool {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.LocalToken)) != 1 {
			w.WriteHeader(401)
			return false
		}
		return true
	}
	local.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !localAuth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"version": version,
			"egress":  pool.Status(),
			"tenants": tenantIDs,
		})
	})
	local.HandleFunc("POST /call", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !localAuth(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		var in request
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
			w.WriteHeader(400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		v, e := execute(ctx, in.Tool, in.Arguments)
		if e != nil {
			status := http.StatusBadGateway
			if upstream.SafeFault(e).Code == "RATE_LIMITED" {
				status = http.StatusTooManyRequests
				w.Header().Set("Retry-After", "1")
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": upstream.SafeFault(e)})
			return
		}
		out, e := operation.Result(in.Tool, v)
		if e != nil {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": upstream.SafeFault(e)})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": out})
	})
	secure := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
	pub := &http.Server{Addr: c.Listen, Handler: secure, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	priv := &http.Server{Addr: c.LocalListen, Handler: local, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if watcher != nil {
		go watcher.Run(ctx)
	}
	for _, tenant := range tenants {
		if tenant.watch != nil {
			go tenant.watch.Run(ctx)
		}
	}
	errs := make(chan error, 2)
	go func() { errs <- pub.ListenAndServe() }()
	go func() { errs <- priv.ListenAndServe() }()
	select {
	case e = <-errs:
	case <-ctx.Done():
		e = nil
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = pub.Shutdown(shutdown)
	_ = priv.Shutdown(shutdown)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}

func watchSender(c watchConfig) (notify.Sender, error) {
	senders := notify.Multi{}
	if strings.TrimSpace(c.FeishuWebhook) != "" {
		senders = append(senders, notify.Feishu{Webhook: c.FeishuWebhook})
	}
	if strings.TrimSpace(c.NtfyURL) != "" && strings.TrimSpace(c.NtfyTopic) != "" {
		senders = append(senders, notify.Ntfy{BaseURL: c.NtfyURL, Topic: c.NtfyTopic})
	}
	if strings.TrimSpace(c.GotifyURL) != "" && strings.TrimSpace(c.GotifyToken) != "" {
		senders = append(senders, notify.Gotify{BaseURL: c.GotifyURL, Token: c.GotifyToken})
	}
	if len(senders) == 0 {
		return nil, errors.New("watch is enabled but no feishu_webhook, ntfy_url/ntfy_topic, or gotify_url/gotify_token is configured")
	}
	return senders, nil
}
