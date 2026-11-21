//go:build unix

package tray

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a session of its own, so it outlives the tray (a
// terminal it opened stays when the tray quits or restarts).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
