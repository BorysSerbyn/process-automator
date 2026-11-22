package tray

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The terminal is $TERMINAL, else the first installed of
// xdg-terminal-exec, x-terminal-emulator, ptyxis, kgx, gnome-terminal,
// konsole, xfce4-terminal and xterm, each with its way of running a command.
func TestTheTerminalFallbackOrder(t *testing.T) {
	command := []string{"/opt/acme/bin/acme-auto", "setup"}
	shell := []string{"sh", "-c", keepOpen, "/opt/acme/bin/acme-auto", "setup"}
	for _, c := range []struct {
		name      string
		terminal  string
		installed []string
		want      []string
	}{
		{"$TERMINAL first", "alacritty --class x", []string{"alacritty", "xterm"}, append([]string{"alacritty", "--class", "x", "-e"}, shell...)},
		{"$TERMINAL not installed", "nope", []string{"konsole"}, append([]string{"konsole", "-e"}, shell...)},
		{"$TERMINAL whose -e takes one string", "/usr/bin/xfce4-terminal", []string{"/usr/bin/xfce4-terminal"}, append([]string{"/usr/bin/xfce4-terminal", "-x"}, shell...)},
		{"$TERMINAL with no flag", "kitty", []string{"kitty"}, append([]string{"kitty"}, shell...)},
		{"xdg-terminal-exec", "", []string{"xterm", "x-terminal-emulator", "xdg-terminal-exec"}, append([]string{"xdg-terminal-exec"}, shell...)},
		{"ptyxis alone (Fedora's GNOME)", "", []string{"ptyxis"}, append([]string{"ptyxis", "--"}, shell...)},
		{"GNOME Console", "", []string{"kgx", "xterm"}, append([]string{"kgx", "--"}, shell...)},
		{"xfce4-terminal", "", []string{"xfce4-terminal", "xterm"}, append([]string{"xfce4-terminal", "-x"}, shell...)},
		{"x-terminal-emulator", "", []string{"xterm", "konsole", "gnome-terminal", "x-terminal-emulator"}, append([]string{"x-terminal-emulator", "-e"}, shell...)},
		{"gnome-terminal", "", []string{"xterm", "konsole", "gnome-terminal"}, append([]string{"gnome-terminal", "--"}, shell...)},
		{"konsole", "", []string{"xterm", "konsole"}, append([]string{"konsole", "-e"}, shell...)},
		{"xterm", "", []string{"xterm"}, append([]string{"xterm", "-e"}, shell...)},
	} {
		installed := map[string]bool{}
		for _, n := range c.installed {
			installed[n] = true
		}
		getenv := func(k string) string {
			if k == "TERMINAL" {
				return c.terminal
			}
			return ""
		}
		lookPath := func(n string) (string, error) {
			if installed[n] {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		}
		got, err := TerminalCommand(getenv, lookPath, command)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q %v\nwant %q", c.name, got, err, c.want)
		}
	}
	none := func(string) (string, error) { return "", errors.New("not found") }
	if _, err := TerminalCommand(func(string) string { return "" }, none, command); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("no terminal: %v", err)
	}
}

// Each window is its CLI command in the terminal for now.
func TestWindowsAreTheirCommands(t *testing.T) {
	for w, want := range map[Window][]string{
		{Kind: SetupWindow}:                         {"setup"},
		{Kind: SettingsWindow}:                      {"settings", "list", "--scope", "app"},
		{Kind: RoutinesWindow}:                      {"routines", "list", "--kind", "all"},
		{Kind: RoutineWindow, Routine: "my_report"}: {"routines", "show", "my_report"},
		{Kind: UpdateWindow}:                        {"update", "progress"},
		{Kind: ConfirmUpdateWindow}:                 {"update", "--ask"},
	} {
		if got := w.Command(); !reflect.DeepEqual(got, want) {
			t.Errorf("%+v: %q", w, got)
		}
	}
}

// windowsApp writes a stand-in windows app beside a CLI, running body, and
// returns the CLI's path.
func windowsApp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, WindowsApp), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "acme-auto")
}

func display(k string) string {
	if k == "DISPLAY" {
		return ":0"
	}
	return ""
}

// Every window opens in the windows app beside the CLI, given a display;
// no display or no windows app: the terminal.
func TestWindowsOpenInTheWindowsAppWhereItHasThem(t *testing.T) {
	cliPath := windowsApp(t, "exit 0\n")
	app := filepath.Join(filepath.Dir(cliPath), WindowsApp)
	wayland := func(k string) string {
		if k == "WAYLAND_DISPLAY" {
			return "wayland-0"
		}
		return ""
	}
	for _, c := range []struct {
		name   string
		getenv func(string) string
		cli    string
		w      Window
		want   []string
	}{
		{"setup", display, cliPath, Window{Kind: SetupWindow}, []string{app, "setup"}},
		{"settings on Wayland", wayland, cliPath, Window{Kind: SettingsWindow}, []string{app, "settings"}},
		{"routines", display, cliPath, Window{Kind: RoutinesWindow}, []string{app, "routines"}},
		{"a routine", display, cliPath, Window{Kind: RoutineWindow, Routine: "standup"}, []string{app, "routine", "standup"}},
		{"the update", display, cliPath, Window{Kind: UpdateWindow}, []string{app, "update"}},
		{"asking before an update", display, cliPath, Window{Kind: ConfirmUpdateWindow}, []string{app, "confirm-update"}},
		{"no display", func(string) string { return "" }, cliPath, Window{Kind: SetupWindow}, nil},
		{"no windows app", display, windowsApp(t, ""), Window{Kind: SetupWindow}, nil},
	} {
		if got := WindowsCommand(c.getenv, c.cli, c.w); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A windows app that fails as it starts (no OpenGL, a display that won't
// open) has its window opened in the terminal; one that runs, or that
// handed the window to one already open (exit 0), doesn't.
func TestAWindowsAppThatFailsFallsBackToTheTerminal(t *testing.T) {
	for _, c := range []struct {
		name     string
		body     string
		terminal bool
	}{
		{"fails", "echo 'no display' >&2; exit 1\n", true},
		{"handed over", "exit 0\n", false},
		{"runs", "sleep 2\n", false},
	} {
		cliPath := windowsApp(t, c.body)
		opened := make(chan struct{}, 1)
		err := openWindow(display, cliPath, Window{Kind: SetupWindow}, func() error {
			opened <- struct{}{}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		select {
		case <-opened:
			if !c.terminal {
				t.Errorf("%s: opened in the terminal", c.name)
			}
		case <-time.After(time.Second):
			if c.terminal {
				t.Errorf("%s: not opened in the terminal", c.name)
			}
		}
	}
	// Without the windows app, the terminal at once.
	called := false
	if err := openWindow(display, windowsApp(t, ""), Window{Kind: SettingsWindow}, func() error { called = true; return ErrNoTerminal }); !errors.Is(err, ErrNoTerminal) || !called {
		t.Errorf("no windows app: %v %v", err, called)
	}
}
