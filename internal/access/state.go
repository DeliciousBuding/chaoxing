// Package access provides a single-owner OAuth authorization server for MCP.
package access

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	scopeRead     = "chaoxing.read"
	scopeSign     = "chaoxing.sign"
	maxClients    = 128
	maxTokens     = 4096
	maxPending    = 128
	maxStateBytes = 2 << 20
	accessTTL     = 15 * time.Minute
	refreshTTL    = 30 * 24 * time.Hour
	clientTTL     = 90 * 24 * time.Hour
)

type client struct {
	ID        string   `json:"client_id"`
	Name      string   `json:"client_name"`
	Redirects []string `json:"redirect_uris"`
	Scope     string   `json:"scope,omitempty"`
	Expires   int64    `json:"expires"`
	Created   int64    `json:"created"`
	Approved  bool     `json:"approved"`
}
type token struct {
	Client   string `json:"client"`
	Family   string `json:"family"`
	Resource string `json:"resource,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Expires  int64  `json:"expires"`
	Created  int64  `json:"created"`
	Digest   string `json:"digest,omitempty"`
	Counter  uint64 `json:"counter,omitempty"`
	AccessID string `json:"access_id,omitempty"`
}
type diskState struct {
	Version      int               `json:"version"`
	Issuer       string            `json:"issuer"`
	RefreshKeyID string            `json:"refresh_key_id"`
	Clients      map[string]client `json:"clients"`
	Access       map[string]token  `json:"access"`
	Refresh      map[string]token  `json:"refresh"`
}

func emptyState() diskState {
	return diskState{Version: 2, Clients: map[string]client{}, Access: map[string]token{}, Refresh: map[string]token{}}
}
func randomToken() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}
func randomBytes(n int) []byte {
	b := make([]byte, n)
	// crypto/rand.Read in Go 1.26 cannot return a recoverable error.
	_, _ = rand.Read(b)
	return b
}
func digest(s string) string {
	b := sha256.Sum256([]byte(s))
	return hex.EncodeToString(b[:])
}

func normalizeScope(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return scopeRead, true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, scope := range strings.Fields(raw) {
		if scope != scopeRead && scope != scopeSign || seen[scope] {
			return "", false
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return strings.Join(out, " "), true
}

func scopeSubset(requested, allowed string) bool {
	if requested == "" {
		return true
	}
	allowedSet := map[string]bool{}
	for _, scope := range strings.Fields(allowed) {
		allowedSet[scope] = true
	}
	for _, scope := range strings.Fields(requested) {
		if !allowedSet[scope] {
			return false
		}
	}
	return true
}
func (s *Server) pruneLocked() {
	now := time.Now().Unix()
	for k, v := range s.state.Clients {
		if v.Expires <= now {
			delete(s.state.Clients, k)
		}
	}
	for k, v := range s.state.Access {
		if v.Expires <= now {
			delete(s.state.Access, k)
		}
	}
	for k, v := range s.state.Refresh {
		if v.Expires <= now {
			delete(s.state.Access, v.AccessID)
			delete(s.state.Refresh, k)
		}
	}
	for k, v := range s.sessions {
		if v.expires <= now {
			delete(s.sessions, k)
		}
	}
	for k, v := range s.pending {
		if v.expires <= now {
			delete(s.pending, k)
		}
	}
	for k, v := range s.codes {
		if v.expires <= now {
			delete(s.codes, k)
		}
	}
	for k, v := range s.logins {
		if v <= now {
			delete(s.logins, k)
		}
	}
}

// saveLocked commits an atomic snapshot; on failure memory is restored from the
// last successful snapshot. Callers must hold mu across mutation and save.
func (s *Server) saveLocked() error {
	b, err := json.Marshal(s.state)
	if err == nil && len(b) > maxStateBytes {
		err = errors.New("OAuth state capacity exceeded")
	}
	if err == nil {
		err = atomicWrite(s.path, b)
	}
	if err != nil {
		s.state = emptyState()
		_ = json.Unmarshal(s.committed, &s.state)
		return err
	}
	s.committed = b
	return nil
}
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".oauth-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		d, err := os.Open(filepath.Dir(path))
		if err == nil {
			_ = d.Sync()
			_ = d.Close()
		}
	}
	return nil
}
func loadState(path string) (diskState, error) {
	st := emptyState()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxStateBytes {
		return st, errors.New("invalid OAuth state file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return st, errors.New("OAuth state must have mode 0600")
	}
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil || len(b) > maxStateBytes {
		return st, errors.New("OAuth state exceeds size limit")
	}
	if err = uniqueJSON(b); err != nil {
		return st, err
	}
	st = diskState{}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&st); err != nil {
		return st, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return st, errors.New("trailing OAuth state data")
	}
	if st.Version != 2 {
		return st, errors.New("unsupported OAuth state version; legacy state requires explicit offline reset")
	}
	if st.Clients == nil || st.Access == nil || st.Refresh == nil || len(st.Clients) > maxClients || len(st.Access) > maxTokens || len(st.Refresh) > maxTokens || !hashShape(st.RefreshKeyID) {
		return st, errors.New("invalid OAuth state version or bounds")
	}
	if !validIssuer(st.Issuer) {
		return st, errors.New("OAuth state issuer is missing or invalid")
	}
	for id, c := range st.Clients {
		if !randomIDShape(id) || c.ID != id || len(c.Name) > 120 || len(c.Redirects) < 1 || len(c.Redirects) > 8 || !lifetimeOK(c.Created, c.Expires, clientTTL) {
			return st, errors.New("invalid persisted OAuth client")
		}
		for _, uri := range c.Redirects {
			if !validRedirect(uri) {
				return st, errors.New("invalid persisted OAuth redirect")
			}
		}
	}
	for index, tokens := range []map[string]token{st.Access, st.Refresh} {
		ttl := accessTTL
		if index == 1 {
			ttl = refreshTTL
		}
		for id, t := range tokens {
			validID := hashShape(id)
			if index == 1 {
				validID = randomIDShape(id) && id == t.Family && hashShape(t.Digest) && hashShape(t.AccessID)
			}
			if !validID || (!randomIDShape(t.Client) && !validCIMD(t.Client)) || !randomIDShape(t.Family) || !lifetimeOK(t.Created, t.Expires, ttl) || (t.Resource != "" && t.Resource != st.Issuer+"/mcp") {
				return st, errors.New("invalid persisted OAuth token")
			}
		}
	}
	for id, t := range st.Access {
		f, ok := st.Refresh[t.Family]
		if t.Digest != "" || t.Counter != 0 || t.AccessID != "" || !ok || f.AccessID != id || f.Client != t.Client {
			return st, errors.New("invalid persisted access family binding")
		}
	}
	for _, t := range st.Refresh {
		if a, ok := st.Access[t.AccessID]; ok && (a.Family != t.Family || a.Client != t.Client) {
			return st, errors.New("invalid persisted refresh family binding")
		}
	}
	return st, nil
}

func lifetimeOK(created, expires int64, ttl time.Duration) bool {
	// A synchronized clock is required at startup and during token issuance.
	return created > 0 && created <= time.Now().Add(5*time.Minute).Unix() && expires > created && expires-created <= int64(ttl.Seconds())
}

func randomIDShape(raw string) bool {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(raw) == 43 && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == raw
}
func hashShape(raw string) bool {
	b, err := hex.DecodeString(raw)
	return err == nil && len(b) == 32 && raw == strings.ToLower(raw)
}

// Reject ambiguous duplicate keys and deeply nested input before decoding state.
func uniqueJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 8 {
			return errors.New("OAuth state nesting limit exceeded")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return errors.New("duplicate OAuth state key")
					}
					seen[name] = true
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
			default:
				return errors.New("invalid OAuth state JSON")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing OAuth state JSON")
	}
	return nil
}
