package tray

import (
	"errors"
	"reflect"
	"testing"
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
		{Kind: RoutinesWindow}:                      {"routines", "list"},
		{Kind: RoutineWindow, Routine: "my_report"}: {"routines", "show", "my_report"},
		{Kind: UpdateWindow}:                        {"update", "progress"},
		{Kind: ConfirmUpdateWindow}:                 {"update"},
	} {
		if got := w.Command(); !reflect.DeepEqual(got, want) {
			t.Errorf("%+v: %q", w, got)
		}
	}
}
