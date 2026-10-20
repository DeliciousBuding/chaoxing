package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeishuSendsTextCard(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		io.WriteString(w, `{"code":0,"msg":"success"}`)
	}))
	defer srv.Close()
	if err := (Feishu{Webhook: srv.URL, Client: srv.Client()}).Send(context.Background(), Message{Title: "title", Body: "body"}); err != nil {
		t.Fatal(err)
	}
	if got["msg_type"] != "text" {
		t.Fatalf("%+v", got)
	}
	text := got["content"].(map[string]any)["text"].(string)
	if !strings.Contains(text, "title") || !strings.Contains(text, "body") {
		t.Fatalf("%q", text)
	}
}

func TestFeishuRejectsErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":19001}`)
	}))
	defer srv.Close()
	if err := (Feishu{Webhook: srv.URL, Client: srv.Client()}).Send(context.Background(), Message{Title: "t", Body: "b"}); err == nil {
		t.Fatal("error code accepted")
	}
}

func TestNtfySetsPriorityAndTopic(t *testing.T) {
	var path, priority, title, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, priority, title = r.URL.Path, r.Header.Get("Priority"), r.Header.Get("Title")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()
	if err := (Ntfy{BaseURL: srv.URL, Topic: "synthetic-topic", Client: srv.Client()}).Send(context.Background(), Message{Title: "alert", Body: "text"}); err != nil {
		t.Fatal(err)
	}
	if path != "/synthetic-topic" || priority != "5" || title != "alert" || body != "text" {
		t.Fatalf("path=%q priority=%q title=%q body=%q", path, priority, title, body)
	}
}

func TestMessagePriorityIsBoundedAndDefaultsToFive(t *testing.T) {
	if p := (Message{}).priority(); p != 5 {
		t.Fatal(p)
	}
	if p := (Message{Priority: -3}).priority(); p != 5 {
		t.Fatal(p)
	}
	if p := (Message{Priority: 42}).priority(); p != 10 {
		t.Fatal(p)
	}
}

func TestGotifySendsMessage(t *testing.T) {
	var path, token string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, token = r.URL.Path, r.Header.Get("X-Gotify-Key")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &payload)
		io.WriteString(w, `{"id":1}`)
	}))
	defer srv.Close()
	if err := (Gotify{BaseURL: srv.URL, Token: "synthetic-token", Client: srv.Client()}).Send(context.Background(), Message{Title: "title", Body: "body"}); err != nil {
		t.Fatal(err)
	}
	if path != "/message" || token != "synthetic-token" || payload["title"] != "title" || payload["message"] != "body" || payload["priority"] != float64(5) {
		t.Fatalf("path=%q token=%q payload=%+v", path, token, payload)
	}
}

func TestGotifyUsesMessagePriority(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &payload)
		io.WriteString(w, `{"id":1}`)
	}))
	defer srv.Close()
	if err := (Gotify{BaseURL: srv.URL, Token: "synthetic-token", Client: srv.Client()}).Send(context.Background(), Message{Title: "urgent", Body: "body", Priority: 9}); err != nil {
		t.Fatal(err)
	}
	if payload["priority"] != float64(9) {
		t.Fatal(payload)
	}
}

func TestGotifyRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	if err := (Gotify{BaseURL: srv.URL, Token: "synthetic-token", Client: srv.Client()}).Send(context.Background(), Message{}); err == nil {
		t.Fatal("error accepted")
	}
}

func TestMultiReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	if err := (Multi{Ntfy{BaseURL: srv.URL, Topic: "t", Client: srv.Client()}}).Send(context.Background(), Message{}); err == nil {
		t.Fatal("failure hidden")
	}
}
