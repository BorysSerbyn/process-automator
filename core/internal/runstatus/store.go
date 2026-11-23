package runstatus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
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
	return record(path, routine, false, run, now)
}

// RecordHook stores a hook's latest run, as Record does a routine's.
func RecordHook(path, hook string, run Run, now time.Time) error {
	return record(path, hook, true, run, now)
}

func record(path, name string, hook bool, run Run, now time.Time) error {
	return update(path, now, func(s *Status) {
		for _, list := range [][]Routine{s.Routines, s.Hooks} {
			for i := range list {
				if list[i].Name == name {
					list[i].LastRun = &run
					return
				}
			}
		}
		entry := Routine{Name: name, Enabled: true, LastRun: &run}
		if hook {
			s.Hooks = append(s.Hooks, entry)
		} else {
			s.Routines = append(s.Routines, entry)
		}
	})
}

// Setup is how a routine (or a hook) is set up on the device, as Sync
// records it.
type Setup struct {
	Enabled   bool
	NextRunAt *time.Time
	// Problem is what keeps it from running; a hook's, besides its
	// watcher's (WatcherProblem).
	Problem string
	// Hook lists it among the hooks; Watcher is the watcher it listens to,
	// and WatcherProblem what's wrong with that watcher, said about it.
	Hook                    bool
	Watcher, WatcherProblem string
}

// Sync makes the file list exactly the routines and hooks set up on the
// device, keeping their last runs (and when their health was reset).
// Removed ones (and their old failures) drop out.
func Sync(path string, setups map[string]Setup, now time.Time) error {
	return update(path, now, func(s *Status) {
		prev := map[string]Routine{}
		for _, r := range append(slices.Clone(s.Routines), s.Hooks...) {
			prev[r.Name] = r
		}
		s.Routines, s.Hooks = s.Routines[:0], nil
		for name, setup := range setups {
			entry := Routine{
				Name: name, Enabled: setup.Enabled, Problem: setup.Problem,
				NextRunAt: setup.NextRunAt, LastRun: prev[name].LastRun, HealthResetAt: prev[name].HealthResetAt,
			}
			if setup.Hook {
				entry.Watcher, entry.WatcherProblem = setup.Watcher, setup.WatcherProblem
				if setup.WatcherProblem != "" {
					entry.Problem = joinProblems(setup.Problem, watcherPart(setup.WatcherProblem))
				}
				s.Hooks = append(s.Hooks, entry)
			} else {
				s.Routines = append(s.Routines, entry)
			}
		}
	})
}

// watcherPart is a hook's problem about its watcher, as its Problem ends.
func watcherPart(watcherProblem string) string { return "its watcher " + watcherProblem }

// joinProblems is problems in one line, leaving out empty ones.
func joinProblems(problems ...string) string {
	var out []string
	for _, p := range problems {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

// ownProblem is what keeps a hook from running besides its watcher (all
// of a routine's Problem).
func ownProblem(r Routine) string {
	if r.WatcherProblem == "" {
		return r.Problem
	}
	return strings.TrimSuffix(strings.TrimSuffix(r.Problem, watcherPart(r.WatcherProblem)), "; ")
}

// ResetHealth resets the health of the routines and hooks named (others
// are left alone): what ran before now no longer counts toward it, until
// they run again. Their runs stay recorded as they were. The scheduler
// calls it when a permission one needs changes.
func ResetHealth(path string, names []string, now time.Time) error {
	return update(path, now, func(s *Status) {
		for _, list := range [][]Routine{s.Routines, s.Hooks} {
			for i := range list {
				if slices.Contains(names, list[i].Name) {
					at := now
					list[i].HealthResetAt = &at
				}
			}
		}
	})
}

// reset reports whether a routine's health was reset since its last run
// started (or it has none since): that run no longer counts toward its
// health. A run still going, or waiting for its turn, is current.
func reset(r Routine) bool {
	if r.HealthResetAt == nil {
		return false
	}
	run := r.LastRun
	if run == nil {
		return true
	}
	if run.Outcome == OutcomeRunning || run.Outcome == OutcomeQueued {
		return false
	}
	return run.StartedAt.Before(*r.HealthResetAt)
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
	if n := len(s.Waiting); n > 0 {
		text, titles := "(1) Acknowledgement pending", []string{}
		if n > 1 {
			text = fmt.Sprintf("(%d) Acknowledgements pending", n)
		}
		for _, w := range s.Waiting {
			titles = append(titles, w.Title)
		}
		reasons = append(reasons, Reason{Acknowledge: true, Text: text, Detail: strings.Join(titles, "\n")})
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
	// A watcher's problem is one reason, naming the hooks that listen to it;
	// theirs say only what else is wrong.
	listeners, watcherProblems := map[string][]string{}, map[string]string{}
	var failing []string
	for _, h := range s.Hooks {
		if !h.Enabled {
			continue
		}
		if h.WatcherProblem != "" {
			if _, seen := watcherProblems[h.Watcher]; !seen {
				failing = append(failing, h.Watcher)
				watcherProblems[h.Watcher] = h.WatcherProblem
			}
			listeners[h.Watcher] = append(listeners[h.Watcher], h.Name)
			h.Problem = ownProblem(h)
		}
		if why := whyUnhealthy(h, now); why.Text != "" {
			why.Hook = true
			if strings.HasSuffix(why.Text, " can't be scheduled") {
				why.Text = h.Name + " can't run"
			}
			reasons = append(reasons, why)
		}
		verdict = HealthOK
	}
	sort.Strings(failing)
	for _, w := range failing {
		hooks, verb := listeners[w], "listens"
		if len(hooks) > 1 {
			verb = "listen"
		}
		reasons = append(reasons, Reason{Watcher: w,
			Text: fmt.Sprintf("%s (%s %s to it)", watcherProblems[w], andList(hooks), verb)})
	}
	if len(reasons) > 0 {
		return HealthError, reasons
	}
	return verdict, reasons
}

// andList is names as said: "a", "a and b", "a, b and c".
func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
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
	return entryState(r, now, "Runs by hand")
}

// entryState is a routine's or a hook's own health and summary; idle is
// its summary with nothing scheduled.
func entryState(r Routine, now time.Time, idle string) (Health, string) {
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
	summary := idle
	if r.NextRunAt != nil {
		summary = "Next: " + when(*r.NextRunAt, now)
	}
	if reset(r) {
		// Neither red nor green from before: it hasn't run since.
		return HealthNone, "Permissions changed; " + strings.ToLower(summary[:1]) + summary[1:]
	}
	return HealthOK, summary
}

// hookState is a hook's own health and summary: as a routine's, but it
// runs when its events come.
func hookState(h Routine, now time.Time) (Health, string) {
	health, summary := entryState(h, now, "Waiting for events")
	if health == HealthError && h.Problem != "" {
		return health, h.Name + " can't run"
	}
	return health, summary
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

// busy reports whether a routine (or a hook) is running right now.
func busy(s *Status, now time.Time) bool {
	for _, r := range append(slices.Clone(s.Routines), s.Hooks...) {
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
	if run == nil || reset(r) {
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
// Unchanged, it writes nothing (frontends following run status see no change).
func SetSetup(path string, left []string, now time.Time) error {
	if cur, err := Load(path); err == nil && slices.Equal(cur.SetupLeft, left) {
		return nil
	}
	return update(path, now, func(s *Status) { s.SetupLeft = left })
}

// SetWaiting records the notifications waiting to be acknowledged, as
// waiting gives them: called under the run-status lock, so the last of
// several writers records what the notifications file says last.
// Unchanged, it writes nothing.
func SetWaiting(path string, waiting func() ([]Waiting, error), now time.Time) error {
	return atomicfile.Locked(path, func() error {
		list, err := waiting()
		if err != nil {
			return err
		}
		s, err := Load(path)
		if err != nil {
			return err
		}
		if slices.Equal(s.Waiting, list) {
			return nil
		}
		s.Waiting = list
		recompute(s, now)
		return atomicfile.WriteJSON(path, s)
	})
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
		for _, list := range [][]Routine{s.Routines, s.Hooks} {
			for i := range list {
				if run := list[i].LastRun; run != nil && (run.Outcome == OutcomeRunning || run.Outcome == OutcomeQueued) {
					fin := now
					run.Reason = "interrupted: the scheduler stopped during the run"
					if run.Outcome == OutcomeQueued {
						run.Reason = "interrupted: the scheduler stopped while the run waited for its turn"
					}
					run.FinishedAt, run.Outcome = &fin, OutcomeFailed
					marked[list[i].Name] = *run
				}
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
		recompute(s, now)
		return nil
	})
}

// recompute sorts the lists and works out each entry's and the app's
// health, as of now.
func recompute(s *Status, now time.Time) {
	sort.Slice(s.Routines, func(i, j int) bool { return s.Routines[i].Name < s.Routines[j].Name })
	if s.Routines == nil {
		s.Routines = []Routine{}
	}
	s.SchemaVersion, s.UpdatedAt = SchemaVersion, now
	for i := range s.Routines {
		s.Routines[i].Health, s.Routines[i].Summary = routineState(s.Routines[i], now)
	}
	sort.Slice(s.Hooks, func(i, j int) bool { return s.Hooks[i].Name < s.Hooks[j].Name })
	for i := range s.Hooks {
		s.Hooks[i].Health, s.Hooks[i].Summary = hookState(s.Hooks[i], now)
	}
	s.PendingAcknowledgements = len(s.Waiting)
	s.Health, s.Reasons = assess(s, now)
	s.Summary = summary(s.Health)
	s.Busy = busy(s, now)
}
