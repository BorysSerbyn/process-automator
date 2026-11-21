package cli

import (
	"os/exec"
	"syscall"
)

// endWithParent has the kernel stop cmd when the tray goes, however it goes.
func endWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
