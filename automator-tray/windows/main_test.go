package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/ui"
)

// The arguments name one window: a kind, and a routine's name for a
// routine's window; anything else is refused.
func TestTheArgumentsNameAWindow(t *testing.T) {
	for _, c := range []struct {
		args []string
		want tray.Window
		ok   bool
	}{
		{[]string{"setup"}, tray.Window{Kind: tray.SetupWindow}, true},
		{[]string{"routines"}, tray.Window{Kind: tray.RoutinesWindow}, true},
		{[]string{"routine", "standup"}, tray.Window{Kind: tray.RoutineWindow, Routine: "standup"}, true},
		{[]string{"hooks"}, tray.Window{Kind: tray.HooksWindow}, true},
		{[]string{"acknowledgements"}, tray.Window{Kind: tray.AcknowledgementsWindow}, true},
		{[]string{"hook", "open-meet"}, tray.Window{Kind: tray.HookWindow, Routine: "open-meet"}, true},
		{[]string{"watcher", "cal"}, tray.Window{Kind: tray.WatcherWindow, Routine: "cal"}, true},
		{[]string{"hook"}, tray.Window{}, false},
		{[]string{"hooks", "x"}, tray.Window{}, false},
		{[]string{"update"}, tray.Window{Kind: tray.UpdateWindow}, true},
		{[]string{"confirm-update"}, tray.Window{Kind: tray.ConfirmUpdateWindow}, true},
		{[]string{"routine"}, tray.Window{}, false},
		{[]string{"settings", "x"}, tray.Window{}, false},
		{[]string{"nope"}, tray.Window{}, false},
		{nil, tray.Window{}, false},
	} {
		got, err := window(c.args)
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Errorf("%q: %+v %v", c.args, got, err)
		}
	}
}

// One window per kind, one per routine, each with a short lock name
// (sockets' paths are short), whatever the routine's name.
func TestEachRoutineHasItsOwnWindow(t *testing.T) {
	a := lockName(tray.Window{Kind: tray.RoutineWindow, Routine: "standup"})
	b := lockName(tray.Window{Kind: tray.RoutineWindow, Routine: "digest"})
	long := lockName(tray.Window{Kind: tray.RoutineWindow, Routine: strings.Repeat("x", 200) + "/.."})
	if a == b || len(long) > 40 || strings.ContainsAny(long, "/.") {
		t.Fatal(a, b, long)
	}
	// A hook and a watcher have windows of their own, apart from a routine's.
	hook := lockName(tray.Window{Kind: tray.HookWindow, Routine: "standup"})
	watcher := lockName(tray.Window{Kind: tray.WatcherWindow, Routine: "standup"})
	if hook == a || watcher == a || hook == watcher || len(hook) > 40 {
		t.Fatal(a, hook, watcher)
	}
	if !strings.HasPrefix(a, "window-routine-") {
		t.Fatal("a routine's window's lock moved: one open from before an update wouldn't be found")
	}
	if lockName(tray.Window{Kind: tray.SettingsWindow}) != "window-settings" {
		t.Fatal("the Settings window's lock moved: one open from before an update wouldn't be found")
	}
}

// A reason about a watcher (one wrong for every hook on it), chosen in the
// tray's menu, starts this app for the watcher's window, which opens on
// that watcher.
func TestAReasonAboutAWatcherOpensItsWindow(t *testing.T) {
	line := `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","validUntil":"2026-11-17T08:06:00Z","health":"error",` +
		`"busy":false,"routines":[],"hooks":[{"name":"home-joke","enabled":true,"health":"error"},{"name":"say-hi","enabled":true,"health":"error"}],` +
		`"reasons":[{"watcher":"home-key","text":"home-key is failing: can't listen (home-joke and say-hi listen to it)"}]}`
	s := cli.ParseStatus([]byte(line))
	menu := tray.Menu("Acme", "1.0", s, time.Date(2026, 11, 17, 8, 5, 30, 0, time.UTC), tray.MenuItem{})
	reason := menu[1]
	if reason.Text != "home-key is failing: can't listen (home-joke and say-hi listen to it)" || reason.Action.Kind != tray.ShowWindow {
		t.Fatalf("the reason: %+v", reason)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tray.WindowsApp), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	display := func(k string) string {
		if k == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	argv := tray.WindowsCommand(display, filepath.Join(dir, "acme-auto"), reason.Action.Window)
	if len(argv) < 2 {
		t.Fatalf("not the windows app: %q", argv)
	}
	which, err := window(argv[1:])
	if err != nil || which != (tray.Window{Kind: tray.WatcherWindow, Routine: "home-key"}) {
		t.Fatalf("%q: %+v %v", argv[1:], which, err)
	}
	w, view, _ := newWindow(test.NewTempApp(t), cli.CLI{Path: "/nonexistent", Command: "acme-auto"}, "Acme", which, func(tray.Window) {})
	if _, ok := view.(*ui.Watcher); !ok || w.Title() != "home-key" {
		t.Fatalf("opened %T titled %q", view, w.Title())
	}
}
