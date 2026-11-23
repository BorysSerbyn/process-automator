package engine

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// Waking the scheduler: what changes what the daemon runs (trusting,
// turning something on or off, pausing, resuming, adding, updating or
// deleting a routine, program, hook or watcher, a setting, a permission
// granted) reaches it within about a second, not on its next pass up to
// TickInterval later. A command that changed something writes the wake
// file (Changed); the daemon looks for it every WakePoll and makes a pass
// at once: routines' slots, run status, and the watchers hooks listen to
// started or stopped. A file, as stop and permission requests are: it
// works the same on every OS, with no socket or signal to clean up, and
// costs one failed remove when there's none.

// WakePoll is how often the daemon looks for the wake file.
const WakePoll = 250 * time.Millisecond

// followPoll is how often the daemon checks a permission it was asked for,
// while it follows it (followFor after the request): one granted in the
// OS's settings makes a pass at once. Variables for tests.
var (
	followPoll = 2 * time.Second
	followFor  = 3 * time.Minute
)

// Wake asks the scheduler for a pass now. With no scheduler running the
// file waits; the next one to start removes it (its first pass covers it).
func (a *App) Wake() error {
	return os.WriteFile(a.Layout.WakeRequest(), nil, 0o600)
}

// Changed is what follows a change to what the app runs (a command that
// isn't read-only): each routine's and hook's state in run status at once
// (SyncStatus), and the scheduler woken to act on it (Wake).
func (a *App) Changed(now time.Time) error {
	return errors.Join(a.SyncStatus(now), a.Wake())
}

// waker turns wake requests into the daemon's passes: pokes while a pass
// is pending make one pass.
type waker struct{ c chan struct{} }

func newWaker() *waker { return &waker{c: make(chan struct{}, 1)} }

func (w *waker) poke() {
	select {
	case w.c <- struct{}{}:
	default: // one is pending already
	}
}

// watchWake pokes w each time the wake file appears, until ctx ends. The
// file is removed before the pass, so a change made during the pass gets
// a pass of its own.
func watchWake(ctx context.Context, path string, w *waker) {
	t := time.NewTicker(WakePoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if os.Remove(path) == nil {
			w.poke()
		}
	}
}

// follower follows the permissions the daemon was asked for, so one
// granted in the OS's settings (where a request often sends me) is acted on
// at once, not on the next pass.
type follower struct {
	wake       *waker
	poll, span time.Duration

	mu        sync.Mutex
	following map[string]bool
}

func newFollower(w *waker) *follower {
	return &follower{wake: w, poll: followPoll, span: followFor, following: map[string]bool{}}
}

// follow checks name every poll for span, in a fresh child process (as a
// pass does), and wakes the daemon once its state differs from what was
// last recorded. A permission already followed isn't followed twice.
func (f *follower) follow(ctx context.Context, a *App, name string) {
	f.mu.Lock()
	if f.following[name] {
		f.mu.Unlock()
		return
	}
	f.following[name] = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.following, name)
		f.mu.Unlock()
	}()
	for deadline := time.Now().Add(f.span); time.Now().Before(deadline); {
		if !sleepCtx(ctx, f.poll) {
			return
		}
		was := a.PermissionState().Of(name)
		if now := checkAll()[name]; now != was && now != permissions.NotChecked {
			logf("permissions: %s is %s now; acting on it", name, now)
			f.wake.poke()
			return
		}
	}
}
