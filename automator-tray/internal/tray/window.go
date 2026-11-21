package tray

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
var ErrNoTerminal = errors.New("no terminal found: set $TERMINAL, or install one (gnome-terminal, ptyxis, konsole, xterm)")

// terminals are the terminals looked for, in order, each with what comes
// before the command it runs (its arguments one by one). xdg-terminal-exec
// is the desktop's chosen terminal; ptyxis and kgx are GNOME's (Fedora's
// and Ubuntu's default, with no gnome-terminal or xterm beside them).
var terminals = []struct{ name, flag string }{
	{"xdg-terminal-exec", ""}, {"x-terminal-emulator", "-e"}, {"ptyxis", "--"}, {"kgx", "--"},
	{"gnome-terminal", "--"}, {"konsole", "-e"}, {"xfce4-terminal", "-x"}, {"xterm", "-e"},
}

// terminalFlags are how terminals $TERMINAL may name run a command given as
// separate arguments, where it isn't -e (theirs takes one string, or none).
var terminalFlags = map[string][]string{
	"xdg-terminal-exec": nil, "kitty": nil, "foot": nil, "wezterm": {"start", "--"},
	"gnome-terminal": {"--"}, "ptyxis": {"--"}, "kgx": {"--"}, "tilix": {"--"},
	"xfce4-terminal": {"-x"}, "mate-terminal": {"-x"}, "terminator": {"-x"},
}

// TerminalCommand is the command line that runs command in the user's
// terminal: $TERMINAL, else the first installed of terminals.
func TerminalCommand(getenv func(string) string, lookPath func(string) (string, error), command []string) ([]string, error) {
	shell := append([]string{"sh", "-c", keepOpen}, command...)
	if t := strings.Fields(getenv("TERMINAL")); len(t) > 0 {
		if _, err := lookPath(t[0]); err == nil {
			flag, known := terminalFlags[filepath.Base(t[0])]
			if !known {
				flag = []string{"-e"}
			}
			return append(append(t, flag...), shell...), nil
		}
	}
	for _, t := range terminals {
		if _, err := lookPath(t.name); err == nil {
			argv := []string{t.name}
			if t.flag != "" {
				argv = append(argv, t.flag)
			}
			return append(argv, shell...), nil
		}
	}
	return nil, ErrNoTerminal
}
