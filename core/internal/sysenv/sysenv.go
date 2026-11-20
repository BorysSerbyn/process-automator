// Package sysenv adapts the process to the machine it runs on: the user's real
// PATH, and keeping the computer awake while work is in progress. The OS-specific
// parts live in per-OS files; elsewhere these are no-ops.
package sysenv

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// SaveLoginShellPath runs the login shell for its PATH (slow: profiles can
// take seconds), adds it to this process's PATH and saves it to cache, for
// UseSavedLoginShellPath in later processes. A shell that fails saves an empty
// PATH, so later commands don't each pay for it again; the next call here
// (the scheduler starting, or a tool not being found) tries again.
func SaveLoginShellPath(cache string) error {
	shellPath, shellErr := loginShellPath()
	if err := atomicfile.Write(cache, []byte(shellPath)); err != nil {
		return err
	}
	if shellErr != nil {
		return shellErr
	}
	return os.Setenv("PATH", mergePath(os.Getenv("PATH"), shellPath))
}

// UseSavedLoginShellPath adds the login shell PATH saved in cache to this
// process's PATH: cheap, for every command, so one started by an app (with
// the OS's minimal PATH) finds the same tools as one typed in a terminal.
// With no cache yet and resolve set, it runs the login shell once and saves it.
func UseSavedLoginShellPath(cache string, resolve bool) error {
	b, err := os.ReadFile(cache)
	switch {
	case err == nil:
		return os.Setenv("PATH", mergePath(os.Getenv("PATH"), strings.TrimSpace(string(b))))
	case errors.Is(err, fs.ErrNotExist) && resolve:
		return SaveLoginShellPath(cache)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return err
	}
}

// mergePath appends extra's entries that current doesn't have.
func mergePath(current, extra string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(filepath.SplitList(current), filepath.SplitList(extra)...) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
