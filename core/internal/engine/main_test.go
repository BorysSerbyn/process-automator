package engine

import (
	"os"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// TestMain keeps every test from asking the OS about permissions: a test
// that needs answers fakes them (fakePermissions).
func TestMain(m *testing.M) {
	checkPermission = func(string) permissions.Status { return permissions.Unsupported }
	canRequestPermission = func(string) (bool, string) { return false, "tests don't ask the OS" }
	requestPermission = func(string) {}
	os.Exit(m.Run())
}
