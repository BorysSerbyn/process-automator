package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/scheduler"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
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

// Execute runs one routine once it has its turn (see takeTurn), then
// records the outcome in run status and history. Dry runs record nothing.
func (a *App) Execute(ctx context.Context, set *routines.Set, r routines.Routine, o RunOptions) (*agent.Result, error) {
	called := time.Now()
	occ := o.Occurrence
	if occ.Trigger == "" {
		occ = trigger.New(trigger.Manual, called)
	}
	inputs := routines.Inputs(occ)
	cfg, cfgErr := a.RoutineConfig(r)
	// Dry runs never keep a session: it would sit among my own agent CLI
	// sessions. A kept session's ID is on record while the run runs when the
	// app picks it (see agent.SessionPicker), from its end when the CLI does.
	saveSession := cfgErr == nil && cfg.SaveSession && !o.DryRun
	sessionID, backendName := "", ""
	if !o.DryRun && cfgErr == nil {
		if err := a.InitTrust(); err != nil {
			return nil, err
		}
		why, err := a.Untrusted(r, cfg)
		if err != nil {
			return nil, err
		}
		if why != "" {
			return nil, fmt.Errorf("%s isn't trusted to run for real: %s (a dry run needs no trust)", r.Name, why)
		}
	}
	mem, err := a.Memories().Render(r.Name)
	if err != nil {
		return nil, fmt.Errorf("reading memories: %w", err)
	}
	queuedAt := o.QueuedAt
	// record puts the run's state in run status while it waits or runs;
	// finish records how it ended. Dry runs record nothing.
	record := func(run runstatus.Run) {
		if !o.DryRun {
			run.SessionID, run.Folder, run.QueuedAt, run.Backend = sessionID, cfg.WorkFolder, queuedAt, backendName
			writeStatus(a.Layout.RunStatus(), r.Name, forOccurrence(run, occ))
		}
	}
	finish := func(run runstatus.Run, res *agent.Result) {
		if !o.DryRun {
			run.SessionID, run.Folder, run.QueuedAt, run.Backend = sessionID, cfg.WorkFolder, queuedAt, backendName
			recordRun(a, r.Name, occ, inputs, run, res)
		}
	}

	// A stop request made since the run was asked for (including while it
	// waited in the scheduler's queue) stops it, waiting or running.
	since := called
	if queuedAt != nil {
		since = *queuedAt
	}
	ctx, endWatch := watchStop(ctx, a.Layout.StopRequest(r.Name), since)
	defer endWatch()
	ended := func(started time.Time, res *agent.Result, err error) error {
		var limit *agent.PlanLimitError
		if errors.As(err, &limit) {
			if rerr := usage.RecordLimitHit(a.Layout.PlanUsage(), limit.Window, limit.ResetsAt, time.Now()); rerr != nil {
				logTo(o.Log, "warning: recording the Claude plan limit: %v", rerr)
			}
		}
		run := runOutcome(started, time.Now(), res, err)
		switch {
		case errors.Is(context.Cause(ctx), ErrStopped):
			err = ErrStopped
			run.Outcome, run.Reason = runstatus.OutcomeStopped, "stopped on request ("+product.Command+" stop)"
		case errors.Is(err, context.Canceled), err != nil && ctx.Err() != nil:
			// However the backend says it ended: an agent CLI killed with
			// the run says "signal: killed", not that it was cancelled.
			run.Reason = "interrupted before it finished (the scheduler stopped, or the run was cancelled)"
		}
		finish(run, res)
		return err
	}

	// Its turn: the routine's own lock and, for queued routines, the run queue.
	// Waiting is on record as queued, so a run that's cut off (crash, restart)
	// is found and failed by the scheduler instead of being lost.
	release, err := takeTurn(ctx, a, r.Name, cfg.Parallel, func() {
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
	if !o.DryRun {
		if skip, err := overBudget(ctx, a, occ, o.Log, started); err != nil {
			return nil, ended(started, nil, err)
		} else if skip != "" {
			finish(runstatus.Run{StartedAt: started, FinishedAt: &started, Outcome: runstatus.OutcomeSkipped, Reason: skip}, nil)
			return nil, errors.New("skipped: " + skip)
		}
	}
	defer sysenv.KeepAwake()() // a run isn't cut off by idle sleep
	record(runstatus.Run{StartedAt: started, Outcome: runstatus.OutcomeRunning})

	res, runErr := func() (*agent.Result, error) {
		if cfgErr != nil {
			return nil, cfgErr
		}
		deadline := started.Add(cfg.Timeout)
		backend, err := a.NewBackend()
		if err != nil {
			return nil, err
		}
		backendName = backend.Name()
		if saveSession && !agent.PicksSessionID(backend) {
			sessionID = newSessionID()
		}
		record(runstatus.Run{StartedAt: started, Outcome: runstatus.OutcomeRunning, Deadline: &deadline})
		runCtx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		if cfg.WorkFolder != cfg.Folder { // the routine's own: made on its first run
			if err := os.MkdirAll(cfg.WorkFolder, 0o700); err != nil {
				return nil, err
			}
		}
		ag := &agent.Agent{Connector: a.Connector, Backend: backend, DryRun: o.DryRun, Log: o.Log}
		attempt := func() (*agent.Result, error) {
			return ag.Run(runCtx, agent.Request{
				Name: r.Name, Source: r.Source, Origin: r.Path,
				Input: withParams(inputs, cfg.Params), Tools: r.Tools, Servers: r.Servers,
				Run:      RunOf(r, cfg.WorkFolder, history.RunID(r.Name, started)),
				Partials: set.PromptPartials(mem),
				Effort:   cfg.Effort,
				Folder:   cfg.WorkFolder, SaveSession: saveSession, SessionID: sessionID, BuiltinTools: cfg.BuiltinTools,
			})
		}
		res, err := attempt()
		if err != nil && runCtx.Err() == nil && slept(started) && safeToRetry(res, cfg.BuiltinTools, err) {
			// The computer slept mid-run (a closed lid, a maintenance wake
			// ending): its connections died with it. Nothing was changed yet,
			// so it runs again from the start, once.
			logTo(o.Log, "the computer slept during the run (%v); running it again", err)
			if saveSession && !agent.PicksSessionID(backend) {
				sessionID = newSessionID() // the first attempt's may be taken
			}
			res, err = attempt()
		}
		if res != nil && res.SessionID != "" {
			sessionID = res.SessionID // the one the CLI kept
		}
		if err != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s (routines.%s.timeout): %w", settings.DurationText(cfg.Timeout), r.Name, err)
		}
		return res, err
	}()
	return res, ended(started, res, runErr)
}

// slept is whether the computer slept since a time (a variable for tests).
var slept = sysenv.Slept

// safeToRetry is whether a failed run can run again from the start without
// doing anything twice: it made no changes through its tools, can't have made
// any through the agent CLI's own (it has none), and didn't fail on something
// a retry can't fix (a sign-in, the plan's limit).
func safeToRetry(res *agent.Result, builtinTools []string, err error) bool {
	var limit *agent.PlanLimitError
	switch {
	case len(builtinTools) > 0, errors.Is(err, mcp.ErrNeedsReauth), errors.As(err, &limit):
		return false
	case res == nil:
		return true
	default:
		return len(res.Writes) == 0
	}
}

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
func recordRun(a *App, name string, occ trigger.Occurrence, inputs map[string]any, run runstatus.Run, res *agent.Result) {
	run = forOccurrence(run, occ)
	writeStatus(a.Layout.RunStatus(), name, run)
	saveHistory(a, history.Entry{
		Routine: name, Trigger: occ.Trigger, TriggerID: occ.ID, For: run.For, SessionID: run.SessionID, Folder: run.Folder,
		StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Outcome: run.Outcome, Reason: run.Reason, Inputs: inputs, Result: res,
	})
	addFailureNotification(a, name, run)
}

func runOutcome(started, finished time.Time, res *agent.Result, runErr error) runstatus.Run {
	run := runstatus.Run{StartedAt: started, FinishedAt: &finished, Outcome: runstatus.OutcomeSuccess}
	switch {
	case errors.Is(runErr, mcp.ErrNeedsReauth):
		run.Outcome, run.Reason = runstatus.OutcomeNeedsReauth, runErr.Error()
	case runErr != nil:
		run.Outcome, run.Reason = runstatus.OutcomeFailed, runErr.Error()
	case res != nil && res.Outcome == agent.OutcomeNothingToDo:
		run.Reason = "nothing to do: " + res.Reason
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
