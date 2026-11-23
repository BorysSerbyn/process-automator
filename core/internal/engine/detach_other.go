//go:build !unix

package engine

import "os/exec"

// startDetached starts cmd and lets it go.
func startDetached(cmd *exec.Cmd, _ string) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
