package webui

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/access"
)

var onePixelPNG = func() []byte {
	value, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		panic(err)
	}
	return value
}()

func TestProfileAndMediaProxyRequireSessionAndValidateInput(t *testing.T) {
	hash, err := access.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{
		Version: "test", Issuer: "https://mcp.example.com", OwnerPasswordHash: hash,
		Execute: func(context.Context, string, string, map[string]any) (any, error) { return nil, nil },
		Profile: func(context.Context, string) (ProfileView, error) {
			return ProfileView{DisplayName: "学习通用户", AvatarAvailable: true}, nil
		},
		Avatar: func(context.Context, string) ([]byte, string, error) {
			return onePixelPNG, "image/png", nil
		},
		CourseCover: func(context.Context, string, string, string) ([]byte, string, error) {
			return onePixelPNG, "image/png", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.Register(mux)

	if w := do(t, mux, "GET", "/api/media/avatar", ""); w.Code != 401 {
		t.Fatalf("unauthenticated avatar status=%d", w.Code)
	}
	if w := do(t, mux, "GET", "/api/profile", ""); w.Code != 401 {
		t.Fatalf("unauthenticated profile status=%d", w.Code)
	}

	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != 200 {
		t.Fatalf("login status=%d body=%s", w.Code, w.Body.String())
	}
	session := &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}

	w = do(t, mux, "GET", "/api/profile", "", csrfCookie, session)
	profile := jsonMap(t, w)
	if profile["display_name"] != "学习通用户" || profile["avatar_url"] != "/api/media/avatar" {
		t.Fatalf("unexpected profile: %#v", profile)
	}

	w = do(t, mux, "GET", "/api/media/avatar", "", csrfCookie, session)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("avatar status=%d type=%q", w.Code, w.Header().Get("Content-Type"))
	}
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("Cache-Control") != "private, max-age=21600" {
		t.Fatalf("avatar cache contract missing: %#v", w.Header())
	}
	r := httptest.NewRequest("GET", "https://mcp.example.com/api/media/avatar", nil)
	r.AddCookie(csrfCookie)
	r.AddCookie(session)
	r.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("etag status=%d", rec.Code)
	}

	if w := do(t, mux, "GET", "/api/media/course-cover?course_id=x", "", csrfCookie, session); w.Code != 400 {
		t.Fatalf("missing class_id status=%d", w.Code)
	}
	w = do(t, mux, "GET", "/api/media/course-cover?course_id=x&class_id=y", "", csrfCookie, session)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("cover status=%d type=%q", w.Code, w.Header().Get("Content-Type"))
	}
}
