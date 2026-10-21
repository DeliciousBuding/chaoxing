//go:build !windows

package statelock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File) (func() error, error) {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrInUse
		}
		return nil, err
	}
	return func() error {
		return unix.Flock(int(f.Fd()), unix.LOCK_UN)
	}, nil
}
