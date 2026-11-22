package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/scheduler"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

// Daemon timings.
const (
	// TickInterval is how often the daemon checks for due routines.
	TickInterval = 30 * time.Second
	// HeartbeatInterval is how often the daemon confirms it's alive in run status.
	HeartbeatInterval = 10 * time.Second
	// HeartbeatTTL is how long one heartbeat holds. If the daemon stops (crash,
	// not running) the status expires, which frontends report as not responding.
	HeartbeatTTL = time.Minute
	// WakeSettle is how long the daemon gives the network to come back after
	// the computer wakes, before starting runs.
	WakeSettle = 15 * time.Second
	// awakePoll is how often a run waiting for the computer to wake fully
	// (not a maintenance wake) checks again.
	awakePoll = 15 * time.Second
)

// Daemon runs routines on their schedules until ctx ends, re-reading config,
// settings and routine files on every check. Only one runs at a time (a
// lock).
func (a *App) Daemon(ctx context.Context) error {
	unlock, err := filelock.Lock(a.Layout.DaemonLock(), false)
	if err != nil {
		return errors.New("another " + product.Command + " daemon is already running")
	}
	defer unlock()

	plugins := a.Plugins
	reload := func() (*App, error) { return Load(a.Layout.Config(), plugins, a.Connector.Secrets) }
	logf("daemon started (config %s)", a.Layout.Config())
	statusPath := a.Layout.RunStatus()
	// Runs still marked running were cut off when the last daemon stopped.
	marked, err := runstatus.MarkInterrupted(statusPath, time.Now())
	if err != nil {
		logf("run status: %v", err)
	}
	for name, run := range marked {
		logf("%s: %s", name, run.Reason)
		occ := trigger.Occurrence{Trigger: run.Trigger}
		if occ.Trigger == "" { // recorded before runs carried their trigger
			occ.Trigger = trigger.Schedule
		}
		if run.For != nil {
			occ.At = *run.For
		}
		recordRun(a, name, occ, nil, run, nil)
	}
	go heartbeat(ctx, statusPath)
	// Started by the OS with a minimal PATH: add the login shell's, so the
	// tools routines start (claude, MCP servers) are found as in a terminal.
	// After the heartbeat starts, so a slow shell profile can't delay status.
	pathStart := time.Now()
	if err := sysenv.SaveLoginShellPath(a.Layout.LoginPath()); err != nil {
		logf("login shell PATH: %v (keeping %s)", err, os.Getenv("PATH"))
	} else {
		logf("login shell PATH added (%s)", time.Since(pathStart).Round(100*time.Millisecond))
	}
	// Know the plan's usage before the first run (and for frontends to show).
	go func() {
		if _, err := a.Usage(ctx, time.Now()); err != nil {
			logf("usage: %v", err)
		}
	}()

	s := &scheduler.Scheduler{
		StatePath: a.Layout.SchedulerState(),
		Jobs: func() ([]scheduler.Job, error) {
			cur, err := reload()
			if err != nil {
				return nil, err
			}
			jobs, problems, err := scheduledJobs(cur)
			if err != nil {
				return nil, err
			}
			// Before due slots are handled, so each one's nextRunAt has already
			// moved on: a nextRunAt left in the past means the scheduler stalled.
			if err := runstatus.Sync(cur.Layout.RunStatus(), statusSetups(jobs, problems, time.Now()), time.Now()); err != nil {
				logf("run status: %v", err)
			}
			recordSetup(cur)
			return jobs, nil
		},
		Run: func(ctx context.Context, j scheduler.Job, occ trigger.Occurrence, queuedAt *time.Time) {
			name := j.Name
			// Anything that stops the routine from running is recorded as a
			// failed run, not just logged: the slot is already handled.
			fail := func(err error) {
				logf("%s: %v", name, err)
				run := runOutcome(occ.At, time.Now(), nil, err)
				run.QueuedAt = queuedAt
				recordRun(a, name, occ, nil, run, nil)
			}
			cur, err := reload()
			if err != nil {
				fail(err)
				return
			}
			r, set, err := cur.Routine(name)
			if err != nil {
				fail(err)
				return
			}
			if !untilFullyAwake(ctx, name) {
				fail(errors.New("interrupted before it started (the scheduler stopped while waiting for the computer to wake fully)"))
				return
			}
			logf("%s: running (slot %s)", name, occ.At.Format("Mon 15:04"))
			res, err := cur.Execute(ctx, set, r, RunOptions{
				Occurrence: occ, QueuedAt: queuedAt, StaleAfter: occ.At.Add(j.Staleness), Log: logWriter{name},
			})
			if err != nil {
				logf("%s: failed: %v", name, err)
				return
			}
			logf("%s: done [%s]", name, res.Summary())
		},
		Queued: func(name string, occ trigger.Occurrence, at time.Time) {
			logf("%s: queued behind another run (slot %s)", name, occ.At.Format("Mon 15:04"))
			writeStatus(statusPath, name, forOccurrence(runstatus.Run{StartedAt: at, QueuedAt: &at, Outcome: runstatus.OutcomeQueued}, occ))
		},
		Missed: func(name string, occ trigger.Occurrence, late time.Duration) {
			logf("%s: missed the %s slot (%s late)", name, occ.At.Format("Mon 15:04"), late.Round(time.Minute))
			recordRun(a, name, occ, nil, runstatus.Run{
				StartedAt: occ.At, Outcome: runstatus.OutcomeMissed,
				Reason: fmt.Sprintf("not run at %s (computer asleep or off?); %s late is past the staleness window",
					occ.At.Format("Mon 15:04"), late.Round(time.Minute)),
			}, nil)
		},
	}

	go stopWaiting(ctx, a, s)

	lastPrune := time.Time{}
	lastTick := time.Now()
	for {
		// Just woken: the network can take a few seconds to come back, and
		// a run that starts without it fails.
		if slept := sysenv.SleptSince(lastTick); slept >= sysenv.SleepGap {
			logf("the computer woke up (asleep %s)", slept.Round(time.Minute))
			sleepCtx(ctx, WakeSettle) // if ctx ends meanwhile, the select below stops the daemon
		}
		lastTick = time.Now()
		// A scheduler that can't work (e.g. the config doesn't load) is a
		// problem in run status, so health turns red; cleared once it works.
		problem := ""
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			logf("scheduler: %v", err)
			problem = err.Error()
		}
		if err := runstatus.SetProblem(statusPath, problem, time.Now()); err != nil {
			logf("run status: %v", err)
		}
		if time.Since(lastPrune) > time.Hour {
			if cur, err := reload(); err == nil {
				pruneHistory(cur)
			}
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			// Runs in progress were cancelled with ctx: let them record how they ended.
			done := make(chan struct{})
			go func() { s.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				logf("runs still going after 30s; they'll be marked interrupted on the next start")
			}
			logf("daemon stopped")
			return nil
		case <-time.After(TickInterval):
		}
	}
}

// untilFullyAwake waits until the computer is awake for work before a
// scheduled run starts: a Mac in a maintenance wake ("dark wake") sleeps again
// within seconds, cutting the run off. Once it has waited, it gives the network
// WakeSettle to come back. The run's staleness is checked after this, so a
// long wait makes it missed. False when ctx ended first.
func untilFullyAwake(ctx context.Context, name string) bool {
	if sysenv.FullyAwake() {
		return true
	}
	logf("%s: waiting for the computer to wake fully (it's in a maintenance wake)", name)
	for !sysenv.FullyAwake() {
		if !sleepCtx(ctx, awakePoll) {
			return false
		}
	}
	return sleepCtx(ctx, WakeSettle)
}

// sleepCtx waits for d, or until ctx ends (false).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// heartbeat keeps the run status valid while the daemon is alive. It also
// re-evaluates health over time (e.g. a slot that isn't picked up makes it "error").
func heartbeat(ctx context.Context, path string) {
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	for {
		if err := runstatus.Heartbeat(path, time.Now(), HeartbeatTTL); err != nil {
			logf("heartbeat: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SyncStatus records each routine's state in run status (paused or not, its
// next run, why it can't be scheduled), as the scheduler does on each pass:
// after a command changes a routine, frontends see it at once, not up to a
// pass later. Until the scheduler has first recorded what's trusted, it's
// left to the scheduler.
func (a *App) SyncStatus(now time.Time) error {
	if _, initialized, err := a.TrustStore().Load(); err != nil || !initialized {
		return err
	}
	jobs, problems, err := scheduledJobs(a)
	if err != nil {
		return err
	}
	return runstatus.Sync(a.Layout.RunStatus(), statusSetups(jobs, problems, now), now)
}

// statusSetups is how each routine is set up, for runstatus.Sync: enabled,
// next slot, and why it can't be scheduled (broken file or settings).
func statusSetups(jobs []scheduler.Job, problems map[string]string, now time.Time) map[string]runstatus.Setup {
	setups := map[string]runstatus.Setup{}
	for _, j := range jobs {
		setup := runstatus.Setup{Enabled: j.Enabled}
		if j.Enabled && j.Schedule != "" {
			next := scheduler.Next(j.Schedule, now)
			setup.NextRunAt = &next
		}
		setups[j.Name] = setup
	}
	for name, problem := range problems {
		setups[name] = runstatus.Setup{Enabled: true, Problem: problem}
	}
	return setups
}

// scheduledJobs is every routine with its effective settings, plus why the
// others can't be scheduled (by routine name). Jobs always have valid schedules.
func scheduledJobs(a *App) ([]scheduler.Job, map[string]string, error) {
	if err := a.InitTrust(); err != nil {
		return nil, nil, err
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return nil, nil, err
	}
	problems := map[string]string{}
	for _, e := range set.Errors {
		problems[strings.TrimSuffix(filepath.Base(e.Path), routines.Ext)] = e.Err.Error()
	}
	jobs := make([]scheduler.Job, 0, len(set.Routines))
	for _, r := range set.Routines {
		rd := a.Assess(r)
		if problem := scheduleProblem(rd); problem != "" {
			problems[r.Name] = problem
			continue
		}
		c := rd.Config
		jobs = append(jobs, scheduler.Job{Name: r.Name, Enabled: c.Enabled, Schedule: c.Schedule, Staleness: c.Staleness, Parallel: c.Parallel})
	}
	return jobs, problems, nil
}

// scheduleProblem is why a routine can't be scheduled ("" when it can): its
// settings don't resolve or its schedule doesn't parse, or, when it's enabled
// and scheduled, what keeps it from running (a missing folder or server, or
// it isn't trusted).
func scheduleProblem(rd Readiness) string {
	c := rd.Config
	if rd.ConfigErr != nil {
		return rd.ConfigErr.Error()
	}
	if c.Schedule == "" {
		return ""
	}
	if _, err := scheduler.Parse(c.Schedule); err != nil {
		return err.Error()
	}
	if !c.Enabled {
		return ""
	}
	var why []string
	for _, p := range rd.All() {
		why = append(why, p.Text)
	}
	return strings.Join(why, "; ")
}

// pruneHistory deletes history entries older than history.retention-days.
func pruneHistory(a *App) {
	vals, err := a.Settings()
	if err != nil {
		logf("history: %v", err)
		return
	}
	days := vals.Int("history.retention-days")
	if err := history.Prune(a.Layout.History(), time.Now().AddDate(0, 0, -days)); err != nil {
		logf("history: %v", err)
	}
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// logWriter prefixes a routine's progress lines (tool calls) in the daemon log.
type logWriter struct{ routine string }

func (w logWriter) Write(p []byte) (int, error) {
	logf("%s: %s", w.routine, string(trimNL(p)))
	return len(p), nil
}

func trimNL(p []byte) []byte {
	for len(p) > 0 && p[len(p)-1] == '\n' {
		p = p[:len(p)-1]
	}
	return p
}

// stopWaiting stops runs still waiting in the scheduler's queue when a stop
// request for them comes in (runs already handed over watch for it
// themselves): they're taken out of the queue and recorded as stopped.
func stopWaiting(ctx context.Context, a *App, s *scheduler.Scheduler) {
	t := time.NewTicker(stopPoll)
	defer t.Stop()
	dir := a.Layout.StopRequests()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // no requests yet
		}
		for _, e := range entries {
			name := e.Name()
			occ, queuedAt, ok := s.Drop(name)
			if !ok {
				continue
			}
			_ = os.Remove(filepath.Join(dir, name))
			logf("%s: stopped while waiting for its turn", name)
			now := time.Now()
			recordRun(a, name, occ, nil, runstatus.Run{
				StartedAt: now, FinishedAt: &now, QueuedAt: queuedAt,
				Outcome: runstatus.OutcomeStopped, Reason: "stopped on request (" + product.Command + " stop) while waiting for its turn",
			}, nil)
		}
	}
}

// recordSetup puts the setup steps still to do in run status, so unfinished
// setup shows (red, with what's left) without frontends asking.
func recordSetup(a *App) {
	left, err := a.SetupLeft()
	if err != nil {
		logf("setup: %v", err)
		return
	}
	if err := runstatus.SetSetup(a.Layout.RunStatus(), left, time.Now()); err != nil {
		logf("run status: %v", err)
	}
}
