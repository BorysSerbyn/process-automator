package sysenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMergePathKeepsOrderAndDedupes(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := mergePath(strings.Join([]string{"/usr/bin", "/bin"}, sep), strings.Join([]string{"/opt/homebrew/bin", "/usr/bin", ""}, sep))
	if want := strings.Join([]string{"/usr/bin", "/bin", "/opt/homebrew/bin"}, sep); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLoginShellPathFindsWhatTheProfileAdds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	home := t.TempDir()
	// A noisy profile that extends PATH, as a user's shell profile would.
	profile := "echo welcome!\nexport PATH=\"/from/profile:$PATH\"\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	p, err := loginShellPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, "/from/profile"+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want the profile's entry first", p)
	}
}

func TestKeepAwakeHoldsUntilReleased(t *testing.T) {
	release := KeepAwake()
	if runtime.GOOS == "darwin" {
		out, _ := exec.Command("pmset", "-g", "assertions").Output()
		if !strings.Contains(string(out), "caffeinate") {
			t.Errorf("no caffeinate assertion while held:\n%s", out)
		}
	}
	release()
	release() // idempotent enough not to panic
}

func TestSavedLoginShellPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	cache := filepath.Join(t.TempDir(), "login-path")
	t.Setenv("PATH", "/usr/bin"+sep+"/bin")
	// No cache and not resolving: nothing changes.
	if err := UseSavedLoginShellPath(cache, false); err != nil || os.Getenv("PATH") != "/usr/bin"+sep+"/bin" {
		t.Fatalf("no cache: %v %q", err, os.Getenv("PATH"))
	}
	// A saved PATH is added after what's there.
	if err := os.WriteFile(cache, []byte("/opt/tools"+sep+"/usr/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UseSavedLoginShellPath(cache, true); err != nil {
		t.Fatal(err)
	}
	if got, want := os.Getenv("PATH"), strings.Join([]string{"/usr/bin", "/bin", "/opt/tools"}, sep); got != want {
		t.Errorf("PATH %q, want %q", got, want)
	}
}
