package dispatch

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

// recorder is a dispatcher whose runs block until their gate opens, and
// that records what ran and what was missed.
type recorder struct {
	d       *Dispatcher
	mu      sync.Mutex
	ran     []string
	started []string
	missed  []string
	gates   map[string]chan struct{}
}

func newRecorder() *recorder {
	r := &recorder{gates: map[string]chan struct{}{}}
	r.d = &Dispatcher{
		Run: func(_ context.Context, d Due) {
			r.mu.Lock()
			g := r.gates[d.Occurrence.ID]
			r.started = append(r.started, d.Occurrence.ID)
			r.mu.Unlock()
			if g != nil {
				<-g
			}
			r.mu.Lock()
			r.ran = append(r.ran, d.Occurrence.ID)
			r.mu.Unlock()
		},
		Missed: func(d Due, _ time.Duration) {
			r.mu.Lock()
			r.missed = append(r.missed, d.Occurrence.ID)
			r.mu.Unlock()
		},
	}
	return r
}

func (r *recorder) gate(id string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := make(chan struct{})
	r.gates[id] = g
	return g
}

func (r *recorder) got() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ran), slices.Clone(r.missed)
}

func occ(id string) trigger.Occurrence {
	return trigger.Occurrence{Trigger: trigger.Event, ID: id, At: time.Now()}
}

func due(target, id string, parallel bool) Due {
	return Due{Target: target, Occurrence: occ(id), Staleness: time.Hour, Parallel: parallel}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

// A target never overlaps itself: what comes while it runs waits, in order.
func TestOneRunPerTargetInOrder(t *testing.T) {
	r := newRecorder()
	ctx := context.Background()
	g := r.gate("a1")
	r.d.Submit(ctx, due("a", "a1", true))
	r.d.Submit(ctx, due("a", "a2", true))
	r.d.Submit(ctx, due("a", "a3", true))
	if !r.d.Busy("a") {
		t.Fatal("a isn't busy while it runs")
	}
	if ran, _ := r.got(); len(ran) != 0 {
		t.Fatalf("ran before the first finished: %v", ran)
	}
	close(g)
	waitFor(t, func() bool { ran, _ := r.got(); return len(ran) == 3 })
	r.d.Wait()
	if ran, _ := r.got(); !slices.Equal(ran, []string{"a1", "a2", "a3"}) {
		t.Fatalf("ran %v", ran)
	}
	if r.d.Busy("a") {
		t.Fatal("a is still busy")
	}
}

// Queued targets run one at a time, tightest staleness first; parallel ones
// run alongside them.
func TestQueueAndParallel(t *testing.T) {
	r := newRecorder()
	ctx := context.Background()
	var queued []string
	var qmu sync.Mutex
	r.d.Queued = func(d Due) { qmu.Lock(); queued = append(queued, d.Target); qmu.Unlock() }
	g := r.gate("first")
	r.d.Submit(ctx, due("first", "first", false))
	waitFor(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return slices.Contains(r.started, "first") })
	loose := due("loose", "loose", false)
	loose.Staleness = 2 * time.Hour
	tight := due("tight", "tight", false)
	tight.Staleness = time.Minute
	r.d.Submit(ctx, loose)
	r.d.Submit(ctx, tight)
	r.d.Submit(ctx, due("side", "side", true)) // doesn't wait for the queue
	waitFor(t, func() bool { ran, _ := r.got(); return slices.Contains(ran, "side") })
	if ran, _ := r.got(); slices.Contains(ran, "tight") || slices.Contains(ran, "loose") {
		t.Fatalf("queued runs didn't wait their turn: %v", ran)
	}
	close(g)
	r.d.Wait()
	if ran, _ := r.got(); !slices.Equal(ran, []string{"side", "first", "tight", "loose"}) {
		t.Fatalf("ran %v", ran)
	}
	qmu.Lock()
	defer qmu.Unlock()
	if !slices.Equal(queued, []string{"loose", "tight"}) {
		t.Fatalf("queued %v", queued)
	}
}

// An occurrence that waited past its staleness is missed, not run late.
func TestStaleIsMissed(t *testing.T) {
	r := newRecorder()
	var clock sync.Mutex
	now := time.Now()
	r.d.Now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	ctx := context.Background()
	g := r.gate("a1")
	r.d.Submit(ctx, due("a", "a1", true))
	late := due("a", "a2", true)
	late.Staleness = time.Minute
	r.d.Submit(ctx, late)
	clock.Lock()
	now = now.Add(2 * time.Minute) // a1 took long
	clock.Unlock()
	close(g)
	r.d.Wait()
	waitFor(t, func() bool { _, missed := r.got(); return len(missed) == 1 })
	if ran, missed := r.got(); !slices.Equal(ran, []string{"a1"}) || !slices.Equal(missed, []string{"a2"}) {
		t.Fatalf("ran %v, missed %v", ran, missed)
	}
}

// A target that batches folds what comes while it waits into one run.
func TestBatching(t *testing.T) {
	r := newRecorder()
	ctx := context.Background()
	merge := func(waiting, next trigger.Occurrence) trigger.Occurrence {
		waiting.ID += "+" + next.ID
		return waiting
	}
	g := r.gate("a1")
	for _, id := range []string{"a1", "a2", "a3", "a4"} {
		d := due("a", id, true)
		d.Merge = merge
		r.d.Submit(ctx, d)
	}
	close(g)
	r.d.Wait()
	waitFor(t, func() bool { ran, _ := r.got(); return len(ran) == 2 })
	if ran, _ := r.got(); !slices.Equal(ran, []string{"a1", "a2+a3+a4"}) {
		t.Fatalf("ran %v", ran)
	}
}

// Past MaxPending, an occurrence is dropped as missed; Drop takes what
// waits out.
func TestPendingCapAndDrop(t *testing.T) {
	r := newRecorder()
	ctx := context.Background()
	g := r.gate("first")
	r.d.Submit(ctx, due("a", "first", true))
	for i := 0; i < MaxPending+2; i++ {
		r.d.Submit(ctx, due("a", "more", true))
	}
	if _, missed := r.got(); len(missed) != 2 {
		t.Fatalf("missed %d, want 2", len(missed))
	}
	if _, ok := r.d.Drop("a"); ok {
		t.Fatal("a running target's run was dropped as if it were waiting")
	}
	close(g)
	r.d.Wait()
	if ran, _ := r.got(); !slices.Equal(ran, []string{"first"}) {
		t.Fatalf("what waited still ran after Drop: %v", ran)
	}

	// A queued run waiting for the worker is taken out.
	r2 := newRecorder()
	g2 := r2.gate("x")
	r2.d.Submit(ctx, due("x", "x", false))
	waitFor(t, func() bool { r2.mu.Lock(); defer r2.mu.Unlock(); return slices.Contains(r2.started, "x") })
	r2.d.Submit(ctx, due("y", "y", false))
	d, ok := r2.d.Drop("y")
	if !ok || d.Occurrence.ID != "y" || d.QueuedAt == nil {
		t.Fatalf("dropped %+v %v", d, ok)
	}
	close(g2)
	r2.d.Wait()
	if ran, _ := r2.got(); !slices.Equal(ran, []string{"x"}) {
		t.Fatalf("ran %v", ran)
	}
}
