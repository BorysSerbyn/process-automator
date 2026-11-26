// Package instance keeps one window: the first windows app started holds
// it. One started after either asks it for what it was started for (to come
// to the front, showing a screen) and exits (Ask), or replaces it: asks it
// to quit, and holds the window once it's gone (Replace). It works with or
// without a session bus: a lock the kernel lets go of when the holder
// exits, however it exits (it holds the holder's PID), and a socket beside
// it to ask through.
package instance

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrHeld says another process holds the window and was asked to come to
// the front.
var ErrHeld = errors.New("the window is open in another process")

// Holder holds a window: Raised gets what another process asked of it
// each time one did ("raise": come to the front; or what Ask gave).
type Holder struct {
	Raised <-chan string
	lock   *os.File
	ln     net.Listener
}

// Dir is the folder the locks and sockets of an app's windows are in: the
// session's runtime folder (private to me), else one of mine in the temp folder.
func Dir(getenv func(string) string, app string) string {
	if d := getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, app)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", app, os.Getuid()))
}

// Claim makes this process the one holding the window named name, in dir.
// If another already does, it's asked to come to the front and Claim
// returns ErrHeld.
func Claim(dir, name string) (*Holder, error) { return Ask(dir, name, "raise") }

// Ask is Claim, asking the window that's open for ask (one line: what it
// should show as it comes to the front) when another process holds it.
func Ask(dir, name, ask string) (*Holder, error) {
	h, err := claim(dir, name)
	if errors.Is(err, errBusy) {
		return nil, raise(filepath.Join(dir, name+".sock"), ask)
	}
	return h, err
}

// QuitAsk is what Replace asks the holder: to quit, letting go of the window.
const QuitAsk = "quit"

// Terminate ends the process pid (tests replace it).
var Terminate = terminate

// Replace makes this process the one holding the window named name, in
// dir: the process holding it, if one does, is asked to quit (QuitAsk) and
// given wait to let go, then terminated and given wait again.
func Replace(dir, name string, wait time.Duration) (*Holder, error) {
	h, err := claim(dir, name)
	if !errors.Is(err, errBusy) {
		return h, err
	}
	pid := holderPID(dir, name)
	_ = raise(filepath.Join(dir, name+".sock"), QuitAsk) // a stuck one doesn't answer: it's terminated
	if h, err := waitClaim(dir, name, wait); !errors.Is(err, errBusy) {
		return h, err
	}
	if pid <= 0 || pid == os.Getpid() {
		return nil, fmt.Errorf("the window is open in another process, which didn't quit")
	}
	if err := Terminate(pid); err != nil {
		return nil, fmt.Errorf("the window is open in process %d, which didn't quit and can't be ended: %w", pid, err)
	}
	if h, err := waitClaim(dir, name, wait); !errors.Is(err, errBusy) {
		return h, err
	}
	return nil, fmt.Errorf("the window is open in process %d, which didn't quit", pid)
}

// waitClaim tries to claim the window for up to wait.
func waitClaim(dir, name string, wait time.Duration) (*Holder, error) {
	deadline := time.Now().Add(wait)
	for {
		h, err := claim(dir, name)
		if !errors.Is(err, errBusy) || time.Now().After(deadline) {
			return h, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// errBusy says another process holds the window.
var errBusy = errors.New("held by another process")

// holderPID is the PID the holder wrote in the lock file (0 when unknown).
func holderPID(dir, name string) int {
	b, err := os.ReadFile(filepath.Join(dir, name+".lock"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// claim holds the window if no one does (errBusy if someone does),
// listening for what later starts ask.
func claim(dir, name string) (*Holder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, name+".sock")
	held, err := tryLock(lock)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if !held {
		_ = lock.Close()
		return nil, errBusy
	}
	if err := lock.Truncate(0); err == nil {
		_, _ = lock.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	// A socket left by a holder that's gone is in the way.
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	raised := make(chan string, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			line, _ := bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close()
			select {
			case raised <- strings.TrimSpace(line):
			default:
			}
		}
	}()
	return &Holder{Raised: raised, lock: lock, ln: ln}, nil
}

// Release lets go of the window (exiting does too).
func (h *Holder) Release() {
	_ = h.ln.Close()
	_ = h.lock.Close()
}

// raise asks the holder to come to the front: its socket may not be there
// yet when it has only just started, so it's tried for a moment.
func raise(sock, ask string) error {
	var err error
	for range 20 {
		var conn net.Conn
		if conn, err = net.DialTimeout("unix", sock, time.Second); err == nil {
			_, err = conn.Write([]byte(ask + "\n"))
			_ = conn.Close()
			if err == nil {
				return ErrHeld
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the window is open in another process, which didn't answer: %w", err)
}
