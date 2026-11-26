//go:build unix

package instance

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes the lock on f if no one holds it.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

// terminate asks the process pid to end (SIGTERM).
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
