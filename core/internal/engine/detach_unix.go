//go:build unix && !linux

package engine

import (
	"os/exec"
	"syscall"
)

// startDetached starts cmd in a session of its own, so it outlives this
// process and whatever stops it (launchd restarting the app).
func startDetached(cmd *exec.Cmd, _ string) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
