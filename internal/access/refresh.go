package access

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Refresh proofs are not JWTs. The MAC authenticates the family, sequence and
// random nonce, so replay can be recognized without keeping used-token history.
func (s *Server) signRefresh(family string, counter uint64) string {
	payload := "rt1." + family + "." + strconv.FormatUint(counter, 10) + "." + randomToken()
	return payload + "." + base64.RawURLEncoding.EncodeToString(s.refreshMAC(payload))
}
func (s *Server) refreshMAC(payload string) []byte {
	m := hmac.New(sha256.New, s.refreshKey)
	_, _ = m.Write([]byte("chaoxing-refresh-v1\x00" + s.cfg.Issuer + "\x00" + payload))
	return m.Sum(nil)
}
func (s *Server) verifyRefresh(raw string) (string, uint64, bool) {
	if len(raw) > 256 {
		return "", 0, false
	}
	p := strings.Split(raw, ".")
	if len(p) != 5 || p[0] != "rt1" || !randomIDShape(p[1]) || !randomIDShape(p[3]) {
		return "", 0, false
	}
	n, err := strconv.ParseUint(p[2], 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != p[2] {
		return "", 0, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(p[4])
	if err != nil || len(sig) != 32 || base64.RawURLEncoding.EncodeToString(sig) != p[4] || !hmac.Equal(sig, s.refreshMAC(strings.Join(p[:4], "."))) {
		return "", 0, false
	}
	return p[1], n, true
}
func loadRefreshKey(path string, required bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if required {
			return nil, errors.New("OAuth refresh proof key is missing; refusing to replace it")
		}
		key := randomBytes(32)
		if err := atomicWrite(path, key); err != nil {
			return nil, err
		}
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != 32 || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		return nil, errors.New("invalid OAuth refresh proof key file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid OAuth refresh proof key length")
	}
	return key, nil
}
