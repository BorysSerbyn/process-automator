package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/notify"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Queued routines take turns, shown as queued while they wait; a parallel
// routine runs meanwhile.
func TestQueueAndParallel(t *testing.T) {
	gate := make(chan struct{})
	a, set := enginetest.Fixture(t, map[string]string{"first": "queue", "second": "queue", "side": "parallel"},
		map[string]chan struct{}{"first": gate})
	first := enginetest.Start(t, a, set, "first")
	enginetest.WaitOutcome(t, a, "first", runstatus.OutcomeRunning)
	second := enginetest.Start(t, a, set, "second")
	enginetest.WaitOutcome(t, a, "second", runstatus.OutcomeQueued)
	if r := enginetest.LastRun(a, "second"); r.QueuedAt == nil {
		t.Fatal("queued run without queuedAt")
	}
	select {
	case err := <-enginetest.Start(t, a, set, "side"):
		if err != nil {
			t.Fatalf("side: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a parallel routine waited for the queue")
	}
	close(gate)
	for _, done := range []chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("queued run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued runs didn't finish")
		}
	}
	if r := enginetest.LastRun(a, "second"); r.QueuedAt == nil || r.StartedAt.Before(*r.QueuedAt) {
		t.Fatalf("second's record: %+v", r)
	}
}

// A scheduled run that only gets its turn after its slot went stale (waiting
// behind a run elsewhere) is recorded as missed, not run late.
func TestStaleAfterWaiting(t *testing.T) {
	gate := make(chan struct{})
	a, set := enginetest.Fixture(t, map[string]string{"manual": "queue", "sched": "queue"}, map[string]chan struct{}{"manual": gate})
	manual := enginetest.Start(t, a, set, "manual")
	enginetest.WaitOutcome(t, a, "manual", runstatus.OutcomeRunning)
	r, _ := set.Find("sched")
	done := make(chan error, 1)
	go func() {
		_, err := a.Execute(context.Background(), set, r, engine.RunOptions{StaleAfter: time.Now().Add(300 * time.Millisecond)})
		done <- err
	}()
	enginetest.WaitOutcome(t, a, "sched", runstatus.OutcomeQueued)
	time.Sleep(400 * time.Millisecond)
	close(gate)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "missed") {
		t.Fatalf("stale run: %v", err)
	}
	if run := enginetest.LastRun(a, "sched"); run.Outcome != runstatus.OutcomeMissed {
		t.Fatalf("recorded %+v", run)
	}
	if err := <-manual; err != nil {
		t.Fatalf("manual run: %v", err)
	}
}

// A cancelled run (the scheduler stopping) says so in plain words.
func TestInterruptedReason(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	a, set := enginetest.Fixture(t, map[string]string{"long": "queue"}, map[string]chan struct{}{"long": gate})
	r, _ := set.Find("long")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Execute(ctx, set, r, engine.RunOptions{})
		done <- err
	}()
	enginetest.WaitOutcome(t, a, "long", runstatus.OutcomeRunning)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run returned %v", err)
	}
	if run := enginetest.LastRun(a, "long"); run.Outcome != runstatus.OutcomeFailed || !strings.Contains(run.Reason, "interrupted") {
		t.Fatalf("recorded %+v", run)
	}
}

// The llm.backend setting offers exactly the registered engine.Backends, and each
// is complete.
func TestBackendSettingMatchesRegistry(t *testing.T) {
	for name, b := range engine.Backends {
		if b.New == nil || b.AgentModel == nil || b.Ready == nil {
			t.Errorf("backend %s is missing New, AgentModel or Ready", name)
		}
	}
	def, ok := settings.Lookup("llm.backend")
	if !ok {
		t.Fatal("llm.backend not registered")
	}
	var names []string
	for n := range engine.Backends {
		names = append(names, n)
	}
	slices.Sort(names)
	enum := slices.Sorted(slices.Values(def.Enum))
	if !slices.Equal(names, enum) || !slices.Contains(enum, def.Default.(string)) {
		t.Fatalf("Backends %v vs setting enum %v (default %v)", names, enum, def.Default)
	}
}

// failingBackend fails every run.
type failingBackend struct{}

func (failingBackend) Name() string { return "fake" }

func (failingBackend) Run(context.Context, agent.Job) (*agent.Output, error) {
	return nil, errors.New("the model fell over")
}

func TestFailureNotification(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	enginetest.UseBackend(t, failingBackend{})
	r, _ := set.Find("r")
	if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{}); err == nil {
		t.Fatal("run should fail")
	}
	f, _ := notify.Load(a.Layout.Notifications())
	if len(f.Notifications) != 1 || f.Notifications[0].Kind != notify.KindFailure || f.Notifications[0].Title != "r failed" ||
		!strings.Contains(f.Notifications[0].Body, "fell over") || f.Notifications[0].Run == "" {
		t.Fatalf("failure notification: %+v", f.Notifications)
	}
	enginetest.Set(t, a.Layout.Config(), "runs.failure-notifications", "false")
	_, _ = a.Execute(context.Background(), set, r, engine.RunOptions{})
	if f, _ := notify.Load(a.Layout.Notifications()); len(f.Notifications) != 1 {
		t.Fatal("notified with runs.failure-notifications off")
	}
	// Dry runs record nothing, notifications included.
	_ = os.Remove(a.Layout.Notifications())
	enginetest.Set(t, a.Layout.Config(), "runs.failure-notifications", "true")
	_, _ = a.Execute(context.Background(), set, r, engine.RunOptions{DryRun: true})
	if f, _ := notify.Load(a.Layout.Notifications()); len(f.Notifications) != 0 {
		t.Fatal("a dry run notified")
	}
}

// notify_me records what the routine asked, tied to its run; opening on
// click is only for documents and folders.
func TestNotifyMe(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	a, err := engine.Load(cfg, nil, enginetest.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	_ = os.WriteFile(filepath.Join(folder, "report.md"), []byte("# hi"), 0o600)
	_ = os.WriteFile(filepath.Join(folder, "evil.sh"), []byte("rm -rf ~"), 0o600)
	_ = os.WriteFile(filepath.Join(folder, "run.bin"), []byte("x"), 0o755)
	ctx := context.Background()
	cs, err := a.Connector.ConnectFor(ctx, "notify", engine.RunOf(routines.Routine{Name: "scout"}, folder, "scout/2026-11-19T092000"))
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(args map[string]any) *sdk.CallToolResult {
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "notify_me", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call(map[string]any{"title": "3 open threads", "body": "see report", "open": "report.md", "urgent": true}); res.IsError {
		t.Fatalf("notify_me: %+v", res.Content)
	}
	for _, bad := range []string{"evil.sh", "run.bin", "missing.md"} {
		if res := call(map[string]any{"title": "x", "open": bad}); !res.IsError {
			t.Errorf("open %s accepted", bad)
		}
	}
	f, _ := notify.Load(a.Layout.Notifications())
	if len(f.Notifications) != 1 {
		t.Fatalf("notifications: %+v", f.Notifications)
	}
	n := f.Notifications[0]
	if n.Routine != "scout" || n.Run != "scout/2026-11-19T092000" || !n.Urgent || n.Open != filepath.Join(folder, "report.md") || n.Kind != notify.KindRoutine {
		t.Fatalf("recorded %+v", n)
	}
}
