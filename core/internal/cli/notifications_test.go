package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/notify"
)

// Clicking: the notification's own file, else the routine's action, else the
// global one; show-run opens a report of the run.
func TestNotificationClick(t *testing.T) {
	var opened []string
	orig := openExternal
	openExternal = func(target string) error { opened = append(opened, target); return nil }
	t.Cleanup(func() { openExternal = orig })

	a, set := enginetest.Fixture(t, map[string]string{"r": "queue", "q": "queue"}, nil)
	r, _ := set.Find("r")
	if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	runID := lastRunID(t, a, "r")
	add := func(n notify.Notification) string {
		n, err := notify.Add(a.Layout.Notifications(), n, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	cfg := a.Layout.Config()

	// Default: show-run opens a readable report of the run.
	id := add(notify.Notification{Kind: notify.KindRoutine, Title: "t", Routine: "r", Run: runID})
	if out, err := cli(t, cfg, "notifications", "open", id); err != nil || len(opened) != 1 || !strings.HasSuffix(opened[0], ".txt") {
		t.Fatalf("show-run: %s %v %v", out, err, opened)
	}
	if b, _ := os.ReadFile(opened[0]); !strings.Contains(string(b), runID) {
		t.Fatalf("report: %s", b)
	}
	// A frontend that shows runs gets them back instead.
	if out, _ := cli(t, cfg, "notifications", "open", id, "--frontend-shows-runs", "--json"); !strings.Contains(out, `"run": "`+runID) || len(opened) != 1 {
		t.Fatalf("frontend-shows-runs: %s %v", out, opened)
	}
	// The routine's own action wins over the global one; a command runs.
	marker := filepath.Join(t.TempDir(), "clicked")
	_, _ = cli(t, cfg, "settings", "set", "notifications.on-click", "none")
	_, _ = cli(t, cfg, "settings", "set", "routines.q.onNotificationClick", "run echo \"$PROCAUTO_ROUTINE\" > "+marker)
	qid := add(notify.Notification{Kind: notify.KindRoutine, Title: "t", Routine: "q"})
	if _, err := cli(t, cfg, "notifications", "open", qid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == "q" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if b, _ := os.ReadFile(marker); strings.TrimSpace(string(b)) != "q" {
		t.Fatalf("click command didn't run with its env: %q", b)
	}
	// Others fall back to the global one (none: nothing opens).
	if _, err := cli(t, cfg, "notifications", "open", id); err != nil || len(opened) != 1 {
		t.Fatalf("global none: %v %v", err, opened)
	}
	// A notification's own file wins over everything.
	doc := filepath.Join(t.TempDir(), "report.md")
	_ = os.WriteFile(doc, nil, 0o600)
	fid := add(notify.Notification{Kind: notify.KindRoutine, Title: "t", Routine: "q", Open: doc})
	if _, err := cli(t, cfg, "notifications", "open", fid); err != nil || opened[len(opened)-1] != doc {
		t.Fatalf("own file: %v %v", err, opened)
	}
	// Delivered marking.
	if _, err := cli(t, cfg, "notifications", "delivered", id, qid); err != nil {
		t.Fatal(err)
	}
	if out, _ := cli(t, cfg, "notifications", "list", "--pending", "--json"); strings.Contains(out, id) || !strings.Contains(out, fid) {
		t.Fatalf("pending: %s", out)
	}
}

func lastRunID(t *testing.T, a *engine.App, routine string) string {
	t.Helper()
	run := lastRun(a, routine)
	if run == nil {
		t.Fatal("no run")
	}
	return routine + "/" + run.StartedAt.Format("2006-01-02T150405")
}
