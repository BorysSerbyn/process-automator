package tray

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The log starts again past its limit, keeping the one before as .old; one
// already past it when opened starts again at once.
func TestTheLogStaysSmall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "frontend.log")
	l, err := OpenLog(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{strings.Repeat("a", 40) + "\n", strings.Repeat("b", 40) + "\n"} {
		if _, err := l.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	now, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".old")
	if !strings.HasPrefix(string(now), "b") || !strings.HasPrefix(string(old), "a") {
		t.Fatalf("now %q, old %q", now, old)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("c"), 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLog(path, 64); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Size() != 0 {
		t.Fatalf("not started again: %d bytes", st.Size())
	}
}

// The tray's messages go to its log, and so does what a windows app that
// fails says, before the window opens in the terminal.
func TestTheLogHasTheWindowsAppsErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frontend.log")
	l, err := OpenLog(path, LogLimit)
	if err != nil {
		t.Fatal(err)
	}
	out := log.Writer()
	SetLog(l)
	t.Cleanup(func() {
		logMu.Lock()
		logFile = nil
		logMu.Unlock()
		log.SetOutput(out)
	})
	cliPath := windowsApp(t, "echo 'no OpenGL here' >&2; exit 1\n")
	opened := make(chan struct{})
	if err := openWindow(display, cliPath, Window{Kind: SettingsWindow}, func() error { close(opened); return nil }); err != nil {
		t.Fatal(err)
	}
	<-opened
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "no OpenGL here\n") ||
		!strings.Contains(string(b), "the settings window didn't open (exit status 1: what it said is above); opening it in a terminal") {
		t.Fatalf("%s", b)
	}
	// No windows app: why, too.
	_ = openWindow(display, windowsApp(t, ""), Window{Kind: SetupWindow}, func() error { return nil })
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "the setup window: no windows app at ") {
		t.Fatalf("%s", b)
	}
}
