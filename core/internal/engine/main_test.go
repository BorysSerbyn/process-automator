package engine

import (
	"os"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// TestMain keeps every test from asking the OS about permissions: a test
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
	checkPermission = func(string) permissions.Status { return permissions.Unsupported }
	canRequestPermission = func(string) (bool, string) { return false, "tests don't ask the OS" }
	requestPermission = func(string) {}
	os.Exit(m.Run())
}
