package programs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A downloaded program's copy carries no quarantine mark, so Gatekeeper
// never blocks a scheduled run.
func TestCopyIsNotQuarantined(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "prog")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(exe, quarantineAttr, []byte("0081;00000000;Safari;"), 0); err != nil {
		t.Skipf("can't mark a file here: %v", err)
	}
	copied := filepath.Join(dir, "copy")
	if err := copyFile(exe, copied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(copied, quarantineAttr, []byte("0081;00000000;Safari;"), 0); err != nil {
		t.Fatal(err)
	}
	if err := unquarantine(copied); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Getxattr(copied, quarantineAttr, nil); err == nil {
		t.Fatal("the copy is still quarantined")
	}
	if err := unquarantine(copied); err != nil {
		t.Fatalf("unquarantining an unmarked file: %v", err)
	}
}
