//go:build unix

package programs

import (
	"os/exec"
	"syscall"
)

// newGroup starts cmd in a process group of its own, so it and whatever it
// starts end together.
func newGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup asks cmd's process group to end (SIGTERM), or kills it.
func signalGroup(cmd *exec.Cmd, kill bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
