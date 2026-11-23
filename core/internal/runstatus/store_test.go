package runstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)

func at(d time.Duration) *time.Time { t := t0.Add(d); return &t }

func TestHealth(t *testing.T) {
	ok := &Run{StartedAt: t0, Outcome: OutcomeSuccess}
	failed := &Run{StartedAt: t0, Outcome: OutcomeFailed}
	for name, tc := range map[string]struct {
		routines []Routine
		want     Health
	}{
		"no routines":                {nil, HealthNone},
		"only disabled":              {[]Routine{{Name: "a", LastRun: failed}}, HealthNone},
		"new, first run still ahead": {[]Routine{{Name: "a", Enabled: true, NextRunAt: at(time.Hour)}}, HealthOK},
		"last run succeeded":         {[]Routine{{Name: "a", Enabled: true, LastRun: ok}}, HealthOK},
		"slot due, within grace":     {[]Routine{{Name: "a", Enabled: true, NextRunAt: at(-time.Minute)}}, HealthOK},
		"slot not picked up":         {[]Routine{{Name: "a", Enabled: true, NextRunAt: at(-10 * time.Minute)}}, HealthError},
		"can't be scheduled":         {[]Routine{{Name: "a", Enabled: true, Problem: "bad cron"}}, HealthError},
		"last run failed":            {[]Routine{{Name: "a", Enabled: true, LastRun: failed}}, HealthError},
		"disabled failure ignored":   {[]Routine{{Name: "a", Enabled: true, LastRun: ok}, {Name: "b", LastRun: failed}}, HealthOK},
	} {
		if got := health(&Status{Routines: tc.routines}, t0); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || s.Health != HealthNone || len(s.Routines) != 0 || s.ValidUntil != nil {
		t.Fatalf("got %+v, %v", s, err)
	}
}

// Sync owns a routine's setup and Record its last run; neither clobbers the other.
func TestSyncAndRecordOwnTheirFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	if err := Sync(path, map[string]Setup{"standup": {Enabled: false}}, t0); err != nil {
		t.Fatal(err)
	}
	if err := Record(path, "standup", Run{StartedAt: t0, Outcome: OutcomeFailed}, t0); err != nil {
		t.Fatal(err)
	}
	s := load(t, path)
	if r := s.Routines[0]; r.Enabled || r.LastRun == nil {
		t.Fatalf("record changed setup or lost the run: %+v", r)
	}
	if s.Health != HealthNone {
		t.Fatalf("a disabled routine's failure counted: %s", s.Health)
	}
	if err := Sync(path, map[string]Setup{"standup": {Enabled: true}}, t0); err != nil {
		t.Fatal(err)
	}
	if s := load(t, path); s.Routines[0].LastRun == nil || s.Health != HealthError {
		t.Fatalf("sync dropped the last run: %+v", s)
	}
}

// A routine Sync hasn't seen yet (manual run before the daemon's first tick).
func TestRecordAddsUnknownRoutine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	if err := Record(path, "new", Run{StartedAt: t0, Outcome: OutcomeSuccess}, t0); err != nil {
		t.Fatal(err)
	}
	if s := load(t, path); len(s.Routines) != 1 || !s.Routines[0].Enabled || s.Health != HealthOK {
		t.Fatalf("got %+v", s)
	}
}

// Fixing or removing a routine clears its red at the next sync, not at its next run.
func TestSyncClearsFixedAndRemovedRoutines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	_ = Sync(path, map[string]Setup{"typo": {Enabled: true, Problem: "bad cron"}, "old": {Enabled: true}}, t0)
	_ = Record(path, "old", Run{StartedAt: t0, Outcome: OutcomeFailed}, t0)
	if s := load(t, path); s.Health != HealthError {
		t.Fatalf("before fix: %s", s.Health)
	}
	if err := Sync(path, map[string]Setup{"typo": {Enabled: true}}, t0); err != nil {
		t.Fatal(err)
	}
	s := load(t, path)
	if s.Health != HealthOK || len(s.Routines) != 1 || s.Routines[0].Problem != "" {
		t.Fatalf("after fix: %+v", s)
	}
}

// The heartbeat sets validity and re-evaluates health over time; other writers
// never extend validity.
func TestHeartbeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	_ = Sync(path, map[string]Setup{"standup": {Enabled: true, NextRunAt: at(0)}}, t0)
	if err := Heartbeat(path, t0, time.Minute); err != nil {
		t.Fatal(err)
	}
	if s := load(t, path); !s.ValidUntil.Equal(t0.Add(time.Minute)) || s.Health != HealthOK {
		t.Fatalf("first beat: %+v", s)
	}
	_ = Record(path, "standup", Run{StartedAt: t0, Outcome: OutcomeSuccess}, t0.Add(30*time.Second))
	if s := load(t, path); !s.ValidUntil.Equal(t0.Add(time.Minute)) {
		t.Fatalf("a run extended validity: %v", s.ValidUntil)
	}
	// Nobody moved standup's slot forward: the next beats see it as not picked up.
	_ = Heartbeat(path, t0.Add(5*time.Minute), time.Minute)
	if s := load(t, path); s.Health != HealthError {
		t.Fatalf("stale slot not caught: %s", s.Health)
	}
}

func load(t *testing.T, path string) *Status {
	t.Helper()
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSchedulerProblemAndRunningRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	_ = Sync(path, map[string]Setup{"standup": {Enabled: true}}, t0)

	_ = SetProblem(path, "config: bad yaml", t0)
	if s := load(t, path); s.Health != HealthError || s.Problem == "" {
		t.Fatalf("problem not unhealthy: %+v", s)
	}
	_ = SetProblem(path, "", t0)
	if s := load(t, path); s.Health != HealthOK || s.Problem != "" {
		t.Fatalf("problem not cleared: %+v", s)
	}

	if load(t, path).Busy {
		t.Fatal("busy with nothing running")
	}
	_ = Record(path, "standup", Run{StartedAt: t0, Outcome: OutcomeRunning}, t0)
	if s := load(t, path); s.Health != HealthOK || !s.Busy {
		t.Fatalf("a fresh run is healthy and busy: %+v", s)
	}
	_ = Heartbeat(path, t0.Add(MaxRunDuration+time.Minute), time.Minute)
	if s := load(t, path); s.Health != HealthError || s.Busy {
		t.Fatalf("a run stuck past MaxRunDuration is unhealthy, not busy: %+v", s)
	}

	marked, _ := MarkInterrupted(path, t0.Add(time.Hour))
	if _, ok := marked["standup"]; !ok || len(marked) != 1 {
		t.Fatalf("marked = %v", marked)
	}
	s := load(t, path)
	if r := s.Routines[0].LastRun; r.Outcome != OutcomeFailed || !strings.Contains(r.Reason, "interrupted") || r.FinishedAt == nil {
		t.Fatalf("interrupted run: %+v", r)
	}
}

func TestRunDeadline(t *testing.T) {
	deadline := t0.Add(90 * time.Minute)
	r := Routine{Name: "sweep", Enabled: true, LastRun: &Run{StartedAt: t0, Outcome: OutcomeRunning, Deadline: &deadline}}
	s := &Status{Routines: []Routine{r}}
	if health(s, t0.Add(80*time.Minute)) != HealthOK || !busy(s, t0.Add(80*time.Minute)) {
		t.Fatal("a long run within its own timeout is healthy and busy (past the old fixed 25m limit)")
	}
	if health(s, deadline.Add(StuckGrace+time.Minute)) != HealthError || busy(s, deadline.Add(StuckGrace+time.Minute)) {
		t.Fatal("past its deadline plus grace it's stuck")
	}
}

func TestQueuedAndStopped(t *testing.T) {
	queued := Routine{Name: "a", Enabled: true, LastRun: &Run{StartedAt: t0, QueuedAt: &t0, Outcome: OutcomeQueued}}
	s := &Status{Routines: []Routine{queued}}
	later := t0.Add(3 * time.Hour) // long behind a long run: still fine, and not busy
	if health(s, later) != HealthOK || busy(s, later) {
		t.Fatal("a queued run is healthy and not busy")
	}
	stopped := Routine{Name: "a", Enabled: true, LastRun: &Run{StartedAt: t0, Outcome: OutcomeStopped}}
	if health(&Status{Routines: []Routine{stopped}}, t0) != HealthOK {
		t.Fatal("a stopped run is my choice, not a failure")
	}
	path := filepath.Join(t.TempDir(), "run-status.json")
	_ = Record(path, "a", Run{StartedAt: t0, QueuedAt: &t0, Outcome: OutcomeQueued}, t0)
	marked, _ := MarkInterrupted(path, t0.Add(time.Minute))
	if r, ok := marked["a"]; !ok || r.Outcome != OutcomeFailed || !strings.Contains(r.Reason, "waited for its turn") {
		t.Fatalf("a run left queued by a restart: %+v", marked)
	}
}

// Reasons come from the same rules as health: error ⇔ reasons, with words a
// frontend shows as they are, and the run they're about.
func TestReasons(t *testing.T) {
	failed := &Run{ID: "standup/2026-11-18T093000", StartedAt: t0, Outcome: OutcomeFailed, Reason: "timed out"}
	for name, tc := range map[string]struct {
		s    Status
		want []string
	}{
		"healthy":    {Status{Routines: []Routine{{Name: "a", Enabled: true, LastRun: &Run{StartedAt: t0, Outcome: OutcomeSuccess}}}}, nil},
		"failed run": {Status{Routines: []Routine{{Name: "standup", Enabled: true, LastRun: failed}}}, []string{"standup failed"}},
		"disabled":   {Status{Routines: []Routine{{Name: "standup", LastRun: failed}}}, nil},
		"problem":    {Status{Routines: []Routine{{Name: "x", Enabled: true, Problem: "not trusted yet"}}}, []string{"x can't be scheduled"}},
		"scheduler":  {Status{Problem: "config doesn't load"}, []string{"The scheduler can't work"}},
		"stalled":    {Status{Routines: []Routine{{Name: "a", Enabled: true, NextRunAt: at(-10 * time.Minute)}}}, []string{"a didn't start at"}},
		"stuck":      {Status{Routines: []Routine{{Name: "a", Enabled: true, LastRun: &Run{ID: "a/x", StartedAt: t0.Add(-5 * time.Hour), Deadline: at(-4 * time.Hour), Outcome: OutcomeRunning}}}}, []string{"a is stuck"}},
	} {
		h, reasons := assess(&tc.s, t0)
		if (h == HealthError) != (len(reasons) > 0) {
			t.Errorf("%s: health %s with reasons %v", name, h, reasons)
		}
		if len(reasons) != len(tc.want) {
			t.Errorf("%s: reasons %v, want %v", name, reasons, tc.want)
			continue
		}
		for i, w := range tc.want {
			if !strings.Contains(reasons[i].Text, w) {
				t.Errorf("%s: %q, want %q", name, reasons[i].Text, w)
			}
		}
		if name == "failed run" && (reasons[0].Run != failed.ID || reasons[0].Detail != "timed out") {
			t.Errorf("reason doesn't point at its run: %+v", reasons[0])
		}
		if name == "stuck" && reasons[0].Run != "" {
			t.Errorf("a run still going has no record to open, but the reason points at %q", reasons[0].Run)
		}
	}
}

func TestSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	if err := Sync(path, map[string]Setup{"a": {Enabled: true}}, t0); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(path)
	if s.Summary != "All good" {
		t.Errorf("summary %q", s.Summary)
	}
	if err := Sync(path, map[string]Setup{"a": {}}, t0); err != nil {
		t.Fatal(err)
	}
	if s, _ = Load(path); s.Summary != "No routines enabled" {
		t.Errorf("summary %q", s.Summary)
	}
}

func TestRoutineState(t *testing.T) {
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.Local)
	tomorrow := time.Date(2026, 11, 20, 9, 20, 0, 0, time.Local)
	monday := time.Date(2026, 11, 23, 9, 20, 0, 0, time.Local)
	for name, tc := range map[string]struct {
		r       Routine
		health  Health
		summary string
	}{
		"paused":  {Routine{Name: "a", NextRunAt: &tomorrow}, HealthNone, "Paused"},
		"next":    {Routine{Name: "a", Enabled: true, NextRunAt: &tomorrow}, HealthOK, "Next: tomorrow 09:20"},
		"weekday": {Routine{Name: "a", Enabled: true, NextRunAt: &monday}, HealthOK, "Next: Mon 09:20"},
		"by hand": {Routine{Name: "a", Enabled: true}, HealthOK, "Runs by hand"},
		"failed":  {Routine{Name: "a", Enabled: true, LastRun: &Run{StartedAt: now, Outcome: OutcomeFailed}}, HealthError, "a failed"},
		"running": {Routine{Name: "a", Enabled: true, LastRun: &Run{StartedAt: now.Add(-time.Minute), Outcome: OutcomeRunning}}, HealthOK, "Running since 19:59"},
		"problem": {Routine{Name: "a", Enabled: true, Problem: "not trusted yet"}, HealthError, "a can't be scheduled"},
	} {
		h, s := routineState(tc.r, now)
		if h != tc.health || s != tc.summary {
			t.Errorf("%s: %s %q, want %s %q", name, h, s, tc.health, tc.summary)
		}
	}
}

// Unfinished setup makes health error, with what's left.
func TestSetupLeft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	if err := SetSetup(path, []string{"Model backend", "Trust standup"}, t0); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(path)
	if s.Health != HealthError || len(s.Reasons) != 1 || s.Reasons[0].Text != "Finish Setup (2 to do)…" || !s.Reasons[0].Setup ||
		!strings.Contains(s.Reasons[0].Detail, "Model backend, Trust standup") {
		t.Errorf("unfinished setup: %s %+v", s.Health, s.Reasons)
	}
	// The same again writes nothing: frontends following run status see no change.
	if err := SetSetup(path, []string{"Model backend", "Trust standup"}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if s, _ := Load(path); !s.UpdatedAt.Equal(t0) {
		t.Errorf("rewritten without a change (updated %v)", s.UpdatedAt)
	}
	if err := SetSetup(path, nil, t0); err != nil {
		t.Fatal(err)
	}
	if s, _ = Load(path); s.Health == HealthError || s.SetupLeft != nil {
		t.Errorf("finished setup: %s %v", s.Health, s.SetupLeft)
	}
}

// A health reset (a permission it needs changed): the last run no longer
// counts, red or green, until it runs again; the run itself stays recorded,
// and Sync keeps when the reset was. A run since counts again; one still
// going is current.
func TestResetHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	next := at(time.Hour)
	_ = Sync(path, map[string]Setup{"standup": {Enabled: true, NextRunAt: next}, "digest": {Enabled: true}}, t0)
	failed := Run{ID: "standup/1", StartedAt: t0.Add(-time.Hour), Outcome: OutcomeFailed, Reason: "no permission"}
	_ = Record(path, "standup", failed, t0)
	_ = Record(path, "digest", Run{StartedAt: t0.Add(-time.Hour), Outcome: OutcomeSuccess}, t0)
	if s := load(t, path); s.Health != HealthError {
		t.Fatalf("before: %s", s.Health)
	}
	if err := ResetHealth(path, []string{"standup", "digest", "not-here"}, t0); err != nil {
		t.Fatal(err)
	}
	_ = Sync(path, map[string]Setup{"standup": {Enabled: true, NextRunAt: next}, "digest": {Enabled: true}}, t0)
	s := load(t, path)
	if s.Health != HealthOK || len(s.Reasons) != 0 {
		t.Fatalf("a reset failure still counts: %s %+v", s.Health, s.Reasons)
	}
	for _, r := range s.Routines {
		if r.Health != HealthNone || !strings.HasPrefix(r.Summary, "Permissions changed; ") || r.HealthResetAt == nil {
			t.Errorf("%s: %s %q, want neutral", r.Name, r.Health, r.Summary)
		}
	}
	if r := s.Routines[1]; r.Name != "standup" || r.LastRun == nil || r.LastRun.ID != "standup/1" || r.LastRun.Outcome != OutcomeFailed {
		t.Fatalf("the run itself changed: %+v", r.LastRun)
	}
	// It runs again: that run counts, failed or not.
	_ = Record(path, "standup", Run{StartedAt: t0.Add(time.Minute), Outcome: OutcomeFailed}, t0.Add(time.Minute))
	_ = Record(path, "digest", Run{StartedAt: t0.Add(time.Minute), Outcome: OutcomeSuccess}, t0.Add(time.Minute))
	s = load(t, path)
	if s.Health != HealthError || len(s.Reasons) != 1 || s.Reasons[0].Text != "standup failed" {
		t.Fatalf("a failure since the reset: %s %+v", s.Health, s.Reasons)
	}
	if d := s.Routines[0]; d.Health != HealthOK || d.Summary != "Runs by hand" {
		t.Fatalf("a success since the reset: %s %q", d.Health, d.Summary)
	}
	// A still-missing permission is a problem of its own, reset or not.
	_ = ResetHealth(path, []string{"standup"}, t0.Add(2*time.Minute))
	_ = Sync(path, map[string]Setup{"standup": {Enabled: true, Problem: "it needs the Input Monitoring permission"}, "digest": {Enabled: true}}, t0)
	if s = load(t, path); s.Health != HealthError || len(s.Reasons) != 1 || s.Reasons[0].Text != "standup can't be scheduled" {
		t.Fatalf("a missing permission after a reset: %s %+v", s.Health, s.Reasons)
	}
	// A run still going started before the reset is current.
	r := Routine{Name: "a", Enabled: true, HealthResetAt: at(0), LastRun: &Run{StartedAt: t0.Add(-time.Minute), Outcome: OutcomeRunning}}
	if h, sum := routineState(r, t0); h != HealthOK || !strings.HasPrefix(sum, "Running since") {
		t.Fatalf("running: %s %q", h, sum)
	}
	if h, sum := hookState(Routine{Name: "h", Enabled: true, HealthResetAt: at(0)}, t0); h != HealthNone || sum != "Permissions changed; waiting for events" {
		t.Fatalf("a hook: %s %q", h, sum)
	}
}

// Hooks sharing a watcher that's wrong: one reason, about the watcher,
// naming them; each hook's own problem still says it waits on its watcher,
// and a hook with something else wrong has its own reason for that alone.
func TestOneReasonPerWatcher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	failing := "home-key is failing: can't listen to the keyboard"
	_ = Sync(path, map[string]Setup{
		"home-joke": {Hook: true, Enabled: true, Watcher: "home-key", WatcherProblem: failing},
		"say-hi":    {Hook: true, Enabled: true, Watcher: "home-key", WatcherProblem: failing},
		"wave":      {Hook: true, Enabled: true, Watcher: "home-key", WatcherProblem: failing, Problem: "it isn't trusted"},
		"off":       {Hook: true, Watcher: "home-key", WatcherProblem: failing},
		"cal-brief": {Hook: true, Enabled: true, Watcher: "cal"},
	}, t0)
	s := load(t, path)
	if s.Health != HealthError || len(s.Reasons) != 2 {
		t.Fatalf("reasons: %s %+v", s.Health, s.Reasons)
	}
	own, watcher := s.Reasons[0], s.Reasons[1]
	if !own.Hook || own.Routine != "wave" || own.Text != "wave can't run" || own.Detail != "it isn't trusted" {
		t.Errorf("the hook's own reason: %+v", own)
	}
	if watcher.Watcher != "home-key" || watcher.Routine != "" || watcher.Hook ||
		watcher.Text != failing+" (home-joke, say-hi and wave listen to it)" {
		t.Errorf("the watcher's reason: %+v", watcher)
	}
	for _, h := range s.Hooks {
		switch h.Name {
		case "home-joke", "say-hi":
			if h.Problem != "its watcher "+failing || h.Health != HealthError || h.Summary != h.Name+" can't run" {
				t.Errorf("%s: %+v", h.Name, h)
			}
		case "wave":
			if h.Problem != "it isn't trusted; its watcher "+failing {
				t.Errorf("%s: %q", h.Name, h.Problem)
			}
		case "cal-brief":
			if h.Health != HealthOK || h.Problem != "" || h.Watcher != "cal" {
				t.Errorf("%s: %+v", h.Name, h)
			}
		}
	}
	// One hook: it listens.
	_ = Sync(path, map[string]Setup{"home-joke": {Hook: true, Enabled: true, Watcher: "home-key", WatcherProblem: "home-key is paused"}}, t0)
	if s = load(t, path); len(s.Reasons) != 1 || s.Reasons[0].Text != "home-key is paused (home-joke listens to it)" {
		t.Fatalf("one hook: %+v", s.Reasons)
	}
}

// A notification waiting to be acknowledged makes health error, with a
// reason to choose; none waiting, health is what it otherwise is.
func TestWaitingNotifications(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	if err := Sync(path, map[string]Setup{"a": {Enabled: true, NextRunAt: at(time.Hour)}}, t0); err != nil {
		t.Fatal(err)
	}
	set := func(w ...Waiting) *Status {
		t.Helper()
		if err := SetWaiting(path, func() ([]Waiting, error) { return w, nil }, t0); err != nil {
			t.Fatal(err)
		}
		s, _ := Load(path)
		return s
	}
	s := set(Waiting{ID: "1", Title: "Ana asked for the report"})
	if s.Health != HealthError || s.PendingAcknowledgements != 1 || len(s.Reasons) != 1 || !s.Reasons[0].Acknowledge ||
		s.Reasons[0].Text != "(1) Acknowledgement pending" || s.Reasons[0].Detail != "Ana asked for the report" {
		t.Fatalf("one waiting: %+v", s)
	}
	if s := set(Waiting{ID: "1", Title: "x"}, Waiting{ID: "2", Title: "y"}); s.Reasons[0].Text != "(2) Acknowledgements pending" {
		t.Fatalf("two waiting: %+v", s.Reasons)
	}
	if s := set(); s.Health != HealthOK || len(s.Reasons) != 0 || s.Waiting != nil || s.PendingAcknowledgements != 0 {
		t.Fatalf("none waiting: %+v", s)
	}
	before, _ := os.Stat(path)
	time.Sleep(10 * time.Millisecond)
	set()
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged list was written")
	}
}

// What only needs turning on (it, or its watcher, isn't trusted) is no
// reason of its own: setup lists it, and setup's one reason opens setup.
// What's otherwise wrong stays its own reason.
func TestWhatNeedsTurningOnIsSetups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-status.json")
	if err := Sync(path, map[string]Setup{
		"pr-reviewed":   {Hook: true, Enabled: true, Problem: "it isn't trusted: its executable changed", TurnOn: true},
		"rick-gh-scout": {Hook: true, Enabled: true, Watcher: "gh", WatcherProblem: "gh isn't trusted (not trusted yet)", TurnOn: true},
		"digest":        {Enabled: true, Problem: "not trusted yet", TurnOn: true},
		"broken":        {Enabled: true, Problem: "its folder is missing"},
	}, t0); err != nil {
		t.Fatal(err)
	}
	if err := SetSetup(path, []string{"Turn on digest", "Turn on pr-reviewed", "Turn on rick-gh-scout"}, t0); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(path)
	var texts []string
	for _, r := range s.Reasons {
		texts = append(texts, r.Text)
	}
	if len(s.Reasons) != 2 || !s.Reasons[0].Setup || s.Reasons[0].Text != "Finish Setup (3 to do)…" ||
		s.Reasons[1].Text != "broken can't be scheduled" || s.Health != HealthError {
		t.Fatalf("reasons %q", texts)
	}
	for _, h := range s.Hooks {
		if h.Health != HealthError || h.Summary != "Needs turning on" {
			t.Errorf("%s: %s %q", h.Name, h.Health, h.Summary)
		}
	}
}
