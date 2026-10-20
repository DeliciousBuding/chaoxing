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

func TestMultiReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	if err := (Multi{Ntfy{BaseURL: srv.URL, Topic: "t", Client: srv.Client()}}).Send(context.Background(), Message{}); err == nil {
		t.Fatal("failure hidden")
	}
}
