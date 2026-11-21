package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeXdgOpen puts an xdg-open running script first on PATH.
func fakeXdgOpen(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// xdg-open running the browser itself until it's closed doesn't hold up
// what opened it; one that fails says so.
func TestOpenExternalDoesntWaitForTheBrowser(t *testing.T) {
	orig := openWait
	openWait = 200 * time.Millisecond
	t.Cleanup(func() { openWait = orig })

	fakeXdgOpen(t, "exec sleep 30")
	start := time.Now()
	if err := xdgOpen("http://127.0.0.1/x"); err != nil {
		t.Fatalf("a browser still open: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("waited %s for the browser", took)
	}

	fakeXdgOpen(t, "echo 'xdg-open: no method available' >&2; exit 3")
	if err := xdgOpen("http://127.0.0.1/x"); err == nil {
		t.Error("xdg-open failing: no error")
	}
}
