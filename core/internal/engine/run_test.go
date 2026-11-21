package engine_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
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

// A cancelled run whose backend ends with an error of its own (an agent CLI
// killed with it: "signal: killed") is interrupted too, not a failure in the
// backend's words.
func TestInterruptedWhateverTheBackendSays(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	a, set := enginetest.Fixture(t, map[string]string{"long": "queue"}, nil)
	enginetest.UseBackend(t, &enginetest.Backend{Gates: map[string]chan struct{}{"long": gate}, KilledErr: errors.New("claude: signal: killed")})
	r, _ := set.Find("long")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Execute(ctx, set, r, engine.RunOptions{})
		done <- err
	}()
	enginetest.WaitOutcome(t, a, "long", runstatus.OutcomeRunning)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a cancelled run ended without an error")
	}
	if run := enginetest.LastRun(a, "long"); !strings.Contains(run.Reason, "interrupted") {
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
	f, _ := notifications.Load(a.Layout.Notifications())
	if len(f.Notifications) != 1 || f.Notifications[0].Kind != notifications.KindFailure || f.Notifications[0].Title != "r failed" ||
		!strings.Contains(f.Notifications[0].Body, "fell over") || f.Notifications[0].Run == "" {
		t.Fatalf("failure notification: %+v", f.Notifications)
	}
	enginetest.Set(t, a.Layout.Config(), "runs.failure-notifications", "false")
	_, _ = a.Execute(context.Background(), set, r, engine.RunOptions{})
	if f, _ := notifications.Load(a.Layout.Notifications()); len(f.Notifications) != 1 {
		t.Fatal("notified with runs.failure-notifications off")
	}
	// Dry runs record nothing, notifications included.
	_ = os.Remove(a.Layout.Notifications())
	enginetest.Set(t, a.Layout.Config(), "runs.failure-notifications", "true")
	_, _ = a.Execute(context.Background(), set, r, engine.RunOptions{DryRun: true})
	if f, _ := notifications.Load(a.Layout.Notifications()); len(f.Notifications) != 0 {
		t.Fatal("a dry run notified")
	}
}

// A routine that saves its sessions without a folder works in a folder of its
// own that's kept, recorded with the run: its session resumes from there.
// Turning saving off again doesn't change what it was trusted with.
func TestSessionWithoutFolder(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"sweep": "queue"}, nil)
	r, _ := set.Find("sweep")
	before, _ := a.RoutineConfig(r)
	enginetest.Set(t, a.Layout.Config(), "routines.sweep.session", "save", r.Settings()...)
	jobs := make(chan agent.Job, 1)
	enginetest.UseBackend(t, &enginetest.Backend{Jobs: jobs})
	a = enginetest.Load(t, a.Layout.Config())
	if err := <-enginetest.Start(t, a, set, "sweep"); err != nil {
		t.Fatal(err)
	}
	want := a.Layout.SessionFolder("sweep")
	if j := <-jobs; j.Folder != want || j.SessionID == "" || !j.SaveSession {
		t.Errorf("ran in %q with session %q, want %s", j.Folder, j.SessionID, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Errorf("its folder isn't kept: %v", err)
	}
	if run := enginetest.LastRun(a, "sweep"); run == nil || run.Folder != want || run.SessionID == "" {
		t.Errorf("last run: %+v", run)
	}
	if list, _ := history.List(a.Layout.History(), history.Filter{}); len(list) != 1 || list[0].Folder != want {
		t.Errorf("history: %+v", list)
	}
	after, _ := a.RoutineConfig(r)
	if !engine.SurfaceOf(r, before).Equal(engine.SurfaceOf(r, after)) {
		t.Error("saving its session changed what it's trusted with")
	}
}

// A backend whose CLI picks the session ID (as codex does) gets none from the
// app; the ID it reports is recorded with the run, and so is the backend
// that resumes it.
func TestBackendPicksSessionID(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"sweep": "queue"}, nil)
	r, _ := set.Find("sweep")
	enginetest.Set(t, a.Layout.Config(), "routines.sweep.session", "save", r.Settings()...)
	jobs := make(chan agent.Job, 2)
	enginetest.UseBackend(t, &enginetest.Backend{Jobs: jobs, SessionID: "019a7c3e-thread"})
	a = enginetest.Load(t, a.Layout.Config())
	if err := <-enginetest.Start(t, a, set, "sweep"); err != nil {
		t.Fatal(err)
	}
	if j := <-jobs; j.SessionID != "" || !j.SaveSession {
		t.Errorf("job: save %v, session %q (the CLI picks it)", j.SaveSession, j.SessionID)
	}
	if run := enginetest.LastRun(a, "sweep"); run == nil || run.SessionID != "019a7c3e-thread" || run.Backend != "fake" {
		t.Errorf("last run: %+v", run)
	}
	list, _ := history.List(a.Layout.History(), history.Filter{})
	if len(list) != 1 || list[0].SessionID != "019a7c3e-thread" || list[0].Backend != "fake" {
		t.Errorf("history: %+v", list)
	}

	// A dry run keeps no session, whoever would pick its ID.
	if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if j := <-jobs; j.SaveSession || j.SessionID != "" {
		t.Errorf("dry run job keeps a session: %+v", j)
	}
}
