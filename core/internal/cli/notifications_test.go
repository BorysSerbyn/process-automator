package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
)

// Clicking one of the app's notifications opens the app: one about a run
// shows that run in its routine's window, one about the app shows the app.
// The core opens nothing.
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
	click := func(id string) ClickOutput {
		t.Helper()
		out, err := cli(t, cfg, "notifications", "open", id, "--json")
		var got ClickOutput
		if err != nil || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("open %s: %s %v", id, out, err)
		}
		return got
	}

	id := add(notifications.Notification{Kind: notifications.KindFailure, Title: "r failed", Routine: "r", Run: runID})
	if got := click(id); got != (ClickOutput{Notification: id, Action: "show-run", Routine: "r", Run: runID}) {
		t.Fatalf("run click: %+v", got)
	}
	upd := add(notifications.Notification{Kind: notifications.KindApp, Title: "Updated"})
	if got := click(upd); got != (ClickOutput{Notification: upd, Action: "show-app"}) {
		t.Fatalf("app click: %+v", got)
	}
	if len(opened) != 0 {
		t.Fatalf("a click opened %v", opened)
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
