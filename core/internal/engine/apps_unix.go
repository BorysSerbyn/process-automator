//go:build unix

package engine

import "os/exec"

// appRunning reports whether a process is named process (tests replace it).
var appRunning = func(process string) bool { return exec.Command("pgrep", "-x", process).Run() == nil }
