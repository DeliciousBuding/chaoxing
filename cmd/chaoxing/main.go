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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/vault"
)

var version = "dev"

type config struct {
	StateDir          string `json:"state_dir"`
	KeyPath           string `json:"key_path"`
	Issuer            string `json:"issuer"`
	Listen            string `json:"listen"`
	LocalListen       string `json:"local_listen"`
	LocalToken        string `json:"local_token"`
	OwnerPasswordHash string `json:"owner_password_hash"`
}
type request struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

func main() {
	if e := run(); e != nil {
		f := &upstream.Fault{Code: "ERROR", Message: e.Error()}
		var v *upstream.Fault
		if errors.As(e, &v) {
			f = v
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "error": f})
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
	b, e := os.ReadFile(configFile())
	if e != nil {
		return c, errors.New("configuration unavailable")
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, errors.New("configuration invalid")
	}
	return c, nil
}
func (c config) client() (*upstream.Client, error) {
	return upstream.New(upstream.Config{CredentialsPath: filepath.Join(c.StateDir, "credentials.sealed"), KeyPath: c.KeyPath, SessionPath: filepath.Join(c.StateDir, "session.sealed")})
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
	if e != nil {
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
		return errors.New("usage: chaoxing version | configure | query | call | serve")
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
		x, e := c.client()
		if e != nil {
			return e
		}
		r, e := readRequest()
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		v, e := x.Call(ctx, r.Tool, r.Arguments)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "data": v})
	case "call":
		c, e := load()
		if e != nil {
			return e
		}
		r, e := readRequest()
		if e != nil {
			return e
		}
		b, _ := json.Marshal(r)
		req, e := http.NewRequest("POST", "http://"+c.LocalListen+"/call", strings.NewReader(string(b)))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+c.LocalToken)
		h := http.Client{Timeout: 100 * time.Second}
		resp, e := h.Do(req)
		if e != nil {
			return errors.New("local daemon unavailable")
		}
		defer resp.Body.Close()
		b, e = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if e != nil {
			return errors.New("local response failed")
		}
		if _, e = os.Stdout.Write(b); e != nil {
			return e
		}
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		return nil
	case "serve":
		return serve()
	default:
		return errors.New("unknown command")
	}
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
	c := config{*state, *key, *issuer, "127.0.0.1:8792", "127.0.0.1:8793", randomToken(), hash}
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
