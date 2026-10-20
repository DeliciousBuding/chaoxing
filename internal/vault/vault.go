// Package vault stores recoverable upstream credentials encrypted at rest.
// A root-owned, separately stored key is necessary for unattended login.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Credentials struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

func Atomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func key(path string, create bool) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && create {
		b = make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return nil, err
		}
		err = Atomic(path, b)
	}
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("invalid encryption key")
	}
	return b, nil
}
func Seal(path, keyPath string, c Credentials) error {
	if c.Phone == "" || c.Password == "" {
		return errors.New("phone and password are required")
	}
	k, e := key(keyPath, true)
	if e != nil {
		return e
	}
	block, e := aes.NewCipher(k)
	if e != nil {
		return e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return e
	}
	p, e := json.Marshal(c)
	if e != nil {
		return e
	}
	defer clear(p)
	return Atomic(path, append([]byte("CXV1"), g.Seal(n, n, p, []byte("chaoxing-credentials-v1"))...))
}

// SealData protects session material using a separate authenticated purpose.
func SealData(path, keyPath string, p []byte) error {
	k, e := key(keyPath, true)
	if e != nil {
		return e
	}
	block, e := aes.NewCipher(k)
	if e != nil {
		return e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return e
	}
	return Atomic(path, append([]byte("CXS1"), g.Seal(n, n, p, []byte("chaoxing-session-v1"))...))
}
func OpenData(path, keyPath string) ([]byte, error) {
	k, e := key(keyPath, false)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(k)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if len(b) < 4+g.NonceSize()+g.Overhead() || string(b[:4]) != "CXS1" {
		return nil, errors.New("invalid session envelope")
	}
	b = b[4:]
	return g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("chaoxing-session-v1"))
}
func Open(path, keyPath string) (Credentials, error) {
	var c Credentials
	k, e := key(keyPath, false)
	if e != nil {
		return c, e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	block, e := aes.NewCipher(k)
	if e != nil {
		return c, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return c, e
	}
	if len(b) < 4+g.NonceSize()+g.Overhead() || string(b[:4]) != "CXV1" {
		return c, errors.New("invalid credential envelope")
	}
	b = b[4:]
	p, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("chaoxing-credentials-v1"))
	if e != nil {
		return c, errors.New("credential decryption failed")
	}
	defer clear(p)
	e = json.Unmarshal(p, &c)
	return c, e
}
