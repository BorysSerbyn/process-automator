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

// runShell isn't implemented on this OS.
func runShell(string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("shell commands aren't supported on " + runtime.GOOS)
}
