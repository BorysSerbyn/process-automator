package sysenv

import (
	"os"
	"os/exec"
	"strconv"
)

// KeepAwake stops the computer idle-sleeping until release is called, so a run
// isn't cut off halfway. It uses caffeinate tied to this process (-w), so the
// assertion can't outlive a crash. Closing the lid still sleeps.
func KeepAwake() (release func()) {
	cmd := exec.Command("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}
