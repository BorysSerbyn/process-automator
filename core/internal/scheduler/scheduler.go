// Package scheduler decides when routines run. It is pure policy: it knows cron
// schedules, staleness and what already ran, and calls back to run a routine or
// record a missed one. The daemon drives it with a periodic Tick.
//
// Semantics:
//   - Each routine's most recent schedule slot that hasn't been handled is due.
//   - A slot older than the routine's staleness is recorded as missed, not run
//     (e.g. the Mac was asleep through the standup).
//   - Slots are marked handled before running: a crash never re-runs a routine
//     that may already have posted (at most once).
//   - Queued routines (the default) run one at a time, tightest staleness
//     first, so a standup isn't held up behind a digest; staleness is
//     re-checked just before each run. Parallel routines start right away,
//     alongside the queue and each other. Ticks never wait for runs.
//   - A routine never overlaps itself: while its run (queued or running) is
//     going, its next slot isn't handled, so it waits, and is missed once it's
//     past its staleness.
//   - With no history (first start), only a slot within its staleness runs.
//   - Disabled routines don't run and don't build up a backlog.
//
// Runs are handed over as trigger occurrences, and what was handled is kept
// per routine and per trigger, so triggers other than cron can be added.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

// Job is one routine as the scheduler sees it.
type Job struct {
	Name      string
	Schedule  string // 5-field cron, local time; empty = manual only
	Staleness time.Duration
	Enabled   bool
	// Parallel runs alongside other runs instead of in the queue.
	Parallel bool
}

// Scheduler runs due jobs. All callbacks are required.
type Scheduler struct {
	// StatePath stores what was handled, per routine and trigger.
	StatePath string
	// Jobs is called on every tick, so config and routine files reload live.
	// Jobs must only return valid schedules; invalid ones are skipped.
	Jobs func() ([]Job, error)
	// Run runs one routine for an occurrence (and records its outcome). The
	// job carries its staleness: a run that has to wait (for runs elsewhere)
	// re-checks it before it starts. queuedAt is when it started waiting in
	// the queue, nil when it didn't.
	Run func(ctx context.Context, j Job, o trigger.Occurrence, queuedAt *time.Time)
	// Missed records an occurrence that was too late to run.
	Missed func(name string, o trigger.Occurrence, late time.Duration)
	// Queued, if set, records an occurrence waiting in the queue behind
	// another run since at. Called outside the scheduler's lock.
	Queued func(name string, o trigger.Occurrence, at time.Time)
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	busy    map[string]bool // routines with a run from this scheduler, waiting or running
	queue   []due           // queued routines waiting for the worker
	working bool            // the queue worker is running
	runs    sync.WaitGroup  // runs (and the worker) in progress
}

// Parse parses a 5-field cron expression (local time).
func Parse(expr string) (cron.Schedule, error) {
	return cron.ParseStandard(expr)
}

// FirstOn is the schedule's first slot on day's date (local), or zero if it
// has none that day or the expression is invalid.
func FirstOn(expr string, day time.Time) time.Time {
	s, err := Parse(expr)
	if err != nil {
		return time.Time{}
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	t := s.Next(start.Add(-time.Nanosecond))
	if !t.Before(start.AddDate(0, 0, 1)) {
		return time.Time{}
	}
	return t
}

// Next is the next time the schedule fires after t, or zero for an invalid one.
func Next(expr string, t time.Time) time.Time {
	s, err := Parse(expr)
	if err != nil {
		return time.Time{}
	}
	return s.Next(t)
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type due struct {
	job    Job
	occ    trigger.Occurrence
	queued *time.Time // when it started waiting in the queue
}

// Tick handles everything due now.
func (s *Scheduler) Tick(ctx context.Context) error {
	jobs, err := s.Jobs()
	if err != nil {
		return err
	}

	state, err := s.load()
	if err != nil {
		return err
	}
	now := s.now()
	var run []due
	for _, j := range jobs {
		if j.Schedule == "" {
			continue
		}
		if !j.Enabled {
			state.setTime(j.Name, trigger.Schedule, now)
			continue
		}
		if s.isBusy(j.Name) {
			continue // its slot waits for the run going on (or goes stale)
		}
		sched, err := Parse(j.Schedule)
		if err != nil {
			continue
		}
		last, seen := state.time(j.Name, trigger.Schedule)
		if !seen {
			last = now.Add(-j.Staleness) // first start: only catch up within staleness
			state.setTime(j.Name, trigger.Schedule, last)
		}
		slot := latestSlot(sched, last, now)
		if slot.IsZero() {
			continue
		}
		state.setTime(j.Name, trigger.Schedule, slot)
		occ := trigger.New(trigger.Schedule, slot)
		if late := now.Sub(slot); late > j.Staleness {
			s.Missed(j.Name, occ, late)
			continue
		}
		run = append(run, due{job: j, occ: occ})
	}
	if err := s.save(state); err != nil {
		return err // don't run what we couldn't mark handled
	}

	sortByStaleness(run) // so Queued names the ones that wait
	var waiting []due
	s.mu.Lock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	for _, d := range run {
		s.busy[d.job.Name] = true
		if d.job.Parallel {
			s.runs.Add(1)
			go func() {
				defer s.runs.Done()
				defer s.done(d.job.Name)
				s.Run(ctx, d.job, d.occ, nil)
			}()
			continue
		}
		if s.working || len(s.queue) > 0 {
			at := now
			d.queued = &at
			waiting = append(waiting, d)
		}
		s.queue = append(s.queue, d)
	}
	if len(s.queue) > 0 && !s.working {
		s.working = true
		s.runs.Add(1)
		go s.work(ctx)
	}
	s.mu.Unlock()
	if s.Queued != nil {
		for _, d := range waiting {
			s.Queued(d.job.Name, d.occ, *d.queued)
		}
	}
	return nil
}

// sortByStaleness orders runs tightest staleness first (then by name).
func sortByStaleness(ds []due) {
	sort.SliceStable(ds, func(a, b int) bool {
		if ds[a].job.Staleness != ds[b].job.Staleness {
			return ds[a].job.Staleness < ds[b].job.Staleness
		}
		return ds[a].job.Name < ds[b].job.Name
	})
}

// work runs queued routines one at a time, tightest staleness first, until
// the queue is empty. After shutdown (ctx done) it still hands each one to
// Run, which records that it didn't run.
func (s *Scheduler) work(ctx context.Context) {
	defer s.runs.Done()
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.working = false
			s.mu.Unlock()
			return
		}
		sortByStaleness(s.queue)
		d := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		if late := s.now().Sub(d.occ.At); late > d.job.Staleness {
			s.Missed(d.job.Name, d.occ, late) // went stale waiting for an earlier run
		} else {
			s.Run(ctx, d.job, d.occ, d.queued)
		}
		s.done(d.job.Name)
	}
}

// Drop takes a routine out of the queue if it's still waiting there (not
// yet handed to Run), returning its occurrence and when it started waiting.
// It's how a waiting run is stopped.
func (s *Scheduler) Drop(name string) (trigger.Occurrence, *time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.queue {
		if d.job.Name == name {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			delete(s.busy, name)
			return d.occ, d.queued, true
		}
	}
	return trigger.Occurrence{}, nil, false
}

func (s *Scheduler) isBusy(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy[name]
}

func (s *Scheduler) done(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.busy, name)
}

// Wait waits for the runs this scheduler started. Call it after the last
// Tick (on shutdown).
func (s *Scheduler) Wait() { s.runs.Wait() }

// latestSlot is the most recent firing in (after, now], or zero.
func latestSlot(sched cron.Schedule, after, now time.Time) time.Time {
	var latest time.Time
	for t := sched.Next(after); !t.After(now); t = sched.Next(t) {
		latest = t
	}
	return latest
}

// state is what each routine has handled, per trigger: for cron, the last
// handled slot (RFC 3339); other triggers keep their own marker (e.g. an ID).
type state struct {
	Version  int                          `json:"version"`
	Routines map[string]map[string]string `json:"routines"`
}

// stateVersion is the state file's format: 2 keeps a marker per trigger
// (version 1, one cron slot per routine, isn't read).
const stateVersion = 2

func (st *state) time(name, kind string) (time.Time, bool) {
	v, ok := st.Routines[name][kind]
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	return t, err == nil
}

func (st *state) setTime(name, kind string, t time.Time) {
	if st.Routines[name] == nil {
		st.Routines[name] = map[string]string{}
	}
	st.Routines[name][kind] = t.Format(time.RFC3339Nano)
}

func (s *Scheduler) load() (*state, error) {
	st := &state{Version: stateVersion, Routines: map[string]map[string]string{}}
	b, err := os.ReadFile(s.StatePath)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("%s: %w", s.StatePath, err)
	}
	if st.Version != stateVersion {
		return nil, fmt.Errorf("%s: unknown state version %d", s.StatePath, st.Version)
	}
	if st.Routines == nil {
		st.Routines = map[string]map[string]string{}
	}
	return st, nil
}

func (s *Scheduler) save(st *state) error { return atomicfile.WriteJSON(s.StatePath, st) }
