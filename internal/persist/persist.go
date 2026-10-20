// Package persist owns atomic private state-file replacement.
package persist

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("state file create failed: %w", err)
	}
	if err = f.Chmod(perm); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("state file mode failed: %w", err)
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state file write failed: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state file close failed: %w", closeErr)
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state file replace failed: %w", err)
	}
	return nil
}
