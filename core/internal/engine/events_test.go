//go:build unix

package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/dispatch"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// The package's own tests can't use enginetest: these add watchers and hooks
// themselves.

func writeExe(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// addWatcher adds and trusts a watcher whose executable ./watch is a shell
// script.
func addWatcher(t *testing.T, a *App, name, desc, body string) watchers.Watcher {
	t.Helper()
	src := t.TempDir()
	writeExe(t, src, "watch", body)
	path := filepath.Join(src, watchers.DescriptionFile)
	if err := os.WriteFile(path, []byte(desc), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := watchers.Add(a.Layout.Watchers(), name, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.TrustWatcher(w); err != nil {
		t.Fatal(err)
	}
	return w
}

// addHook adds and trusts a hook from file (a hook.yaml whose ./act is a
// shell script with body, or a .prompt file).
func addHook(t *testing.T, a *App, name, file, source, body string) {
	t.Helper()
	src := t.TempDir()
	if body != "" {
		writeExe(t, src, "act", body)
	}
	path := filepath.Join(src, file)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Add(a.Layout.Hooks(), name, path, false); err != nil {
		t.Fatal(err)
	}
	x, err := a.FindHook(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Trust(x); err != nil {
		t.Fatal(err)
	}
}

// recordingHook is a program hook that appends the event it gets on stdin
// to got.jsonl in its folder.
const recordingHook = "executable: ./act\nwrites: false\non: {watcher: cal, type: meeting.started}\n"

// gotEvents are the IDs of the events the recording hook name got.
func gotEvents(a *App, name string) []string {
	b, _ := os.ReadFile(filepath.Join(a.Layout.HookData(name), "got.jsonl"))
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e watchers.Event
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func startEvents(t *testing.T, a *App) (*events, context.CancelFunc) {
	t.Helper()
	e := newEvents(a, func() (*App, error) { return a, nil })
	e.awakePoll = 20 * time.Millisecond
	e.minBackoff = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	e.sync(ctx, a)
	return e, func() { cancel(); e.stop() }
}

// A poll watcher's events reach the hooks listening for their type, each
// once: its cursor and the IDs it has seen are kept across a restart.
func TestPollWatcherFiresHooks(t *testing.T) {
	a := testApp(t)
	// It prints the same two events every time, and one naming the cursor
	// it was given.
	addWatcher(t, a, "cal", "executable: ./watch\nmode: poll\ninterval: 5s\n", `
echo '{"id":"e1","type":"meeting.started","data":{"title":"Standup"}}'
echo '{"id":"e2","type":"meeting.started"}'
echo '{"id":"other","type":"meeting.ended"}'
echo "{\"id\":\"given-$PA_CURSOR\",\"type\":\"meeting.started\"}"
echo '{"cursor":"c1"}'
`)
	addHook(t, a, "open-meet", hooks.DescriptionFile, recordingHook, "cat >> got.jsonl\n")
	_, stop := startEvents(t, a)
	waitUntil(t, "the first poll's events", func() bool { return len(gotEvents(a, "open-meet")) == 3 })
	stop()
	if got := gotEvents(a, "open-meet"); !slices.Equal(got, []string{"e1", "e2", "given-"}) {
		t.Fatalf("got %v", got)
	}
	runs, _ := history.List(a.Layout.History(), history.Filter{Routine: "open-meet"})
	if len(runs) != 3 || runs[0].Trigger != trigger.Event || !runs[0].Hook {
		t.Fatalf("runs %+v", runs)
	}
	e, _ := history.Get(a.Layout.History(), runs[0].ID)
	if len(e.Events) != 1 || e.Events[0].Watcher != "cal" {
		t.Fatalf("the run's events: %+v", e.Events)
	}

	// After a restart (due to poll at once), only the event that's new runs.
	st, err := watchers.LoadState(a.Layout.WatcherState())
	if err != nil || st.Watchers["cal"].Cursor != "c1" {
		t.Fatalf("state %+v %v", st, err)
	}
	st.Watchers["cal"].LastPollAt = nil
	if err := st.Save(a.Layout.WatcherState()); err != nil {
		t.Fatal(err)
	}
	_, stop = startEvents(t, a)
	waitUntil(t, "the second poll", func() bool { return len(gotEvents(a, "open-meet")) == 4 })
	time.Sleep(200 * time.Millisecond)
	stop()
	if got := gotEvents(a, "open-meet"); !slices.Equal(got, []string{"e1", "e2", "given-", "given-c1"}) {
		t.Fatalf("after a restart: %v", got)
	}
	// A watcher's run status: the hook is listed among hooks, not routines.
	if err := a.SyncStatus(time.Now()); err != nil {
		t.Fatal(err)
	}
	s, _ := runstatus.Load(a.Layout.RunStatus())
	if len(s.Routines) != 0 || len(s.Hooks) != 1 || s.Hooks[0].Name != "open-meet" || s.Hooks[0].LastRun == nil {
		t.Fatalf("run status: %+v", s)
	}
}

// A stream watcher's events reach hooks as it prints them; one that ends is
// started again, and one that keeps failing makes its hooks' problem.
func TestStreamWatcherFiresHooks(t *testing.T) {
	a := testApp(t)
	addWatcher(t, a, "cal", "executable: ./watch\nmode: stream\n", `
echo "{\"id\":\"s-$$\",\"type\":\"meeting.started\"}"
exit 1
`)
	addHook(t, a, "open-meet", hooks.DescriptionFile, recordingHook, "cat >> got.jsonl\n")
	_, stop := startEvents(t, a)
	waitUntil(t, "two starts' events", func() bool { return len(gotEvents(a, "open-meet")) >= 2 })
	stop()
	st, _ := watchers.LoadState(a.Layout.WatcherState())
	if ws := st.Watchers["cal"]; ws == nil || !strings.Contains(ws.Error, "its stream ended") {
		t.Fatalf("state %+v", st.Watchers["cal"])
	}
	x, _ := a.FindHook("open-meet")
	rd := a.Assess(x)
	if len(rd.Problems) != 1 || !strings.Contains(rd.Problems[0].Text, "its watcher cal is failing") {
		t.Fatalf("problems %+v", rd.Problems)
	}
}

// An event older than the hook's staleness is dropped; a watcher isn't
// polled in a maintenance wake.
func TestStaleEventsAndDarkWake(t *testing.T) {
	a := testApp(t)
	addWatcher(t, a, "cal", "executable: ./watch\nmode: poll\ninterval: 5s\n", `
echo '{"id":"old","type":"meeting.started","at":"2020-01-01T00:00:00Z"}'
echo '{"id":"fresh","type":"meeting.started"}'
`)
	addHook(t, a, "open-meet", hooks.DescriptionFile, recordingHook+"staleness: 2m\n", "cat >> got.jsonl\n")
	awake := false
	var mu sync.Mutex
	orig := fullyAwake
	fullyAwake = func() bool { mu.Lock(); defer mu.Unlock(); return awake }
	t.Cleanup(func() { fullyAwake = orig })
	_, stop := startEvents(t, a)
	defer stop()
	time.Sleep(300 * time.Millisecond)
	if st, _ := watchers.LoadState(a.Layout.WatcherState()); st.Watchers["cal"] != nil {
		t.Fatalf("polled in a maintenance wake: %+v", st.Watchers["cal"])
	}
	mu.Lock()
	awake = true
	mu.Unlock()
	waitUntil(t, "the fresh event", func() bool { return len(gotEvents(a, "open-meet")) == 1 })
	time.Sleep(200 * time.Millisecond)
	if got := gotEvents(a, "open-meet"); !slices.Equal(got, []string{"fresh"}) {
		t.Fatalf("got %v", got)
	}
}

// A hook starts at most its rate of runs an hour; one due far past it is
// paused, with a skipped run saying why.
func TestRateLimitAndLoopPause(t *testing.T) {
	a := testApp(t)
	addWatcher(t, a, "cal", "executable: ./watch\nmode: stream\n", "exec sleep 30\n")
	addHook(t, a, "open-meet", hooks.DescriptionFile, recordingHook+"rate: 1\n", "cat >> got.jsonl\n")
	e := newEvents(a, func() (*App, error) { return a, nil })
	fire := func(id string) {
		ev := watchers.Event{ID: id, Type: "meeting.started", At: time.Now(), Watcher: "cal"}
		e.run(context.Background(), dueFor("open-meet", ev))
	}
	fire("1") // runs
	fire("2") // past its rate: dropped
	fire("3") // dropped
	if got := gotEvents(a, "open-meet"); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("got %v", got)
	}
	fire("4") // a fourth in the hour: past three times its rate
	x, _ := a.FindHook("open-meet")
	c, _ := a.HookConfig(x)
	if c.Enabled {
		t.Fatal("not paused")
	}
	runs, _ := history.List(a.Layout.History(), history.Filter{Routine: "open-meet"})
	if len(runs) != 2 || runs[0].Outcome != runstatus.OutcomeSkipped || !strings.Contains(runs[0].Reason, "paused") ||
		!strings.Contains(runs[0].Reason, "hooks resume open-meet") {
		t.Fatalf("runs %+v", runs)
	}
	if got := gotEvents(a, "open-meet"); len(got) != 1 {
		t.Fatalf("it ran again: %v", got)
	}
	// I'm told, on the hook's page.
	f, err := notifications.Load(a.Layout.Notifications())
	if err != nil || len(f.Notifications) != 1 || !f.Notifications[0].Hook || f.Notifications[0].Routine != "open-meet" {
		t.Fatalf("notifications %+v %v", f, err)
	}
}

func dueFor(name string, evs ...watchers.Event) dispatch.Due {
	return dispatch.Due{Target: name, Occurrence: EventOccurrence(trigger.Event, evs...), Staleness: time.Hour}
}

// fakeModel is a model backend for an LLM hook: its first run waits for
// gate; it records each job.
type fakeModel struct {
	gate chan struct{}
	mu   sync.Mutex
	jobs []agent.Job
}

func (*fakeModel) Name() string { return "fake" }

func (f *fakeModel) Run(ctx context.Context, j agent.Job) (*agent.Output, error) {
	f.mu.Lock()
	first := len(f.jobs) == 0
	f.jobs = append(f.jobs, j)
	f.mu.Unlock()
	if first {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	for _, tool := range j.Tools {
		if tool.Name == agent.OutcomeTool {
			_, _ = tool.Call(ctx, map[string]any{"outcome": "done"})
		}
	}
	return &agent.Output{Text: "ok"}, nil
}

func useModel(t *testing.T, a *App, m agent.Backend) {
	orig := Backends["claude-code"]
	Backends["claude-code"] = BackendDef{
		New:        func(*App) (agent.Backend, error) { return m, nil },
		AgentModel: orig.AgentModel,
		Ready:      func(*App) (string, string) { return "", "" },
		PlanUsage:  func(context.Context, *App) ([]usage.Limit, error) { return nil, usage.Unreadable("no plan in tests") },
	}
	t.Cleanup(func() { Backends["claude-code"] = orig })
	if _, err := (settings.Store{Path: a.Layout.Config()}).Set("llm.backend", "claude-code"); err != nil {
		t.Fatal(err)
	}
}

// An LLM hook that batches gets, in one run, every event that came while
// its previous run went on.
func TestBatching(t *testing.T) {
	a := testApp(t)
	m := &fakeModel{gate: make(chan struct{})}
	useModel(t, a, m)
	addWatcher(t, a, "inbox", "executable: ./watch\nmode: stream\n", "exec sleep 30\n")
	addHook(t, a, "triage", "triage.prompt", "---\nautomator.on: {watcher: inbox}\nautomator.batch: true\nautomator.outcome: {done: It is done, nothingToDo: []}\n---\nGo. {{> memories}}\n", "")
	e, stop := startEvents(t, a)
	defer stop()
	if len(e.listening["inbox"]) != 1 {
		t.Fatalf("listening: %+v", e.listening)
	}
	ev := func(id string) watchers.Event {
		return watchers.Event{ID: id, Type: "email.received", At: time.Now(), Watcher: "inbox"}
	}
	ctx := context.Background()
	e.handle(ctx, "inbox", []watchers.Event{ev("1")}, nil, nil)
	waitUntil(t, "the first run", func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.jobs) == 1 })
	e.handle(ctx, "inbox", []watchers.Event{ev("2")}, nil, nil)
	e.handle(ctx, "inbox", []watchers.Event{ev("3"), ev("4")}, nil, nil)
	e.handle(ctx, "inbox", []watchers.Event{ev("2")}, nil, nil) // seen: dropped
	close(m.gate)
	waitUntil(t, "the batch's run", func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.jobs) == 2 })
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := func(j agent.Job) []string {
		var out []string
		for _, e := range j.Input["events"].([]any) {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(e.(map[string]any)["id"].(string), UntrustedOpen), UntrustedClose))
		}
		return out
	}
	if got := ids(m.jobs[0]); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("first run: %v", got)
	}
	if got := ids(m.jobs[1]); !slices.Equal(got, []string{"2", "3", "4"}) {
		t.Fatalf("the batch: %v", got)
	}
}
