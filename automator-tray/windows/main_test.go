package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/shell"
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
		{[]string{"routine", "standup", "run", "r-42"}, tray.Window{Kind: tray.RoutineWindow, Routine: "standup", Run: "r-42"}, true},
		{[]string{"hook", "open-meet", "run", "r-7"}, tray.Window{Kind: tray.HookWindow, Routine: "open-meet", Run: "r-7"}, true},
		{[]string{"watcher", "cal", "run", "r-7"}, tray.Window{}, false},
		{[]string{"routine", "standup", "run"}, tray.Window{}, false},
		{[]string{"hook"}, tray.Window{}, false},
		{[]string{"hooks", "x"}, tray.Window{}, false},
		{[]string{"updates"}, tray.Window{Kind: tray.UpdatesWindow}, true},
		{[]string{"template-updates"}, tray.Window{Kind: tray.TemplateUpdatesWindow}, true},
		{[]string{"template-updates", "x"}, tray.Window{}, false},
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

// A windows app from before replacing hands the open window its screen's
// arguments as one line of JSON: the window still shows that screen.
func TestAnOlderStartHandsItsScreenToTheWindow(t *testing.T) {
	for _, args := range [][]string{
		{"setup"}, {"routine", "my report"}, {"hook", "open-meet", "review"}, {"routine", "standup", "run", "r-42"},
	} {
		b, _ := json.Marshal(args)
		want, _ := window(args)
		if got, ok := asked(string(b)); !ok || got != want {
			t.Errorf("%q: %+v", args, got)
		}
	}
	for _, line := range []string{"raise", "review", "quit", `["nope"]`, ""} {
		if _, ok := asked(line); ok {
			t.Errorf("%q asked for a screen", line)
		}
	}
}

// A reason about a watcher (one wrong for every hook on it), chosen in the
// tray's menu, starts this app for the watcher's screen, which the window
// opens on that watcher, under Hooks.
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
	w := test.NewTempApp(t).NewWindow("Acme")
	sh := shell.New(cli.CLI{Path: "/nonexistent", Command: "acme-auto"}, w, func(work func() func()) { work()() }, "Acme")
	sh.Show(which)
	if _, ok := sh.Shown().(*ui.Watcher); !ok || w.Title() != "home-key — Acme" || sh.State().Section() != tray.HooksWindow {
		t.Fatalf("showing %T titled %q", sh.Shown(), w.Title())
	}
}
