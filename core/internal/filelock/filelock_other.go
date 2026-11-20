//go:build !unix

package filelock

import (
	"errors"
	"os"
	"runtime"
)

func lock(*os.File, bool) error {
	return errors.New("file locks aren't implemented on " + runtime.GOOS)
}

func unlock(*os.File) error { return nil }
