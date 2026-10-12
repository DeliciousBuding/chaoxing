// Package upstream implements authenticated, bounded, read-only Chaoxing queries.
package upstream

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/vault"
	"golang.org/x/net/publicsuffix"
)

const api = "https://mooc1-api.chaoxing.com"
const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *Fault) Error() string     { return f.Code + ": " + f.Message }
func fault(code, msg string) error { return &Fault{code, msg} }

type Config struct{ CredentialsPath, KeyPath, SessionPath string }
type SavedCookies struct {
	URL     string         `json:"url"`
	Cookies []*http.Cookie `json:"cookies"`
}
type Client struct {
	mu               sync.Mutex
	config           Config
	http             *http.Client
	saved            map[string]SavedCookies
	lastLogin        time.Time
	loginFailedAt    time.Time
	courses          []Course
	coursesAt        time.Time
	assignments      []Assignment
	assignmentsAt    time.Time
	coverage         string
	extraAssignments map[string]Assignment
}

func New(c Config) (*Client, error) {
	jar, e := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if e != nil {
		return nil, e
	}
	h := &http.Client{Jar: jar, Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second}}
	h.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("redirect limit")
		}
		if !safeURL(r.URL) {
			return errors.New("blocked redirect")
		}
		return nil
	}
	x := &Client{config: c, http: h, saved: map[string]SavedCookies{}}
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
	return x, nil
}
func safeURL(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && (u.Hostname() == "chaoxing.com" || strings.HasSuffix(u.Hostname(), ".chaoxing.com"))
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
	if cs := resp.Cookies(); len(cs) > 0 {
		u := *resp.Request.URL
		u.RawQuery = ""
		u.Path = "/"
		record := SavedCookies{u.String(), cs}
		old := c.saved[record.URL]
		merged := map[string]*http.Cookie{}
		for _, v := range old.Cookies {
			merged[v.Name+"|"+v.Domain+"|"+v.Path] = v
		}
		for _, v := range cs {
			merged[v.Name+"|"+v.Domain+"|"+v.Path] = v
		}
		record.Cookies = nil
		for _, v := range merged {
			record.Cookies = append(record.Cookies, v)
		}
		c.saved[record.URL] = record
	}
	all := []SavedCookies{}
	for _, s := range c.saved {
		all = append(all, s)
	}
	b, e := json.Marshal(all)
	if e != nil {
		return e
	}
	defer clear(b)
	return vault.SealData(c.config.SessionPath, c.config.KeyPath, b)
}
func (c *Client) raw(ctx context.Context, method, rawurl string, form url.Values) ([]byte, *http.Response, error) {
	u, e := url.Parse(rawurl)
	if e != nil || !safeURL(u) {
		return nil, nil, fault("INVALID_URL", "upstream URL not permitted")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, e := http.NewRequestWithContext(ctx, method, rawurl, body)
	if e != nil {
		return nil, nil, e
	}
	r.Header.Set("User-Agent", ua)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, e := c.http.Do(r)
	if e != nil {
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
	// Save cookies at every redirect hop in Jar but snapshot final response here.
	if e = c.save(resp); e != nil {
		return nil, resp, fault("STATE_WRITE_FAILED", "cannot persist session")
	}
	if resp.StatusCode != 200 {
		return nil, resp, fault("UPSTREAM_HTTP", fmt.Sprintf("upstream HTTP %d", resp.StatusCode))
	}
	return b, resp, nil
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
	form := url.Values{"fid": {"-1"}, "uname": {EncryptLogin(cred.Phone)}, "password": {EncryptLogin(cred.Password)}, "refer": {"https%3A%2F%2Fi.chaoxing.com"}, "t": {"true"}, "forbidotherlogin": {"0"}, "validate": {""}, "doubleFactorLogin": {"0"}, "independentId": {"0"}}
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
	return nil
}
func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	return c.authenticatedRead(ctx, "GET", u, nil)
}
func (c *Client) authenticatedRead(ctx context.Context, method, u string, form url.Values) ([]byte, error) {
	b, r, e := c.raw(ctx, method, u, form)
	if e != nil {
		return nil, e
	}
	if loginRequired(b, r) {
		if e = c.login(ctx); e != nil {
			return nil, e
		}
		b, r, e = c.raw(ctx, method, u, form)
		if e != nil {
			return nil, e
		}
		if loginRequired(b, r) {
			return nil, fault("AUTH_REQUIRED", "session rejected after automatic login")
		}
	}
	return b, nil
}
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if name == "get_connection_status" {
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
		return c.fetchCourses(ctx)
	case "list_assignments":
		return c.listAssignments(ctx, args)
	case "get_assignment":
		return c.detail(ctx, str(args, "id"))
	case "list_exams":
		b, e := c.get(ctx, api+"/exam-ans/exam/test/examcode/examlist")
		if e != nil {
			return nil, e
		}
		return parseExams(b)
	case "list_notices":
		return c.notices(ctx)
	case "get_schedule":
		return c.schedule(ctx, args)
	case "list_materials":
		return c.materials(ctx, args)
	default:
		return nil, fault("INVALID_TOOL", "unknown tool")
	}
}
func str(a map[string]any, k string) string { v, _ := a[k].(string); return v }
func number(a map[string]any, k string, def int) int {
	v, ok := a[k].(float64)
	if ok {
		return int(v)
	}
	return def
}
