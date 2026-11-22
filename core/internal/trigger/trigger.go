// Package trigger describes why a run happens: an occurrence of one of a
// routine's or a hook's triggers. Runs carry an occurrence (not a bare time)
// through the scheduler and the dispatcher, run execution, run status and
// history, whatever fired them: a schedule's slot, a hand, or an event.
package trigger

import "time"

// Trigger kinds.
const (
	// Schedule is a cron slot fired by the scheduler.
	Schedule = "schedule"
	// Manual is `procauto run` (or a frontend's "run now").
	Manual = "manual"
	// Backfill is `procauto run --date`: a run as of a past day.
	Backfill = "backfill"
	// Event is a hook's run for an event a watcher printed (a hook fired by
	// hand with a sample event is Manual).
	Event = "event"
)

// Occurrence is one firing of a trigger.
type Occurrence struct {
	// Trigger is the kind of trigger that fired (Schedule, Manual, Backfill,
	// Event).
	Trigger string `json:"trigger"`
	// ID is unique per routine and trigger, e.g. "schedule:2026-11-19T09:20:00".
	ID string `json:"id"`
	// At is the time the run is for: the slot, the start of a manual run, or
	// the time on the backfilled day. A routine's today/since/now come from it.
	At time.Time `json:"at"`
	// Data is what the trigger carries: a hook's events, under "events".
	Data map[string]any `json:"data,omitempty"`
}

// New is an occurrence of kind at t, identified by kind and time.
func New(kind string, t time.Time) Occurrence {
	return Occurrence{Trigger: kind, ID: kind + ":" + t.Format("2006-01-02T15:04:05"), At: t}
}

// Input is the occurrence as the routine's `trigger` input.
func (o Occurrence) Input() map[string]any {
	in := map[string]any{"kind": o.Trigger, "id": o.ID, "at": o.At.Format(time.RFC3339)}
	if len(o.Data) > 0 {
		in["data"] = o.Data
	}
	return in
}
