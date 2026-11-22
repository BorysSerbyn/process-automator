package main

import (
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
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
	if lockName(tray.Window{Kind: tray.SettingsWindow}) != "window-settings" {
		t.Fatal("the Settings window's lock moved: one open from before an update wouldn't be found")
	}
}
