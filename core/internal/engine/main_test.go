package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

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
	os.Exit(m.Run())
}
