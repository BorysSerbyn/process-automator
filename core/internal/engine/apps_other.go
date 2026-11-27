//go:build !darwin && !linux

package engine

import (
	"errors"
	"runtime"
)

// openApp isn't implemented here.
var openApp = func(string) error { return errors.New("can't open apps on " + runtime.GOOS) }
