package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

func tellReplies(show string) map[string]string {
	return map[string]string{
		"hooks show tell --json":              show,
		"hooks trust tell --check --json":     "@trust-hook-tell.json",
		"settings list --routine tell --json": "[]",
		"runs tell --limit 50 --json":         "[]",
		"memory list --routine tell --json":   "[]",
		"permissions request input-monitoring --json": `{"permission":"input-monitoring","label":"Input Monitoring","state":"missing","asked":true,"by":[],` +
			`"where":"in System Settings > Privacy & Security > Input Monitoring","settingsUrl":"x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent"}`,
		"permissions request notifications --json": `{"permission":"notifications","label":"Notifications","state":"not-checked","asked":false,"why":"it can't be asked","by":[],` +
			`"where":"in Settings > Notifications","settingsUrl":"x-test:notifications"}`,
	}
}

// The turn-on review lists each permission that's missing or not checked
// yet, with why and Grant…; unknown and unsupported ones aren't shown.
// Grant… asks through the scheduler and says what to do: answer the OS's
// prompt (nothing opens over it), or, when the OS didn't ask, switch it on
// in the settings page it opens. Turning on stays possible, saying it won't
// run until they're allowed.
func TestTheReviewAsksForPermissions(t *testing.T) {
	AppName = "Acme"
	t.Cleanup(func() { AppName = "" })
	h, f, _ := newHookFor(t, "tell", tellReplies("@hook-tell-off.json"))
	var opened []string
	h.turnOn.granter.openURL = func(u string) error { opened = append(opened, u); return nil }
	test.Tap(button(t, h.Content(), "Turn On…"))
	c := h.Content()
	mustShow(t, c, "Input Monitoring", "for its watcher home-key · Not allowed", "Notifications", "for the notify plugin · Not checked yet",
		"It won't run until Input Monitoring and Notifications are allowed.")
	mustNotShow(t, c, "Full Disk Access")
	mustNotShow(t, c, "Camera")
	if b := button(t, c, "Trust and Turn On"); b.Disabled() {
		t.Error("turning on isn't allowed while a permission is missing")
	}
	// The review's two, then the window's card behind it (its banner gives
	// way to the open review).
	if n := len(buttons(c, "Grant…")); n != 4 {
		t.Fatalf("%d Grant… buttons", n)
	}
	mustNotShow(t, c, "tell needs permissions")
	test.Tap(buttons(c, "Grant…")[0])
	f.mustHaveRun(t, "permissions request input-monitoring --json")
	if len(opened) != 0 {
		t.Fatalf("opened %q over the OS's prompt", opened)
	}
	mustShow(t, h.Content(), "Answer the prompt, or switch on Acme in System Settings > Privacy & Security > Input Monitoring.")
	test.Tap(buttons(h.Content(), "Grant…")[1])
	f.mustHaveRun(t, "permissions request notifications --json")
	if strings.Join(opened, ",") != "x-test:notifications" {
		t.Fatalf("opened %q", opened)
	}
	if f.count("hooks trust tell --check --json") < 2 {
		t.Error("the review wasn't read again after asking")
	}
}

// A row turns allowed by itself: run status changes (the scheduler
// recorded it), and the open review is read again.
func TestAPermissionTurnsAllowedOnAStatusChange(t *testing.T) {
	h, _, _ := newHookFor(t, "tell", tellReplies("@hook-tell-off.json"))
	test.Tap(button(t, h.Content(), "Turn On…"))
	mustShow(t, h.Content(), "Not allowed")
	granted := newFakeCLI(t, map[string]string{
		"hooks trust tell --check --json": "@trust-hook-tell-granted.json",
		"hooks show tell --json":          "@hook-tell-off-granted.json",
	})
	h.cli, h.turnOn.cli = granted.CLI, granted.CLI
	s := statusFrom(t, "status-hooks.json")
	s.Hooks = append(s.Hooks, cli.RoutineStatus{Name: "tell", Problem: "something changed"})
	h.Update(s)
	c := h.Content()
	mustNotShow(t, c, "Not allowed")
	mustNotShow(t, c, "It won't run until")
	if len(buttons(c, "Grant…")) != 0 {
		t.Error("Grant… for an allowed permission")
	}
}

// A window shows what it needs: a banner while a permission isn't allowed
// (where a reason about it lands), with Grant…, and every permission's
// state in Overview; unknown and unsupported ones aren't shown.
func TestAWindowShowsItsPermissions(t *testing.T) {
	h, f, _ := newHookFor(t, "tell", tellReplies("@hook-tell.json"))
	c := h.Content()
	mustShow(t, c, "tell needs a permission", "It won't run until Input Monitoring is allowed.", "Permissions",
		"for its watcher home-key · Not allowed", "for the notify plugin · Allowed")
	mustNotShow(t, c, "Full Disk Access")
	mustNotShow(t, c, "Turn On…")
	h.granter.openURL = func(string) error { return nil }
	test.Tap(buttons(c, "Grant…")[0])
	f.mustHaveRun(t, "permissions request input-monitoring --json")

	// A watcher's window too.
	w := NewWatcher(newFakeCLI(t, inboxReplies).CLI, window(t), now, "inbox", func() {}, func(string) {})
	w.Load()
	mustShow(t, w.Content(), "inbox needs a permission", "for its watcher inbox · Not allowed")
}

// Nothing's shown for permissions that can't be checked or aren't on this
// OS (Linux's usual answer).
func TestNoRowsForUnknownOrUnsupported(t *testing.T) {
	g := newGranter(cli.CLI{}, now, func() {}, func() {})
	quiet := []cli.PermissionNeed{{Permission: "full-disk-access", Label: "Full Disk Access", State: "unknown"},
		{Permission: "input-monitoring", Label: "Input Monitoring", State: "unsupported"}}
	if g.banner("x", quiet) != nil || g.card(quiet) != nil || len(g.rows(cli.BlockingPermissions(quiet))) != 0 {
		t.Fatal("rows for unknown or unsupported permissions")
	}
}

// Once a permission Grant… asked for is allowed (at once, or when what
// needs it reads its permissions again), the window comes to the front,
// once: the OS's prompt or settings page may have left it behind another
// app. One it didn't ask for, or still missing, doesn't.
func TestAnAllowedPermissionBringsTheWindowBack(t *testing.T) {
	focused := 0
	focus := Focus
	Focus = func() { focused++ }
	t.Cleanup(func() { Focus = focus })
	replies := tellReplies("@hook-tell-off.json")
	replies["permissions request camera --json"] = `{"permission":"camera","label":"Camera","state":"granted","asked":true,"by":[]}`
	f := newFakeCLI(t, replies)
	g := newGranter(f.CLI, now, func() {}, func() {})
	g.openURL = func(string) error { return nil }
	missing := []cli.PermissionNeed{{Permission: "input-monitoring", Label: "Input Monitoring", State: "missing"}}
	allowed := []cli.PermissionNeed{{Permission: "input-monitoring", Label: "Input Monitoring", State: "granted"}}

	g.saw(allowed) // not asked for
	g.grant("input-monitoring")
	g.saw(missing)
	if focused != 0 {
		t.Fatalf("focused %d times before it was allowed", focused)
	}
	g.saw(allowed)
	g.saw(allowed)
	if focused != 1 || g.notes["input-monitoring"] != "" {
		t.Fatalf("focused %d times; note %q", focused, g.notes["input-monitoring"])
	}
	g.grant("camera") // allowed in the prompt before asking returned
	if focused != 2 {
		t.Fatalf("focused %d times", focused)
	}
}
