package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
	"github.com/DeliciousBuding/chaoxing/internal/mcphttp"
)

func ownerHash(s string) (string, error) { return access.HashPassword(s) }
func serve() error {
	c, e := load()
	if e != nil {
		return e
	}
	if !loopback(c.Listen) || !loopback(c.LocalListen) || c.Listen == c.LocalListen {
		return errors.New("public and management listeners must be distinct loopback addresses")
	}
	x, e := c.client()
	if e != nil {
		return e
	}
	a, e := access.New(access.Config{Issuer: c.Issuer, StateDir: filepath.Join(c.StateDir, "oauth"), OwnerPasswordHash: c.OwnerPasswordHash})
	if e != nil {
		return e
	}
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/mcp", a.Protect(mcphttp.New(x.Call)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Chaoxing Read-only MCP</title><h1>Chaoxing Read-only MCP</h1><p>Connect your MCP client to /mcp using OAuth. This service does not provide public course data.</p></html>`))
	})
	local := http.NewServeMux()
	slots := make(chan struct{}, 2)
	local.HandleFunc("POST /call", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.LocalToken)) != 1 {
			w.WriteHeader(401)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.WriteHeader(429)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		var in request
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		v, e := x.Call(ctx, in.Tool, in.Arguments)
		if e != nil {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": e.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": v})
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
