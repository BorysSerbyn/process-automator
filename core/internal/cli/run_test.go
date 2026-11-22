package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// setup --json lists every step and the commands for each, changing nothing.
func TestSetupSteps(t *testing.T) {
	connectClaude(t)
	a, _ := enginetest.Fixture(t, map[string]string{"old": "queue"}, nil)
	cfg := a.Layout.Config()
	steps := func() (SetupOutput, map[string]SetupStep) {
		t.Helper()
		out, err := cli(t, cfg, "setup", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		var got SetupOutput
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		m := map[string]SetupStep{}
		for _, s := range got.Steps {
			m[s.Step+" "+s.Subject] = s
		}
		return got, m
	}
	trustFile := filepath.Join(filepath.Dir(cfg), "trust.json")
	_, m := steps()
	if _, err := os.Stat(trustFile); err == nil {
		t.Error("setup --json wrote trust.json")
	}
	if s := m["turn-on old"]; !s.Done {
		t.Errorf("before trust is first used, an installed routine counts as trusted: %+v", s)
	}
	if s := m["templates "]; !s.Done || !s.Optional {
		t.Errorf("templates with routines installed: %+v", s)
	}

	// Untrusted and paused: one step trusts it and turns its schedule on.
	src := "---\nautomator.schedule: \"20 9 * * 1-5\"\n---\nGo.\n"
	if _, err := cli(t, cfg, "routines", "create", "fresh", "--source", src); err != nil {
		t.Fatal(err)
	}
	got, m := steps()
	if s := m["turn-on fresh"]; s.Done || s.Optional || !slices.Contains(s.Commands, "procauto routines trust fresh --yes --resume") ||
		!strings.Contains(s.Ask, "and run it on its schedule (At 09:20, Monday through Friday)") {
		t.Errorf("turn-on step: %+v", s)
	}
	for _, s := range got.Steps {
		if s.Step == "trust" || s.Step == "enable" {
			t.Errorf("a separate %s step: %+v", s.Step, s)
		}
	}
	if got.Done || got.Remaining != 1 {
		t.Errorf("done %v with %d remaining, want 1 (turn on fresh)", got.Done, got.Remaining)
	}
	if out, _ := cli(t, cfg, "settings", "get", "routines.fresh.enabled"); !strings.Contains(out, "false") {
		t.Errorf("setup --json changed a setting: %s", out)
	}

	// Trusted but paused, even with no routine on its schedule: paused is my
	// choice, so resuming it is only offered, and setup is done.
	if _, err := cli(t, cfg, "routines", "trust", "fresh", "--yes"); err != nil {
		t.Fatal(err)
	}
	got, m = steps()
	if s := m["turn-on fresh"]; s.Done || !s.Optional || !strings.HasPrefix(s.Status, "Trusted, but paused") ||
		!strings.HasPrefix(s.Ask, "Run fresh on its schedule") {
		t.Errorf("trusted but paused, nothing scheduled: %+v", s)
	}
	if !got.Done {
		t.Error("not done with only a paused routine")
	}
	// The same command finishes it: trusted already, it only resumes.
	if out, err := cli(t, cfg, "routines", "trust", "fresh", "--yes", "--resume"); err != nil || !strings.Contains(out, "Resumed fresh") {
		t.Fatalf("trust --resume: %s %v", out, err)
	}
	if got, m = steps(); !m["turn-on fresh"].Done || !got.Done {
		t.Errorf("after turning it on: %+v", m["turn-on fresh"])
	}

	// Pausing it again doesn't make setup unfinished: run status stays without setup left.
	if _, err := cli(t, cfg, "routines", "pause", "fresh"); err != nil {
		t.Fatal(err)
	}
	if got, m = steps(); !m["turn-on fresh"].Optional || !got.Done {
		t.Errorf("paused again: %+v (done %v)", m["turn-on fresh"], got.Done)
	}
	if s, _ := runstatus.Load(filepath.Join(filepath.Dir(cfg), "run-status.json")); len(s.SetupLeft) > 0 || s.Health == runstatus.HealthError {
		t.Errorf("run status after pausing: %s %v", s.Health, s.SetupLeft)
	}

	// A routine whose settings don't resolve is a step to fix; setup still answers.
	if _, err := cli(t, cfg, "settings", "set", "routines.old.timeout", "1h"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg)
	_ = os.WriteFile(cfg, []byte(strings.Replace(string(raw), "1h", "bogus", 1)), 0o600)
	_, m = steps()
	if s, ok := m["routine old"]; !ok || s.Done || !strings.Contains(s.Status, "timeout") {
		t.Errorf("broken settings: %+v", s)
	}
}

func TestDevNullIsNoTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Error("/dev/null counts as a terminal")
	}
}

// With no routines installed, setup is done once the backend is ready.
func TestSetupDoneWithoutRoutines(t *testing.T) {
	connectClaude(t)
	a, _ := enginetest.Fixture(t, nil, nil)
	out, err := cli(t, a.Layout.Config(), "setup", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got SetupOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil || !got.Done || got.Remaining != 0 {
		t.Errorf("no routines: %+v (%v)", got, err)
	}
}

// stop ends a run, running or still waiting, as stopped (not failed).
func TestStop(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	a, set := enginetest.Fixture(t, map[string]string{"long": "queue", "waiting": "queue"},
		map[string]chan struct{}{"long": gate})
	cfg := a.Layout.Config()
	long := enginetest.Start(t, a, set, "long")
	enginetest.WaitOutcome(t, a, "long", runstatus.OutcomeRunning)
	waiting := enginetest.Start(t, a, set, "waiting")
	enginetest.WaitOutcome(t, a, "waiting", runstatus.OutcomeQueued)

	for _, name := range []string{"waiting", "long"} {
		out, err := cli(t, cfg, "stop", name)
		if err != nil {
			t.Fatalf("stop %s: %s %v", name, out, err)
		}
		if run := enginetest.LastRun(a, name); run.Outcome != runstatus.OutcomeStopped {
			t.Fatalf("%s: %+v", name, run)
		}
	}
	for _, done := range []chan error{long, waiting} {
		if err := <-done; !errors.Is(err, engine.ErrStopped) {
			t.Fatalf("run returned %v", err)
		}
	}
	if _, err := cli(t, cfg, "stop", "long"); err == nil {
		t.Fatal("stopping a routine that isn't running")
	}
}

// Past the usage limit, a scheduled run is skipped with the reason; a run
// On a Claude plan, the usage limit is the plan's own usage: past it (from
// Claude Code's /usage, or a limit a run hit), a scheduled run is skipped; a
// run started by hand still runs. A single model's weekly limit isn't applied.
func TestUsageLimitPlan(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	cfg := a.Layout.Config()
	r, _ := set.Find("r")
	scheduled := engine.RunOptions{Occurrence: trigger.New(trigger.Schedule, time.Now())}
	if _, err := a.Execute(context.Background(), set, r, scheduled); err != nil {
		t.Fatalf("nothing known of the plan, yet the run was stopped: %v", err)
	}
	enginetest.UsePlanUsage(usage.Limit{Kind: "session", Percent: 95, ResetsAt: time.Now().Add(time.Hour)},
		usage.Limit{Kind: "weekly_all", Percent: 20, ResetsAt: time.Now().Add(72 * time.Hour)},
		usage.Limit{Kind: "weekly_scoped", Model: "Fable", Percent: 99, ResetsAt: time.Now().Add(72 * time.Hour)})
	_ = os.Remove(a.Layout.PlanUsage()) // the reading from the first run said nothing; read again
	if out, _ := cli(t, cfg, "usage"); !strings.Contains(out, "95%") || !strings.Contains(out, "Scheduled runs don't start") {
		t.Fatalf("usage:\n%s", out)
	}
	_, err := a.Execute(context.Background(), set, r, scheduled)
	if err == nil || !strings.Contains(err.Error(), "skipped: the Claude plan's 5-hour usage is at 95%") {
		t.Fatalf("scheduled run past the limit: %v", err)
	}
	if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{}); err != nil {
		t.Fatalf("a run started by hand was stopped: %v", err)
	}
	if out, _ := cli(t, cfg, "doctor"); !strings.Contains(out, "scheduled runs don't start") {
		t.Errorf("doctor doesn't say:\n%s", out)
	}
	if out, _ := cli(t, cfg, "settings", "list"); !strings.Contains(out, "[only used with llm.backend claude-api]") {
		t.Errorf("budgets aren't marked as the claude-api backend's:\n%s", out)
	}
}

// A run that hits the plan's limit records it until the limit resets.
func TestPlanLimitHit(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	resets := time.Now().Add(2 * time.Hour).Truncate(time.Minute)
	enginetest.UseBackend(t, limitBackend{&agent.PlanLimitError{Window: "weekly", ResetsAt: resets, Message: "You've hit your weekly limit"}})
	r, _ := set.Find("r")
	if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{}); err == nil {
		t.Fatal("the run hit the limit, yet succeeded")
	}
	rep, err := a.Usage(context.Background(), time.Now())
	if err != nil || !rep.Over || !strings.Contains(rep.Reason, "weekly limit is reached") {
		t.Errorf("after a limit hit: %+v %v", rep, err)
	}
}

type limitBackend struct{ err error }

func (limitBackend) Name() string { return "limit" }

func (b limitBackend) Run(context.Context, agent.Job) (*agent.Output, error) { return nil, b.err }

// With an API key, the limit is spend against the budgets (api runs only).
func TestUsageLimitBudget(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	cfg := a.Layout.Config()
	for _, kv := range [][2]string{{"llm.backend", "claude-api"}, {"llm.budget.window-usd", "1"}} {
		if _, err := cli(t, cfg, "settings", "set", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Hour)
	// api is claude-api's former name, in older runs; a plan's run never counts.
	for i, run := range []struct {
		backend string
		cost    float64
	}{{"api", 1}, {"claude-api", 0.5}, {"claude-code", 1.5}} {
		started := at.Add(time.Duration(i) * time.Minute)
		_ = history.Save(a.Layout.History(), history.Entry{ID: history.RunID("r", started), Routine: "r", Trigger: trigger.Manual,
			StartedAt: started, Outcome: runstatus.OutcomeSuccess, Result: &agent.Result{Backend: run.backend, CostUSD: run.cost}})
	}
	a = enginetest.Load(t, cfg)
	if out, _ := cli(t, cfg, "usage"); !strings.Contains(out, "$1.50") || !strings.Contains(out, "past its $1.00 budget") {
		t.Fatalf("usage:\n%s", out)
	}
	r, _ := set.Find("r")
	_, err := a.Execute(context.Background(), set, r, engine.RunOptions{Occurrence: trigger.New(trigger.Schedule, time.Now())})
	if err == nil || !strings.Contains(err.Error(), "skipped: LLM spend in the last 5h is $1.50") {
		t.Fatalf("scheduled run over the budget: %v", err)
	}
	s, _ := runstatus.Load(a.Layout.RunStatus())
	if run := s.Routines[0].LastRun; run == nil || run.Outcome != runstatus.OutcomeSkipped {
		t.Fatalf("recorded as %+v", s.Routines[0].LastRun)
	}
}

// While /usage can't be read, setup asks to read the plan's usage from Claude
// Code's status line; installed, the status line records it and still shows
// mine. Not a health problem: doctor says nothing about it.
func TestPlanUsageFallback(t *testing.T) {
	connectClaude(t)
	a, _ := enginetest.Fixture(t, nil, nil) // its /usage can't be read
	cfg := a.Layout.Config()
	if _, err := a.Usage(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	step := func() (SetupStep, bool) {
		out, _ := cli(t, cfg, "setup", "--json")
		var got SetupOutput
		_ = json.Unmarshal([]byte(out), &got)
		for _, s := range got.Steps {
			if s.Step == stepUsage {
				return s, got.Done
			}
		}
		return SetupStep{}, got.Done
	}
	if s, done := step(); s.Step == "" || s.Done || done {
		t.Fatalf("no unfinished plan usage step: %+v (setup done %v)", s, done)
	}
	// Run status (the menu) agrees with setup (the setup window) at once: after
	// `setup --json`, and after `usage` reads the plan's usage again.
	left := func() []string { s, _ := runstatus.Load(a.Layout.RunStatus()); return s.SetupLeft }
	if !slices.Contains(left(), "Plan usage") {
		t.Errorf("run status after setup --json: %v", left())
	}
	unreadable := engine.Backends["claude-code"]
	enginetest.UsePlanUsage(usage.Limit{Kind: "session", Percent: 10, ResetsAt: time.Now().Add(time.Hour)})
	_ = os.Remove(a.Layout.PlanUsage()) // due for a reading
	if _, err := cli(t, cfg, "usage"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(left(), "Plan usage") {
		t.Errorf("run status after usage read it: %v", left())
	}
	engine.Backends["claude-code"] = unreadable
	_ = os.Remove(a.Layout.PlanUsage())
	if _, err := a.Usage(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if out, _ := cli(t, cfg, "doctor"); strings.Contains(out, "usage") {
		t.Errorf("doctor reports it:\n%s", out)
	}
	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	_ = os.WriteFile(settings, []byte(`{"statusLine": {"type": "command", "command": "printf mine"}}`), 0o600)
	t.Cleanup(func() { _ = os.Remove(settings) })
	if _, err := cli(t, cfg, "usage", "statusline", "install"); err != nil {
		t.Fatal(err)
	}
	if s, done := step(); !s.Done || !done {
		t.Errorf("installed, yet: %+v (setup done %v)", s, done)
	}
	// Claude Code runs it: it records the usage and shows my status line.
	root := NewRoot(nil)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--config", cfg, "usage", "statusline"})
	root.SetIn(strings.NewReader(fmt.Sprintf(`{"rate_limits":{"five_hour":{"used_percentage":33,"resets_at":%d}}}`,
		time.Now().Add(time.Hour).Unix())))
	if err := root.Execute(); err != nil || out.String() != "mine" {
		t.Errorf("status line printed %q (%v)", out.String(), err)
	}
	rep, _ := a.Usage(context.Background(), time.Now())
	if w := rep.Windows[0]; w.UsedPercent == nil || *w.UsedPercent != 33 || rep.RecordedAt == nil {
		t.Errorf("not recorded: %+v", rep)
	}
	if _, err := cli(t, cfg, "usage", "statusline", "uninstall"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), `"command": "printf mine"`) {
		t.Errorf("not put back:\n%s", b)
	}
}

// A plugin's preparation is shown, and has to be done, before any credential
// is asked for.
func TestSignInPrepares(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	p := pluginapi.Plugin{Name: "pt-app", Serve: func(pluginapi.Host) (*sdk.Server, error) { return nil, nil },
		Secrets: []pluginapi.SecretSpec{{Name: "token", Prompt: "the token"}}, Prepare: "1. Create the app."}
	a := enginetest.Load(t, cfg, p)
	var out bytes.Buffer
	ui := &terminalUI{in: bufio.NewReader(strings.NewReader("n\n")), out: &out}
	if err := login(context.Background(), a, "pt-app", ui); err == nil || strings.Contains(out.String(), "Paste the token") {
		t.Fatalf("went on without the preparation done: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "1. Create the app.") {
		t.Errorf("preparation not shown:\n%s", out.String())
	}
	out.Reset()
	ui = &terminalUI{in: bufio.NewReader(strings.NewReader("y\nsecret\n")), out: &out}
	if err := login(context.Background(), a, "pt-app", ui); err != nil {
		t.Fatal(err)
	}
	// Signed in, it isn't shown again.
	out.Reset()
	ui = &terminalUI{in: bufio.NewReader(strings.NewReader("other\n")), out: &out}
	_ = login(context.Background(), a, "pt-app", ui)
	if strings.Contains(out.String(), "Create the app") {
		t.Errorf("preparation shown once signed in:\n%s", out.String())
	}
}

// A command that changes setup updates run status at once.
func TestSetupStatusRefreshes(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"old": "queue"}, nil)
	cfg := a.Layout.Config()
	if _, err := cli(t, cfg, "routines", "create", "fresh", "--source", "---\nautomator.schedule: \"20 9 * * 1-5\"\n---\nGo.\n"); err != nil {
		t.Fatal(err)
	}
	left := func() []string {
		s, _ := runstatus.Load(a.Layout.RunStatus())
		if s == nil {
			return nil
		}
		return s.SetupLeft
	}
	if !slices.Contains(left(), "Turn on fresh") {
		t.Fatalf("after create: %v", left())
	}
	if _, err := cli(t, cfg, "routines", "trust", "fresh", "--yes", "--resume"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(left(), "Turn on fresh") {
		t.Errorf("still left after turning it on: %v", left())
	}
}

// A routine turned on (or paused) shows so in run status at once, without
// waiting for the scheduler's next pass; a read-only command writes nothing.
func TestRoutineStateAtOnce(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	enginetest.Load(t, cfg)
	src := "---\nautomator.schedule: \"20 9 * * 1-5\"\n---\nGo.\n"
	if _, err := cli(t, cfg, "routines", "create", "daily", "--source", src); err != nil {
		t.Fatal(err)
	}
	if _, err := cli(t, cfg, "routines", "trust", "daily", "--yes", "--resume"); err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(filepath.Dir(cfg), "run-status.json")
	routine := func() runstatus.Routine {
		s, _ := runstatus.Load(status)
		for _, r := range s.Routines {
			if r.Name == "daily" {
				return r
			}
		}
		return runstatus.Routine{}
	}
	if r := routine(); !r.Enabled || r.NextRunAt == nil {
		t.Errorf("turned on, run status says: %+v", r)
	}
	if _, err := cli(t, cfg, "routines", "pause", "daily"); err != nil {
		t.Fatal(err)
	}
	if r := routine(); r.Enabled || r.Summary != "Paused" {
		t.Errorf("paused, run status says: %+v", r)
	}
	info, _ := os.Stat(status)
	if _, err := cli(t, cfg, "setup", "--json"); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(status); !after.ModTime().Equal(info.ModTime()) {
		t.Error("setup --json rewrote run status")
	}
}

// Recording what setup has left never makes the data folder again: after
// `uninstall --purge` it's gone, and stays gone.
func TestSetupStatusNotWithoutData(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "purged", "config.yaml")
	cmd, _, err := NewRoot(nil).Find([]string{"uninstall"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--config", cfg}); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	refreshStatus(cmd)
	if _, err := os.Stat(filepath.Dir(cfg)); err == nil {
		t.Error("the data folder was made again")
	}
}

// Interactive setup lists the backends one per line with what each means,
// takes a number or a name, and asks again for anything else.
// setupWith runs the interactive setup on cfg, answering with input.
func setupWith(t *testing.T, cfg, input string) string {
	t.Helper()
	a := enginetest.Load(t, cfg)
	cmd, _, err := NewRoot(nil).Find([]string{"setup"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--config", cfg}); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(input))
	if err := setupInteractive(cmd, a); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String()
}

func TestSetupInteractiveBackend(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	got := setupWith(t, cfg, "9\n4\n\n\n\n\n\n")
	for _, want := range []string{
		"    1. claude-code  Claude Code on your Claude subscription (recommended)\n",
		"    2. claude-api   Anthropic's API, paid per token\n",
		"    3. local        Your own model server, e.g. Ollama (experimental, untested)\n",
		"    4. codex        OpenAI's Codex CLI (experimental, untested)\n",
		`"9" isn't one of them: a number from 1 to 4, or a name`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if v, _ := cli(t, cfg, "settings", "get", "llm.backend"); !strings.Contains(v, "codex") {
		t.Errorf("backend after choosing 4: %s", v)
	}
}

// A config that still names the api backend runs on claude-api, its new
// name, and says so; the file keeps what it says.
func TestFormerBackendName(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("settings:\n  llm.backend: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := cli(t, cfg, "settings", "get", "llm.backend"); err != nil || !strings.Contains(out, "claude-api") {
		t.Errorf("settings get: %v\n%s", err, out)
	}
	a := enginetest.Load(t, cfg)
	if def, err := a.BackendDef(); err != nil || def.Name != "claude-api" || !def.Priced {
		t.Errorf("backend: %+v %v", def.Name, err)
	}
	if b, err := a.BackendReady(); err != nil || !strings.HasPrefix(b.Status(), "Runs with claude-api") {
		t.Errorf("status: %+v %v", b, err)
	}
	if raw, _ := os.ReadFile(cfg); !strings.Contains(string(raw), "llm.backend: api") {
		t.Errorf("the config was rewritten:\n%s", raw)
	}
}

// Choosing local, setup asks for the server's URL (Enter keeps the default,
// Ollama on this machine), then a model: picked by number from those the
// server lists, or typed when it doesn't answer.
func TestSetupInteractiveLocal(t *testing.T) {
	get := func(cfg, key string) settings.State {
		t.Helper()
		a := enginetest.Load(t, cfg)
		vals, err := a.Settings()
		if err != nil {
			t.Fatal(err)
		}
		st, _ := vals.Get(key)
		return st
	}

	// The default URL, kept; the model typed (whether or not Ollama runs here).
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	out := setupWith(t, cfg, "3\n\nqwen3:8b\n")
	if !strings.Contains(out, "Your model server's URL [Enter keeps http://localhost:11434/v1 (Ollama on this machine)]:") {
		t.Errorf("URL question:\n%s", out)
	}
	if st := get(cfg, engine.LocalURLSetting); !st.IsDefault {
		t.Errorf("Enter changed the URL: %+v", st)
	}
	if st := get(cfg, engine.LocalModelSetting); st.Value != "qwen3:8b" {
		t.Errorf("model: %+v\n%s", st, out)
	}

	// A server that answers: its models, one picked by number.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"qwen3:8b"},{"id":"llama3.2:3b"}]}`)
	}))
	defer srv.Close()
	cfg = filepath.Join(t.TempDir(), "config.yaml")
	out = setupWith(t, cfg, "3\n"+srv.URL+"/v1\n7\n2\n")
	for _, want := range []string{"The server has:\n    1. qwen3:8b\n    2. llama3.2:3b\n", "7 isn't one of them: a number from 1 to 2, or a name", "All set."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if st := get(cfg, engine.LocalURLSetting); st.Value != srv.URL+"/v1" {
		t.Errorf("URL: %+v", st)
	}
	if st := get(cfg, engine.LocalModelSetting); st.Value != "llama3.2:3b" {
		t.Errorf("model by number: %+v", st)
	}

	// A server that doesn't answer: it says so, and takes the model's name.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	down := "http://" + l.Addr().String() + "/v1"
	_ = l.Close()
	cfg = filepath.Join(t.TempDir(), "config.yaml")
	out = setupWith(t, cfg, "3\nlocalhost:1\n"+down+"\nqwen3:8b\n")
	for _, want := range []string{`"localhost:1" isn't an http(s) URL`, "Nothing answers at " + down + ".", "Its name on the server (e.g. qwen3:8b):"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if st := get(cfg, engine.LocalModelSetting); st.Value != "qwen3:8b" {
		t.Errorf("typed model: %+v", st)
	}
	if b, _ := enginetest.Load(t, cfg).BackendReady(); b.Name != "local" || b.Problem != "" {
		t.Errorf("backend: %+v", b)
	}
}

// The backend step names the settings its backend needs (local: its server
// and model), so a frontend shows them with the backend, and none otherwise.
func TestBackendStepSettings(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	enginetest.Load(t, cfg)
	settingsOf := func() []string {
		out, _ := cli(t, cfg, "setup", "--json")
		var got SetupOutput
		_ = json.Unmarshal([]byte(out), &got)
		for _, s := range got.Steps {
			if s.Step == stepBackend {
				return s.Settings
			}
		}
		return nil
	}
	if s := settingsOf(); len(s) != 0 {
		t.Errorf("claude-code: %v", s)
	}
	if _, err := cli(t, cfg, "settings", "set", "llm.backend", "local"); err != nil {
		t.Fatal(err)
	}
	if s := settingsOf(); !slices.Equal(s, []string{"llm.local.url", "llm.local.model"}) {
		t.Errorf("local: %v", s)
	}
}
