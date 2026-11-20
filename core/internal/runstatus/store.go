package runstatus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// Load reads the status file; a missing file is an empty status.
func Load(path string) (*Status, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Status{SchemaVersion: SchemaVersion, Health: HealthNone, Routines: []Routine{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Record stores a routine's latest run. A routine Sync hasn't listed yet is
// added as enabled; the next Sync corrects that.
func Record(path, routine string, run Run, now time.Time) error {
	return update(path, now, func(s *Status) {
		for i := range s.Routines {
			if s.Routines[i].Name == routine {
				s.Routines[i].LastRun = &run
				return
			}
		}
		s.Routines = append(s.Routines, Routine{Name: routine, Enabled: true, LastRun: &run})
	})
}

// Setup is how a routine is set up on the device, as Sync records it.
type Setup struct {
	Enabled   bool
	NextRunAt *time.Time
	Problem   string
}

// Sync makes the file list exactly the routines set up on the device, keeping
// their last runs. Removed routines (and their old failures) drop out.
func Sync(path string, routines map[string]Setup, now time.Time) error {
	return update(path, now, func(s *Status) {
		last := map[string]*Run{}
		for _, r := range s.Routines {
			last[r.Name] = r.LastRun
		}
		s.Routines = s.Routines[:0]
		for name, setup := range routines {
			s.Routines = append(s.Routines, Routine{
				Name: name, Enabled: setup.Enabled, Problem: setup.Problem,
				NextRunAt: setup.NextRunAt, LastRun: last[name],
			})
		}
	})
}

// Heartbeat says the scheduler is alive: the status holds until now+ttl.
// Only the scheduler beats; other writers (manual runs) leave it alone.
func Heartbeat(path string, now time.Time, ttl time.Duration) error {
	return update(path, now, func(s *Status) {
		until := now.Add(ttl)
		s.ValidUntil = &until
	})
}

// health is the core's verdict at a given time: an error when the scheduler
// can't work, or when an enabled routine can't be scheduled, its last run
// didn't succeed (or is stuck running), or the scheduler didn't pick up its
// slot in time.
func health(s *Status, now time.Time) Health {
	h, _ := assess(s, now)
	return h
}

// assess is health and the reasons for it, from one set of rules.
func assess(s *Status, now time.Time) (Health, []Reason) {
	reasons := []Reason{}
	if s.Problem != "" {
		reasons = append(reasons, Reason{Text: "The scheduler can't work", Detail: s.Problem})
	}
	if n := len(s.SetupLeft); n > 0 {
		reasons = append(reasons, Reason{Text: fmt.Sprintf("Setup isn't finished (%d to do)", n),
			Detail: "Still to do: " + strings.Join(s.SetupLeft, ", ")})
	}
	verdict := HealthNone
	for _, r := range s.Routines {
		if !r.Enabled {
			continue
		}
		if why := whyUnhealthy(r, now); why.Text != "" {
			reasons = append(reasons, why)
		}
		verdict = HealthOK
	}
	if len(reasons) > 0 {
		return HealthError, reasons
	}
	return verdict, reasons
}

// summary is the state in a few words when there are no reasons.
func summary(h Health) string {
	switch h {
	case HealthOK:
		return "All good"
	case HealthNone:
		return "No routines enabled"
	default:
		return ""
	}
}

// routineState is a routine's own health and summary.
func routineState(r Routine, now time.Time) (Health, string) {
	if !r.Enabled {
		return HealthNone, "Paused"
	}
	if why := whyUnhealthy(r, now); why.Text != "" {
		return HealthError, why.Text
	}
	if run := r.LastRun; run != nil {
		switch run.Outcome {
		case OutcomeRunning:
			return HealthOK, "Running since " + run.StartedAt.Local().Format("15:04")
		case OutcomeQueued:
			return HealthOK, "Waiting for its turn"
		}
	}
	if r.NextRunAt != nil {
		return HealthOK, "Next: " + when(*r.NextRunAt, now)
	}
	return HealthOK, "Runs by hand"
}

// when is a time soon, as people say it: "today 09:20", "tomorrow 09:20",
// "Mon 09:20", or "Nov 30 09:20" further off.
func when(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.Local) }
	days := int(day(t).Sub(day(now)).Hours()/24 + 0.5)
	switch {
	case days == 0:
		return "today " + t.Format("15:04")
	case days == 1:
		return "tomorrow " + t.Format("15:04")
	case days > 1 && days < 7:
		return t.Format("Mon 15:04")
	default:
		return t.Format("Jan 2 15:04")
	}
}

// busy reports whether a routine is running right now.
func busy(s *Status, now time.Time) bool {
	for _, r := range s.Routines {
		if run := r.LastRun; run != nil && run.Outcome == OutcomeRunning && !stuck(*run, now) {
			return true
		}
	}
	return false
}

// stuck reports whether a running run is past its deadline plus StuckGrace
// (or MaxRunDuration, for runs recorded without one).
func stuck(run Run, now time.Time) bool {
	if run.Deadline != nil {
		return now.Sub(*run.Deadline) > StuckGrace
	}
	return now.Sub(run.StartedAt) > MaxRunDuration
}

// whyUnhealthy says why an enabled routine makes health error (Text empty
// when it doesn't).
func whyUnhealthy(r Routine, now time.Time) Reason {
	why := Reason{Routine: r.Name}
	if r.Problem != "" {
		why.Text, why.Detail = r.Name+" can't be scheduled", r.Problem
		return why
	}
	if r.NextRunAt != nil && now.Sub(*r.NextRunAt) > SchedulerGrace {
		why.Text = fmt.Sprintf("%s didn't start at %s", r.Name, r.NextRunAt.Local().Format("15:04"))
		why.Detail = "Its slot passed without a run: the scheduler may be stuck."
		return why
	}
	run := r.LastRun
	if run == nil {
		return why
	}
	if run.Outcome != OutcomeRunning {
		why.Run = run.ID // a run still going has no history record yet
	}
	switch run.Outcome {
	case OutcomeSuccess, OutcomeStopped, OutcomeQueued:
	case OutcomeRunning:
		if stuck(*run, now) {
			why.Text, why.Detail = r.Name+" is stuck", "Its run is still going past its deadline."
		}
	case OutcomeNeedsReauth:
		why.Text, why.Detail = r.Name+" needs a sign-in", run.Reason
	case OutcomeMissed:
		why.Text, why.Detail = r.Name+" missed its run", run.Reason
	case OutcomeSkipped:
		why.Text, why.Detail = r.Name+" was skipped", run.Reason
	default:
		why.Text, why.Detail = r.Name+" failed", run.Reason
	}
	return why
}

// SetSetup records the setup steps still to do (none: setup is done).
func SetSetup(path string, left []string, now time.Time) error {
	return update(path, now, func(s *Status) { s.SetupLeft = left })
}

// SetProblem records why the scheduler can't work; "" clears it.
func SetProblem(path, problem string, now time.Time) error {
	return update(path, now, func(s *Status) { s.Problem = problem })
}

// MarkInterrupted fails every run still marked running or queued: called
// when the scheduler starts, so a run cut off by a crash or restart isn't
// lost. It returns the runs it failed, by routine.
func MarkInterrupted(path string, now time.Time) (map[string]Run, error) {
	marked := map[string]Run{}
	err := update(path, now, func(s *Status) {
		for i := range s.Routines {
			if run := s.Routines[i].LastRun; run != nil && (run.Outcome == OutcomeRunning || run.Outcome == OutcomeQueued) {
				fin := now
				run.Reason = "interrupted: the scheduler stopped during the run"
				if run.Outcome == OutcomeQueued {
					run.Reason = "interrupted: the scheduler stopped while the run waited for its turn"
				}
				run.FinishedAt, run.Outcome = &fin, OutcomeFailed
				marked[s.Routines[i].Name] = *run
			}
		}
	})
	return marked, err
}

// update applies fn under a cross-process lock (the daemon and manual runs
// both write), recomputes health and writes the file atomically, so readers
// never see a partial write.
func update(path string, now time.Time, fn func(*Status)) error {
	return atomicfile.UpdateJSON(path, Load, func(s *Status) error {
		fn(s)
		sort.Slice(s.Routines, func(i, j int) bool { return s.Routines[i].Name < s.Routines[j].Name })
		if s.Routines == nil {
			s.Routines = []Routine{}
		}
		s.SchemaVersion, s.UpdatedAt = SchemaVersion, now
		for i := range s.Routines {
			s.Routines[i].Health, s.Routines[i].Summary = routineState(s.Routines[i], now)
		}
		s.Health, s.Reasons = assess(s, now)
		s.Summary = summary(s.Health)
		s.Busy = busy(s, now)
		return nil
	})
}
