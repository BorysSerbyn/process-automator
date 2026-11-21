//go:build unix

package cli

import (
	"io"
	"os/exec"
	"syscall"
)

// detach makes cmd outlive this process's session (and whatever starts or
// stops it, such as launchd restarting the app).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// runShell runs a shell command with the given stdin, stdout and stderr,
// and waits for it.
func runShell(command string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}
