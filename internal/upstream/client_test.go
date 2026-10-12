package upstream

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/vault"
)

type roundtrip func(*http.Request) (*http.Response, error)

func (f roundtrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testClient(t *testing.T) *Client {
	t.Helper()
	d := t.TempDir()
	cfg := Config{filepath.Join(d, "credentials"), filepath.Join(d, "key"), filepath.Join(d, "session")}
	if e := vault.Seal(cfg.CredentialsPath, cfg.KeyPath, vault.Credentials{Phone: "test-user", Password: "test-password"}); e != nil {
		t.Fatal(e)
	}
	c, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func response(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func TestAutomaticLoginRetriesOnce(t *testing.T) {
	c := testClient(t)
	n, logins := 0, 0
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/fanyalogin" {
			logins++
			if r.Method != "POST" {
				t.Fatal("not post")
			}
			return response(r, `{"status":true}`), nil
		}
		n++
		if n == 1 {
			return response(r, `{"result":0,"msg":"请重新登录"}`), nil
		}
		return response(r, `{"result":1,"channelList":[]}`), nil
	})
	if _, e := c.Call(context.Background(), "list_courses", nil); e != nil {
		t.Fatal(e)
	}
	if n != 2 || logins != 1 {
		t.Fatalf("n=%d logins=%d", n, logins)
	}
}
func TestRejectAfterLogin(t *testing.T) {
	c := testClient(t)
	logins := 0
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/fanyalogin" {
			logins++
			return response(r, `{"status":true}`), nil
		}
		return response(r, `{"msg":"请重新登录"}`), nil
	})
	if _, e := c.Call(context.Background(), "list_courses", nil); e == nil {
		t.Fatal("accepted rejected session")
	}
	if logins != 1 {
		t.Fatal("login loop")
	}
}
func TestLoginProtocolPadding(t *testing.T) {
	for _, s := range []string{"test", "0123456789abcdef", "中文"} {
		b, _ := base64.StdEncoding.DecodeString(EncryptLogin(s))
		k := []byte("u2oh6Vu^HWe4_AES")
		a, _ := aes.NewCipher(k)
		cipher.NewCBCDecrypter(a, k).CryptBlocks(b, b)
		p := int(b[len(b)-1])
		if string(b[:len(b)-p]) != s {
			t.Fatal("roundtrip")
		}
	}
}
func TestParseAssignmentsProtectsURL(t *testing.T) {
	b := []byte(`<ul><li onclick="goTask(this);" data="https://mooc1-api.chaoxing.com/mooc-ans/android/mtaskmsgspecial?courseId=1&amp;clazzId=2&amp;taskrefId=3&amp;enc_task=secret"><p>测试作业</p><span class="status">未提交</span><span class="fr">剩余2小时</span></li><li onclick="goTask(this);" data="https://evil.example/">bad</li></ul>`)
	rows, e := parseAssignments(b)
	if e != nil || len(rows) != 1 || rows[0].ID != "1:2:3" {
		t.Fatalf("%+v %v", rows, e)
	}
}
func TestUnknownPageNotEmptySuccess(t *testing.T) {
	if _, e := parseAssignments([]byte(`<html>Maintenance</html>`)); e == nil {
		t.Fatal("unknown page accepted")
	}
}
