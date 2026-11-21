// Package runstatus defines the run-status record shared with frontends.
// The shape is the contract in contracts/run-status.schema.json; keep both in sync.
package runstatus

import "time"

// SchemaVersion is the contract version this package reads and writes.
const SchemaVersion = 1

// Health is the core's verdict; frontends show it as is.
type Health string

// Health values.
const (
	HealthOK    Health = "ok"    // every routine is healthy
	HealthError Health = "error" // one or more routines aren't
	HealthNone  Health = "none"  // no routines are set up
)

// SchedulerGrace is how long past a slot the scheduler may take to pick it up
// before the routine counts as unhealthy (the scheduler checks every 30s).
const SchedulerGrace = 2 * time.Minute

// StuckGrace is how long past its deadline a run may still be "running" before
// it counts as stuck (the run is cancelled at its deadline; this covers the
// time it takes to wind down and record its outcome).
const StuckGrace = 5 * time.Minute

// MaxRunDuration is the stuck limit for runs recorded without a deadline.
const MaxRunDuration = 25 * time.Minute

// Outcome of a routine run.
type Outcome string

// Outcome values.
const (
	OutcomeSuccess     Outcome = "success"
	OutcomeFailed      Outcome = "failed"
	OutcomeSkipped     Outcome = "skipped"
	OutcomeMissed      Outcome = "missed"
	OutcomeNeedsReauth Outcome = "needs-reauth"
	// OutcomeRunning is a run in progress. One still running after
	// MaxRunDuration, or left behind by a daemon that stopped, isn't healthy.
	OutcomeRunning Outcome = "running"
	// OutcomeQueued is a run waiting for its turn (the run queue, or the same
	// routine's previous run). Waiting doesn't count toward busy, the timeout
	// or the stuck limit.
	OutcomeQueued Outcome = "queued"
	// OutcomeStopped is a run stopped on request (`procauto stop`); not a failure.
	OutcomeStopped Outcome = "stopped"
)

// Status is the whole record.
type Status struct {
	SchemaVersion int       `json:"schemaVersion"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// ValidUntil is the scheduler's heartbeat: the status holds until then.
	// Past it (or absent), the core isn't responding.
	ValidUntil *time.Time `json:"validUntil,omitempty"`
	Health     Health     `json:"health" jsonschema:"enum=ok,enum=error,enum=none"`
	// SetupLeft names the setup steps still to do (`setup --json`'s titles;
	// empty when setup is done). Unfinished setup makes health error.
	SetupLeft []string `json:"setupLeft,omitempty"`
	// Busy is true while a routine is running (not counting runs stuck past
	// MaxRunDuration); frontends show activity from it.
	Busy bool `json:"busy"`
	// Summary is the state in a few words ("All good", "No routines
	// enabled"), for frontends to show when there are no reasons.
	Summary string `json:"summary,omitempty"`
	// Problem says why the scheduler can't work right now (e.g. the config
	// file doesn't load); set and cleared by the scheduler on every check.
	Problem  string    `json:"problem,omitempty"`
	Routines []Routine `json:"routines"`
	// Reasons say why health is error, in words, for frontends to show as
	// they are; empty when it isn't. They come from the same rules as health.
	Reasons []Reason `json:"reasons,omitempty"`
}

// Reason is one thing making health error.
type Reason struct {
	// Routine it's about; empty for the scheduler itself.
	Routine string `json:"routine,omitempty"`
	// Text is a short headline ("standup failed"); Detail the full story (the
	// run's reason), for frontends to show on demand.
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
	// Run is the run it's about (a history ID, as `runs show` takes), if any.
	Run string `json:"run,omitempty"`
}

// Routine is one routine's entry. Sync owns Enabled, NextRunAt and Problem
// (the routine's setup); Record owns LastRun.
type Routine struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Problem says why the routine can't be scheduled (e.g. its file doesn't
	// parse). It's current state, cleared as soon as the routine is fixed.
	Problem string `json:"problem,omitempty"`
	// NextRunAt is the next slot the scheduler will handle (enabled, scheduled
	// routines only). The scheduler moves it forward as soon as it picks a slot
	// up, so one more than SchedulerGrace in the past means it didn't.
	NextRunAt *time.Time `json:"nextRunAt,omitempty"`
	LastRun   *Run       `json:"lastRun,omitempty"`
	// Health is the routine's own verdict: error when it makes the app's
	// health error, ok while it's enabled and fine, none while it's paused.
	Health Health `json:"health,omitempty"`
	// Summary is its state in a few words ("Next: tomorrow 09:20", "standup
	// failed", "Paused"), for frontends to show as is.
	Summary string `json:"summary,omitempty"`
}

// Run is the most recent run of a routine.
type Run struct {
	// Trigger is what started the run: "schedule", "manual" or "backfill"
	// (empty in records from before triggers were recorded).
	Trigger string `json:"trigger,omitempty"`
	// For is the time the run is for (the schedule slot, the backfilled day).
	For *time.Time `json:"for,omitempty"`
	// ID is the run's history ID (as `runs show` takes).
	ID string `json:"id,omitempty"`
	// SessionID is the run's saved agent CLI session (routines with
	// session: save), set when the run starts.
	SessionID string `json:"sessionId,omitempty"`
	// Folder is the folder the run worked in (where its session resumes
	// from), unless it was an empty temporary one.
	Folder     string     `json:"folder,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// Deadline is when a running run is stopped (its routine's timeout).
	Deadline *time.Time `json:"deadline,omitempty"`
	// QueuedAt is when the run started waiting for its turn, if it had to.
	QueuedAt *time.Time `json:"queuedAt,omitempty"`
	Outcome  Outcome    `json:"outcome" jsonschema:"enum=success,enum=failed,enum=skipped,enum=missed,enum=needs-reauth,enum=running,enum=queued,enum=stopped"`
	Reason   string     `json:"reason,omitempty"`
}
