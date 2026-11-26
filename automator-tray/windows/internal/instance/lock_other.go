//go:build !unix

package instance

import (
	"errors"
	"os"
)

// tryLock: windows run on Linux (and macOS for development); elsewhere
// every process holds its own window.
func tryLock(*os.File) (bool, error) { return true, nil }

// terminate: there's no other holder to end here.
func terminate(int) error { return errors.New("not supported on this OS") }
