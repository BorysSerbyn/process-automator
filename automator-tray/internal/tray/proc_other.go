//go:build !unix

package tray

import "os/exec"

// detach: nothing to do where the tray doesn't run.
func detach(*exec.Cmd) {}
