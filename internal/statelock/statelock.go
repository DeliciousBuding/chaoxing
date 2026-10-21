// Package statelock prevents two daemon processes from sharing one state
// directory. The lock is advisory but mandatory for this daemon because the
// watcher state file and the sign-in scheduler are single-writer designs.
package statelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrInUse means another process currently owns the state directory.
var ErrInUse = errors.New("state directory is already in use")

type Lock struct {
	file   *os.File
	unlock func() error
}

// Acquire creates or opens the per-state-directory lock and holds an
// exclusive non-blocking file lock until Close.
func Acquire(dir string) (*Lock, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("state directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state directory unavailable: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("state directory is not a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("state directory permissions must be 0700 (got %04o)", info.Mode().Perm())
	}
	path := filepath.Join(dir, ".chaoxing.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("state lock unavailable: %w", err)
	}
	unlock, err := lockFile(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("state lock failed: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = unlock()
		_ = f.Close()
		return nil, fmt.Errorf("state lock mode failed: %w", err)
	}
	return &Lock{file: f, unlock: unlock}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	var unlockErr error
	if l.unlock != nil {
		unlockErr = l.unlock()
	}
	closeErr := l.file.Close()
	l.file = nil
	l.unlock = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
