//go:build !unix

package cli

import (
	"errors"
	"io"
	"os/exec"
	"runtime"
)

// detach does nothing special on this OS.
func detach(*exec.Cmd) {}

// startShell isn't implemented on this OS.
func startShell(string, []string) error {
	return errors.New("click commands aren't supported on " + runtime.GOOS)
}

// runShell isn't implemented on this OS.
func runShell(string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("shell commands aren't supported on " + runtime.GOOS)
}
