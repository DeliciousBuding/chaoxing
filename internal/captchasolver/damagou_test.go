package captchasolver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDamaGouSolvesCoordinate(t *testing.T) {
	var gotType, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotType, gotKey = r.PostFormValue("type"), r.PostFormValue("userkey")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"0","data":"120"}`))
	}))
	defer server.Close()
	solver, err := NewDamaGou("synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	solver.Endpoint = server.URL
	x, err := solver.Solve(context.Background(), []byte("image-bytes"), []byte("cutout-bytes"))
	if err != nil || x != 120 {
		t.Fatalf("x=%d err=%v", x, err)
	}
	if gotType != "1004" || gotKey != "synthetic-key" {
		t.Fatalf("type=%q key=%q", gotType, gotKey)
	}
}

func TestDamaGouRejectsBadCoordinate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"0","data":"300"}`))
	}))
	defer server.Close()
	solver, err := NewDamaGou("synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	solver.Endpoint = server.URL
	if _, err = solver.Solve(context.Background(), []byte("image-bytes"), []byte("cutout-bytes")); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err=%v", err)
	}
}
