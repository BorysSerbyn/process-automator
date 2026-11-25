// Package enginetest has what tests of the engine and its commands share: a
// config with routines run by a fake model backend, and a memory secret store.
package enginetest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Secrets is the secret store tests use instead of the system's.
var Secrets = secrets.NewMemory()

// Load loads the app for the config at cfg with Secrets.
func Load(t *testing.T, cfg string, plugins ...pluginapi.Plugin) *engine.App {
	t.Helper()
	a, err := engine.Load(cfg, plugins, Secrets)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Set sets a setting in the config at cfg.
func Set(t *testing.T, cfg, key, value string, extra ...settings.Setting) {
	t.Helper()
	if _, err := (settings.Store{Path: cfg, Extra: extra}).Set(key, value); err != nil {
		t.Fatal(err)
	}
}

// Backend stands in for the model: each routine's run blocks until its gate
// opens (or the run is cancelled), then reports done.
type Backend struct {
	Gates map[string]chan struct{}
	// Jobs, if set, gets each job the backend runs.
	Jobs chan<- agent.Job
	// SessionID, if set, makes it a backend that picks a kept session's ID
	// itself (agent.SessionPicker), and reports this one.
	SessionID string
	// KilledErr, if set, is what a cancelled run returns instead of the
	// context's error, as an agent CLI killed with it does ("signal: killed").
	KilledErr error
}

// Name implements agent.Backend.
func (*Backend) Name() string { return "fake" }

// PicksSessionID implements agent.SessionPicker.
func (b *Backend) PicksSessionID() bool { return b.SessionID != "" }

// Run implements agent.Backend.
func (b *Backend) Run(ctx context.Context, j agent.Job) (*agent.Output, error) {
	if g := b.Gates[j.Name]; g != nil {
		select {
		case <-g:
		case <-ctx.Done():
			if b.KilledErr != nil {
				return nil, b.KilledErr
			}
			return nil, ctx.Err()
		}
	}
	if b.Jobs != nil {
		b.Jobs <- j
	}
	for _, tool := range j.Tools { // report done, as a routine would
		if tool.Name == "report_outcome" {
			if _, err := tool.Call(ctx, map[string]any{"outcome": "done"}); err != nil {
				return nil, err
			}
		}
	}
	out := &agent.Output{Text: "ok"}
	if j.SaveSession {
		out.SessionID = j.SessionID
		if b.SessionID != "" {
			out.SessionID = b.SessionID
		}
	}
	return out, nil
}

// UseBackend makes the claude-code backend b for the test. Its plan usage
// can't be read (runs go ahead) unless the test sets it with UsePlanUsage.
func UseBackend(t *testing.T, b agent.Backend) {
	orig := engine.Backends["claude-code"]
	engine.Backends["claude-code"] = engine.BackendDef{
		New:        func(*engine.App) (agent.Backend, error) { return b, nil },
		AgentModel: orig.AgentModel,
		Ready:      func(*engine.App) (string, string) { return "", "" },
		PlanUsage: func(context.Context, *engine.App) ([]usage.Limit, error) {
			return nil, usage.Unreadable("no Claude plan in tests")
		},
	}
	t.Cleanup(func() { engine.Backends["claude-code"] = orig })
}

// UsePlanUsage makes the claude-code backend's /usage report limits.
func UsePlanUsage(limits ...usage.Limit) {
	def := engine.Backends["claude-code"]
	def.PlanUsage = func(context.Context, *engine.App) ([]usage.Limit, error) { return limits, nil }
	engine.Backends["claude-code"] = def
}

// Fixture is a config with routines (name → concurrency) run by a Backend
// with gates, on the claude-code backend.
func Fixture(t *testing.T, routinesByConcurrency map[string]string, gates map[string]chan struct{}) (*engine.App, *routines.Set) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	Set(t, cfg, "llm.backend", "claude-code")
	dir := routines.Dir(filepath.Dir(cfg))
	_ = os.MkdirAll(dir, 0o700)
	for name, conc := range routinesByConcurrency {
		src := "---\nautomator.outcome: {done: It went., nothingToDo: []}\nautomator.concurrency: " + conc + "\n---\nGo. {{> memories}}\n"
		_ = os.WriteFile(filepath.Join(dir, name+".prompt"), []byte(src), 0o600)
	}
	UseBackend(t, &Backend{Gates: gates})
	a := Load(t, cfg)
	set, err := routines.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return a, set
}

// LastRun is a routine's last run in run status (nil when it has none).
func LastRun(a *engine.App, name string) *runstatus.Run {
	s, err := runstatus.Load(a.Layout.RunStatus())
	if err != nil {
		return nil
	}
	for _, r := range s.Routines {
		if r.Name == name {
			return r.LastRun
		}
	}
	return nil
}

// WaitOutcome waits until a routine's last run has the outcome.
func WaitOutcome(t *testing.T, a *engine.App, name string, want runstatus.Outcome) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if run := LastRun(a, name); run != nil && run.Outcome == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never became %s (last: %+v)", name, want, LastRun(a, name))
}

// Start runs a routine in the background; its error comes on the channel.
func Start(t *testing.T, a *engine.App, set *routines.Set, name string) chan error {
	t.Helper()
	r, err := set.Find(name)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.Execute(context.Background(), set, r, engine.RunOptions{})
		done <- err
	}()
	return done
}

// AddProgram adds a program whose executable is a shell script with body
// (the description file is desc, naming ./prog), and trusts it unless the
// test says not to. Trust is first used before, so the LLM routines already
// there stay trusted.
func AddProgram(t *testing.T, a *engine.App, name, desc, body string, trusted bool) programs.Program {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "prog"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, programs.DescriptionFile)
	if err := os.WriteFile(path, []byte(desc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	p, err := programs.Add(a.Layout.Programs(), name, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		x, err := a.Find(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Trust(x); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// RunNamed runs a routine of either kind in the background; its error comes
// on the channel.
func RunNamed(t *testing.T, a *engine.App, name string, o engine.RunOptions) chan error {
	t.Helper()
	x, err := a.Find(name)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.Run(context.Background(), x, o)
		done <- err
	}()
	return done
}

// AddWatcher adds a watcher whose executable is a shell script with body
// (desc is its watcher.yaml, naming ./watch), and trusts it unless the test
// says not to.
func AddWatcher(t *testing.T, a *engine.App, name, desc, body string, trusted bool) watchers.Watcher {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "watch"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, watchers.DescriptionFile)
	if err := os.WriteFile(path, []byte(desc), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := watchers.Add(a.Layout.Watchers(), name, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		if err := a.TrustWatcher(w); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// AddHook adds a hook from file (hook.prompt, a .prompt file, or
// hook.yaml, whose executable ./act is a shell script with body), and
// trusts it unless the test says not to. Trust is first used before, so the
// LLM routines already there stay trusted.
func AddHook(t *testing.T, a *engine.App, name, file, source, body string, trusted bool) hooks.Hook {
	t.Helper()
	src := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(src, "act"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(src, file)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	h, err := hooks.Add(a.Layout.Hooks(), name, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		x, err := a.FindHook(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Trust(x); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// UpdateHook replaces a hook's file (and its executable ./act, with body)
// as `hooks update` does.
func UpdateHook(t *testing.T, a *engine.App, name, file, source, body string) {
	t.Helper()
	src := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(src, "act"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(src, file)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Add(a.Layout.Hooks(), name, path, true); err != nil {
		t.Fatal(err)
	}
}

// UpdateWatcher replaces a watcher's copy (its ./watch with body) as
// `watchers update` does.
func UpdateWatcher(t *testing.T, a *engine.App, name, desc, body string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "watch"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, watchers.DescriptionFile)
	if err := os.WriteFile(path, []byte(desc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := watchers.Add(a.Layout.Watchers(), name, path, true); err != nil {
		t.Fatal(err)
	}
}
