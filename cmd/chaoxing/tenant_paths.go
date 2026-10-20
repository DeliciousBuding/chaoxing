package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolve existing ancestors too: new directories below a symlink must not
// bypass isolation just because EvalSymlinks cannot resolve the final path.
func canonicalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("private state path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	probe := abs
	tail := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			if runtime.GOOS == "windows" {
				resolved = strings.ToLower(resolved)
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", errors.New("private state path cannot be resolved")
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", errors.New("private state path cannot be resolved")
		}
		tail = append(tail, filepath.Base(probe))
		probe = parent
	}
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func validateTenantPaths(states, keys []string) error {
	for _, paths := range [][]string{states, keys} {
		for i, path := range paths {
			resolved, err := canonicalPath(path)
			if err != nil {
				return err
			}
			paths[i] = resolved
		}
	}
	for i := range states {
		for j := 0; j < i; j++ {
			if within(states[i], states[j]) || within(states[j], states[i]) || keys[i] == keys[j] {
				return errors.New("tenant state paths overlap or keys are shared")
			}
			if within(states[i], keys[j]) || within(states[j], keys[i]) {
				return errors.New("tenant key belongs to another account state")
			}
		}
	}
	// Existing hard-linked key files are aliases even when their paths differ.
	for i, key := range keys {
		info, err := os.Stat(key)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, other := range keys[:i] {
			st, err := os.Stat(other)
			if err == nil && os.SameFile(info, st) {
				return errors.New("tenant keys alias the same file")
			}
		}
	}
	return nil
}
