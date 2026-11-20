package engine

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
)

// A run takes its turn before it starts: its routine's own lock (a routine
// never overlaps itself) and, unless the routine runs in parallel, the
// device-wide run queue (one queued routine at a time, scheduled or by hand).
// Waiting is polled rather than blocking, so a wait can be cancelled or
// stopped, and is recorded as queued.

// turnPoll is how often a waiting run checks for its turn.
const turnPoll = 500 * time.Millisecond

// takeTurn takes the locks a run needs; waiting is called once if it has to
// wait. release frees them.
func takeTurn(ctx context.Context, a *App, routine string, parallel bool, waiting func()) (release func(), err error) {
	paths := []string{a.Layout.RoutineLock(routine)}
	if !parallel {
		paths = append(paths, a.Layout.RunQueueLock())
	}
	var unlocks []func()
	release = func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	waited := false
	for _, p := range paths {
		for {
			unlock, err := filelock.Lock(p, false)
			if err == nil {
				unlocks = append(unlocks, unlock)
				break
			}
			if !errors.Is(err, filelock.ErrHeld) {
				release()
				return nil, err
			}
			if !waited {
				waited = true
				waiting()
			}
			select {
			case <-ctx.Done():
				release()
				return nil, context.Cause(ctx)
			case <-time.After(turnPoll):
			}
		}
	}
	return release, nil
}

// ErrStopped is why a run stopped on request (`procauto stop`).
var ErrStopped = errors.New("stopped on request")

// stopPoll is how often a run checks for a stop request.
const stopPoll = time.Second

// watchStop cancels ctx (cause ErrStopped) when a stop request for the routine
// appears, made at or after since (older ones were for earlier runs). done
// ends the watch.
func watchStop(ctx context.Context, path string, since time.Time) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	quit := make(chan struct{})
	go func() {
		t := time.NewTicker(stopPoll)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if fi, err := os.Stat(path); err == nil && !fi.ModTime().Before(since) {
					_ = os.Remove(path)
					cancel(ErrStopped)
					return
				}
			}
		}
	}()
	return ctx, func() {
		close(quit)
		cancel(nil)
		// A request that came as the run ended was for this run: don't leave it.
		if fi, err := os.Stat(path); err == nil && !fi.ModTime().Before(since) {
			_ = os.Remove(path)
		}
	}
}
