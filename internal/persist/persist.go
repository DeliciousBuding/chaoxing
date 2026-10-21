// Package persist owns atomic private state-file replacement.
package persist

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Write replaces path atomically after flushing the complete temporary file.
func Write(path string, data []byte, perm fs.FileMode) error {
	if path == "" {
		return errors.New("state path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state directory unavailable: %w", err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("state file create failed: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(perm); err != nil {
		_ = f.Close()
		return fmt.Errorf("state file mode failed: %w", err)
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("state file write failed: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("state file close failed: %w", closeErr)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("state file replace failed: %w", err)
	}
	// Persist the directory entry so a power loss cannot leave the rename
	// visible in memory but absent after reboot. Some filesystems do not
	// support directory fsync; those are safe to treat as best effort.
	d, err := os.Open(dir)
	if err == nil {
		syncErr := d.Sync()
		_ = d.Close()
		if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) && !errors.Is(syncErr, syscall.ENOTSUP) {
			return fmt.Errorf("state directory sync failed: %w", syncErr)
		}
	}
	return nil
}
