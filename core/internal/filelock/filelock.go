// Package filelock provides exclusive, cross-process file locks. The OS-specific
// part lives in per-OS files.
package filelock

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrHeld is returned by a non-blocking Lock when another process holds the lock.
var ErrHeld = errors.New("lock is held by another process")

// Lock takes an exclusive lock on path (created if needed). With wait=false it
// returns ErrHeld instead of waiting. The returned func releases it.
func Lock(path string, wait bool) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lock(f, wait); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unlock(f)
		_ = f.Close()
	}, nil
}
