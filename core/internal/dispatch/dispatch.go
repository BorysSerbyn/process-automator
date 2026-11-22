// Package dispatch runs occurrences of triggers: sources (watchers' events
// today; the scheduler's slots once it's moved here) submit what's due, and
// the dispatcher runs each target for it, taking turns as runs must. It's pure
// policy, like the scheduler: it knows staleness, turns and batching, and
// calls back to run a target or record one that went stale.
//
// Semantics, the same as the scheduler's:
//   - A target never overlaps itself: an occurrence for a target that's
//     running (or waiting) waits behind it, in order, up to MaxPending;
//     one past that is dropped as stale.
//   - Queued targets run one at a time, tightest staleness first; parallel
//     ones start right away. Submitting never waits for runs.
//   - Staleness is checked just before each run: an occurrence that waited
//     past it is dropped (Missed), not run late.
//   - A target that batches folds an occurrence into the one already
//     waiting for it (Due.Merge), so one run gets them all.
package dispatch

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

// MaxPending is how many occurrences may wait behind a target's run.
const MaxPending = 50

// Due is an occurrence a target is due to run for.
type Due struct {
	Target     string
	Occurrence trigger.Occurrence
	// Staleness is how long after Occurrence.At it may still start.
	Staleness time.Duration
	// Parallel runs it alongside other runs instead of in the queue.
	Parallel bool
	// Merge, if set, folds a later occurrence for the same target into this
	// one while it waits (batching).
	Merge func(waiting, next trigger.Occurrence) trigger.Occurrence
	// QueuedAt is when it started waiting in the queue behind another run.
	QueuedAt *time.Time
}

// Dispatcher runs dues. Run and Missed are required.
type Dispatcher struct {
	// Run runs a target for an occurrence and records its outcome.
	Run func(ctx context.Context, d Due)
	// Missed records an occurrence that went stale before it could start
	// (late is how late it was), or that found too many waiting already.
	Missed func(d Due, late time.Duration)
	// Queued, if set, records an occurrence waiting in the queue behind
	// another run. Called outside the dispatcher's lock.
	Queued func(d Due)
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	targets map[string]*target
	queue   []*Due // queued targets' next runs, waiting for the worker
	working bool
	runs    sync.WaitGroup
}

// target is one target's runs: whether one is going (running, or waiting in
// the queue), and what waits behind it.
type target struct {
	busy    bool
	queued  *Due   // its run waiting in the queue, not yet handed to Run
	pending []*Due // waiting behind its run
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Dispatcher) target(name string) *target {
	if d.targets == nil {
		d.targets = map[string]*target{}
	}
	t := d.targets[name]
	if t == nil {
		t = &target{}
		d.targets[name] = t
	}
	return t
}

// Submit hands over a due: it runs now (or takes its turn in the queue), or
// waits behind the target's run going on. ctx is what runs get.
func (d *Dispatcher) Submit(ctx context.Context, due Due) {
	var missed *Due
	var queued []*Due
	d.mu.Lock()
	t := d.target(due.Target)
	switch {
	case due.Merge != nil && t.queued != nil:
		t.queued.Occurrence = due.Merge(t.queued.Occurrence, due.Occurrence)
	case due.Merge != nil && len(t.pending) > 0:
		last := t.pending[len(t.pending)-1]
		last.Occurrence = due.Merge(last.Occurrence, due.Occurrence)
	case t.busy && len(t.pending) >= MaxPending:
		missed = &due
	case t.busy:
		t.pending = append(t.pending, &due)
	default:
		queued = d.start(ctx, t, &due, false)
	}
	d.mu.Unlock()
	if missed != nil {
		d.Missed(*missed, d.now().Sub(missed.Occurrence.At))
	}
	d.announce(queued)
}

// start runs due for t (locked): at once when it's parallel, else through the
// queue. It returns what now waits in the queue behind another run. ownTurn
// is set when the worker starts what waited behind t's own run: it's next,
// not waiting behind another.
func (d *Dispatcher) start(ctx context.Context, t *target, due *Due, ownTurn bool) []*Due {
	t.busy = true
	if due.Parallel {
		d.runs.Add(1)
		go func() {
			defer d.runs.Done()
			d.runOne(ctx, due)
			d.finished(ctx, due.Target, false)
		}()
		return nil
	}
	var waiting []*Due
	if d.working && !ownTurn || len(d.queue) > 0 {
		at := d.now()
		due.QueuedAt = &at
		waiting = append(waiting, due)
	}
	t.queued = due
	d.queue = append(d.queue, due)
	if !d.working {
		d.working = true
		d.runs.Add(1)
		go d.work(ctx)
	}
	return waiting
}

func (d *Dispatcher) announce(waiting []*Due) {
	if d.Queued == nil {
		return
	}
	for _, w := range waiting {
		d.Queued(*w)
	}
}

// runOne runs due, or records it as missed when it's gone stale.
func (d *Dispatcher) runOne(ctx context.Context, due *Due) {
	if late := d.now().Sub(due.Occurrence.At); late > due.Staleness {
		d.Missed(*due, late)
		return
	}
	d.Run(ctx, *due)
}

// work runs queued targets one at a time, tightest staleness first, until
// the queue is empty. After shutdown (ctx done) it still hands each one to
// Run, which records that it didn't run.
func (d *Dispatcher) work(ctx context.Context) {
	defer d.runs.Done()
	for {
		d.mu.Lock()
		if len(d.queue) == 0 {
			d.working = false
			d.mu.Unlock()
			return
		}
		sort.SliceStable(d.queue, func(a, b int) bool {
			if d.queue[a].Staleness != d.queue[b].Staleness {
				return d.queue[a].Staleness < d.queue[b].Staleness
			}
			return d.queue[a].Target < d.queue[b].Target
		})
		due := d.queue[0]
		d.queue = d.queue[1:]
		d.target(due.Target).queued = nil
		d.mu.Unlock()
		d.runOne(ctx, due)
		d.finished(ctx, due.Target, true)
	}
}

// finished starts what waited behind a target's run, if anything (byWorker:
// the queue's worker ran it).
func (d *Dispatcher) finished(ctx context.Context, name string, byWorker bool) {
	d.mu.Lock()
	t := d.target(name)
	t.busy = false
	var queued []*Due
	if len(t.pending) > 0 {
		next := t.pending[0]
		t.pending = t.pending[1:]
		queued = d.start(ctx, t, next, byWorker)
	}
	d.mu.Unlock()
	d.announce(queued)
}

// Busy reports whether a target has a run going (running, or waiting in the
// queue).
func (d *Dispatcher) Busy(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.targets[name]
	return t != nil && t.busy
}

// Drop takes a target's runs that haven't started out of the dispatcher: its
// run waiting in the queue (returned, when there was one) and whatever waits
// behind its run. It's how a waiting run is stopped.
func (d *Dispatcher) Drop(name string) (Due, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.targets[name]
	if t == nil {
		return Due{}, false
	}
	t.pending = nil
	if t.queued == nil {
		return Due{}, false
	}
	due := *t.queued
	for i, q := range d.queue {
		if q == t.queued {
			d.queue = append(d.queue[:i], d.queue[i+1:]...)
			break
		}
	}
	t.queued, t.busy = nil, false
	return due, true
}

// Wait waits for the runs the dispatcher started. Call it after the last
// Submit (on shutdown).
func (d *Dispatcher) Wait() { d.runs.Wait() }
