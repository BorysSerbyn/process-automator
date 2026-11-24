package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// TestMain keeps every test from asking the OS about permissions (and a
// daemon a test starts from the login shell and the plan's usage): a test
// that needs answers fakes them (fakePermissions). Started as the fresh
// check's child (PermissionsProbeEnv), the test binary answers with fixed
// statuses instead.
func TestMain(m *testing.M) {
	if os.Getenv(PermissionsProbeEnv) != "" {
		checkPermission = func(name string) permissions.Status {
			if name == "camera" {
				return permissions.Missing
			}
			return permissions.Granted
		}
		_ = PrintPermissionsProbe(os.Stdout)
		os.Exit(0)
	}
	checkAll = checkHere
	// Nor runs the real claude: a stand-in is first on PATH, and its version
	// is testClaudeVersion's, without running it.
	undo := fakeClaudeOnPath()
	// A daemon started by a test never runs the login shell or reads the
	// plan's usage (Claude Code's sign-in).
	saveLoginPath = func(string) error { return nil }
	refreshUsage = func(context.Context, *App) error { return nil }
	// Nor checks for updates or installs one.
	autoUpdates = false
	updateCommand = func(*App, []string) (*exec.Cmd, error) { return nil, errors.New("tests never install updates") }
	checkPermission = func(string) permissions.Status { return permissions.Unsupported }
	canRequestPermission = func(string) (bool, string) { return false, "tests don't ask the OS" }
	requestPermission = func(string) {}
	code := m.Run()
	undo()
	os.Exit(code)
}

// testClaudeVersion is the version the stand-in claude reports ("" fails).
var testClaudeVersion = agent.NewestClaudeCode().Since().String()

// fakeClaudeOnPath puts a claude that only fails first on PATH, and has
// --version answered without running it (testClaudeVersion).
func fakeClaudeOnPath() (undo func()) {
	dir, err := os.MkdirTemp("", "fake-claude-")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\necho 'tests never run claude' >&2\nexit 1\n"), 0o700); err != nil {
		panic(err)
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	restore := agent.SetClaudeVersionForTests(func() (string, error) {
		if testClaudeVersion == "" {
			return "", errors.New("claude --version failed")
		}
		return testClaudeVersion, nil
	})
	return func() { restore(); _ = os.RemoveAll(dir) }
}
