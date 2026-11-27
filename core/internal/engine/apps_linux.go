package engine

import (
	"os/exec"
	"syscall"
)

// openApp starts an executable in the background, in a session of its own
// so it outlives this process (tests replace it).
var openApp = func(path string) error {
	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
