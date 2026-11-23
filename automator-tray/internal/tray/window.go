package tray

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	// HooksWindow is every hook and watcher; HookWindow one hook's and
	// WatcherWindow one watcher's (Window.Routine names it).
	HooksWindow WindowKind = "hooks"
	// AcknowledgementsWindow lists the notifications waiting for me to
	// acknowledge them.
	AcknowledgementsWindow WindowKind = "acknowledgements"
	HookWindow             WindowKind = "hook"
	WatcherWindow          WindowKind = "watcher"
	// UpdateWindow follows the update installing.
	UpdateWindow WindowKind = "update"
	// ConfirmUpdateWindow asks before installing an update (only where
	// notifications can't ask), then follows it.
	ConfirmUpdateWindow WindowKind = "confirm-update"
)

// Window is a window to open, and what it's about.
type Window struct {
	Kind WindowKind
	// Routine names the routine, hook or watcher it's about.
	Routine string
	// Review opens a routine's or hook's window on its Turn On review, when
	// it needs turning on (from the menu, a reason).
	Review bool
}

// Named is whether the window is about one routine, hook or watcher.
func (w Window) Named() bool {
	return w.Kind == RoutineWindow || w.Kind == HookWindow || w.Kind == WatcherWindow
}

// Command is the CLI command that does in a terminal what the window does.
func (w Window) Command() []string {
	switch w.Kind {
	case SetupWindow:
		return []string{"setup"}
	case SettingsWindow:
		return []string{"settings", "list", "--scope", "app"}
	case RoutineWindow:
		if w.Review {
			return []string{"routines", "trust", w.Routine, "--resume"}
		}
		return []string{"routines", "show", w.Routine}
	case HooksWindow:
		return []string{"hooks", "list"}
	case AcknowledgementsWindow:
		return []string{"notifications", "list", "--waiting"}
	case HookWindow:
		if w.Review {
			return []string{"hooks", "trust", w.Routine, "--resume"}
		}
		return []string{"hooks", "show", w.Routine}
	case WatcherWindow:
		return []string{"watchers", "show", w.Routine}
	case UpdateWindow:
		return []string{"update", "progress"}
	case ConfirmUpdateWindow:
		return []string{"update", "--ask"}
	}
	return []string{"routines", "list", "--kind", "all"}
}

// WindowsApp is the windows app (automator-windows), beside the tray and
// the CLI: the windows it has open there, the others in a terminal.
const WindowsApp = "automator-windows"

// StartGrace is how long a windows app just started is watched: one that
// fails in that time (no OpenGL, a display that won't open) has its window
// opened in a terminal instead.
var StartGrace = 3 * time.Second

// OpenWindow is the window seam: every window the tray opens goes through
// it. Each is the windows app's (WindowsApp), one process per window that
// exits when it closes; where the windows app isn't installed, there's no
// display, or it fails as it starts, the window is its CLI command run in my
// terminal (Window.Command), which stays open until Enter.
func OpenWindow(cliPath string, w Window) error {
	return openWindow(os.Getenv, cliPath, w, func() error { return openInTerminal(cliPath, w) })
}

func openWindow(getenv func(string) string, cliPath string, w Window, terminal func() error) error {
	argv := WindowsCommand(getenv, cliPath, w)
	if argv == nil {
		return terminal()
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	// What it says goes to the tray's log, straight to the file (no pipe
	// to outlive: it outlives the tray); without a log, kept for the message.
	var stderr bytes.Buffer
	toLog := childStderr()
	if toLog != nil {
		cmd.Stderr = toLog
	} else {
		cmd.Stderr = &limitedWriter{w: &stderr, left: 4096}
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		log.Printf("the %s window couldn't start (%v); opening it in a terminal", w.Kind, err)
		return terminal()
	}
	started := time.Now()
	go func() {
		err := cmd.Wait()
		if err == nil || time.Since(started) > StartGrace {
			return
		}
		said := "what it said is above"
		if toLog == nil {
			said = strings.TrimSpace(stderr.String())
		}
		log.Printf("the %s window didn't open (%v: %s); opening it in a terminal", w.Kind, err, said)
		if err := terminal(); err != nil {
			log.Printf("the %s window: %v", w.Kind, err)
		}
	}()
	return nil
}

// WindowsCommand is the command line that opens w in the windows app
// (`automator-windows <kind> [<name>]`), or nil when it isn't installed
// beside the CLI, or there's no display to show it on.
func WindowsCommand(getenv func(string) string, cliPath string, w Window) []string {
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		log.Printf("the %s window: no display (DISPLAY, WAYLAND_DISPLAY); opening it in a terminal", w.Kind)
		return nil
	}
	exe := filepath.Join(filepath.Dir(cliPath), WindowsApp)
	if st, err := os.Stat(exe); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		log.Printf("the %s window: no windows app at %s; opening it in a terminal", w.Kind, exe)
		return nil
	}
	if w.Named() {
		if w.Review {
			return []string{exe, string(w.Kind), w.Routine, "review"}
		}
		return []string{exe, string(w.Kind), w.Routine}
	}
	return []string{exe, string(w.Kind)}
}

// openInTerminal runs w's CLI command in my terminal.
func openInTerminal(cliPath string, w Window) error {
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

// limitedWriter keeps the first left bytes written to it: what a windows
// app that failed said, for the log.
type limitedWriter struct {
	w    io.Writer
	left int
	mu   sync.Mutex
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := p[:min(len(p), l.left)]
	l.left -= len(keep)
	_, _ = l.w.Write(keep)
	return len(p), nil
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
