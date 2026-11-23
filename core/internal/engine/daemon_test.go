package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/dispatch"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/scheduler"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

func TestStatusSetups(t *testing.T) {
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.Local)
	setups := statusSetups([]scheduler.Job{
		{Name: "standup", Schedule: "20 9 * * 1-5", Enabled: true},
		{Name: "off", Schedule: "20 9 * * 1-5", Enabled: false},
		{Name: "manual", Enabled: true},
	}, map[string]string{"typo": "bad cron"}, now)

	if s := setups["standup"]; !s.Enabled || s.NextRunAt == nil || s.NextRunAt.Format("15:04") != "09:20" {
		t.Errorf("standup: %+v", s)
	}
	if s := setups["off"]; s.Enabled || s.NextRunAt != nil {
		t.Errorf("disabled routines have no next run: %+v", s)
	}
	if s := setups["manual"]; !s.Enabled || s.NextRunAt != nil {
		t.Errorf("manual-only routines have no next run: %+v", s)
	}
	if s := setups["typo"]; !s.Enabled || s.Problem != "bad cron" {
		t.Errorf("broken routines are enabled with their problem: %+v", s)
	}
}

func TestScheduledJobsReportsProblems(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(cfg, "settings:\n  routines.badcron.schedule: \"61 9 * * *\"\n")
	rd := filepath.Join(dir, "routines")
	write(filepath.Join(rd, "good.prompt"), "---\nautomator.schedule: \"20 9 * * 1-5\"\n---\nhi")
	write(filepath.Join(rd, "badcron.prompt"), "---\nautomator.schedule: \"20 9 * * 1-5\"\n---\nhi")
	write(filepath.Join(rd, "typo.prompt"), "---\nautomator.schedul: \"20 9 * * *\"\n---\nhi")
	// Enabled and scheduled, but its folder is gone: every run would fail.
	write(filepath.Join(rd, "nofolder.prompt"), "---\nautomator.schedule: \"20 9 * * 1-5\"\nautomator.folder: "+filepath.Join(dir, "gone")+"\n---\nhi")

	a, err := Load(cfg, nil, secrets.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	// Made after trust was first used: enabled, but not trusted yet.
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(rd, "fresh.prompt"), "---\nautomator.schedule: \"20 9 * * 1-5\"\n---\nhi")
	jobs, problems, err := scheduledJobs(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Name != "good" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if problems["typo"] == "" || problems["badcron"] == "" || !strings.Contains(problems["nofolder"], "folder") ||
		!strings.Contains(problems["fresh"], "routines trust fresh") || len(problems) != 4 {
		t.Fatalf("problems = %v", problems)
	}
	// The one that only isn't trusted is setup's to turn on, no reason of
	// its own; the one missing its folder stays one.
	setups := allSetups(a, jobs, problems, time.Now())
	if !setups["fresh"].TurnOn || setups["nofolder"].TurnOn || setups["typo"].TurnOn {
		t.Fatalf("turn on: fresh %v, nofolder %v, typo %v", setups["fresh"].TurnOn, setups["nofolder"].TurnOn, setups["typo"].TurnOn)
	}
}

func TestRunOutcomeMapsEveryError(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 9, 20, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		res    *RunResult
		err    error
		want   runstatus.Outcome
		reason string
	}{
		"done":            {res: &RunResult{Agent: &agent.Result{Outcome: agent.OutcomeDone}}, want: runstatus.OutcomeSuccess},
		"nothing to do":   {res: &RunResult{Note: "nothing to do: already posted"}, want: runstatus.OutcomeSuccess, reason: "nothing to do: already posted"},
		"routine failed":  {err: &agent.RoutineFailedError{Reason: "send_message failed: refused"}, want: runstatus.OutcomeFailed, reason: "send_message failed: refused"},
		"backend error":   {err: errors.New("claude: max turns"), want: runstatus.OutcomeFailed, reason: "claude: max turns"},
		"sign-in expired": {err: &mcp.NeedsReauthError{Server: "chat"}, want: runstatus.OutcomeNeedsReauth},
	} {
		got := runOutcome(t0, t0.Add(time.Minute), tc.res, tc.err)
		if got.Outcome != tc.want || (tc.reason != "" && got.Reason != tc.reason) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestEffectiveConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(cfg, []byte("settings:\n  routines.sweep.timeout: 90m\n  routines.sweep.effort: high\n"), 0o600)
	rd := filepath.Join(dir, "routines")
	_ = os.MkdirAll(rd, 0o700)
	_ = os.WriteFile(filepath.Join(rd, "sweep.prompt"), []byte("---\nautomator.schedule: \"0 9 * * 1\"\nautomator.timeout: 30m\n---\nhi"), 0o600)
	_ = os.WriteFile(filepath.Join(rd, "adhoc.prompt"), []byte("---\nautomator.effort: low\n---\nhi"), 0o600)
	a, err := Load(cfg, nil, secrets.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	set, _ := routines.Load(rd)
	sweep, _ := set.Find("sweep")
	c, err := a.RoutineConfig(sweep)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || c.Schedule != "0 9 * * 1" || c.Staleness != time.Hour || c.Timeout != 90*time.Minute || c.Effort != "high" {
		t.Fatalf("settings override the file, defaults fill the rest: %+v", c)
	}
	adhoc, _ := set.Find("adhoc")
	if c, err := a.RoutineConfig(adhoc); err != nil || c.Schedule != "" || c.Timeout != 20*time.Minute || c.Effort != "low" {
		t.Fatalf("manual-only routine: %+v %v", c, err)
	}
}

func TestBackendModelMapping(t *testing.T) {
	for name, want := range map[string]string{"claude-api": "anthropic/claude-haiku-5-5", "claude-code": "claude-haiku-5-5", "codex": ""} {
		if name == "codex" {
			if _, err := Backends[name].AgentModel(nil, "anthropic/claude-haiku-5-5"); err == nil {
				t.Error("codex took a Claude model")
			}
			continue
		}
		got, err := Backends[name].AgentModel(nil, "anthropic/claude-haiku-5-5")
		if err != nil || got != want {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	if got, err := Backends["codex"].AgentModel(nil, "openai/gpt-5.5"); err != nil || got != "gpt-5.5" {
		t.Errorf("codex: %q %v", got, err)
	}
	if got, _ := Backends["claude-code"].AgentModel(nil, ""); got != "" {
		t.Errorf("no model means the CLI's default, got %q", got)
	}
}

func TestBackfillOccurrence(t *testing.T) {
	now := time.Date(2026, 11, 19, 14, 7, 30, 0, time.Local) // Thursday
	o, err := Backfill("2026-11-17", "20 9 * * 1-5", now)
	if err != nil || o.Trigger != trigger.Backfill || !o.At.Equal(time.Date(2026, 11, 17, 9, 20, 0, 0, time.Local)) {
		t.Fatalf("scheduled day: %+v %v", o, err)
	}
	// No slot that day (weekend, or a manual-only routine): now's time of day.
	o, _ = Backfill("2026-11-15", "20 9 * * 1-5", now)
	if !o.At.Equal(time.Date(2026, 11, 15, 14, 7, 30, 0, time.Local)) {
		t.Fatalf("no slot: %s", o.At)
	}
	o, _ = Backfill("2026-11-16", "", now)
	if !o.At.Equal(time.Date(2026, 11, 16, 14, 7, 30, 0, time.Local)) {
		t.Fatalf("manual-only: %s", o.At)
	}
	in := routines.Inputs(o)
	if in["today"] != "Monday 2026-11-16" || in["since"] != "Friday 2026-11-13" {
		t.Fatalf("inputs as of the day: %v", in)
	}
	for _, bad := range []string{"2026-11-20", "11/17", ""} {
		if _, err := Backfill(bad, "", now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A run still waiting in the daemon's queue is stopped by a stop request.
func TestStopWaitingInDaemonQueue(t *testing.T) {
	a := testApp(t)
	release := make(chan struct{})
	defer close(release)
	job := func(name string, staleness time.Duration) scheduler.Job {
		return scheduler.Job{Name: name, Schedule: "20 9 * * *", Staleness: staleness, Enabled: true}
	}
	now := time.Date(2026, 11, 18, 8, 0, 0, 0, time.Local)
	s := &scheduler.Scheduler{
		StatePath: filepath.Join(t.TempDir(), "state.json"),
		Jobs: func() ([]scheduler.Job, error) {
			return []scheduler.Job{job("first", time.Minute), job("second", time.Hour)}, nil
		},
		Run:    func(context.Context, scheduler.Job, trigger.Occurrence, *time.Time) { <-release },
		Missed: func(string, trigger.Occurrence, time.Duration) {},
		Now:    func() time.Time { return now },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Tick(ctx)
	now = now.Add(80 * time.Minute) // 09:20: first runs, second waits in the queue
	_ = s.Tick(ctx)
	done := make(chan struct{})
	go func() { stopWaiting(ctx, a, s, &dispatch.Dispatcher{}); close(done) }()
	// It records the run (status, then history) after the status shows:
	// wait for it before the test's folders go.
	defer func() { cancel(); <-done }()
	_ = a.RequestStop("second")
	waitOutcome(t, a, "second", runstatus.OutcomeStopped)
	if _, _, ok := s.Drop("second"); ok {
		t.Fatal("still queued after being stopped")
	}
}

// The scheduler runs programs too, outside the run queue; an untrusted one
// isn't scheduled, saying why.
func TestScheduledJobsHavePrograms(t *testing.T) {
	a := testApp(t)
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	add := func(name string) {
		t.Helper()
		src := t.TempDir()
		_ = os.WriteFile(filepath.Join(src, "prog"), []byte("#!/bin/sh\ntrue\n"), 0o755)
		desc := filepath.Join(src, programs.DescriptionFile)
		_ = os.WriteFile(desc, []byte("executable: ./prog\nwrites: false\nschedule: \"0 9 * * *\"\n"), 0o600)
		if _, err := programs.Add(a.Layout.Programs(), name, desc, false); err != nil {
			t.Fatal(err)
		}
	}
	add("trusted")
	add("fresh")
	x, err := a.Find("trusted")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Trust(x); err != nil {
		t.Fatal(err)
	}
	jobs, problems, err := scheduledJobs(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Name != "trusted" || !jobs[0].Parallel || jobs[0].Schedule != "0 9 * * *" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if !strings.Contains(problems["fresh"], "not trusted yet") {
		t.Fatalf("problems = %v", problems)
	}
}
