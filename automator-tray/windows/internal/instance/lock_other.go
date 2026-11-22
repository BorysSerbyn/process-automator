//go:build !unix

package instance

import "os"

// tryLock: windows run on Linux (and macOS for development); elsewhere
// every process holds its own window.
func tryLock(*os.File) (bool, error) { return true, nil }
