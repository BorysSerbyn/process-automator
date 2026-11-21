package tray

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// WindowKind is one of the app's windows (automator-ui's).
type WindowKind string

// The windows.
const (
	SetupWindow    WindowKind = "setup"
	SettingsWindow WindowKind = "settings"
	RoutinesWindow WindowKind = "routines"
	// RoutineWindow is one routine's (Window.Routine).
	RoutineWindow WindowKind = "routine"
	// UpdateWindow follows the update installing.
	UpdateWindow WindowKind = "update"
	// ConfirmUpdateWindow asks before installing an update (only where
	// notifications can't ask).
	ConfirmUpdateWindow WindowKind = "confirm-update"
)

// Window is a window to open, and what it's about.
type Window struct {
	Kind    WindowKind
	Routine string
}

// Command is the CLI command that does in a terminal what the window does.
func (w Window) Command() []string {
	switch w.Kind {
	case SetupWindow:
		return []string{"setup"}
	case SettingsWindow:
		return []string{"settings", "list", "--scope", "app"}
	case RoutineWindow:
		return []string{"routines", "show", w.Routine}
	case UpdateWindow:
		return []string{"update", "progress"}
	case ConfirmUpdateWindow:
		return []string{"update"}
	}
	return []string{"routines", "list"}
}

// OpenWindow is the window seam: every window the tray opens goes through
// it. Until the app has windows on Linux, a window is its CLI command run in
// the user's terminal (Window.Command), which stays open until Enter.
func OpenWindow(cliPath string, w Window) error {
	argv, err := TerminalCommand(os.Getenv, exec.LookPath, append([]string{cliPath}, w.Command()...))
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// keepOpen runs the command given after it ($0 and its arguments), then
// waits for Enter, so what it printed stays on screen.
const keepOpen = `"$0" "$@"; printf '\nPress Enter to close. '; read -r _`

// ErrNoTerminal says no terminal was found to run a command in.
var ErrNoTerminal = errors.New("no terminal found: set $TERMINAL, or install one (gnome-terminal, konsole, xterm)")

// TerminalCommand is the command line that runs command in the user's
// terminal: $TERMINAL, else the first of x-terminal-emulator,
// gnome-terminal, konsole and xterm that's installed.
func TerminalCommand(getenv func(string) string, lookPath func(string) (string, error), command []string) ([]string, error) {
	shell := append([]string{"sh", "-c", keepOpen}, command...)
	if t := strings.Fields(getenv("TERMINAL")); len(t) > 0 {
		if _, err := lookPath(t[0]); err == nil {
			return append(append(t, "-e"), shell...), nil
		}
	}
	for _, t := range []struct{ name, flag string }{
		{"x-terminal-emulator", "-e"}, {"gnome-terminal", "--"}, {"konsole", "-e"}, {"xterm", "-e"},
	} {
		if _, err := lookPath(t.name); err == nil {
			return append([]string{t.name, t.flag}, shell...), nil
		}
	}
	return nil, ErrNoTerminal
}
