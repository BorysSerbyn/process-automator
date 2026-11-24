// Package instance keeps one window: the first windows app started holds
// it, and one started after asks it for what it was started for (to come
// to the front, showing a screen), then exits. It works with or without a session bus: a lock
// the kernel lets go of when the holder exits, however it exits, and a
// socket beside it to ask through.
package instance

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
		return nil, raise(sock, ask)
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
