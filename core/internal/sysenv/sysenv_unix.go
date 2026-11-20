//go:build unix

package sysenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	startMarker = "__PROCAUTO_PATH__"
	endMarker   = "__PROCAUTO_END__"
)

// loginShellPath runs the user's interactive login shell once and reads its
// PATH. Markers separate it from anything profiles print. Profiles can be slow
// (nvm, pyenv, brew calls: ~1.5s, more on a busy machine), so the timeout is
// generous; it only guards against profiles that wait for input.
func loginShellPath() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-ilc", `printf '%s%s%s' '`+startMarker+`' "$PATH" '`+endMarker+`'`)
	// A profile may start something that keeps its output open: stop waiting
	// for it shortly after the shell itself is done.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	s := string(out)
	start, end := strings.LastIndex(s, startMarker), strings.LastIndex(s, endMarker)
	if start < 0 || end < start {
		return "", errors.New("login shell printed no PATH")
	}
	return s[start+len(startMarker) : end], nil
}
