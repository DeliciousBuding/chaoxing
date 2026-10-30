// Package upstream implements authenticated, bounded Chaoxing course reads and
// the explicitly allowlisted normal/location sign-in state machine.
package upstream

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/admission"
	"github.com/DeliciousBuding/chaoxing/internal/egress"
	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/signaudit"
	"github.com/DeliciousBuding/chaoxing/internal/vault"
	"golang.org/x/net/publicsuffix"
)

const api = "https://mooc1-api.chaoxing.com"

type Fault struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	Category          string `json:"category,omitempty"`
	Retryable         bool   `json:"retryable"`
	Hint              string `json:"hint,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	RetryAfterSource  string `json:"retry_after_source,omitempty"`
}

func (f *Fault) Error() string     { return f.Code + ": " + f.Message }
func fault(code, msg string) error { return &Fault{Code: code, Message: msg} }

// SafeFault discards arbitrary error strings and supplies a stable recovery hint.
func SafeFault(err error) *Fault {
	f := &Fault{Code: "INTERNAL", Message: "Operation failed.", Category: "internal"}
	var src *Fault
	if errors.As(err, &src) {
		f.Code = src.Code
		f.RetryAfterSeconds = src.RetryAfterSeconds
		if src.RetryAfterSource == "upstream" || src.RetryAfterSource == "local_backoff" {
			f.RetryAfterSource = src.RetryAfterSource
		}
	}
	if errors.Is(err, operation.ErrInvalidArgument) {
		f.Code = "INVALID_ARGUMENT"
	}
	if errors.Is(err, operation.ErrUnknownOperation) {
		f.Code = "INVALID_TOOL"
	}
	switch f.Code {
	case "UPSTREAM_RATE_LIMITED":
		f.Category, f.Retryable = "resource", true
		f.Message = "Upstream rejected the request; a retry cooldown is active."
		f.Hint = "Wait for retry_after_seconds; do not increase request frequency."
	case "RATE_LIMITED":
		f.Category = "resource"
		f.Message = "Query capacity is temporarily exhausted."
		f.Retryable = true
		f.Hint = "Retry after the outstanding queries finish."
	case "AUTH_REQUIRED", "AUTH_COOLDOWN", "CREDENTIALS_UNAVAILABLE":
		f.Category = "authentication"
		f.Message = "Upstream authentication is unavailable."
		f.Hint = "Check connection status; complete required verification on the official site."
	case "INVALID_ARGUMENT", "INVALID_TOOL", "INVALID_URL", "AMBIGUOUS_COURSE":
		f.Category = "input"
		f.Message = "Request is not permitted by the operation contract."
		f.Hint = "Inspect schema and use IDs from list_courses."
	case "COURSE_ACCESS", "NOT_FOUND":
		f.Category = "access"
		f.Message = "Requested resource is not available in the verified accessible source."
	case "UPSTREAM_SCHEMA":
		f.Category = "schema"
		f.Message = "Upstream response is not recognized; no empty-result assumption."
		f.Hint = "Report parser drift; do not treat this as no work."
	case "UPSTREAM_UNAVAILABLE":
		f.Category = "upstream"
		f.Message = "Upstream is temporarily unavailable."
		f.Retryable = true
	case "UPSTREAM_HTTP":
		f.Category = "upstream"
		f.Message = "Upstream rejected the request."
	case "SIGN_REJECTED", "SIGN_LOCATION_REJECTED", "SIGN_DEVICE_REJECTED":
		f.Category = "upstream_state"
		f.Message = "The sign-in submission was not accepted."
		f.Hint = "Check the activity state and configured coordinates."
	case "SIGN_VERIFICATION_PENDING":
		f.Category, f.Retryable = "upstream_state", true
		f.Message = "Attendance is not yet confirmed."
		f.Hint = "Wait for attendance verification; do not submit again."
	case "SIGN_EXPIRED", "SIGN_ALREADY":
		f.Category = "upstream_state"
		f.Message = "The sign-in activity state changed."
	case "SIGN_CODE_REJECTED", "SIGN_CAPTCHA_REJECTED", "SIGN_QR_EXPIRED":
		f.Category = "upstream_state"
		f.Message = "The sign-in verification value was not accepted."
	case "CAPTCHA_REQUIRED":
		f.Category = "verification"
		f.Message = "A slider captcha challenge is required."
		f.Hint = "Complete the captcha challenge, then retry sign_activity."
	case "CAPTCHA_SOLVER_FAILED":
		f.Category = "verification"
		f.Message = "The configured captcha solver failed."
		f.Hint = "Use the manual captcha flow or check solver configuration."
	case "WORK_UNAVAILABLE":

		f.Category = "upstream_state"
		f.Message = "The work is not currently available for manual answering."
	case "WORK_SUBMIT_REJECTED":
		f.Category = "upstream_state"
		f.Message = "The upstream work save/submit was not accepted."
	case "ANSWER_REJECTED":
		f.Category = "upstream_state"
		f.Message = "The classroom answer activity was not accepted."
	case "UPLOAD_REJECTED", "FACE_ENROLL_REJECTED", "FACE_CHECK_REJECTED":
		f.Category = "upstream_state"
		f.Message = "The upstream upload or face operation was not accepted."
	case "UNSUPPORTED":
		f.Category = "capability"
		f.Message = "Capability is not implemented."
		f.Hint = "Inspect get_capabilities."
	case "RESPONSE_TOO_LARGE", "STATE_WRITE_FAILED", "STATE_READ_FAILED", "STATE_INVALID":
		f.Category = "resource"
		f.Message = "Response or state resource limit reached."
	default:
		f.Code = "INTERNAL"
	}
	return f
}

// Config carries sealed-state paths; Transport optionally overrides outbound egress.
type Config struct {
	CredentialsPath, KeyPath, SessionPath string
	DeviceStatePath                       string
	DeviceCode                            string
	UserAgent, PackageName                string
	EgressAccountKey                      string
	RequestMinInterval                    time.Duration
	RequestJitterPercent                  int
	GroupActivities                       bool
	Transport                             http.RoundTripper
	CaptchaSolver                         CaptchaSolver
	SignAudit                             signaudit.Store
}

// CaptchaSolver turns a slider background and cutout pair into an upstream
// coordinate. External providers may ignore the cutout.
// Implementations stay outside upstream so protocol and provider code do not mix.
type CaptchaSolver interface {
	Solve(ctx context.Context, background, cutout []byte) (int, error)
}
type SavedCookies struct {
	URL     string         `json:"url"`
	Cookies []*http.Cookie `json:"cookies"`
}
type Client struct {
	queries             *admission.Gate
	config              Config
	http                *http.Client
	saved               map[string]SavedCookies
	savedHash           [sha256.Size]byte
	lastLogin           time.Time
	loginFailedAt       time.Time
	courses             []Course
	coursesAt           time.Time
	assignments         []Assignment
	assignmentsAt       time.Time
	classActivities     map[string]cachedClassActivities
	coverage            string
	extraAssignments    map[string]Assignment
	device              deviceState
	identity            *signIdentity
	im                  *imConfig
	imGroups            []imGroup
	imGroupsAt          time.Time
	imGroupCursor       int
	imActivities        []Activity
	imActivitiesAt      time.Time
	imActivitiesOK      bool
	imActivityWarning   []string
	workJobs            map[string]WorkJob
	workQuestionSets    map[string]cachedWorkQuestionSet
	captchaChallenges   map[string]signCaptchaChallenge
	cooldownMu          sync.Mutex
	cooldowns           map[string]endpointCooldown
	evidence            *evidenceWriter
	evidenceFailures    atomic.Uint64
	evidenceConsecutive atomic.Uint64
}

type cachedWorkQuestionSet struct {
	set WorkQuestionSet
	at  time.Time
}

func New(c Config) (*Client, error) {
	if err := validateDeviceConfig(c.DeviceCode, c.UserAgent, c.PackageName); err != nil {
		return nil, err
	}
	if c.RequestJitterPercent < 0 || c.RequestJitterPercent > 50 {
		return nil, fault("INVALID_ARGUMENT", "request jitter must be between 0 and 50 percent")
	}
	jar, e := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if e != nil {
		return nil, e
	}
	rt := c.Transport
	if rt == nil {
		rt = &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	}
	if c.RequestMinInterval > 0 {
		rt = newPacedTransport(rt, c.RequestMinInterval, c.RequestJitterPercent)
	}
	h := &http.Client{Jar: jar, Timeout: 20 * time.Second, Transport: rt}
	h.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("redirect limit")
		}
		if !safeURL(r.URL) {
			return errors.New("blocked redirect")
		}
		return nil
	}
	x := &Client{config: c, http: h, saved: map[string]SavedCookies{}, queries: admission.New(1, 8)}
	if c.SessionPath != "" {
		x.evidence = newEvidenceWriter(c.SessionPath)
	}
	if e = x.loadCooldowns(); e != nil {
		return nil, e
	}
	if e = x.loadDeviceState(); e != nil {
		return nil, e
	}
	if b, e := vault.OpenData(c.SessionPath, c.KeyPath); e == nil {
		var saved []SavedCookies
		if json.Unmarshal(b, &saved) == nil {
			for _, s := range saved {
				u, e := url.Parse(s.URL)
				if e == nil && safeURL(u) {
					jar.SetCookies(u, s.Cookies)
					x.saved[s.URL] = s
				}
			}
		}
		clear(b)
	}
	if payload, err := x.sessionPayload(); err == nil {
		x.savedHash = sha256.Sum256(payload)
	}
	return x, nil
}

func (c *Client) requestContext(ctx context.Context) context.Context {
	return egress.WithAccount(ctx, c.config.EgressAccountKey)
}
func safeURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || !(u.Hostname() == "chaoxing.com" || strings.HasSuffix(u.Hostname(), ".chaoxing.com")) {
		return false
	}
	// GET is not proof of read-only behavior. Block known learning mutations and
	// task-entry paths even if supplied by a known feed or a server redirect.
	p := strings.ToLower(u.Path)
	for _, blocked := range []string{"studentstudyajax", "/log/setlog", "/ananas/job/", "/multimedia/log/", "/savetimepc", "/work/addstudentworknew", "/markread", "/setread"} {
		if strings.Contains(p, blocked) {
			return false
		}
	}
	return true
}
func EncryptLogin(text string) string {
	k := []byte("u2oh6Vu^HWe4_AES")
	b := []byte(text)
	pad := aes.BlockSize - len(b)%aes.BlockSize
	b = append(b, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(k)
	cipher.NewCBCEncrypter(block, k).CryptBlocks(b, b)
	return base64.StdEncoding.EncodeToString(b)
}
func (c *Client) save(resp *http.Response) error {
	cs := resp.Cookies()
	if len(cs) == 0 {
		return nil
	}
	u := *resp.Request.URL
	u.RawQuery = ""
	u.Path = "/"
	record := SavedCookies{URL: u.String(), Cookies: cs}
	old := c.saved[record.URL]
	merged := map[string]*http.Cookie{}
	for _, v := range old.Cookies {
		merged[v.Name+"|"+v.Domain+"|"+v.Path] = v
	}
	for _, v := range cs {
		merged[v.Name+"|"+v.Domain+"|"+v.Path] = v
	}
	record.Cookies = record.Cookies[:0]
	for _, v := range merged {
		record.Cookies = append(record.Cookies, v)
	}
	c.saved[record.URL] = record

	b, e := c.sessionPayload()
	if e != nil {
		return e
	}
	defer clear(b)
	sum := sha256.Sum256(b)
	if sum == c.savedHash {
		return nil
	}
	if e = vault.SealData(c.config.SessionPath, c.config.KeyPath, b); e != nil {
		return e
	}
	c.savedHash = sum
	return nil
}

// sessionPayload serializes cookies in stable order so an unchanged jar does
// not trigger an encrypted disk write on every upstream response.
func (c *Client) sessionPayload() ([]byte, error) {
	all := []SavedCookies{}
	for _, s := range c.saved {
		all = append(all, s)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].URL < all[j].URL })
	for i := range all {
		sort.Slice(all[i].Cookies, func(a, b int) bool {
			left, right := all[i].Cookies[a], all[i].Cookies[b]
			if left.Name != right.Name {
				return left.Name < right.Name
			}
			if left.Domain != right.Domain {
				return left.Domain < right.Domain
			}
			return left.Path < right.Path
		})
	}
	return json.Marshal(all)
}

const maxRawResponseBytes = 4 << 20

func (c *Client) raw(ctx context.Context, method, rawurl string, form url.Values) ([]byte, *http.Response, error) {
	return c.rawLimit(ctx, method, rawurl, form, maxRawResponseBytes)
}

func (c *Client) rawLimit(ctx context.Context, method, rawurl string, form url.Values, limit int64) (bodyBytes []byte, response *http.Response, requestErr error) {
	u, e := url.Parse(rawurl)
	if e != nil || !safeURL(u) {
		return nil, nil, fault("INVALID_URL", "upstream URL not permitted")
	}
	started := time.Now()
	defer func() { c.observeResponse(ctx, method, u, started, bodyBytes, response, requestErr) }()
	if e := c.endpointDeferred(u); e != nil {
		return nil, nil, e
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, e := http.NewRequestWithContext(c.requestContext(ctx), method, rawurl, body)
	if e != nil {
		return nil, nil, e
	}
	r.Header.Set("User-Agent", c.userAgent())
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if u.Path == "/pptSign/stuSignajax" {
		if trace, ok := ctx.Value(signTraceKey{}).(*signAuditTrace); ok && trace != nil {
			trace.Submitted = true
		}
	}
	resp, e := c.http.Do(r)
	if e != nil {
		if errors.Is(e, admission.ErrFull) {
			return nil, nil, fault("RATE_LIMITED", "shared outbound capacity exhausted")
		}
		return nil, nil, fault("UPSTREAM_UNAVAILABLE", "upstream transport failed")
	}
	defer resp.Body.Close()
	if limit <= 0 {
		return nil, resp, fault("RESPONSE_TOO_LARGE", "upstream response exceeds limit")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, resp, fault("UPSTREAM_UNAVAILABLE", "response read failed")
	}
	if int64(len(b)) > limit {
		return nil, resp, fault("RESPONSE_TOO_LARGE", "upstream response exceeds limit")
	}
	if upstreamRateLimited(b) || resp.StatusCode == http.StatusTooManyRequests {
		return b, resp, c.deferEndpoint(u, resp.Header.Get("Retry-After"))
	}
	// Save cookies at every redirect hop in Jar but snapshot final response here.
	if e = c.save(resp); e != nil {
		return nil, resp, fault("STATE_WRITE_FAILED", "cannot persist session")
	}
	if resp.StatusCode != 200 {
		return b, resp, httpStatusFault(resp.StatusCode, "upstream")
	}
	if e = c.endpointSucceeded(u, b); e != nil {
		return nil, resp, e
	}
	return b, resp, nil
}
func (c *Client) rawWrite(ctx context.Context, method, rawurl string, body io.Reader, contentType string) ([]byte, *http.Response, error) {
	u, e := url.Parse(rawurl)
	if e != nil || (!safeURL(u) && u.String() != workSubmitAPI) {
		return nil, nil, fault("INVALID_URL", "upstream URL not permitted")
	}
	if e = c.endpointDeferred(u); e != nil {
		return nil, nil, e
	}
	r, e := http.NewRequestWithContext(c.requestContext(ctx), method, rawurl, body)
	if e != nil {
		return nil, nil, e
	}
	r.Header.Set("User-Agent", c.userAgent())
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	resp, e := c.http.Do(r)
	if e != nil {
		if errors.Is(e, admission.ErrFull) {
			return nil, nil, fault("RATE_LIMITED", "shared outbound capacity exhausted")
		}
		return nil, nil, fault("UPSTREAM_UNAVAILABLE", "upstream transport failed")
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if e != nil {
		return nil, resp, fault("UPSTREAM_UNAVAILABLE", "response read failed")
	}
	if len(b) > 4*1024*1024 {
		return nil, resp, fault("RESPONSE_TOO_LARGE", "upstream response exceeds limit")
	}
	if upstreamRateLimited(b) || resp.StatusCode == http.StatusTooManyRequests {
		return b, resp, c.deferEndpoint(u, resp.Header.Get("Retry-After"))
	}
	if e = c.save(resp); e != nil {
		return nil, resp, fault("STATE_WRITE_FAILED", "cannot persist session")
	}
	if resp.StatusCode != 200 {
		return nil, resp, httpStatusFault(resp.StatusCode, "upstream")
	}
	if e = c.endpointSucceeded(u, b); e != nil {
		return nil, resp, e
	}
	return b, resp, nil
}

func httpStatusFault(status int, area string) error {
	switch {
	case status == http.StatusTooManyRequests:
		return fault("RATE_LIMITED", area+" rate limit reached")
	case status >= 500:
		return fault("UPSTREAM_UNAVAILABLE", fmt.Sprintf("%s HTTP %d", area, status))
	default:
		return fault("UPSTREAM_HTTP", fmt.Sprintf("%s HTTP %d", area, status))
	}
}

func loginRequired(b []byte, r *http.Response) bool {
	if strings.Contains(r.Request.URL.Host, "passport2.") {
		return true
	}
	s := string(b)
	return strings.Contains(s, "请重新登录") || strings.Contains(s, "用户登录") || strings.Contains(s, "passport2.chaoxing.com/login") && strings.Contains(s, "fanyalogin")
}
func (c *Client) login(ctx context.Context) error {
	if time.Since(c.loginFailedAt) < time.Minute {
		return fault("AUTH_COOLDOWN", "last login failed; retry after one minute")
	}
	cred, e := vault.Open(c.config.CredentialsPath, c.config.KeyPath)
	if e != nil {
		return fault("CREDENTIALS_UNAVAILABLE", "sealed credentials unavailable")
	}
	form := url.Values{"fid": {"-1"}, "uname": {EncryptLogin(cred.Phone)}, "password": {EncryptLogin(cred.Password)}, "refer": {"https://i.chaoxing.com"}, "t": {"true"}, "forbidotherlogin": {"0"}, "validate": {""}, "doubleFactorLogin": {"0"}, "independentId": {"0"}, "independentNameId": {"0"}}
	b, _, e := c.raw(ctx, "POST", "https://passport2.chaoxing.com/fanyalogin", form)
	if e != nil {
		c.loginFailedAt = time.Now()
		return e
	}
	var v struct {
		Status bool   `json:"status"`
		Msg    string `json:"msg2"`
	}
	if json.Unmarshal(b, &v) != nil || !v.Status {
		c.loginFailedAt = time.Now()
		return fault("AUTH_REQUIRED", "automatic login failed; check credentials or complete verification on the official site")
	}
	c.lastLogin = time.Now()
	c.courses = nil
	c.assignments = nil
	c.extraAssignments = nil
	c.identity = nil
	c.im = nil
	c.imGroups = nil
	c.imGroupsAt = time.Time{}
	c.imActivities = nil
	c.imActivitiesAt = time.Time{}
	c.imActivitiesOK = false
	c.imActivityWarning = nil
	c.workJobs = nil
	c.workQuestionSets = nil
	c.captchaChallenges = nil
	return nil
}
func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	b, _, e := c.authenticatedReadResponse(ctx, "GET", u, nil)
	return b, e
}
func (c *Client) authenticatedRead(ctx context.Context, method, u string, form url.Values) ([]byte, error) {
	b, _, e := c.authenticatedReadResponse(ctx, method, u, form)
	return b, e
}
func (c *Client) authenticatedReadResponse(ctx context.Context, method, u string, form url.Values) ([]byte, *http.Response, error) {
	return c.authenticatedReadResponseLimit(ctx, method, u, form, maxRawResponseBytes)
}

func (c *Client) authenticatedReadResponseLimit(ctx context.Context, method, u string, form url.Values, limit int64) ([]byte, *http.Response, error) {
	b, r, e := c.rawLimit(ctx, method, u, form, limit)
	if e != nil && !preSignSessionRejected(r) {
		return nil, nil, e
	}
	if preSignSessionRejected(r) || loginRequired(b, r) {
		c.recordEvidence(map[string]any{"event": "session_recovery", "reason": "upstream_login_required"})
		if e = c.login(ctx); e != nil {
			return nil, nil, e
		}
		b, r, e = c.rawLimit(ctx, method, u, form, limit)
		if preSignSessionRejected(r) {
			return nil, nil, fault("AUTH_REQUIRED", "pre-sign session rejected after automatic login")
		}
		if e != nil {
			return nil, nil, e
		}
		if loginRequired(b, r) {
			return nil, nil, fault("AUTH_REQUIRED", "session rejected after automatic login")
		}
	}
	return b, r, nil
}

func preSignSessionRejected(r *http.Response) bool {
	return r != nil && r.StatusCode == http.StatusForbidden && r.Request != nil && r.Request.URL.Hostname() == "mobilelearn.chaoxing.com" && r.Request.URL.Path == "/newsign/preSign"
}
func (c *Client) lock(ctx context.Context) error {
	if err := c.queries.Acquire(ctx); err != nil {
		if errors.Is(err, admission.ErrFull) {
			return fault("RATE_LIMITED", "account query queue full")
		}
		return fault("UPSTREAM_UNAVAILABLE", "request budget expired")
	}
	return nil
}
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	d, ok := operation.Find(name)
	if !ok {
		return nil, fault("INVALID_TOOL", "unknown tool")
	}
	if err := operation.Validate(d, args); err != nil {
		return nil, fault("INVALID_ARGUMENT", "invalid operation arguments")
	}
	name = d.Name
	// Capability discovery does not touch account state or wait on upstream I/O.
	if name == "get_capabilities" {
		return operation.Capabilities(), nil
	}
	if e := c.lock(ctx); e != nil {
		return nil, e
	}
	defer c.queries.Release()
	if err := ctx.Err(); err != nil {
		return nil, fault("UPSTREAM_UNAVAILABLE", "request budget expired")
	}
	if name == "get_connection_status" {
		// A status probe must actually validate the session, not timestamp a cache.
		c.coursesAt = time.Time{}
		_, e := c.fetchCourses(ctx)
		if e != nil {
			return nil, e
		}
		var last any
		if !c.lastLogin.IsZero() {
			last = c.lastLogin.UTC()
		}
		return map[string]any{"authenticated": true, "last_login": last, "fetched_at": time.Now().UTC(), "automatic_login": true}, nil
	}
	switch name {
	case "list_courses":
		cached := c.courses != nil && time.Since(c.coursesAt) < 2*time.Minute
		courses, e := c.fetchCourses(ctx)
		if e != nil {
			return nil, e
		}
		return map[string]any{"result": courses, "fetched_at": c.coursesAt.UTC(), "cache_hit": cached}, nil
	case "list_assignments":
		return c.listAssignments(ctx, args)
	case "get_assignment":
		return c.detail(ctx, str(args, "id"))
	case "get_assignment_questions":
		return c.AssignmentQuestions(ctx, args)
	case "list_exams":
		return c.examList(ctx)
	case "list_notices":
		return c.notices(ctx, args)
	case "get_study_overview":
		return c.studyOverview(ctx, args)
	case "list_action_items":
		return c.actionItems(ctx, args)
	case "get_study_digest":
		return c.studyDigest(ctx, args)
	case "list_chapters":
		return c.chapters(ctx, args)
	case "get_schedule":
		return c.schedule(ctx, args)
	case "list_materials":
		return c.materials(ctx, args)
	case "list_activities":
		return c.activities(ctx, args)
	case "get_sign_status":
		result, e := c.verifySignActivityLocked(ctx, SignRequest{CourseID: str(args, "course_id"), ClassID: str(args, "class_id"), ActivityID: str(args, "activity_id")})
		if e != nil {
			return nil, e
		}
		return map[string]any{"result": result, "complete": true, "coverage": "single_account_attendance", "fetched_at": result.FetchedAt}, nil
	case "get_activity_page":
		return c.activityPageLocked(ctx, args)
	case "list_work_jobs":
		return c.ListWorkJobs(ctx, args)
	case "get_work_questions":
		return c.WorkQuestions(ctx, args)
	case "answer_activity":
		result, e := c.answerActivity(ctx, str(args, "course_id"), str(args, "class_id"), str(args, "activity_id"))
		if e != nil {
			return nil, e
		}
		return map[string]any{"result": result, "complete": true, "coverage": "single_answer_activity", "fetched_at": result.FetchedAt}, nil
	case "enroll_face":
		result, e := c.enrollFace(ctx, str(args, "object_id"))
		if e != nil {
			return nil, e
		}
		return map[string]any{"result": result, "complete": true, "coverage": "face_enrollment", "fetched_at": result.FetchedAt}, nil
	case "get_sign_captcha":
		return c.getSignCaptcha(ctx, args)
	case "answer_sign_captcha":
		return c.answerSignCaptcha(ctx, args)
	case "sign_activity":
		req := SignRequest{
			CourseID: str(args, "course_id"), ClassID: str(args, "class_id"),
			ActivityID: str(args, "activity_id"), Address: str(args, "address"),
			SignCode: str(args, "sign_code"), QRPayload: str(args, "qr_payload"),
			PhotoObjectID: str(args, "photo_object_id"),
		}
		if v, ok := floatArg(args, "latitude"); ok {
			req.Latitude = &v
		}
		if v, ok := floatArg(args, "longitude"); ok {
			req.Longitude = &v
		}
		result, e := c.signActivityLocked(ctx, req)
		if e != nil {
			return nil, e
		}
		return map[string]any{"result": result, "complete": true, "coverage": "single_activity_signin", "fetched_at": result.FetchedAt}, nil
	case "sign_activity_with_face":
		req := SignRequest{
			CourseID: str(args, "course_id"), ClassID: str(args, "class_id"),
			ActivityID: str(args, "activity_id"), Address: str(args, "address"),
			FaceObjectID: str(args, "face_object_id"),
		}
		if v, ok := floatArg(args, "latitude"); ok {
			req.Latitude = &v
		}
		if v, ok := floatArg(args, "longitude"); ok {
			req.Longitude = &v
		}
		result, e := c.signActivityLocked(ctx, req)
		if e != nil {
			return nil, e
		}
		return map[string]any{"result": result, "complete": true, "coverage": "single_activity_face_signin", "fetched_at": result.FetchedAt}, nil
	default:
		return nil, fault("INVALID_TOOL", "unknown tool")
	}
}
func str(a map[string]any, k string) string               { v, _ := a[k].(string); return v }
func boolArg(a map[string]any, k string) bool             { v, _ := a[k].(bool); return v }
func floatArg(a map[string]any, k string) (float64, bool) { v, ok := a[k].(float64); return v, ok }
func number(a map[string]any, k string, def int) int {
	v, ok := a[k].(float64)
	if ok {
		return int(v)
	}
	return def
}
