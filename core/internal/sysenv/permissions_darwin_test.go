package sysenv

import (
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// Every permission has a System Settings pane that opens it.
func TestDarwinPermissionPanes(t *testing.T) {
	for _, p := range permissions.All {
		where, url := PermissionPane(p.Name)
		if !strings.HasPrefix(where, "in System Settings > ") || !strings.HasPrefix(url, "x-apple.systempreferences:") {
			t.Errorf("%s: %q %q", p.Name, where, url)
		}
	}
	if _, url := PermissionPane("input-monitoring"); url != "x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent" {
		t.Errorf("input monitoring: %s", url)
	}
}
