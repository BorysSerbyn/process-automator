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
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
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
type Backend struct{ Gates map[string]chan struct{} }

// Name implements agent.Backend.
func (*Backend) Name() string { return "fake" }

// Run implements agent.Backend.
func (b *Backend) Run(ctx context.Context, j agent.Job) (*agent.Output, error) {
	if g := b.Gates[j.Name]; g != nil {
		select {
		case <-g:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	for _, tool := range j.Tools { // report done, as a routine would
		if tool.Name == "report_outcome" {
			if _, err := tool.Call(ctx, map[string]any{"outcome": "done"}); err != nil {
				return nil, err
			}
		}
	}
	return &agent.Output{Text: "ok"}, nil
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
		src := "---\nautomator.concurrency: " + conc + "\n---\nGo. {{> memories}}\n"
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
