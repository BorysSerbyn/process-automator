package sysenv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// On Linux, input-monitoring is read access to the input devices; the rest
// isn't checked, never taken for missing.
func TestLinuxPermissions(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := t.TempDir()
	orig := inputDevices
	inputDevices = filepath.Join(dir, "event*")
	t.Cleanup(func() { inputDevices = orig })
	if got := CheckPermission("input-monitoring"); got != permissions.Unknown {
		t.Fatalf("no devices: %s", got)
	}
	dev := filepath.Join(dir, "event0")
	if err := os.WriteFile(dev, nil, 0o200); err != nil {
		t.Fatal(err)
	}
	if got := CheckPermission("input-monitoring"); got != permissions.Missing {
		t.Fatalf("unreadable: %s", got)
	}
	if err := os.Chmod(dev, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CheckPermission("input-monitoring"); got != permissions.Granted {
		t.Fatalf("readable: %s", got)
	}
	if CheckPermission("notifications") != permissions.Unsupported || CheckPermission("accessibility") != permissions.Unsupported {
		t.Fatal("the rest isn't checked on Linux")
	}
	if where, url := PermissionPane("input-monitoring"); where == "" || url != "" {
		t.Fatalf("%q %q", where, url)
	}
}
