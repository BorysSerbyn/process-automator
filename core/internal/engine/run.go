package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/scheduler"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

// Backfill is a run as of date: at the schedule's first slot that
// day, or at now's time of day for routines with no slot then.
func Backfill(date, schedule string, now time.Time) (trigger.Occurrence, error) {
	day, err := time.ParseInLocation("2006-01-02", date, now.Location())
	if err != nil {
		return trigger.Occurrence{}, fmt.Errorf("--date %q: use YYYY-MM-DD", date)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if day.After(today) {
		return trigger.Occurrence{}, fmt.Errorf("--date %s is in the future", date)
	}
	at := time.Time{}
	if schedule != "" {
		at = scheduler.FirstOn(schedule, day)
	}
	if at.IsZero() {
		at = time.Date(day.Year(), day.Month(), day.Day(), now.Hour(), now.Minute(), now.Second(), 0, now.Location())
	}
	return trigger.New(trigger.Backfill, at), nil
}

// RunOptions say what a run is for and how it runs.
type RunOptions struct {
	DryRun bool
	// Occurrence is what the run is for; zero means a manual run starting now.
	Occurrence trigger.Occurrence
	// QueuedAt is when the run started waiting, if it already waited (in the
	// scheduler's queue) before this call.
	QueuedAt *time.Time
	// StaleAfter, for scheduled runs, is when the slot goes stale: a run that
	// only gets its turn after that is recorded as missed instead of running
	// late. Zero for runs started by hand.
	StaleAfter time.Time
	Log        io.Writer
}

// Run runs a routine of either kind once it has its turn (see takeTurn),
// then records the outcome in run status and history. Dry runs record
// nothing. What's the same for every kind is here: trust, its turn,
// staleness, the usage limit, keeping awake, stopping, the timeout and the
// records; the kind runs it.
func (a *App) Run(ctx context.Context, x Runnable, o RunOptions) (*RunResult, error) {
	called := time.Now()
	name := x.Name()
	occ := o.Occurrence
	if occ.Trigger == "" {
		occ = trigger.New(trigger.Manual, called)
	}
	cfg, cfgErr := x.runConfig(a)
	if cfgErr == nil && (!o.DryRun || cfg.DryRunsNeedTrust) {
		if err := a.InitTrust(); err != nil {
			return nil, err
		}
		why, err := a.Untrusted(x)
		if err != nil {
			return nil, err
		}
		if why != "" {
			if o.DryRun {
				return nil, fmt.Errorf("%s isn't trusted to run, even as a dry run: %s", name, why)
			}
			return nil, fmt.Errorf("%s isn't trusted to run for real: %s (a dry run needs no trust)", name, why)
		}
	}
	queuedAt := o.QueuedAt
	// live is what run status shows of the run besides its state: where it
	// works, and what its kind notes (an agent CLI's session and backend).
	live := runstatus.Run{Folder: cfg.WorkFolder}
	withLive := func(run runstatus.Run) runstatus.Run {
		run.SessionID, run.Folder, run.QueuedAt, run.Backend = live.SessionID, live.Folder, queuedAt, live.Backend
		return run
	}
	// record puts the run's state in run status while it waits or runs;
	// finish records how it ended. Dry runs record nothing.
	record := func(run runstatus.Run) {
		if !o.DryRun {
			writeStatus(a.Layout.RunStatus(), name, forOccurrence(withLive(run), occ))
		}
	}
	finish := func(run runstatus.Run, res *RunResult) {
		if !o.DryRun {
			recordRun(a, name, occ, withLive(run), res)
		}
	}

	// A stop request made since the run was asked for (including while it
	// waited in the scheduler's queue) stops it, waiting or running.
	since := called
	if queuedAt != nil {
		since = *queuedAt
	}
	ctx, endWatch := watchStop(ctx, a.Layout.StopRequest(name), since)
	defer endWatch()
	ended := func(started time.Time, res *RunResult, err error) error {
		run := runOutcome(started, time.Now(), res, err)
		switch {
		case errors.Is(context.Cause(ctx), ErrStopped):
			err = ErrStopped
			run.Outcome, run.Reason = runstatus.OutcomeStopped, "stopped on request ("+product.Command+" stop)"
		case errors.Is(err, context.Canceled), err != nil && ctx.Err() != nil:
			// However the kind says it ended: an agent CLI killed with the
			// run says "signal: killed", not that it was cancelled.
			run.Reason = "interrupted before it finished (the scheduler stopped, or the run was cancelled)"
		}
		finish(run, res)
		return err
	}

	// Its turn: the routine's own lock and, for queued routines, the run queue.
	// Waiting is on record as queued, so a run that's cut off (crash, restart)
	// is found and failed by the scheduler instead of being lost.
	release, err := takeTurn(ctx, a, name, !cfg.Queued, func() {
		if queuedAt == nil {
			t := called
			queuedAt = &t
		}
		record(runstatus.Run{StartedAt: *queuedAt, Outcome: runstatus.OutcomeQueued})
	})
	if err != nil {
		return nil, ended(called, nil, err)
	}
	defer release()
	started := time.Now() // the run itself starts now; waiting doesn't count
	if !o.StaleAfter.IsZero() && started.After(o.StaleAfter) {
		reason := fmt.Sprintf("got its turn at %s, past its staleness (other runs took longer); not run",
			started.Format("15:04"))
		finish(runstatus.Run{StartedAt: occ.At, FinishedAt: &started, Outcome: runstatus.OutcomeMissed, Reason: reason}, nil)
		return nil, errors.New("missed: " + reason)
	}
	if !o.DryRun && cfg.UsesModel {
		if skip, err := overBudget(ctx, a, occ, o.Log, started); err != nil {
			return nil, ended(started, nil, err)
		} else if skip != "" {
			finish(runstatus.Run{StartedAt: started, FinishedAt: &started, Outcome: runstatus.OutcomeSkipped, Reason: skip}, nil)
			return nil, errors.New("skipped: " + skip)
		}
	}
	defer sysenv.KeepAwake()() // a run isn't cut off by idle sleep
	record(runstatus.Run{StartedAt: started, Outcome: runstatus.OutcomeRunning})
	if cfgErr != nil {
		return nil, ended(started, nil, cfgErr)
	}
	deadline := started.Add(cfg.Timeout)
	running := runstatus.Run{StartedAt: started, Outcome: runstatus.OutcomeRunning, Deadline: &deadline}
	record(running)
	runCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	res, err := x.run(runCtx, a, runContext{
		occ: occ, started: started, dryRun: o.DryRun, log: o.Log, cfg: cfg, id: history.RunID(name, started),
		note: func(change func(*runstatus.Run)) {
			change(&live)
			record(running)
		},
	})
	if err != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s (routines.%s.timeout): %w", settings.DurationText(cfg.Timeout), name, err)
	}
	return res, ended(started, res, err)
}

// slept is whether the computer slept since a time (a variable for tests).
var slept = sysenv.Slept

// forOccurrence adds what the run is for to its run-status record.
func forOccurrence(run runstatus.Run, o trigger.Occurrence) runstatus.Run {
	run.Trigger = o.Trigger
	if !o.At.IsZero() {
		at := o.At
		run.For = &at
	}
	return run
}

// recordRun records how a run ended, or why it never ran: in run status, in
// history, and as a notification when it failed (see addFailureNotification).
// res is nil for a run that didn't get as far as its kind's run.
func recordRun(a *App, name string, occ trigger.Occurrence, run runstatus.Run, res *RunResult) {
	run = forOccurrence(run, occ)
	writeStatus(a.Layout.RunStatus(), name, run)
	e := history.Entry{
		Routine: name, Kind: a.kindOf(name).Written(), Trigger: occ.Trigger, TriggerID: occ.ID, For: run.For,
		SessionID: run.SessionID, Folder: run.Folder, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Outcome: run.Outcome, Reason: run.Reason,
	}
	if res != nil {
		e.Kind, e.Inputs, e.Result, e.Program = res.Kind.Written(), res.Inputs, res.Agent, res.Program
	}
	saveHistory(a, e)
	addFailureNotification(a, name, run)
}

func runOutcome(started, finished time.Time, res *RunResult, runErr error) runstatus.Run {
	run := runstatus.Run{StartedAt: started, FinishedAt: &finished, Outcome: runstatus.OutcomeSuccess}
	switch {
	case errors.Is(runErr, mcp.ErrNeedsReauth):
		run.Outcome, run.Reason = runstatus.OutcomeNeedsReauth, runErr.Error()
	case runErr != nil:
		run.Outcome, run.Reason = runstatus.OutcomeFailed, runErr.Error()
	case res != nil:
		run.Reason = res.Note
	}
	return run
}

func writeStatus(statusPath, name string, run runstatus.Run) {
	if run.ID == "" {
		run.ID = history.RunID(name, run.StartedAt) // the history record it is (or will be)
	}
	if err := runstatus.Record(statusPath, name, run, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record run status:", err)
	}
}

// saveHistory records a run in the run history; a failure to write it is only
// warned about (the run's outcome is already in run status).
func saveHistory(a *App, e history.Entry) {
	if err := history.Save(a.Layout.History(), e); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not save run history:", err)
	}
}

// withParams adds a routine's parameter values to its inputs.
func withParams(inputs, params map[string]any) map[string]any {
	out := make(map[string]any, len(inputs)+len(params))
	for k, v := range inputs {
		out[k] = v
	}
	for k, v := range params {
		out[k] = v
	}
	return out
}

// newSessionID is a random UUID (version 4), as agent CLIs take session IDs.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// overBudget is why a scheduled run doesn't start: LLM usage is past the
// usage limit ("" when it isn't). Runs started by hand go ahead, with a warning.
func overBudget(ctx context.Context, a *App, occ trigger.Occurrence, log io.Writer, now time.Time) (string, error) {
	r, err := a.Usage(ctx, now)
	if err != nil || !r.Over {
		return "", err
	}
	if occ.Trigger == trigger.Schedule {
		return r.Reason, nil
	}
	if log != nil {
		fmt.Fprintf(log, "warning: %s; running anyway, as you started it\n", r.Reason)
	}
	return "", nil
}

// logTo writes a line to a run's log, when it has one.
func logTo(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, format+"\n", args...)
	}
}
