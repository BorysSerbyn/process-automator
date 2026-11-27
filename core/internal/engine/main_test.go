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
	// Nor runs the real claude or codex: stand-ins are first on PATH, their
	// versions are testClaudeVersion's and testCodexVersion's, answered
	// without running them, and Codex's sign-in check says it's signed in.
	undo := fakeCLIsOnPath()
	CodexLoginStatus = func(context.Context, string, agent.CodexProtocol) (string, error) { return "", nil }
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
	// Nor looks for or opens an app plugins need.
	SetAppsForTests(func(string) bool { return false }, func(string) error { return errors.New("tests don't open apps") })
	code := m.Run()
	undo()
	os.Exit(code)
}

// testClaudeVersion and testCodexVersion are the versions the stand-in
// claude and codex report ("" fails).
var (
	testClaudeVersion = agent.NewestClaudeCode().Since().String()
	testCodexVersion  = agent.NewestCodex().Since().String()
)

// testVersion answers `<name> --version` for the stand-ins.
func testVersion(name string) (string, error) {
	v := map[string]string{"claude": testClaudeVersion, "codex": testCodexVersion}[name]
	if v == "" {
		return "", errors.New(name + " --version failed")
	}
	return v, nil
}

// fakeCLIsOnPath puts a claude and a codex that only fail first on PATH,
// and has --version answered without running them (testVersion).
func fakeCLIsOnPath() (undo func()) {
	dir, err := os.MkdirTemp("", "fake-clis-")
	if err != nil {
		panic(err)
	}
	for _, name := range []string{"claude", "codex"} {
		script := "#!/bin/sh\necho 'tests never run " + name + "' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			panic(err)
		}
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	restore := agent.SetVersionForTests(testVersion)
	return func() { restore(); _ = os.RemoveAll(dir) }
}
