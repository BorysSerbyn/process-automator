//go:build !linux

package cli

import "os/exec"

// endWithParent: only Linux can tie a child's life to its parent's; the
// watch ends at its next write once the tray is gone.
func endWithParent(*exec.Cmd) {}
