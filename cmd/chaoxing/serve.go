package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/authz"
	"github.com/DeliciousBuding/chaoxing/internal/capture"
	"github.com/DeliciousBuding/chaoxing/internal/classroomagent"
	"github.com/DeliciousBuding/chaoxing/internal/controlplane"
	"github.com/DeliciousBuding/chaoxing/internal/egress"
	"github.com/DeliciousBuding/chaoxing/internal/location"
	"github.com/DeliciousBuding/chaoxing/internal/mcphttp"
	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/signaudit"
	"github.com/DeliciousBuding/chaoxing/internal/statelock"
	"github.com/DeliciousBuding/chaoxing/internal/statestore"
	"github.com/DeliciousBuding/chaoxing/internal/supervisor"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/watch"
	"github.com/DeliciousBuding/chaoxing/internal/webui"
	"github.com/DeliciousBuding/chaoxing/internal/workdraft"
	"golang.org/x/net/netutil"
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
	cfg        tenantConfig
	pool       *egress.Pool
	signAudits signaudit.Store
	once       sync.Once
	client     *upstream.Client
	err        error
	watch      *watch.Runner
	execOnce   sync.Once
	execute    func(context.Context, string, map[string]any) (any, error)
	execErr    error
}

func (t *tenantRuntime) upstreamClient() (*upstream.Client, error) {
	t.once.Do(func() {
		cfg, err := t.cfg.upstreamConfig()
		if err != nil {
			t.err = err
			return
		}
		cfg.Transport = t.pool
		cfg.SignAudit = t.signAudits
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
	uploadDir := filepath.Join(stateDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		return nil, &upstream.Fault{Code: "UPLOAD_UNAVAILABLE", Message: "private upload directory is unavailable"}
	}
	dirInfo, err := os.Lstat(uploadDir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, &upstream.Fault{Code: "UPLOAD_UNAVAILABLE", Message: "private upload directory is not a real directory"}
	}
	if runtime.GOOS != "windows" && dirInfo.Mode().Perm()&0o077 != 0 {
		return nil, &upstream.Fault{Code: "UPLOAD_UNAVAILABLE", Message: "private upload directory must not be group/world-accessible"}
	}
	path := filepath.Join(uploadDir, filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "upload file is unavailable or is not a regular file"}
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "upload file must not be group/world-readable"}
	}
	if !ownedByCurrentUser(info) {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "upload file must be owned by the service user"}
	}
	content, err := os.ReadFile(path)
	if err != nil || len(content) == 0 || len(content) > 5*1024*1024 {
		return nil, &upstream.Fault{Code: "INVALID_ARGUMENT", Message: "upload file is unavailable or exceeds 5 MiB"}
	}
	result, err := client.UploadFile(ctx, filename, content)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("upload cleanup failed: %s", upstream.SafeFault(err).Code)
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

type toolAuditFunc func(context.Context, string, string, error, time.Duration)

func auditedToolExecute(account string, execute func(context.Context, string, map[string]any) (any, error), audit toolAuditFunc) func(context.Context, string, map[string]any) (any, error) {
	if audit == nil {
		return execute
	}
	return func(ctx context.Context, name string, args map[string]any) (any, error) {
		started := time.Now()
		value, err := execute(ctx, name, args)
		audit(ctx, account, name, err, time.Since(started))
		return value, err
	}
}

func toolAuditRecorder(store *controlplane.Store) toolAuditFunc {
	return func(ctx context.Context, account, tool string, callErr error, elapsed time.Duration) {
		if store == nil {
			return
		}
		actor := authz.Principal(ctx)
		role := "mcp"
		if actor == "local" {
			role = "local"
		} else if actor == "" {
			actor = "mcp"
		}
		code := ""
		if callErr != nil {
			code = upstream.SafeFault(callErr).Code
		}
		details, _ := json.Marshal(map[string]any{
			"tool": tool, "duration_ms": elapsed.Milliseconds(), "error": code,
		})
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := store.RecordAudit(auditCtx, controlplane.AuditEvent{
			ActorUsername: actor, ActorRole: role, AccountKey: account,
			Category: "tool", Action: "tool.call", Target: tool,
			Success: callErr == nil, Details: details,
		}); err != nil {
			log.Printf("tool audit write failed: %v", err)
		}
	}
}

// publicMux mounts the root account and all configured tenants. It returns the
// mux and the ordered tenant ids for the management status endpoint.
func publicMux(c config, x *upstream.Client, a *access.Server, pool *egress.Pool, captures capture.Store, signAudits signaudit.Store, locations location.Service, rootExecute func(context.Context, string, map[string]any) (any, error), audit toolAuditFunc) (*http.ServeMux, []string, []*tenantRuntime, error) {
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/mcp", a.ProtectMCP(mcphttp.NewWithVersion(auditedToolExecute("root", rootExecute, audit), version)))
	// Additional tenants mount a complete isolated OAuth + MCP stack under
	// /t/<id>/. Discovery metadata lives at /t/<id>/.well-known/...; clients
	// requiring RFC 8414 path-insertion discovery must configure endpoints
	// explicitly for tenant issuers.
	tenantIDs := []string{}
	runtimes := []*tenantRuntime{}
	for _, tc := range c.Tenants {
		tr := &tenantRuntime{cfg: tc, pool: pool, signAudits: signAudits}
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
		tm.Handle("/mcp", ta.ProtectMCP(mcphttp.NewWithVersion(auditedToolExecute(tc.ID, texec, audit), version)))
		mux.Handle("/t/"+tc.ID+"/", http.StripPrefix("/t/"+tc.ID, tm))
		if tc.Watch != nil && tc.Watch.Enabled {
			cl, e := tr.upstreamClient()
			if e != nil {
				return nil, nil, nil, e
			}
			tr.watch, e = newWatcher(*tc.Watch, tc.StateDir, cl, captures, location.NewResolver(locations, tc.ID), tc.ID, tc.Answer)
			if e != nil {
				return nil, nil, nil, e
			}
		}
		tenantIDs = append(tenantIDs, tc.ID)
	}
	return mux, tenantIDs, runtimes, nil
}

func newWatcher(c watchConfig, stateDir string, client *upstream.Client, captures capture.Store, locationResolver *location.Resolver, accountKey string, answers *answerConfig) (*watch.Runner, error) {
	sender, err := watchSender(c)
	if err != nil {
		return nil, err
	}
	cfg, err := watchCoreConfig(c, stateDir)
	if err != nil {
		return nil, err
	}
	if locationResolver != nil {
		cfg.LocationResolver = locationResolver
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

// watchCoreConfig maps the daemon configuration to the watcher without
// dropping safety-relevant fields. It is kept separate so the mapping can be
// tested without starting a network client or an event loop.
func watchCoreConfig(c watchConfig, stateDir string) (watch.Config, error) {
	termStart, err := parseWatchTermStart(c.TermStart)
	if err != nil {
		return watch.Config{}, err
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
		PollBefore:            time.Duration(c.PollBeforeMinutes) * time.Minute,
		PollAfter:             time.Duration(c.PollAfterMinutes) * time.Minute,
		GroupScanInterval:     time.Duration(c.GroupScanSeconds) * time.Second,
		TermStart:             termStart,
		AutoSign:              c.AutoSign,
		AutoAnswer:            c.AutoAnswer,
		Classes:               c.Classes,
		StatePath:             filepath.Join(stateDir, "watch-state.json"),
	}
	if cfg.ClassPollDelayMin == 0 && cfg.ClassPollDelayMax == 0 {
		cfg.ClassPollDelayMin, cfg.ClassPollDelayMax = 750*time.Millisecond, 3*time.Second
	}
	if cfg.AutoSignDelayMin == 0 && cfg.AutoSignDelayMax == 0 {
		cfg.AutoSignDelayMin, cfg.AutoSignDelayMax = 5*time.Second, 25*time.Second
	}
	return cfg, nil
}

func parseWatchTermStart(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.ParseInLocation("2006-01-02", raw, time.FixedZone("CST", 8*60*60))
	if err != nil {
		return time.Time{}, errors.New("watch term_start must use YYYY-MM-DD")
	}
	return value, nil
}

// watcherStale flags a live process whose polling loop has not completed a
// cycle for three times the idle sleep cap. The first tick runs immediately,
// so a missing completion after the short startup grace is already a failure.
const (
	watchStaleAfter      = 15 * time.Minute
	maxPublicConnections = 64
	watchFirstTickWait   = 2 * time.Minute
)

func watcherStale(now, startedAt, lastTick time.Time, ticks uint64) bool {
	if ticks == 0 || lastTick.IsZero() {
		if startedAt.IsZero() || now.Before(startedAt) {
			return false
		}
		return now.Sub(startedAt) > watchFirstTickWait
	}
	reference := lastTick
	if startedAt.After(reference) {
		reference = startedAt
	}
	if reference.IsZero() || now.Before(reference) {
		return false
	}
	return now.Sub(reference) > watchStaleAfter
}

func watchCaptureEnabled(w *watchConfig) bool {
	return w != nil && w.Capture != nil && w.Capture.Enabled
}

func supervisorNotifier(c config) (notify.Sender, error) {
	if c.Watch != nil && c.Watch.Enabled {
		return watchSender(*c.Watch)
	}
	for _, tenant := range c.Tenants {
		if tenant.Watch != nil && tenant.Watch.Enabled {
			return watchSender(*tenant.Watch)
		}
	}
	return nil, nil
}

func stateStoreNeeded(_ config) bool {
	// Sign auditing is unconditional: a manual CLI/MCP sign-in still has to
	// produce a private audit row even when watcher, capture, proxy and
	// location features are all disabled.
	return true
}

func lockStateDirs(c config) ([]*statelock.Lock, error) {
	dirs := []string{c.StateDir}
	for _, tenant := range c.Tenants {
		dirs = append(dirs, tenant.StateDir)
	}
	sort.Strings(dirs)
	locks := make([]*statelock.Lock, 0, len(dirs))
	for _, dir := range dirs {
		lock, err := statelock.Acquire(dir)
		if err != nil {
			for _, held := range locks {
				_ = held.Close()
			}
			return nil, fmt.Errorf("state directory lock: %w", err)
		}
		locks = append(locks, lock)
	}
	return locks, nil
}

func serve() error {
	c, e := load()
	if e != nil {
		return e
	}
	if !loopback(c.Listen) || !loopback(c.LocalListen) || c.Listen == c.LocalListen {
		return errors.New("public and management listeners must be distinct loopback addresses")
	}
	if len(strings.TrimSpace(c.LocalToken)) < 32 {
		return errors.New("local management token must be at least 32 characters")
	}
	if e = validateTenants(c); e != nil {
		return e
	}
	stateLocks, e := lockStateDirs(c)
	if e != nil {
		return e
	}
	defer func() {
		for _, lock := range stateLocks {
			if err := lock.Close(); err != nil {
				log.Printf("state directory unlock failed: %v", err)
			}
		}
	}()
	// Process supervision starts before any network service so a procd respawn
	// can distinguish an unexpected exit from a SIGTERM-initiated restart.
	supervisorSender, senderErr := supervisorNotifier(c)
	if senderErr != nil {
		return senderErr
	}
	sup, e := supervisor.Start(supervisor.Config{
		Path:   filepath.Join(c.StateDir, "supervisor-state.json"),
		Sender: supervisorSender,
	})
	if e != nil {
		return e
	}
	// Egress pool: direct by default, SOCKS5 endpoints when configured. The
	// private SQLite store is always opened for sign auditing and also retains
	// proxy health, sticky account egress and production activity captures
	// when those features are enabled.
	var store *statestore.Store
	storeCloseSafe := true
	var health egress.HealthStore
	var captures capture.Store
	var signAudits signaudit.Store
	if stateStoreNeeded(c) {
		store, e = statestore.Open(filepath.Join(c.StateDir, "chaoxing.db"))
		if e != nil {
			return e
		}
		defer func() {
			if storeCloseSafe {
				if closeErr := store.Close(); closeErr != nil {
					log.Printf("state store close failed: %v", closeErr)
				}
			}
		}()
		if len(c.Proxies) > 0 {
			health = healthAdapter{store}
		}
		captures = store
		signAudits = signaudit.NewMetered(store)
	}
	controlStore, e := controlplane.Open(filepath.Join(c.StateDir, "control.db"), c.OwnerUsername, c.OwnerPasswordHash)
	if e != nil {
		return e
	}
	defer func() {
		if closeErr := controlStore.Close(); closeErr != nil {
			log.Printf("control-plane store close failed: %v", closeErr)
		}
	}()
	_ = controlStore.PruneAudit(context.Background(), time.Now().Add(-180*24*time.Hour))
	pool, e := egress.New(c.Proxies, health)
	if e != nil {
		return e
	}
	rootConfig, err := c.upstreamConfig()
	if err != nil {
		return err
	}
	rootConfig.Transport = pool
	rootConfig.SignAudit = signAudits
	x, e := upstream.New(rootConfig)
	if e != nil {
		return e
	}
	rootExecute, e := operationExecutor(x, c.StateDir, c.Answer)
	if e != nil {
		return e
	}
	auditTool := toolAuditRecorder(controlStore)
	var locations location.Service
	var mapConfig *location.MapConfig
	var geocoder location.Geocoder
	if c.Location != nil && c.Location.Enabled {
		if store == nil {
			return errors.New("location service requires the private state store")
		}
		locations = store
		ak, akErr := c.Location.resolveBaiduAK()
		if akErr != nil {
			return akErr
		}
		mapConfig = c.Location.mapConfig(ak)
		if ak != "" {
			timeout := time.Duration(c.Location.TimeoutSecond) * time.Second
			geocoder = location.NewBaiduGeocoder(ak, timeout)
		}
	}
	var watcher *watch.Runner
	if c.Watch != nil && c.Watch.Enabled {
		watcher, e = newWatcher(*c.Watch, c.StateDir, x, captures, location.NewResolver(locations, "root"), "root", c.Answer)
		if e != nil {
			return e
		}
	}
	a, e := access.New(access.Config{Issuer: c.Issuer, StateDir: filepath.Join(c.StateDir, "oauth"), OwnerPasswordHash: c.OwnerPasswordHash, EnableCIMD: os.Getenv("CHAOXING_ENABLE_CIMD") == "true"})
	if e != nil {
		return e
	}
	mux, tenantIDs, tenants, e := publicMux(c, x, a, pool, captures, signAudits, locations, rootExecute, auditTool)
	if e != nil {
		return e
	}
	uiAccounts := []webui.Account{}
	for _, tc := range c.Tenants {
		uiAccounts = append(uiAccounts, webui.Account{ID: tc.ID, Name: tc.ID})
	}

	watcherMetrics := func() map[string]any {
		watchers := map[string]any{}
		if watcher != nil {
			watchers["root"] = watcher.Metrics()
		}
		for _, tenant := range tenants {
			if tenant.watch != nil {
				watchers[tenant.cfg.ID] = tenant.watch.Metrics()
			}
		}
		return watchers
	}
	watcherHealth := func() map[string]any {
		detailed := watcherMetrics()
		count, consecutive, ticks, active := len(detailed), 0, 0, 0
		var lastTick, lastPanic time.Time
		stale := false
		stateDegraded := false
		feedDegraded := false
		now := time.Now().UTC()
		for _, value := range detailed {
			m, ok := value.(watch.Metrics)
			if !ok {
				continue
			}
			consecutive += m.ConsecutivePanics
			ticks += int(m.Ticks)
			active += int(m.ActiveClassPolls)
			if m.LastTickAt.After(lastTick) {
				lastTick = m.LastTickAt
			}
			if m.LastPanicAt.After(lastPanic) {
				lastPanic = m.LastPanicAt
			}
			if watcherStale(now, m.StartedAt, m.LastTickAt, m.Ticks) {
				stale = true
			}
			stateDegraded = stateDegraded || m.StateDegraded
			feedDegraded = feedDegraded || m.FeedDegraded
		}
		return map[string]any{
			"count": count, "consecutive_panics": consecutive, "last_tick_at": lastTick,
			"last_panic_at": lastPanic, "ticks": ticks, "active_class_polls": active,
			"stale": stale, "state_degraded": stateDegraded, "feed_degraded": feedDegraded, "now": now,
		}
	}
	upstreamClientForAccount := func(account string) (*upstream.Client, error) {
		if account == "root" {
			return x, nil
		}
		for _, tenant := range tenants {
			if tenant.cfg.ID == account {
				return tenant.upstreamClient()
			}
		}
		return nil, errors.New("unknown management account")
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
	captureList := func(ctx context.Context, account, activityKey string, limit int, includePage bool) ([]capture.Activity, error) {
		if store == nil {
			return []capture.Activity{}, nil
		}
		return store.ListActivityCaptures(ctx, account, activityKey, limit, includePage)
	}
	signAuditList := func(ctx context.Context, account string, limit int) ([]signaudit.Record, error) {
		if store == nil {
			return []signaudit.Record{}, nil
		}
		return store.ListSignAudits(ctx, account, limit)
	}
	ui, err := webui.New(webui.Config{
		Version:           version,
		Issuer:            c.Issuer,
		OwnerPasswordHash: c.OwnerPasswordHash,
		TrustedProxies:    c.TrustedProxies,
		Accounts:          uiAccounts,
		Control:           controlStore,
		AccountConfigs: func() []webui.AccountConfigView {
			views := make([]webui.AccountConfigView, 0, 1+len(c.Tenants))
			root := webui.AccountConfigView{ID: "root", Name: "默认账号", LocationEnabled: c.Location != nil && c.Location.Enabled}
			if c.Watch != nil {
				root.WatchEnabled = c.Watch.Enabled
				root.AutoSign = c.Watch.AutoSign
				root.AutoAnswer = c.Watch.AutoAnswer
				root.Classes = len(c.Watch.Classes)
				root.NotifyOnly = c.Watch.ClassroomAgent != nil && c.Watch.ClassroomAgent.NotifyOnly
			}
			views = append(views, root)
			for _, tenant := range c.Tenants {
				view := webui.AccountConfigView{ID: tenant.ID, Name: tenant.ID, LocationEnabled: root.LocationEnabled}
				if tenant.Watch != nil {
					view.WatchEnabled = tenant.Watch.Enabled
					view.AutoSign = tenant.Watch.AutoSign
					view.AutoAnswer = tenant.Watch.AutoAnswer
					view.Classes = len(tenant.Watch.Classes)
					view.NotifyOnly = tenant.Watch.ClassroomAgent != nil && tenant.Watch.ClassroomAgent.NotifyOnly
				}
				views = append(views, view)
			}
			return views
		},
		Execute: accountExecute,
		Status: func() any {
			return map[string]any{"egress": pool.Status(), "process": sup.Metrics(), "tenants": tenantIDs, "watchers": watcherMetrics(), "now": time.Now().UTC()}
		},
		Captures:   captureList,
		SignAudits: signAuditList,
		Schedules: func(account string) []webui.ScheduleView {
			var classes []watch.Class
			if account == "root" {
				if c.Watch != nil {
					classes = c.Watch.Classes
				}
			} else {
				for _, tenant := range c.Tenants {
					if tenant.ID == account && tenant.Watch != nil {
						classes = tenant.Watch.Classes
						break
					}
				}
			}
			return webuiScheduleViews(classes)
		},
		ScheduleTermStart: func(account string) string {
			if account == "root" {
				if c.Watch != nil {
					return c.Watch.TermStart
				}
				return ""
			}
			for _, tenant := range c.Tenants {
				if tenant.ID == account && tenant.Watch != nil {
					return tenant.Watch.TermStart
				}
			}
			return ""
		},
		Profile: func(ctx context.Context, account string) (webui.ProfileView, error) {
			client, err := upstreamClientForAccount(account)
			if err != nil {
				return webui.ProfileView{}, err
			}
			profile, err := client.Profile(ctx)
			if err != nil {
				return webui.ProfileView{}, err
			}
			return webui.ProfileView{DisplayName: profile.DisplayName, AvatarAvailable: profile.AvatarAvailable}, nil
		},
		Avatar: func(ctx context.Context, account string) ([]byte, string, error) {
			client, err := upstreamClientForAccount(account)
			if err != nil {
				return nil, "", err
			}
			return client.Avatar(ctx)
		},
		CourseCover: func(ctx context.Context, account, courseID, classID string) ([]byte, string, error) {
			client, err := upstreamClientForAccount(account)
			if err != nil {
				return nil, "", err
			}
			return client.CourseCover(ctx, courseID, classID)
		},
		Location: locations,
		Map:      mapConfig,
		Geocoder: geocoder,
	})
	if err != nil {
		return err
	}
	ui.Register(mux)
	execute := auditedToolExecute("root", rootExecute, auditTool)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		health := watcherHealth()
		stale, _ := health["stale"].(bool)
		panics, _ := health["consecutive_panics"].(int)
		stateDegraded, _ := health["state_degraded"].(bool)
		feedDegraded, _ := health["feed_degraded"].(bool)
		degraded := sup.Metrics().Degraded
		control := ui.ControlHealth()
		auditConsecutive, _ := control["audit_consecutive_write_failures"].(uint64)
		var signAuditConsecutive uint64
		if meter, ok := signAudits.(*signaudit.Metered); ok {
			signAuditConsecutive, _ = meter.Health()
		}
		ok := !stale && panics < 3 && !degraded && !stateDegraded && !feedDegraded && auditConsecutive < 3 && signAuditConsecutive < 3
		watcherCount, _ := health["count"].(int)
		ticks, _ := health["ticks"].(int)
		lastTick, _ := health["last_tick_at"].(time.Time)
		lastTickText := ""
		if !lastTick.IsZero() {
			lastTickText = lastTick.UTC().Format(time.RFC3339)
		}
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": ok, "stale": stale, "consecutive_panics": panics, "degraded": degraded,
			"state_degraded": stateDegraded, "feed_degraded": feedDegraded, "watcher_count": watcherCount, "ticks": ticks,
			"sign_audit_consecutive_write_failures": signAuditConsecutive,
			"last_tick_at":                          lastTickText, "audit_write_failures": control["audit_write_failures"],
			"audit_consecutive_write_failures": auditConsecutive,
		})
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
			"version":  version,
			"egress":   pool.Status(),
			"tenants":  tenantIDs,
			"process":  sup.Metrics(),
			"watchers": watcherMetrics(),
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
		ctx, cancel := context.WithTimeout(authz.WithPrincipal(r.Context(), "local"), 90*time.Second)
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
	var watchers sync.WaitGroup
	if watcher != nil {
		watchers.Add(1)
		go func() {
			defer watchers.Done()
			watcher.Run(ctx)
		}()
	}
	for _, tenant := range tenants {
		if tenant.watch != nil {
			watchers.Add(1)
			go func(r *watch.Runner) {
				defer watchers.Done()
				r.Run(ctx)
			}(tenant.watch)
		}
	}
	pubListener, listenErr := net.Listen("tcp", c.Listen)
	if listenErr != nil {
		return listenErr
	}
	privListener, listenErr := net.Listen("tcp", c.LocalListen)
	if listenErr != nil {
		_ = pubListener.Close()
		return listenErr
	}
	// The relay makes this loopback listener reachable from the internet. Cap
	// concurrent connections before the handler-level limiter so a slow-client
	// flood cannot exhaust the router's small file-descriptor budget.
	errs := make(chan error, 2)
	go func() { errs <- pub.Serve(netutil.LimitListener(pubListener, maxPublicConnections)) }()
	go func() { errs <- priv.Serve(privListener) }()
	select {
	case e = <-errs:
	case <-ctx.Done():
		e = nil
	}
	// Stop signal delivery and cancel every watcher before closing the HTTP
	// listeners and, later, the state store.
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = pub.Shutdown(shutdown)
	_ = priv.Shutdown(shutdown)
	watchersDone := make(chan struct{})
	go func() {
		watchers.Wait()
		close(watchersDone)
	}()
	watchersStopped := false
	select {
	case <-watchersDone:
		watchersStopped = true
	case <-time.After(10 * time.Second):
		log.Printf("shutdown: watcher did not stop within 10s; refusing to mark a clean process exit")
		storeCloseSafe = false
		if e == nil {
			e = errors.New("watcher shutdown timed out")
		}
	}
	if watchersStopped && (e == nil || errors.Is(e, http.ErrServerClosed)) {
		if markErr := sup.MarkClean(); markErr != nil {
			log.Printf("supervisor clean shutdown marker failed: %v", markErr)
		}
	}
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}

func webuiScheduleViews(classes []watch.Class) []webui.ScheduleView {
	out := []webui.ScheduleView{}
	for _, class := range classes {
		for _, schedule := range class.Schedule {
			weeks, _ := watch.NormalizeWeeks(schedule.Weeks)
			out = append(out, webui.ScheduleView{
				CourseID: class.CourseID,
				ClassID:  class.ClassID,
				Key:      schedule.EffectiveKey(),
				Days:     append([]string(nil), schedule.Days...),
				Start:    schedule.Start,
				End:      schedule.End,
				Weeks:    weeks,
				Room:     schedule.Room,
			})
		}
	}
	return out
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
