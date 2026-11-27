package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// openWait is how long openExternal waits for xdg-open to fail.
var openWait = 5 * time.Second

// openExternal opens a URL, file or folder with the desktop's default app
// (tests replace it).
var openExternal = xdgOpen

// xdgOpen opens target with xdg-open, which may wait for the app it starts
// (outside GNOME, KDE and Xfce it runs the browser itself, until it's
// closed): a failure shows in its first few seconds; still running past
// them, it has opened it.
func xdgOpen(target string) error {
	cmd := exec.Command("xdg-open", target)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("xdg-open: %w", err)
		}
		return nil
	case <-time.After(openWait):
		return nil
	}
}

// revealFile shows a file's folder in the file manager (tests replace it).
var revealFile = func(path string) error { return xdgOpen(filepath.Dir(path)) }
