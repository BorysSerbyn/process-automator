package instance

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func shortDir(t *testing.T) string {
	t.Helper()
	// Unix sockets' paths are short: t.TempDir's can be too long on macOS.
	d, err := os.MkdirTemp("/tmp", "inst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// The first claim holds the window; a second asks it to come to the front
// and is told it's held; once let go of, the window can be held again.
func TestOneWindowOfAKind(t *testing.T) {
	dir := shortDir(t)
	h, err := Claim(dir, "windows-setup")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Claim(dir, "windows-settings")
	if err != nil {
		t.Fatalf("another kind of window: %v", err)
	}
	other.Release()
	if _, err := Claim(dir, "windows-setup"); !errors.Is(err, ErrHeld) {
		t.Fatalf("second claim: %v", err)
	}
	select {
	case ask := <-h.Raised:
		if ask != "raise" {
			t.Fatalf("asked %q", ask)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the holder wasn't asked to come to the front")
	}
	// Asked for something as it comes to the front: its review, say.
	if _, err := Ask(dir, "windows-setup", "review"); !errors.Is(err, ErrHeld) {
		t.Fatalf("asking: %v", err)
	}
	select {
	case ask := <-h.Raised:
		if ask != "review" {
			t.Fatalf("asked %q", ask)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the holder wasn't asked for its review")
	}
	h.Release()
	h2, err := Claim(dir, "windows-setup")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	h2.Release()
}

// A holder that died leaves its socket behind: the lock went with it, so
// the next claim holds the window.
func TestAHolderThatDiedLetsGo(t *testing.T) {
	if os.Getenv("INSTANCE_CHILD") != "" {
		if _, err := Claim(os.Getenv("INSTANCE_DIR"), "w"); err != nil {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString("held\n")
		time.Sleep(time.Minute)
		return
	}
	dir := shortDir(t)
	cmd := exec.Command(os.Args[0], "-test.run", "^TestAHolderThatDiedLetsGo$")
	cmd.Env = append(os.Environ(), "INSTANCE_CHILD=1", "INSTANCE_DIR="+dir)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := out.Read(buf); err != nil || string(buf) != "held\n" {
		t.Fatalf("child: %q %v", buf, err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := os.Stat(filepath.Join(dir, "w.sock")); err != nil {
		t.Fatalf("no socket left behind: %v", err)
	}
	h, err := Claim(dir, "w")
	if err != nil {
		t.Fatalf("claim after the holder died: %v", err)
	}
	h.Release()
}

func TestDir(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	if got := Dir(env("/run/user/1000"), "dev.acme"); got != "/run/user/1000/dev.acme" {
		t.Errorf("runtime dir: %s", got)
	}
	if got := Dir(env(""), "dev.acme"); filepath.Dir(got) != filepath.Clean(os.TempDir()) {
		t.Errorf("no runtime dir: %s", got)
	}
}

// A later start replaces the window: the holder is asked to quit, and
// once it lets go, the later start holds the window.
func TestReplaceAsksTheHolderToQuit(t *testing.T) {
	dir := shortDir(t)
	h, err := Claim(dir, "window")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for ask := range h.Raised {
			if ask == QuitAsk {
				h.Release()
				return
			}
		}
	}()
	h2, err := Replace(dir, "window", 2*time.Second)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	defer h2.Release()
	if pid := holderPID(dir, "window"); pid != os.Getpid() {
		t.Fatalf("the lock names %d", pid)
	}
	// It's held now: an older start hands it its screen.
	if _, err := Ask(dir, "window", `["settings"]`); !errors.Is(err, ErrHeld) {
		t.Fatalf("held: %v", err)
	}
}

// A holder that doesn't quit (its window gone, say) is ended, and the later
// start holds the window; one that can't be ended is an error.
func TestReplaceEndsAHolderThatDoesntQuit(t *testing.T) {
	dir := shortDir(t)
	h, err := Claim(dir, "window")
	if err != nil {
		t.Fatal(err)
	}
	// The holder is this test's process: it says another PID, which
	// Terminate "ends" by letting go of the window.
	if err := os.WriteFile(filepath.Join(dir, "window.lock"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := Terminate
	t.Cleanup(func() { Terminate = old })
	var ended int
	Terminate = func(pid int) error { ended = pid; h.Release(); return nil }
	h2, err := Replace(dir, "window", 200*time.Millisecond)
	if err != nil || ended != 4242 {
		t.Fatalf("replace: %v, ended %d", err, ended)
	}
	h2.Release()

	h3, _ := Claim(dir, "window")
	defer h3.Release()
	_ = os.WriteFile(filepath.Join(dir, "window.lock"), []byte("4243\n"), 0o600)
	Terminate = func(int) error { return errors.New("no") }
	if _, err := Replace(dir, "window", 100*time.Millisecond); err == nil {
		t.Fatal("a holder that can't be ended was replaced")
	}
}
