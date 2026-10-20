package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/DeliciousBuding/chaoxing/internal/capture"
	"github.com/DeliciousBuding/chaoxing/internal/classroomagent"
	"github.com/DeliciousBuding/chaoxing/internal/egress"
	"github.com/DeliciousBuding/chaoxing/internal/mcphttp"
	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/statestore"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/watch"
	"github.com/DeliciousBuding/chaoxing/internal/webui"
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
func (a healthAdapter) ProxyAssignments() (map[string]string, error) {
	return a.s.ProxyAssignments()
}
func (a healthAdapter) SetProxyAssignment(accountKey, endpointKey string) error {
	return a.s.SetProxyAssignment(accountKey, endpointKey)
}
func (a healthAdapter) DeleteProxyAssignment(accountKey string) error {
	return a.s.DeleteProxyAssignment(accountKey)
}

var tenantID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

const maxTenants = 32

// tenantRuntime isolates one account: its own OAuth state, sealed credentials,
// session and upstream client. The client is created lazily so unused tenants
// cost nothing on memory-constrained hosts.
type tenantRuntime struct {
	cfg      tenantConfig
	pool     *egress.Pool
	once     sync.Once
	client   *upstream.Client
	err      error
	watch    *watch.Runner
	execOnce sync.Once
	execute  func(context.Context, string, map[string]any) (any, error)
	execErr  error
}

func (t *tenantRuntime) upstreamClient() (*upstream.Client, error) {
	t.once.Do(func() {
		cfg, err := t.cfg.upstreamConfig()
		if err != nil {
			t.err = err
			return
		}
		cfg.Transport = t.pool
		t.client, t.err = upstream.New(cfg)
	})
	return t.client, t.err
}

func (t *tenantRuntime) operationExecutor() (func(context.Context, string, map[string]any) (any, error), error) {
	t.execOnce.Do(func() {
		client, err := t.upstreamClient()
		if err != nil {
			t.execErr = err
			return
		}
		t.execute, t.execErr = operationExecutor(client, t.cfg.StateDir, t.cfg.Answer)
	})
	return t.execute, t.execErr
}

func isWorkDraftOperation(name string) bool {
	switch name {
	case "answer_work", "upsert_work_draft", "list_work_drafts", "delete_work_draft", "export_work_draft":
		return true
	default:
		return false
	}
}

func operationExecutor(client *upstream.Client, stateDir string, answers *answerConfig) (func(context.Context, string, map[string]any) (any, error), error) {
	drafts, err := workdraft.Open(filepath.Join(stateDir, "work-drafts.json"), client)
	if err != nil {
		return nil, err
	}
	engine, err := answers.engine(stateDir)
	if err != nil {
		return nil, err
	}
	drafts.SetAnswerer(engine)
	return func(ctx context.Context, name string, args map[string]any) (any, error) {
		if isWorkDraftOperation(name) {
			return operation.Execute(ctx, name, args, drafts.Call)
		}
		if name == "save_work" || name == "submit_work" {
			return operation.Execute(ctx, name, args, func(ctx context.Context, _ string, args map[string]any) (any, error) {
				return submitPreparedWork(ctx, client, drafts, name, args)
			})
		}
		if name == "upload_file" {
			return operation.Execute(ctx, name, args, func(ctx context.Context, _ string, args map[string]any) (any, error) {
				return uploadPreparedFile(ctx, client, stateDir, args)
			})
		}
		return operation.Execute(ctx, name, args, client.Call)
	}, nil
}

func submitPreparedWork(ctx context.Context, client *upstream.Client, drafts *workdraft.Store, action string, args map[string]any) (any, error) {
	save, submit, questions, answered, err := drafts.Payloads(ctx, args)
	if err != nil {
		return nil, err
	}
	if questions == 0 {
		return nil, &upstream.Fault{Code: "WORK_UNAVAILABLE", Message: "work has no questions"}
	}
	if action == "save_work" && answered == 0 {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "save_work requires at least one draft"}
	}
	if action == "submit_work" && answered != questions {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "submit_work requires every question to have a draft"}
	}
	payload := save
	if action == "submit_work" {
		payload = submit
	}
	result, err := client.SubmitWorkPayload(ctx, strings.TrimSuffix(action, "_work"), payload)
	if err != nil {
		return nil, err
	}
	result.Action = map[string]string{"save_work": "save", "submit_work": "submit"}[action]
	return map[string]any{"result": result, "questions": questions, "answered": answered, "complete": true, "coverage": "upstream_work_submission", "fetched_at": result.FetchedAt}, nil
}

func uploadPreparedFile(ctx context.Context, client *upstream.Client, stateDir string, args map[string]any) (any, error) {
	filename := strings.TrimSpace(fmt.Sprint(args["file_name"]))
	if filename == "" || filepath.Base(filename) != filename || strings.Contains(filename, "/") || strings.Contains(filename, `\`) {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "upload file_name must be a filename without paths"}
	}
	path := filepath.Join(stateDir, "uploads", filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "upload file is unavailable or is not a regular file"}
	}
	content, err := os.ReadFile(path)
	if err != nil || len(content) == 0 || len(content) > 5*1024*1024 {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "upload file is unavailable or exceeds 5 MiB"}
	}
	result, err := client.UploadFile(ctx, filename, content)
	if err != nil {
		return nil, err
	}
	return map[string]any{"result": result, "complete": true, "coverage": "chaoxing_pan_upload", "fetched_at": result.FetchedAt}, nil
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
func publicMux(c config, x *upstream.Client, a *access.Server, pool *egress.Pool, captures capture.Store, rootExecute func(context.Context, string, map[string]any) (any, error)) (*http.ServeMux, []string, []*tenantRuntime, error) {
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
			exec, e := tr.operationExecutor()
			if e != nil {
				return nil, e
			}
			return exec(ctx, name, args)
		}
		tm.Handle("/mcp", ta.ProtectMCP(mcphttp.NewWithVersion(texec, version)))
		mux.Handle("/t/"+tc.ID+"/", http.StripPrefix("/t/"+tc.ID, tm))
		if tc.Watch != nil && tc.Watch.Enabled {
			cl, e := tr.upstreamClient()
			if e != nil {
				return nil, nil, nil, e
			}
			tr.watch, e = newWatcher(*tc.Watch, tc.StateDir, cl, captures, tc.ID, tc.Answer)
			if e != nil {
				return nil, nil, nil, e
			}
		}
		tenantIDs = append(tenantIDs, tc.ID)
	}
	return mux, tenantIDs, runtimes, nil
}

func newWatcher(c watchConfig, stateDir string, client *upstream.Client, captures capture.Store, accountKey string, answers *answerConfig) (*watch.Runner, error) {
	sender, err := watchSender(c)
	if err != nil {
		return nil, err
	}
	interval := time.Duration(c.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	cfg := watch.Config{
		Interval:              interval,
		IntervalJitterPercent: c.IntervalJitterPercent,
		ClassPollDelayMin:     time.Duration(c.ClassPollDelayMinMs) * time.Millisecond,
		ClassPollDelayMax:     time.Duration(c.ClassPollDelayMaxMs) * time.Millisecond,
		AutoSignDelayMin:      time.Duration(c.AutoSignDelayMinSecs) * time.Second,
		AutoSignDelayMax:      time.Duration(c.AutoSignDelayMaxSecs) * time.Second,
		RepeatEvery:           time.Duration(c.RepeatSeconds) * time.Second,
		MaxRepeats:            c.MaxRepeats,
		Window:                c.Window,
		Lead:                  time.Duration(c.LeadSeconds) * time.Second,
		AutoSign:              c.AutoSign,
		Classes:               c.Classes,
		StatePath:             filepath.Join(stateDir, "watch-state.json"),
	}
	if cfg.ClassPollDelayMin == 0 && cfg.ClassPollDelayMax == 0 {
		cfg.ClassPollDelayMin, cfg.ClassPollDelayMax = 750*time.Millisecond, 3*time.Second
	}
	if cfg.AutoSignDelayMin == 0 && cfg.AutoSignDelayMax == 0 {
		cfg.AutoSignDelayMin, cfg.AutoSignDelayMax = 5*time.Second, 25*time.Second
	}
	if c.Capture != nil && c.Capture.Enabled {
		retention := 7 * 24 * time.Hour
		if c.Capture.RetentionHours > 0 {
			retention = time.Duration(c.Capture.RetentionHours) * time.Hour
		}
		cfg.Capture = true
		cfg.CaptureAccount = accountKey
		cfg.CaptureRetention = retention
		cfg.CaptureActivityPages = c.Capture.ActivityPages
		cfg.CaptureStore = captures
		cfg.ActivityPageSource = client
	}
	var answerer []watch.AnswerActivitySource
	if c.AutoAnswer {
		answerer = append(answerer, client)
	}
	if c.AutoAnswer && c.ClassroomAgent != nil && c.ClassroomAgent.Enabled {
		engine, err := answers.engine(stateDir)
		if err != nil {
			return nil, err
		}
		agent, err := classroomagent.New(classroomagent.Policy{
			Enabled:       c.ClassroomAgent.Enabled,
			NotifyOnly:    c.ClassroomAgent.NotifyOnly,
			MinConfidence: c.ClassroomAgent.MinConfidence,
			MaxQuestions:  c.ClassroomAgent.MaxQuestions,
		}, engine, nil)
		if err != nil {
			return nil, err
		}
		cfg.ClassroomAgent = agent
	}
	return watch.New(cfg, client, sender, client, answerer...)
}

func watchCaptureEnabled(w *watchConfig) bool {
	return w != nil && w.Capture != nil && w.Capture.Enabled
}

func stateStoreNeeded(c config) bool {
	if len(c.Proxies) > 0 || watchCaptureEnabled(c.Watch) {
		return true
	}
	for _, tenant := range c.Tenants {
		if watchCaptureEnabled(tenant.Watch) {
			return true
		}
	}
	return false
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
	// Egress pool: direct by default, SOCKS5 endpoints when configured. The
	// same private SQLite store retains proxy health, sticky account egress
	// and production activity captures only when one of those features is on.
	var store *statestore.Store
	var health egress.HealthStore
	var captures capture.Store
	if stateStoreNeeded(c) {
		store, e = statestore.Open(filepath.Join(c.StateDir, "chaoxing.db"))
		if e != nil {
			return e
		}
		defer store.Close()
		if len(c.Proxies) > 0 {
			health = healthAdapter{store}
		}
		captures = store
	}
	pool, e := egress.New(c.Proxies, health)
	if e != nil {
		return e
	}
	rootConfig, err := c.upstreamConfig()
	if err != nil {
		return err
	}
	rootConfig.Transport = pool
	x, e := upstream.New(rootConfig)
	if e != nil {
		return e
	}
	rootExecute, e := operationExecutor(x, c.StateDir, c.Answer)
	if e != nil {
		return e
	}
	var watcher *watch.Runner
	if c.Watch != nil && c.Watch.Enabled {
		watcher, e = newWatcher(*c.Watch, c.StateDir, x, captures, "root", c.Answer)
		if e != nil {
			return e
		}
	}
	a, e := access.New(access.Config{Issuer: c.Issuer, StateDir: filepath.Join(c.StateDir, "oauth"), OwnerPasswordHash: c.OwnerPasswordHash, EnableCIMD: os.Getenv("CHAOXING_ENABLE_CIMD") == "true"})
	if e != nil {
		return e
	}
	mux, tenantIDs, tenants, e := publicMux(c, x, a, pool, captures, rootExecute)
	if e != nil {
		return e
	}
	uiAccounts := []webui.Account{}
	for _, tc := range c.Tenants {
		uiAccounts = append(uiAccounts, webui.Account{ID: tc.ID, Name: tc.ID})
	}
	accountExecute := func(ctx context.Context, account, tool string, arguments map[string]any) (any, error) {
		if account == "root" {
			return rootExecute(ctx, tool, arguments)
		}
		for _, tenant := range tenants {
			if tenant.cfg.ID != account {
				continue
			}
			execute, err := tenant.operationExecutor()
			if err != nil {
				return nil, err
			}
			return execute(ctx, tool, arguments)
		}
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "unknown management account"}
	}
	captureList := func(ctx context.Context, limit int) ([]capture.Activity, error) {
		if store == nil {
			return []capture.Activity{}, nil
		}
		return store.ListActivityCaptures(ctx, limit)
	}
	ui, err := webui.New(webui.Config{
		Version:           version,
		Issuer:            c.Issuer,
		OwnerPasswordHash: c.OwnerPasswordHash,
		Accounts:          uiAccounts,
		Execute:           accountExecute,
		Status: func() any {
			return map[string]any{"egress": pool.Status(), "tenants": tenantIDs}
		},
		Captures: captureList,
	})
	if err != nil {
		return err
	}
	ui.Register(mux)
	execute := func(ctx context.Context, name string, args map[string]any) (any, error) {
		return rootExecute(ctx, name, args)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
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
	ntfyURL, ntfyTopic := strings.TrimSpace(c.NtfyURL), strings.TrimSpace(c.NtfyTopic)
	switch {
	case ntfyURL != "" && ntfyTopic != "":
		senders = append(senders, notify.Ntfy{BaseURL: c.NtfyURL, Topic: c.NtfyTopic})
	case ntfyURL != "":
		return nil, errors.New("watch ntfy_url is configured but ntfy_topic is missing")
	case ntfyTopic != "":
		return nil, errors.New("watch ntfy_topic is configured but ntfy_url is missing")
	}
	gotifyURL, gotifyToken := strings.TrimSpace(c.GotifyURL), strings.TrimSpace(c.GotifyToken)
	switch {
	case gotifyURL != "" && gotifyToken != "":
		senders = append(senders, notify.Gotify{BaseURL: c.GotifyURL, Token: c.GotifyToken})
	case gotifyURL != "":
		return nil, errors.New("watch gotify_url is configured but gotify_token is missing")
	case gotifyToken != "":
		return nil, errors.New("watch gotify_token is configured but gotify_url is missing")
	}
	if len(senders) == 0 {
		return nil, errors.New("watch is enabled but no feishu_webhook, ntfy_url/ntfy_topic, or gotify_url/gotify_token is configured")
	}
	return senders, nil
}
