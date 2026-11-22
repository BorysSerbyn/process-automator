package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/dispatch"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// The daemon runs hooks beside the scheduler, in the same process: it runs
// the watchers hooks listen to, keeps each one's cursor and the events it has
// seen (watcher-state.json), and hands each new event to the hooks listening
// for it through the dispatcher, which runs them as the scheduler runs
// routines (one run of a hook at a time; LLM ones take turns in the run
// queue, programs don't).
//
//   - An event older than a hook's staleness is dropped, when it comes and
//     again before the run starts (one that waited out a sleep).
//   - A watcher isn't polled, nor a stream started, in a Mac's maintenance
//     wake; once awake, a poll watcher due while asleep is polled at once.
//   - Each event is acted on once: its watcher's cursor and the IDs it has
//     seen are saved before it's handed on, across restarts.
//   - A hook starts at most its rate of runs in any hour; past it, the event
//     is dropped. One due to run more than LoopFactor times its rate in an
//     hour is paused, with a run recorded as skipped saying why.

// Event timings and limits.
const (
	// LoopFactor times a hook's rate in an hour pauses it.
	LoopFactor = 3
	// rateWindow is the window rates are counted in.
	rateWindow = time.Hour
	// pollCheck is how often a poll watcher waiting for its interval checks
	// the clock (a sleep shows up as its interval having passed).
	pollCheck = 5 * time.Second
	// streamUp is how long a stream must run to count as working: one that
	// ends sooner is failing, and is started again after a longer pause.
	streamUp = time.Minute
	// MaxStreamBackoff is the longest pause before a stream is started again.
	MaxStreamBackoff = 5 * time.Minute
)

// fullyAwake is sysenv.FullyAwake (a variable for tests).
var fullyAwake = sysenv.FullyAwake

// events is the daemon's event side.
type events struct {
	reload func() (*App, error)
	layout Layout
	disp   *dispatch.Dispatcher
	// minBackoff is the first pause before a stream is started again,
	// awakePoll how often one waiting for a full wake checks, and up how
	// long a stream must run to count as working (variables for tests).
	minBackoff, awakePoll, up time.Duration

	mu        sync.Mutex
	state     *watchers.State
	running   map[string]*watcherRun
	listening map[string][]listener // by watcher
	attempts  map[string][]time.Time
	started   map[string][]time.Time
	lastStart map[string]time.Time
	runners   sync.WaitGroup
}

// watcherRun is a watcher the daemon runs; key is what it runs (it's
// started again when that changes).
type watcherRun struct {
	cancel context.CancelFunc
	key    string
}

// listener is a hook that's on, listening to a watcher.
type listener struct {
	name      string
	on        hooks.On
	staleness time.Duration
	parallel  bool
	batch     bool
}

func newEvents(a *App, reload func() (*App, error)) *events {
	st, err := watchers.LoadState(a.Layout.WatcherState())
	if err != nil {
		logf("watcher state: %v (starting afresh: events may be acted on again)", err)
		st, _ = watchers.LoadState("")
	}
	e := &events{
		reload: reload, layout: a.Layout, state: st, minBackoff: time.Second, awakePoll: awakePoll, up: streamUp,
		running: map[string]*watcherRun{}, listening: map[string][]listener{},
		attempts: map[string][]time.Time{}, started: map[string][]time.Time{}, lastStart: map[string]time.Time{},
	}
	e.disp = &dispatch.Dispatcher{Run: e.run, Missed: e.missed, Queued: e.queued}
	return e
}

// sync starts the watchers that hooks that are on listen to (trusted, not
// paused) and stops the others, on every pass of the daemon, so changes
// apply without a restart.
func (e *events) sync(ctx context.Context, a *App) {
	all, _, err := a.Hooks()
	if err != nil {
		logf("hooks: %v", err)
		return
	}
	listening := map[string][]listener{}
	for _, x := range all {
		if l, ok := listenerOf(a, x); ok {
			listening[l.on.Watcher] = append(listening[l.on.Watcher], l)
		}
	}
	ws, _, err := a.Watchers()
	if err != nil {
		logf("watchers: %v", err)
		return
	}
	want := map[string]watchers.Watcher{}
	keys := map[string]string{}
	for _, w := range ws {
		if len(listening[w.Name]) == 0 {
			continue
		}
		if enabled, err := a.WatcherEnabled(w); err != nil || !enabled {
			continue
		}
		if why, err := a.WatcherUntrusted(w); err != nil || why != "" {
			continue
		}
		ap, err := a.WatcherApproval(w)
		if err != nil {
			continue
		}
		b, _ := json.Marshal(ap)
		want[w.Name], keys[w.Name] = w, string(b)+w.Mode
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listening = listening
	for name, r := range e.running {
		if _, ok := want[name]; !ok || keys[name] != r.key {
			logf("watcher %s: stopping", name)
			r.cancel()
			delete(e.running, name)
		}
	}
	for name, w := range want {
		if _, ok := e.running[name]; ok {
			continue
		}
		wctx, cancel := context.WithCancel(ctx)
		e.running[name] = &watcherRun{cancel: cancel, key: keys[name]}
		logf("watcher %s: starting (%s)", name, w.Mode)
		e.runners.Add(1)
		go func() {
			defer e.runners.Done()
			e.runWatcher(wctx, a, w)
		}()
	}
}

// listenerOf is x as a listener, when it's a hook that's on: enabled, trusted, and
// with nothing keeping it from running but (maybe) its watcher failing.
func listenerOf(a *App, x Runnable) (listener, bool) {
	h := x.(*hook)
	c, err := a.HookConfig(x)
	if err != nil || !c.Enabled {
		return listener{}, false
	}
	if why, err := a.Untrusted(x); err != nil || why != "" {
		return listener{}, false
	}
	if len(h.action.problems(a)) > 0 || len(a.listening(h.h, false)) > 0 {
		return listener{}, false
	}
	return listener{name: x.Name(), on: c.On, staleness: c.Staleness, parallel: !c.Queued, batch: c.Batch}, true
}

// runWatcher runs a watcher until ctx ends.
func (e *events) runWatcher(ctx context.Context, a *App, w watchers.Watcher) {
	if w.Mode == watchers.ModeStream {
		e.stream(ctx, a, w)
		return
	}
	e.poll(ctx, a, w)
}

// poll polls a watcher every interval, by the wall clock: a poll due while
// the computer slept runs once it's fully awake again (after the network
// has had WakeSettle to come back), never in a maintenance wake.
func (e *events) poll(ctx context.Context, a *App, w watchers.Watcher) {
	e.mu.Lock()
	var last time.Time
	if ws := e.state.Watchers[w.Name]; ws != nil && ws.LastPollAt != nil {
		last = *ws.LastPollAt
	}
	e.mu.Unlock()
	for {
		checked := time.Now()
		for !last.IsZero() && wallSince(last) < w.Interval {
			if !sleepCtx(ctx, min(w.Interval-wallSince(last), pollCheck)) {
				return
			}
			if sysenv.Slept(checked) {
				logf("watcher %s: the computer woke up; polling once the network is back", w.Name)
				if !sleepCtx(ctx, WakeSettle) {
					return
				}
			}
			checked = time.Now()
		}
		if !fullyAwake() {
			if !sleepCtx(ctx, e.awakePoll) {
				return
			}
			continue
		}
		inv, err := a.watcherInvocation(w)
		inv.Log = logWriter{"watcher " + w.Name}
		var evs []watchers.Event
		cursor := e.cursor(w.Name)
		if err == nil {
			evs, cursor, err = watchers.Poll(ctx, w, inv, cursor)
		}
		last = time.Now()
		if ctx.Err() != nil {
			return
		}
		e.handle(ctx, w.Name, evs, &cursor, err)
	}
}

// stream runs a stream watcher, starting it again when it ends: at once
// after one that kept running, after a pause that doubles (up to
// MaxStreamBackoff) after one that keeps ending.
func (e *events) stream(ctx context.Context, a *App, w watchers.Watcher) {
	backoff := e.minBackoff
	for {
		if !fullyAwake() {
			if !sleepCtx(ctx, e.awakePoll) {
				return
			}
			continue
		}
		inv, err := a.watcherInvocation(w)
		inv.Log = logWriter{"watcher " + w.Name}
		started := time.Now()
		if err == nil {
			e.mu.Lock()
			at := started
			e.state.Of(w.Name).StartedAt = &at
			e.mu.Unlock()
			// Still running after a while, it works, events or not (one
			// that listens for key presses may print nothing for hours): an
			// earlier failure is over.
			working := time.AfterFunc(e.up, func() {
				e.mu.Lock()
				defer e.mu.Unlock()
				if ws := e.state.Of(w.Name); ws.Error != "" {
					ws.Worked()
					e.save()
				}
			})
			err = watchers.Stream(ctx, w, inv, e.cursor(w.Name),
				func(ev watchers.Event) { e.handle(ctx, w.Name, []watchers.Event{ev}, nil, nil) },
				func(c string) { e.setCursor(w.Name, c) })
			working.Stop()
			if err == nil {
				err = errors.New("it ended")
			}
			err = fmt.Errorf("its stream ended: %w", err)
		}
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= e.up {
			backoff = e.minBackoff // it was working: start it again now
			logf("watcher %s: %v; starting it again", w.Name, err)
			e.mu.Lock()
			e.state.Of(w.Name).Worked()
			e.save()
			e.mu.Unlock()
		} else {
			e.handle(ctx, w.Name, nil, nil, err)
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, MaxStreamBackoff)
	}
}

// wallSince is how long ago t was by the wall clock, which goes on while
// the computer sleeps (Go's monotonic clock doesn't, on macOS and Linux).
func wallSince(t time.Time) time.Duration { return time.Now().Round(0).Sub(t.Round(0)) }

func (e *events) cursor(watcher string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ws := e.state.Watchers[watcher]; ws != nil {
		return ws.Cursor
	}
	return ""
}

func (e *events) setCursor(watcher, cursor string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Of(watcher).Cursor = cursor
	e.save()
}

// save writes the watchers' state (locked).
func (e *events) save() {
	if err := e.state.Save(e.layout.WatcherState()); err != nil {
		logf("watcher state: %v", err)
	}
}

// handle takes what a watcher printed (a poll's events and cursor, or one
// event from a stream; err: why it failed): the events it hasn't printed
// before are saved as seen, with its cursor, then offered to the hooks
// listening for them.
func (e *events) handle(ctx context.Context, watcher string, evs []watchers.Event, cursor *string, err error) {
	now := time.Now()
	e.mu.Lock()
	ws := e.state.Of(watcher)
	if err != nil {
		if ws.Error != err.Error() {
			logf("watcher %s: %v", watcher, err)
		}
		ws.Failed(err, now)
		e.save()
		e.mu.Unlock()
		return
	}
	if cursor != nil { // a poll that worked
		at := now
		ws.LastPollAt, ws.Cursor = &at, *cursor
		ws.Worked()
	}
	fresh := ws.New(evs, now)
	if len(fresh) > 0 && cursor == nil {
		ws.Worked() // a stream printing events works
	}
	// Saved before they're acted on: an event is acted on at most once.
	e.save()
	targets := e.listening[watcher]
	e.mu.Unlock()
	for _, ev := range fresh {
		for _, t := range targets {
			if t.on.Matches(ev.Type) {
				e.offer(ctx, t, ev, now)
			}
		}
	}
}

// offer hands an event to a hook, unless it's past the hook's staleness.
func (e *events) offer(ctx context.Context, l listener, ev watchers.Event, now time.Time) {
	if late := now.Sub(ev.At); late > l.staleness {
		logf("%s: dropped %s event %s: it happened %s ago, past its staleness (%s)", l.name, ev.Type, ev.ID,
			late.Round(time.Second), settings.DurationText(l.staleness))
		return
	}
	due := dispatch.Due{Target: l.name, Occurrence: EventOccurrence(trigger.Event, ev), Staleness: l.staleness, Parallel: l.parallel}
	if l.batch {
		due.Merge = mergeEvents
	}
	e.disp.Submit(ctx, due)
}

// admission is what happens to a hook's run that's due to start.
type admission int

const (
	admit admission = iota
	overRate
	pause
)

// admit counts a run due to start against the hook's rate.
func (e *events) admit(name string, rate int, now time.Time) (admission, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	recent := func(ts []time.Time) []time.Time {
		kept := ts[:0]
		for _, t := range ts {
			if now.Sub(t) < rateWindow {
				kept = append(kept, t)
			}
		}
		return kept
	}
	e.attempts[name] = append(recent(e.attempts[name]), now)
	n := len(e.attempts[name])
	if n > LoopFactor*rate {
		e.attempts[name], e.started[name] = nil, nil
		return pause, n
	}
	e.started[name] = recent(e.started[name])
	if len(e.started[name]) >= rate {
		return overRate, n
	}
	e.started[name] = append(e.started[name], now)
	return admit, n
}

// run runs a hook for an occurrence the dispatcher hands over.
func (e *events) run(ctx context.Context, d dispatch.Due) {
	name := d.Target
	a, err := e.reload()
	if err != nil {
		logf("%s: %v", name, err)
		return
	}
	x, err := a.FindHook(name)
	if err != nil {
		logf("%s: %v", name, err)
		return
	}
	c, err := a.HookConfig(x)
	if err != nil {
		logf("%s: %v", name, err)
		return
	}
	occ, ok := keepFresh(d.Occurrence, c.Staleness, time.Now())
	if !ok {
		logf("%s: dropped %s: it went stale waiting for its turn", name, d.Occurrence.ID)
		return
	}
	switch adm, n := e.admit(name, c.Rate, time.Now()); adm {
	case overRate:
		logf("%s: dropped %s: it already ran %d times in the last hour (its rate)", name, occ.ID, c.Rate)
		return
	case pause:
		e.pause(a, x, occ, fmt.Sprintf("paused: it was due to run %d times in the last hour, more than %d times its rate "+
			"(%d an hour); its action may be setting it off again (a loop). Resume it with `%s hooks resume %s`",
			n, LoopFactor, c.Rate, product.Command, name))
		return
	}
	if !untilFullyAwake(ctx, name) {
		run := runOutcome(time.Now(), time.Now(), nil, errors.New("interrupted before it started (the scheduler stopped while waiting for the computer to wake fully)"))
		recordRun(a, name, occ, run, nil)
		return
	}
	e.spaced(name)
	logf("%s: running for %s", name, occ.ID)
	res, err := a.Run(ctx, x, RunOptions{Occurrence: occ, QueuedAt: d.QueuedAt, StaleAfter: occ.At.Add(c.Staleness), Log: logWriter{name}})
	switch {
	case errors.Is(err, ErrStopped):
		e.disp.Drop(name) // stopping a hook's run drops what waited behind it
		logf("%s: stopped", name)
	case err != nil:
		logf("%s: failed: %v", name, err)
	default:
		logf("%s: done [%s]", name, res.Line)
	}
}

// spaced waits, when it must, so that two runs of a hook never start in the
// same second (a run's history ID is its start, to the second).
func (e *events) spaced(name string) {
	e.mu.Lock()
	last := e.lastStart[name]
	e.mu.Unlock()
	if now := time.Now(); now.Truncate(time.Second).Equal(last.Truncate(time.Second)) {
		time.Sleep(last.Truncate(time.Second).Add(time.Second).Sub(now))
	}
	e.mu.Lock()
	e.lastStart[name] = time.Now()
	e.mu.Unlock()
}

// pause pauses a hook that ran far past its rate, recording why as a
// skipped run (so its health says so, and a notification tells me).
func (e *events) pause(a *App, x Runnable, occ trigger.Occurrence, why string) {
	name := x.Name()
	logf("%s: %s", name, why)
	st := settings.Store{Path: a.Layout.Config(), Extra: x.Settings()}
	if _, err := st.Set("hooks."+name+".enabled", "false"); err != nil {
		logf("%s: couldn't pause it: %v", name, err)
	}
	e.disp.Drop(name)
	e.mu.Lock()
	for w, ls := range e.listening {
		kept := ls[:0]
		for _, l := range ls {
			if l.name != name {
				kept = append(kept, l)
			}
		}
		e.listening[w] = kept
	}
	e.mu.Unlock()
	e.spaced(name)
	now := time.Now()
	recordRun(a, name, occ, runstatus.Run{StartedAt: now, FinishedAt: &now, Outcome: runstatus.OutcomeSkipped, Reason: why}, nil)
}

// missed is a hook's run that went stale waiting behind other runs, or found
// too many runs of it waiting already.
func (e *events) missed(d dispatch.Due, late time.Duration) {
	name := d.Target
	if late <= d.Staleness {
		logf("%s: dropped %s: %d runs of it are waiting already", name, d.Occurrence.ID, dispatch.MaxPending)
		if a, err := e.reload(); err == nil {
			if x, err := a.FindHook(name); err == nil {
				if c, err := a.HookConfig(x); err == nil {
					if adm, n := e.admit(name, c.Rate, time.Now()); adm == pause {
						e.pause(a, x, d.Occurrence, fmt.Sprintf("paused: it was due to run %d times in the last hour, more than %d times its rate "+
							"(%d an hour). Resume it with `%s hooks resume %s`", n, LoopFactor, c.Rate, product.Command, name))
					}
				}
			}
		}
		return
	}
	a, err := e.reload()
	if err != nil {
		return
	}
	logf("%s: missed %s (%s late)", name, d.Occurrence.ID, late.Round(time.Second))
	recordRun(a, name, d.Occurrence, runstatus.Run{
		StartedAt: d.Occurrence.At, Outcome: runstatus.OutcomeMissed,
		Reason: fmt.Sprintf("not run: it waited for its turn until %s late, past its staleness", late.Round(time.Second)),
	}, nil)
}

// queued records a hook's run waiting in the queue behind another run.
func (e *events) queued(d dispatch.Due) {
	a, err := e.reload()
	if err != nil || d.QueuedAt == nil {
		return
	}
	logf("%s: queued behind another run", d.Target)
	writeStatus(a, d.Target, forOccurrence(runstatus.Run{StartedAt: *d.QueuedAt, QueuedAt: d.QueuedAt, Outcome: runstatus.OutcomeQueued}, d.Occurrence))
}

// stop stops the watchers and waits for them and the hooks' runs.
func (e *events) stop() {
	e.mu.Lock()
	for name, r := range e.running {
		r.cancel()
		delete(e.running, name)
	}
	e.mu.Unlock()
	e.runners.Wait()
	e.disp.Wait()
}

// hookSetups is how each hook is set up, for run status: enabled, and why it
// can't run when it's on (its settings, its action's problems, its watcher
// missing, untrusted or failing, or it isn't trusted).
func hookSetups(a *App) map[string]runstatus.Setup {
	out := map[string]runstatus.Setup{}
	all, broken, err := a.Hooks()
	if err != nil {
		return out
	}
	for _, b := range broken {
		out[b.Name] = runstatus.Setup{Hook: true, Enabled: true, Problem: b.Err.Error()}
	}
	for _, x := range all {
		rd := a.Assess(x)
		setup := runstatus.Setup{Hook: true, Enabled: rd.Config.Enabled}
		switch {
		case rd.ConfigErr != nil:
			setup.Enabled, setup.Problem = true, rd.ConfigErr.Error()
		case rd.Config.Enabled:
			var why []string
			for _, p := range rd.All() {
				why = append(why, p.Text)
			}
			setup.Problem = strings.Join(why, "; ")
		}
		out[x.Name()] = setup
	}
	return out
}
