package sysenv

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
)

// KeepAwake stops the computer idle-sleeping until release is called, so a run
// isn't cut off halfway. It uses caffeinate tied to this process (-w), so the
// assertion can't outlive a crash. Closing the lid, or choosing Sleep, still sleeps.
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

// capabilityGraphics is the system capability a Mac has only when fully
// awake (kIOPMSystemCapabilityGraphics): in a maintenance wake ("dark wake",
// the Mac waking for a few seconds while asleep) it's off.
const capabilityGraphics = 0x2

var systemCapabilities = regexp.MustCompile(`"System Capabilities" = (\d+)`)

// FullyAwake is whether the computer is awake for work, not in a maintenance
// wake it will sleep again from in seconds. When it can't tell, it's awake.
func FullyAwake() bool {
	out, err := exec.Command("/usr/sbin/ioreg", "-n", "IOPMrootDomain", "-d1", "-r").Output()
	if err != nil {
		return true
	}
	return fullyAwake(out)
}

func fullyAwake(ioreg []byte) bool {
	m := systemCapabilities.FindSubmatch(ioreg)
	if m == nil {
		return true
	}
	caps, err := strconv.ParseUint(string(m[1]), 10, 32)
	return err != nil || caps&capabilityGraphics != 0
}
