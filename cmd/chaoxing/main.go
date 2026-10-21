package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/answer"
	"github.com/DeliciousBuding/chaoxing/internal/captchasolver"
	"github.com/DeliciousBuding/chaoxing/internal/controlplane"
	"github.com/DeliciousBuding/chaoxing/internal/egress"
	"github.com/DeliciousBuding/chaoxing/internal/location"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/vault"
	"github.com/DeliciousBuding/chaoxing/internal/watch"
)

var version = "dev"

type config struct {
	StateDir          string               `json:"state_dir"`
	KeyPath           string               `json:"key_path"`
	Issuer            string               `json:"issuer"`
	Listen            string               `json:"listen"`
	LocalListen       string               `json:"local_listen"`
	LocalToken        string               `json:"local_token"`
	OwnerUsername     string               `json:"owner_username,omitempty"`
	OwnerPasswordHash string               `json:"owner_password_hash"`
	Watch             *watchConfig         `json:"watch,omitempty"`
	CaptchaSolver     *captchaSolverConfig `json:"captcha_solver,omitempty"`
	Answer            *answerConfig        `json:"answer,omitempty"`
	Location          *locationConfig      `json:"location,omitempty"`
	Device            *deviceConfig        `json:"device,omitempty"`
	RequestPacing     *requestPacingConfig `json:"request_pacing,omitempty"`
	TrustedProxies    []string             `json:"trusted_proxies,omitempty"`
	GroupActivities   bool                 `json:"group_activities,omitempty"`
	// Proxies optionally routes upstream traffic through SOCKS5 endpoints;
	// empty means direct egress. Tenants are additional isolated accounts
	// served under /t/<id>/; empty keeps the single-tenant deployment.
	Proxies []egress.Endpoint `json:"proxies,omitempty"`
	Tenants []tenantConfig    `json:"tenants,omitempty"`
}

// watchConfig enables the optional activity watcher. Notifications are
// outbound only.
type watchConfig struct {
	Enabled               bool                  `json:"enabled"`
	AutoSign              bool                  `json:"auto_sign"`
	AutoAnswer            bool                  `json:"auto_answer"`
	IntervalSeconds       int                   `json:"interval_seconds"`
	IntervalJitterPercent int                   `json:"interval_jitter_percent"`
	ClassPollDelayMinMs   int                   `json:"class_poll_delay_min_ms,omitempty"`
	ClassPollDelayMaxMs   int                   `json:"class_poll_delay_max_ms,omitempty"`
	AutoSignDelayMinSecs  int                   `json:"auto_sign_delay_min_seconds,omitempty"`
	AutoSignDelayMaxSecs  int                   `json:"auto_sign_delay_max_seconds,omitempty"`
	RepeatSeconds         int                   `json:"repeat_seconds"`
	MaxRepeats            int                   `json:"max_repeats"`
	LeadSeconds           int                   `json:"lead_seconds"`
	PollBeforeMinutes     int                   `json:"poll_before_minutes,omitempty"`
	PollAfterMinutes      int                   `json:"poll_after_minutes,omitempty"`
	GroupScanSeconds      int                   `json:"group_scan_seconds,omitempty"`
	TermStart             string                `json:"term_start,omitempty"`
	Window                string                `json:"window,omitempty"`
	Classes               []watch.Class         `json:"classes"`
	ClassroomAgent        *classroomAgentConfig `json:"classroom_agent,omitempty"`
	Capture               *watchCaptureConfig   `json:"capture,omitempty"`
	FeishuWebhook         string                `json:"feishu_webhook,omitempty"`
	NtfyURL               string                `json:"ntfy_url,omitempty"`
	NtfyTopic             string                `json:"ntfy_topic,omitempty"`
	GotifyURL             string                `json:"gotify_url,omitempty"`
	GotifyToken           string                `json:"gotify_token,omitempty"`
}

type watchCaptureConfig struct {
	Enabled        bool `json:"enabled"`
	RetentionHours int  `json:"retention_hours,omitempty"`
	ActivityPages  bool `json:"activity_pages,omitempty"`
}

// classroomAgentConfig bounds the classroom answer agent. It never widens
// scope: homework and practice stay manual, and notify_only keeps every
// decision advisory.
type classroomAgentConfig struct {
	Enabled       bool    `json:"enabled"`
	NotifyOnly    bool    `json:"notify_only"`
	MinConfidence float64 `json:"min_confidence,omitempty"`
	MaxQuestions  int     `json:"max_questions,omitempty"`
}

type requestPacingConfig struct {
	MinIntervalMs int `json:"min_interval_ms,omitempty"`
	JitterPercent int `json:"jitter_percent,omitempty"`
}

type deviceConfig struct {
	Code        string `json:"code,omitempty"`
	UserAgent   string `json:"user_agent,omitempty"`
	PackageName string `json:"package_name,omitempty"`
}

type tenantConfig struct {
	ID                string               `json:"id"`
	StateDir          string               `json:"state_dir"`
	KeyPath           string               `json:"key_path"`
	OwnerPasswordHash string               `json:"owner_password_hash"`
	Device            *deviceConfig        `json:"device,omitempty"`
	RequestPacing     *requestPacingConfig `json:"request_pacing,omitempty"`
	GroupActivities   bool                 `json:"group_activities,omitempty"`
	Watch             *watchConfig         `json:"watch,omitempty"`
	CaptchaSolver     *captchaSolverConfig `json:"captcha_solver,omitempty"`
	Answer            *answerConfig        `json:"answer,omitempty"`
}

func (c *requestPacingConfig) settings() (time.Duration, int, error) {
	if c == nil {
		return 650 * time.Millisecond, 35, nil
	}
	if c.MinIntervalMs < 0 || c.MinIntervalMs > 60000 {
		return 0, 0, errors.New("request pacing min interval must be between 0 and 60000 ms")
	}
	if c.JitterPercent < 0 || c.JitterPercent > 50 {
		return 0, 0, errors.New("request pacing jitter must be between 0 and 50 percent")
	}
	return time.Duration(c.MinIntervalMs) * time.Millisecond, c.JitterPercent, nil
}

// captchaSolverConfig selects a private recognition provider. The user_key is
// deployment secret material and is never exposed through tool contracts.
type captchaSolverConfig struct {
	Provider       string `json:"provider"`
	UserKey        string `json:"user_key,omitempty"`
	UserKeyFile    string `json:"user_key_file,omitempty"`
	UserKeyEnv     string `json:"user_key_env,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type locationConfig struct {
	Enabled       bool    `json:"enabled"`
	BaiduAK       string  `json:"baidu_ak,omitempty"`
	BaiduAKFile   string  `json:"baidu_ak_file,omitempty"`
	BaiduAKEnv    string  `json:"baidu_ak_env,omitempty"`
	CenterLat     float64 `json:"center_latitude,omitempty"`
	CenterLng     float64 `json:"center_longitude,omitempty"`
	Zoom          int     `json:"zoom,omitempty"`
	TimeoutSecond int     `json:"timeout_seconds,omitempty"`
}

func (c *locationConfig) resolveBaiduAK() (string, error) {
	if c == nil {
		return "", nil
	}
	values := []string{c.BaiduAK, c.BaiduAKFile, c.BaiduAKEnv}
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	if count > 1 {
		return "", errors.New("location baidu_ak requires at most one source")
	}
	switch {
	case strings.TrimSpace(c.BaiduAK) != "":
		return strings.TrimSpace(c.BaiduAK), nil
	case strings.TrimSpace(c.BaiduAKFile) != "":
		b, err := os.ReadFile(c.BaiduAKFile)
		if err != nil {
			return "", errors.New("location baidu_ak_file is unavailable")
		}
		ak := strings.TrimSpace(string(b))
		if ak == "" || len(ak) > 128 {
			return "", errors.New("location baidu_ak_file is invalid")
		}
		return ak, nil
	case strings.TrimSpace(c.BaiduAKEnv) != "":
		ak := strings.TrimSpace(os.Getenv(c.BaiduAKEnv))
		if ak == "" {
			return "", errors.New("location baidu_ak_env is empty")
		}
		return ak, nil
	default:
		return "", nil
	}
}

func (c *locationConfig) validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	if c.CenterLat != 0 && (math.IsNaN(c.CenterLat) || c.CenterLat < -90 || c.CenterLat > 90) {
		return errors.New("location center_latitude is outside range")
	}
	if c.CenterLng != 0 && (math.IsNaN(c.CenterLng) || c.CenterLng < -180 || c.CenterLng > 180) {
		return errors.New("location center_longitude is outside range")
	}
	if c.Zoom < 0 || c.Zoom > 21 {
		return errors.New("location zoom must be between 0 and 21")
	}
	if c.TimeoutSecond < 0 || c.TimeoutSecond > 30 {
		return errors.New("location timeout_seconds must be between 0 and 30")
	}
	if strings.TrimSpace(c.BaiduAK) != "" && len(strings.TrimSpace(c.BaiduAK)) > 128 {
		return errors.New("location baidu_ak is invalid")
	}
	return nil
}

func (c *locationConfig) mapConfig(ak string) *location.MapConfig {
	if c == nil || !c.Enabled {
		return &location.MapConfig{Enabled: false}
	}
	zoom := c.Zoom
	if zoom <= 0 {
		zoom = 17
	}
	return &location.MapConfig{
		Enabled: ak != "", AK: ak, CenterLat: c.CenterLat, CenterLng: c.CenterLng, Zoom: zoom,
	}
}

type answerConfig struct {
	Enabled       bool                   `json:"enabled"`
	Providers     []answerProviderConfig `json:"providers"`
	MinConfidence float64                `json:"min_confidence,omitempty"`
}

type answerProviderConfig struct {
	Type           string `json:"type"`
	BaseURL        string `json:"base_url,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	APIKeyFile     string `json:"api_key_file,omitempty"`
	APIKeyEnv      string `json:"api_key_env,omitempty"`
	Model          string `json:"model,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (c *answerConfig) engine(stateDir string) (*answer.Engine, error) {
	if c == nil || !c.Enabled || len(c.Providers) == 0 {
		return nil, nil
	}
	providers := []answer.Provider{}
	for _, item := range c.Providers {
		endpoint, err := publicHTTPURL(item.BaseURL)
		if err != nil {
			return nil, err
		}
		key, err := item.resolveAPIKey()
		if err != nil {
			return nil, err
		}
		timeout := time.Duration(item.TimeoutSeconds) * time.Second
		switch strings.TrimSpace(item.Type) {
		case "llm", "openai":
			if item.Model == "" {
				return nil, errors.New("answer llm provider requires model")
			}
			providers = append(providers, answer.NewLLM(endpoint, key, item.Model, timeout))
		case "responses", "openai-responses":
			if item.Model == "" {
				return nil, errors.New("answer responses provider requires model")
			}
			if key == "" {
				return nil, errors.New("answer responses provider requires an api_key source")
			}
			if strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/chat/completions") {
				return nil, errors.New("answer responses provider base_url must be the API root, not a chat-completions endpoint")
			}
			providers = append(providers, answer.NewOpenAIResponses(endpoint, key, item.Model, timeout))
		case "newapi":
			if item.Model == "" {
				return nil, errors.New("answer newapi provider requires model")
			}
			if key == "" {
				return nil, errors.New("answer newapi provider requires an api_key source")
			}
			provider, err := answer.NewNewAPI(endpoint, key, item.Model, timeout)
			if err != nil {
				return nil, err
			}
			providers = append(providers, provider)
		case "tiku":
			providers = append(providers, answer.NewTiku(endpoint, key, timeout))
		default:
			return nil, errors.New("answer provider type must be llm, newapi, responses or tiku")
		}
	}
	return answer.NewEngine(providers, filepath.Join(stateDir, "answer-cache.json"), c.MinConfidence)
}

func (p answerProviderConfig) resolveAPIKey() (string, error) {
	values := []string{p.APIKey, p.APIKeyFile, p.APIKeyEnv}
	sources := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			sources++
		}
	}
	if sources > 1 {
		return "", errors.New("answer provider requires at most one api_key source")
	}
	switch {
	case strings.TrimSpace(p.APIKey) != "":
		return strings.TrimSpace(p.APIKey), nil
	case strings.TrimSpace(p.APIKeyFile) != "":
		b, err := os.ReadFile(p.APIKeyFile)
		if err != nil {
			return "", errors.New("answer provider api key file is unavailable")
		}
		key := strings.TrimSpace(string(b))
		if key == "" || len(key) > 512 {
			return "", errors.New("answer provider api key file is invalid")
		}
		return key, nil
	case strings.TrimSpace(p.APIKeyEnv) != "":
		key := strings.TrimSpace(os.Getenv(p.APIKeyEnv))
		if key == "" {
			return "", errors.New("answer provider api key environment variable is empty")
		}
		return key, nil
	default:
		return "", nil
	}
}

func publicHTTPURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("answer provider base_url must be an absolute HTTP(S) URL")
	}
	return u.String(), nil
}

func (c *captchaSolverConfig) solver() (upstream.CaptchaSolver, error) {
	if c == nil {
		return nil, nil
	}
	switch strings.TrimSpace(c.Provider) {
	case "local":
		return captchasolver.NewLocal(), nil
	case "damagou":
	default:
		return nil, errors.New("captcha_solver.provider must be local or damagou")
	}
	userKey, err := c.resolveUserKey()
	if err != nil {
		return nil, err
	}
	solver, err := captchasolver.NewDamaGou(userKey)
	if err != nil {
		return nil, err
	}
	if c.TimeoutSeconds > 0 {
		solver.Client.Timeout = time.Duration(c.TimeoutSeconds) * time.Second
	}
	return solver, nil
}

func (c *captchaSolverConfig) resolveUserKey() (string, error) {
	sources := 0
	for _, value := range []string{c.UserKey, c.UserKeyFile, c.UserKeyEnv} {
		if strings.TrimSpace(value) != "" {
			sources++
		}
	}
	if sources != 1 {
		return "", errors.New("captcha_solver requires exactly one of user_key, user_key_file, or user_key_env")
	}
	switch {
	case strings.TrimSpace(c.UserKey) != "":
		return strings.TrimSpace(c.UserKey), nil
	case strings.TrimSpace(c.UserKeyFile) != "":
		b, err := os.ReadFile(c.UserKeyFile)
		if err != nil {
			return "", errors.New("captcha solver user key file is unavailable")
		}
		key := strings.TrimSpace(string(b))
		if key == "" || len(key) > 512 {
			return "", errors.New("captcha solver user key file is invalid")
		}
		return key, nil
	default:
		key := strings.TrimSpace(os.Getenv(c.UserKeyEnv))
		if key == "" {
			return "", errors.New("captcha solver user key environment variable is empty")
		}
		return key, nil
	}
}

type request struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

func main() {
	// OpenWrt's BusyBox TZ value is not sufficient for Go's zoneinfo lookup.
	// Scheduling already uses a fixed Beijing zone; make logs/backups agree.
	time.Local = time.FixedZone("CST", 8*60*60)
	if e := run(); e != nil {
		if errors.Is(e, errPartial) {
			os.Exit(3)
		}
		if errors.Is(e, errHelp) {
			return
		}
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"ok": false, "error": upstream.SafeFault(e)})
		os.Exit(1)
	}
}
func configFile() string {
	if v := os.Getenv("CHAOXING_CONFIG"); v != "" {
		return v
	}
	return "/etc/chaoxing/config.json"
}
func load() (config, error) {
	var c config
	if e := checkPrivateFile(configFile()); e != nil {
		return c, e
	}
	b, e := os.ReadFile(configFile())
	if e != nil {
		return c, errors.New("configuration unavailable")
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, errors.New("configuration invalid")
	}
	if e = c.Location.validate(); e != nil {
		return c, e
	}
	return c, nil
}

func checkPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("configuration unavailable")
	}
	if !info.Mode().IsRegular() {
		return errors.New("configuration must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("configuration permissions must be 0600")
	}
	return nil
}
func (c config) client() (*upstream.Client, error) {
	cfg, err := c.upstreamConfig()
	if err != nil {
		return nil, err
	}
	return upstream.New(cfg)
}

func (c config) upstreamConfig() (upstream.Config, error) {
	cfg := upstream.Config{
		CredentialsPath:  filepath.Join(c.StateDir, "credentials.sealed"),
		KeyPath:          c.KeyPath,
		SessionPath:      filepath.Join(c.StateDir, "session.sealed"),
		DeviceStatePath:  filepath.Join(c.StateDir, "device-state.json"),
		EgressAccountKey: "root",
	}
	if c.Device != nil {
		cfg.DeviceCode, cfg.UserAgent, cfg.PackageName = c.Device.Code, c.Device.UserAgent, c.Device.PackageName
	}
	cfg.GroupActivities = c.GroupActivities
	interval, jitter, err := c.RequestPacing.settings()
	if err != nil {
		return cfg, err
	}
	cfg.RequestMinInterval, cfg.RequestJitterPercent = interval, jitter
	solver, err := c.CaptchaSolver.solver()
	if err != nil {
		return cfg, err
	}
	cfg.CaptchaSolver = solver
	return cfg, nil
}

func (c tenantConfig) upstreamConfig() (upstream.Config, error) {
	cfg := upstream.Config{
		CredentialsPath:  filepath.Join(c.StateDir, "credentials.sealed"),
		KeyPath:          c.KeyPath,
		SessionPath:      filepath.Join(c.StateDir, "session.sealed"),
		DeviceStatePath:  filepath.Join(c.StateDir, "device-state.json"),
		EgressAccountKey: c.ID,
	}
	if c.Device != nil {
		cfg.DeviceCode, cfg.UserAgent, cfg.PackageName = c.Device.Code, c.Device.UserAgent, c.Device.PackageName
	}
	cfg.GroupActivities = c.GroupActivities
	interval, jitter, err := c.RequestPacing.settings()
	if err != nil {
		return cfg, err
	}
	cfg.RequestMinInterval, cfg.RequestJitterPercent = interval, jitter
	solver, err := c.CaptchaSolver.solver()
	if err != nil {
		return cfg, err
	}
	cfg.CaptchaSolver = solver
	return cfg, nil
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func readRequest() (request, error) {
	var r request
	d := json.NewDecoder(io.LimitReader(os.Stdin, 65537))
	d.DisallowUnknownFields()
	e := d.Decode(&r)
	if e != nil || d.Decode(new(any)) != io.EOF {
		return r, errors.New("invalid request JSON")
	}
	if r.Tool == "" {
		return r, errors.New("tool required")
	}
	if r.Arguments == nil {
		r.Arguments = map[string]any{}
	}
	return r, nil
}
func run() error {
	if len(os.Args) < 2 {
		return usage()
	}
	if os.Args[1] == "help" || os.Args[1] == "--help" {
		return usage()
	}
	if os.Args[1] == "schema" {
		if len(os.Args) != 3 {
			return &upstream.Fault{Code: "INVALID_ARGUMENT"}
		}
		d, ok := operation.Find(os.Args[2])
		if !ok {
			return &upstream.Fault{Code: "INVALID_TOOL"}
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"id": d.ID, "name": d.Name, "input_schema": d.InputSchema(), "output_schema": d.OutputSchema()})
	}
	if os.Args[1] == "capabilities" {
		return json.NewEncoder(os.Stdout).Encode(operation.Capabilities())
	}
	if r, matched, e := parseBusiness(os.Args[1:]); matched {
		if e != nil {
			return e
		}
		return callDaemon(r)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
		return nil
	case "configure":
		return configure()
	case "query":
		c, e := load()
		if e != nil {
			return e
		}
		r, e := readRequest()
		if e != nil {
			return e
		}
		if requiresAuditedDaemon(r.Tool) {
			return errors.New("sign-in operations require the daemon so every attempt is audited; use `chaoxing call`")
		}
		x, e := c.client()
		if e != nil {
			return e
		}
		execute, e := operationExecutor(x, c.StateDir, c.Answer)
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		v, e := execute(ctx, r.Tool, r.Arguments)
		if e != nil {
			return e
		}
		return emitResult(r.Tool, v)
	case "call":
		r, e := readRequest()
		if e != nil {
			return e
		}
		return callDaemon(r)
	case "watch":
		return watchCommand(configFile(), os.Args[2:])
	case "courses":
		c, e := load()
		if e != nil {
			return e
		}
		return coursesCommand(c, os.Args[2:])
	case "location":
		c, e := load()
		if e != nil {
			return e
		}
		return locationCommand(c.StateDir, os.Args[2:])
	case "serve":
		return serve()
	default:
		return errors.New("unknown command")
	}
}

func requiresAuditedDaemon(tool string) bool {
	return tool == "sign_activity" || tool == "sign_activity_with_face"
}
func configure() error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	state := fs.String("state-dir", "", "private persistent directory")
	key := fs.String("key-file", "", "separate encryption key path")
	issuer := fs.String("issuer", "", "public HTTPS origin")
	if e := fs.Parse(os.Args[2:]); e != nil {
		return e
	}
	if *state == "" || *key == "" || *issuer == "" {
		return errors.New("state-dir, key-file and issuer required")
	}
	var input struct {
		Phone         string `json:"phone"`
		Password      string `json:"password"`
		OwnerUsername string `json:"owner_username"`
		OwnerPassword string `json:"owner_password"`
	}
	dec := json.NewDecoder(io.LimitReader(os.Stdin, 65536))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil {
		return errors.New("invalid credentials JSON on stdin")
	}
	if len(input.OwnerPassword) < 16 {
		return errors.New("separate owner password must have >=16 characters")
	}
	ownerUsername := controlplane.NormalizeUsername(input.OwnerUsername)
	if ownerUsername == "" {
		ownerUsername = "owner"
	}
	if !controlplane.ValidUsername(ownerUsername) {
		return errors.New("owner username must be 3..32 lowercase letters, digits, dot, dash or underscore")
	}
	// Refuse overwriting an installation accidentally; rotation will be explicit.
	if _, e := os.Stat(configFile()); e == nil {
		return errors.New("configuration exists; refusing implicit replacement")
	}
	if e := vault.Seal(filepath.Join(*state, "credentials.sealed"), *key, vault.Credentials{Phone: input.Phone, Password: input.Password}); e != nil {
		return errors.New("credential sealing failed")
	}
	hash, e := ownerHash(input.OwnerPassword)
	if e != nil {
		return e
	}
	c := config{StateDir: *state, KeyPath: *key, Issuer: *issuer, Listen: "127.0.0.1:8792", LocalListen: "127.0.0.1:8793", LocalToken: randomToken(), OwnerUsername: ownerUsername, OwnerPasswordHash: hash}
	b, _ := json.MarshalIndent(c, "", "  ")
	if e = vault.Atomic(configFile(), b); e != nil {
		return errors.New("config write failed")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "credentials": "encrypted", "owner_auth": "hash_only"})
}
func loopback(addr string) bool {
	host, _, e := net.SplitHostPort(addr)
	return e == nil && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
