//go:build !unix

package programs

import "os/exec"

// newGroup: without process groups, only the program itself is ended.
func newGroup(*exec.Cmd) {}

// signalGroup kills the program (there's no asking it first).
func signalGroup(cmd *exec.Cmd, _ bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
