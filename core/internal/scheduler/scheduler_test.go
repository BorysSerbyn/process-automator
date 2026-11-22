package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

type harness struct {
	s      *Scheduler
	now    time.Time
	jobs   []Job
	events []string
	// runTakes advances the clock during each run.
	runTakes time.Duration
}

func newHarness(t *testing.T, now string) *harness {
	h := &harness{now: at(t, now)}
	h.s = &Scheduler{
		StatePath: filepath.Join(t.TempDir(), "state.json"),
		Jobs:      func() ([]Job, error) { return h.jobs, nil },
		Run: func(_ context.Context, j Job, o trigger.Occurrence, _ *time.Time) {
			h.events = append(h.events, "run "+j.Name+" "+o.At.Format("Mon 15:04"))
			h.now = h.now.Add(h.runTakes)
		},
		Missed: func(name string, o trigger.Occurrence, _ time.Duration) {
			h.events = append(h.events, "missed "+name+" "+o.At.Format("Mon 15:04"))
		},
		Now: func() time.Time { return h.now },
	}
	return h
}

func at(t *testing.T, s string) time.Time {
	v, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (h *harness) tick(t *testing.T, now string) string {
	t.Helper()
	h.now = at(t, now)
	h.events = nil
	if err := h.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.s.Wait()
	return strings.Join(h.events, "; ")
}

var (
	standup = Job{Name: "standup", Schedule: "20 9 * * 1-5", Staleness: 10 * time.Minute, Enabled: true}
	digest  = Job{Name: "digest", Schedule: "20 9 * * 1-5", Staleness: 12 * time.Hour, Enabled: true}
)

func TestRunsOnceAtSlot(t *testing.T) {
	h := newHarness(t, "2026-11-18 09:00")
	h.jobs = []Job{standup}
	if got := h.tick(t, "2026-11-18 09:00"); got != "" {
		t.Fatalf("before slot: %q", got)
	}
	if got := h.tick(t, "2026-11-18 09:20"); got != "run standup Wed 09:20" {
		t.Fatalf("at slot: %q", got)
	}
	if got := h.tick(t, "2026-11-18 09:21"); got != "" {
		t.Fatalf("ran twice: %q", got)
	}
}

func TestCatchUpWithinStalenessElseMissed(t *testing.T) {
	h := newHarness(t, "2026-11-18 08:00")
	h.jobs = []Job{standup, digest}
	h.tick(t, "2026-11-18 08:00")
	// Asleep from 8:00 to 9:45: standup is stale (10m), digest catches up.
	if got := h.tick(t, "2026-11-18 09:45"); got != "missed standup Wed 09:20; run digest Wed 09:20" {
		t.Fatalf("got %q", got)
	}
}

func TestTightestStalenessFirstAndRecheck(t *testing.T) {
	h := newHarness(t, "2026-11-18 08:00")
	h.jobs = []Job{digest, standup}
	h.tick(t, "2026-11-18 08:00")
	if got := h.tick(t, "2026-11-18 09:20"); got != "run standup Wed 09:20; run digest Wed 09:20" {
		t.Fatalf("order: %q", got)
	}

	// A slow first run makes the next one stale before it starts.
	h2 := newHarness(t, "2026-11-18 08:00")
	slow := Job{Name: "a-slow", Schedule: "20 9 * * 1-5", Staleness: 5 * time.Minute, Enabled: true}
	h2.jobs = []Job{slow, standup}
	h2.runTakes = 12 * time.Minute
	h2.tick(t, "2026-11-18 08:00")
	if got := h2.tick(t, "2026-11-18 09:20"); got != "run a-slow Wed 09:20; missed standup Wed 09:20" {
		t.Fatalf("recheck: %q", got)
	}
}

func TestFirstStartAndLongOutage(t *testing.T) {
	// First ever start, 2 minutes after the slot: runs.
	h := newHarness(t, "2026-11-18 09:22")
	h.jobs = []Job{standup}
	if got := h.tick(t, "2026-11-18 09:22"); got != "run standup Wed 09:20" {
		t.Fatalf("first start in window: %q", got)
	}
	// First ever start long after: nothing (no history to call it missed).
	h = newHarness(t, "2026-11-18 11:00")
	h.jobs = []Job{standup}
	if got := h.tick(t, "2026-11-18 11:00"); got != "" {
		t.Fatalf("first start late: %q", got)
	}
	// Off over a weekend: only the latest slot counts, once.
	if got := h.tick(t, "2026-11-23 09:25"); got != "run standup Mon 09:20" {
		t.Fatalf("after outage: %q", got)
	}
}

func TestDisabledBuildsNoBacklog(t *testing.T) {
	h := newHarness(t, "2026-11-18 08:00")
	off := standup
	off.Enabled = false
	h.jobs = []Job{off}
	if got := h.tick(t, "2026-11-18 09:20"); got != "" {
		t.Fatalf("disabled ran: %q", got)
	}
	h.jobs = []Job{standup}
	if got := h.tick(t, "2026-11-18 09:22"); got != "" {
		t.Fatalf("re-enabled ran a slot from while disabled: %q", got)
	}
}

func TestNext(t *testing.T) {
	got := Next("20 9 * * 1-5", at(t, "2026-11-20 10:00")) // Friday
	if got.Format("Mon 15:04") != "Mon 09:20" {
		t.Fatalf("Next = %s", got)
	}
}

func TestRunsGetScheduleOccurrences(t *testing.T) {
	h := newHarness(t, "2026-11-18 09:00")
	h.jobs = []Job{standup}
	var got trigger.Occurrence
	h.s.Run = func(_ context.Context, _ Job, o trigger.Occurrence, _ *time.Time) { got = o }
	h.tick(t, "2026-11-18 09:20")
	if got.Trigger != trigger.Schedule || got.ID != "schedule:2026-11-18T09:20:00" || !got.At.Equal(at(t, "2026-11-18 09:20")) {
		t.Fatalf("occurrence %+v", got)
	}
}

// The state file is versioned: one it doesn't know is an error, not a fresh
// start that would re-run handled slots.
func TestStateVersion(t *testing.T) {
	h := newHarness(t, "2026-11-18 09:21")
	h.jobs = []Job{standup}
	if got := h.tick(t, "2026-11-18 09:20"); got != "run standup Wed 09:20" {
		t.Fatalf("first tick: %q", got)
	}
	b, _ := os.ReadFile(h.s.StatePath)
	if !strings.Contains(string(b), `"version": 2`) || !strings.Contains(string(b), `"schedule"`) {
		t.Fatalf("state: %s", b)
	}
	if err := os.WriteFile(h.s.StatePath, []byte(`{"version": 9, "routines": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.load(); err == nil || !strings.Contains(err.Error(), "unknown state version") {
		t.Fatalf("unknown version: %v", err)
	}
}

func TestFirstOn(t *testing.T) {
	if got := FirstOn("20 9 * * 1-5", at(t, "2026-11-17 15:00")); !got.Equal(at(t, "2026-11-17 09:20")) {
		t.Fatalf("weekday: %s", got)
	}
	if got := FirstOn("20 9 * * 1-5", at(t, "2026-11-21 15:00")); !got.IsZero() {
		t.Fatalf("saturday has no slot: %s", got)
	}
	if got := FirstOn("0 0 * * *", at(t, "2026-11-17 15:00")); !got.Equal(at(t, "2026-11-17 00:00")) {
		t.Fatalf("midnight slot: %s", got)
	}
}

// Parallel routines run at the same time, alongside the queue.
func TestParallelRunsOverlap(t *testing.T) {
	h := newHarness(t, "2026-11-18 08:00")
	a := Job{Name: "pa", Schedule: "20 9 * * *", Staleness: time.Hour, Enabled: true, Parallel: true}
	b := Job{Name: "pb", Schedule: "20 9 * * *", Staleness: time.Hour, Enabled: true, Parallel: true}
	h.jobs = []Job{a, b}
	h.tick(t, "2026-11-18 08:00")
	started := make(chan string, 2)
	release := make(chan struct{})
	h.s.Run = func(_ context.Context, j Job, _ trigger.Occurrence, _ *time.Time) {
		started <- j.Name
		<-release
	}
	h.now = at(t, "2026-11-18 09:20")
	if err := h.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("parallel routines didn't run at the same time")
		}
	}
	close(release)
	h.s.Wait()
}

// A routine never overlaps itself: its next slot waits for its run, and is
// missed once it's past its staleness.
func TestBusyRoutineSlotWaits(t *testing.T) {
	h := newHarness(t, "2026-11-18 08:00")
	every := Job{Name: "p", Schedule: "*/10 * * * *", Staleness: 15 * time.Minute, Enabled: true, Parallel: true}
	h.jobs = []Job{every}
	h.tick(t, "2026-11-18 08:00")
	release := make(chan struct{})
	var runs []string
	var mu sync.Mutex
	h.s.Run = func(_ context.Context, _ Job, o trigger.Occurrence, _ *time.Time) {
		mu.Lock()
		runs = append(runs, o.At.Format("15:04"))
		mu.Unlock()
		<-release
	}
	h.now = at(t, "2026-11-18 08:10")
	_ = h.s.Tick(context.Background()) // starts the 08:10 run, which keeps going
	h.now = at(t, "2026-11-18 08:20")
	_ = h.s.Tick(context.Background()) // 08:20 is due, but p is still running
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(runs) != 1 {
		t.Fatalf("overlapped itself: %v", runs)
	}
	mu.Unlock()
	close(release)
	h.s.Wait()
	// Done at 08:22: the waiting 08:20 slot is still fresh, so it runs now.
	if got := h.tick(t, "2026-11-18 08:22"); got != "" || len(runs) != 2 || runs[1] != "08:20" {
		t.Fatalf("waiting slot: %q %v", got, runs)
	}
}

func TestQueuedCallback(t *testing.T) {
	h := newHarness(t, "2026-11-18 08:00")
	h.jobs = []Job{standup, digest}
	h.tick(t, "2026-11-18 08:00")
	var queued []string
	h.s.Queued = func(name string, _ trigger.Occurrence, _ time.Time) { queued = append(queued, name) }
	h.tick(t, "2026-11-18 09:20")
	if len(queued) != 1 || queued[0] != "digest" {
		t.Fatalf("queued = %v; the second of two due queued routines waits", queued)
	}
}

// A routine still waiting in the queue can be dropped (stopped) before it runs.
func TestDropWaiting(t *testing.T) {
	h := newHarness(t, "2026-11-18 08:00")
	h.jobs = []Job{standup, digest}
	h.tick(t, "2026-11-18 08:00")
	release := make(chan struct{})
	ran := make(chan string, 2)
	h.s.Run = func(_ context.Context, j Job, _ trigger.Occurrence, _ *time.Time) {
		ran <- j.Name
		<-release
	}
	h.now = at(t, "2026-11-18 09:20")
	_ = h.s.Tick(context.Background())
	if first := <-ran; first != "standup" {
		t.Fatalf("first = %s", first)
	}
	if _, _, ok := h.s.Drop("digest"); !ok {
		t.Fatal("digest wasn't waiting")
	}
	if _, _, ok := h.s.Drop("standup"); ok {
		t.Fatal("dropped a run already handed over")
	}
	close(release)
	h.s.Wait()
	select {
	case name := <-ran:
		t.Fatalf("%s ran after being dropped", name)
	default:
	}
}
