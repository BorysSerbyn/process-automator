package engine

import (
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/trust"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Turning something on (a routine, a program, a hook) is one review and
// one yes: what it can reach or runs, a hook's watcher with it, and the
// sign-ins it still needs.

// WatcherReview is a hook's watcher as the hook's review shows it.
type WatcherReview struct {
	// Watcher is nil when there's no watcher by that name (Untrusted says so).
	Watcher   *watchers.Watcher
	Trusted   bool
	Untrusted string
	// Changes since it was last trusted (empty for a first trust).
	Changes []string
	// Approval is what trusting it records, while it isn't trusted.
	Approval *trust.Approval
}

// HookWatcherReview is a hook's watcher in the hook's review: trusting the
// hook trusts it too while it isn't trusted.
func (a *App) HookWatcherReview(name string) WatcherReview {
	var r WatcherReview
	w, err := a.FindWatcher(name)
	if err != nil {
		r.Untrusted = err.Error()
		return r
	}
	r.Watcher = &w
	ap, err := a.WatcherApproval(w)
	if err != nil {
		r.Untrusted = err.Error()
		return r
	}
	f, _, err := a.TrustStore().Load()
	if err != nil {
		r.Untrusted = err.Error()
		return r
	}
	if rec, ok := f.Routines[name]; ok {
		r.Changes = rec.ChangesTo(ap)
		r.Trusted = rec.Approves(ap)
		if !r.Trusted {
			r.Untrusted = "changed since it was trusted"
		}
	} else {
		r.Untrusted = "not trusted yet"
	}
	if !r.Trusted {
		r.Approval = &ap
	}
	return r
}

// SignInsNeeded are the sign-ins x still needs before it works, by the
// name `auth login` takes: its servers', its own secrets' (a program's or
// a hook's), and its hook's watcher's.
func (a *App) SignInsNeeded(x Runnable) []string {
	names := []string{x.Name()}
	if r, _, err := AsRoutine(x, ""); err == nil {
		names = append(names, r.Servers...)
	}
	if h, ok := AsHook(x); ok {
		if h.Prompt != nil {
			names = append(names, h.Prompt.Servers...)
		}
		if _, err := a.FindWatcher(h.On.Watcher); err == nil {
			names = append(names, h.On.Watcher)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if s, ok := a.FindSignIn(n); ok && a.Missing(s) {
			out = append(out, n)
		}
	}
	return out
}

// NeedsTurnOn is whether turning x on (trusting it, with a hook's watcher,
// and resuming it) has something to do: it isn't trusted; it's paused with
// a schedule; or (a hook) it's off, or its watcher isn't trusted.
func (a *App) NeedsTurnOn(x Runnable) bool {
	if why, err := a.Untrusted(x); err != nil || why != "" {
		return true
	}
	c, err := a.RunConfig(x)
	if err != nil {
		return true
	}
	h, isHook := AsHook(x)
	if !isHook {
		return !c.Enabled && c.Schedule != ""
	}
	if !c.Enabled {
		return true
	}
	w := a.HookWatcherReview(h.On.Watcher)
	return w.Watcher != nil && !w.Trusted
}

// TrustWith records what a review showed as trusted, in one write: x's
// approval (nil: it's trusted already) and its hook's watcher's, while that
// isn't trusted (watcher nil, or trusted: none).
func (a *App) TrustWith(name string, approval *trust.Approval, watcherName string, watcher *WatcherReview) error {
	approve := map[string]trust.Approval{}
	if approval != nil {
		approve[name] = *approval
	}
	if watcher != nil && watcher.Approval != nil {
		approve[watcherName] = *watcher.Approval
	}
	if len(approve) == 0 {
		return nil
	}
	return a.TrustStore().Trust(approve, time.Now())
}
