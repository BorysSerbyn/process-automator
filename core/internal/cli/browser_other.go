//go:build !darwin && !linux

package cli

import (
	"errors"
	"runtime"
)

// openExternal isn't implemented on this OS; callers print the URL instead.
var openExternal = func(string) error { return errors.New("can't open files or URLs on " + runtime.GOOS) }

// revealFile isn't implemented on this OS; callers print the path instead.
var revealFile = func(string) error { return errors.New("can't show files on " + runtime.GOOS) }
