//go:build unix

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// The CLI asks the OS nothing: it shows what the scheduler recorded. A
// hook's review lists the permissions it needs, its action's and its
// watcher's, with their state ("not checked" before the scheduler has
// checked, never granted); one that isn't allowed is a doctor finding
// naming it, and the list says what needs what.
func TestPermissionsInTheCLI(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	cfg := a.Layout.Config()
	watcher := hookFiles(t, watchers.DescriptionFile, "executable: ./watch\nmode: stream\npermissions: [input-monitoring]\n", "watch", "sleep 60\n")
	program := hookFiles(t, hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: perm-keys}\npermissions: [accessibility]\n", "act", "cat\n")
	cliJSON[WatcherChange](t, cfg, "watchers", "add", "perm-keys", "--from", watcher)
	cliJSON[HookChange](t, cfg, "hooks", "add", "perm-click", "--from", program)

	review := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "perm-click", "--check")
	if len(review.Permissions) != 2 || review.Permissions[0].Permission != "input-monitoring" ||
		review.Permissions[0].By[0] != (engine.PermissionSource{Kind: "watcher", Name: "perm-keys"}) ||
		review.Permissions[1].Permission != "accessibility" || review.Permissions[1].State != permissions.NotChecked {
		t.Fatalf("the review's permissions: %+v", review.Permissions)
	}
	if out, _ := cli(t, cfg, "hooks", "trust", "perm-click", "--check"); !strings.Contains(out, "It needs these permissions") ||
		!strings.Contains(out, "Input Monitoring") || !strings.Contains(out, "not checked yet") {
		t.Fatalf("the review in words: %s", out)
	}
	if w := cliJSON[WatcherTrustOutput](t, cfg, "watchers", "trust", "perm-keys", "--check"); len(w.Permissions) != 1 {
		t.Fatalf("a watcher's review: %+v", w.Permissions)
	}

	// What the scheduler found: input monitoring isn't allowed.
	if err := permissions.Update(a.Layout.Permissions(), func(s *permissions.State) {
		s.CheckedAt = time.Now()
		s.Permissions = map[string]permissions.Status{"input-monitoring": permissions.Missing, "accessibility": permissions.Granted}
	}); err != nil {
		t.Fatal(err)
	}
	on := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "perm-click", "--yes", "--resume")
	if !on.Trusted || on.Permissions[0].State != permissions.Missing || on.Permissions[1].State != permissions.Granted {
		t.Fatalf("turned on: %+v", on.Permissions)
	}
	// doctor fails with its findings: one names the permission.
	if out, err := cli(t, cfg, "doctor", "--json"); err == nil || !strings.Contains(out, `"permission": "input-monitoring"`) ||
		!strings.Contains(out, "its watcher perm-keys needs the Input Monitoring permission") {
		t.Fatalf("doctor: %v %s", err, out)
	}
	if hl := cliJSON[[]HookInfo](t, cfg, "hooks", "list"); len(hl) != 1 || !strings.Contains(hl[0].Problem, "Input Monitoring") || len(hl[0].Permissions) != 2 {
		t.Fatalf("hooks list: %+v", hl)
	}

	list := cliJSON[engine.PermissionReport](t, cfg, "permissions", "list")
	if list.CheckedAt == nil || len(list.Permissions) != 2 {
		t.Fatalf("the list: %+v", list)
	}
	im := list.Permissions[0]
	if im.Permission != "input-monitoring" || len(im.NeededBy) != 2 || im.NeededBy[0].Kind != "hook" || im.NeededBy[1].Kind != "watcher" {
		t.Fatalf("what needs input monitoring: %+v", im)
	}
	if all := cliJSON[engine.PermissionReport](t, cfg, "permissions", "list", "--all"); len(all.Permissions) != len(permissions.All) {
		t.Fatalf("--all: %d", len(all.Permissions))
	}

	// No scheduler running: nothing is asked, and it says why.
	req := cliJSON[engine.PermissionRequest](t, cfg, "permissions", "request", "input-monitoring")
	if req.Asked || !strings.Contains(req.Why, "scheduler isn't running") || req.State != permissions.Missing {
		t.Fatalf("a request without the scheduler: %+v", req)
	}
	if _, err := cli(t, cfg, "permissions", "request", "keyboard"); err == nil || !strings.Contains(err.Error(), "unknown permission") {
		t.Fatalf("an unknown permission: %v", err)
	}
}

// A server added by URL or command declares its permissions with
// --permission; an unknown one is refused before anything is tried.
func TestPluginsAddRefusesUnknownPermissions(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	if _, err := cli(t, a.Layout.Config(), "plugins", "add", "keys", "--command", "/bin/true", "--permission", "keyboard"); err == nil ||
		!strings.Contains(err.Error(), `unknown permission "keyboard"`) {
		t.Fatalf("%v", err)
	}
}
