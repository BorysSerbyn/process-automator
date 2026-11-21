package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
)

// Clicking one of the app's notifications shows the run it's about (a
// readable report, or the frontend shows it); one about no run does nothing.
func TestNotificationClick(t *testing.T) {
	var opened []string
	orig := openExternal
	openExternal = func(target string) error { opened = append(opened, target); return nil }
	t.Cleanup(func() { openExternal = orig })

	a, set := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	r, _ := set.Find("r")
	if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	runID := lastRunID(t, a, "r")
	add := func(n notifications.Notification) string {
		n, err := notifications.Add(a.Layout.Notifications(), n, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	cfg := a.Layout.Config()

	id := add(notifications.Notification{Kind: notifications.KindFailure, Title: "r failed", Routine: "r", Run: runID})
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
	// One about no run (an update) does nothing.
	upd := add(notifications.Notification{Kind: notifications.KindApp, Title: "Updated"})
	if out, err := cli(t, cfg, "notifications", "open", upd, "--json"); err != nil || !strings.Contains(out, `"action": "none"`) || len(opened) != 1 {
		t.Fatalf("app click: %s %v %v", out, err, opened)
	}
	// Delivered marking.
	if _, err := cli(t, cfg, "notifications", "delivered", id); err != nil {
		t.Fatal(err)
	}
	if out, _ := cli(t, cfg, "notifications", "list", "--pending", "--json"); strings.Contains(out, id) || !strings.Contains(out, upd) {
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
