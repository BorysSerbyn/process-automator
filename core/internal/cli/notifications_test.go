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
	link := add(notifications.Notification{Kind: notifications.KindRun, Title: "PR reviewed", URL: "https://github.com/o/r/pull/1", Routine: "r", Run: runID})
	if got := click(link); got != (ClickOutput{Notification: link, Action: "open-url", URL: "https://github.com/o/r/pull/1"}) {
		t.Fatalf("link click: %+v", got)
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

// Notifications waiting to be acknowledged: listed as waiting, in run
// status (red) until `notifications ack`, one by one or all.
func TestNotificationsAck(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	cfg := a.Layout.Config()
	var ids []string
	for _, title := range []string{"Ana asked for the report", "Ben asked for a review"} {
		n, err := notifications.Add(a.Layout.Notifications(), notifications.Notification{Kind: notifications.KindRun, Title: title, Body: "b", Acknowledge: true, Routine: "r"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	if err := a.SyncStatus(time.Now()); err != nil {
		t.Fatal(err)
	}
	var waiting []notifications.Notification
	if out, err := cli(t, cfg, "notifications", "list", "--waiting", "--json"); err != nil || json.Unmarshal([]byte(out), &waiting) != nil ||
		len(waiting) != 2 || !waiting[0].Acknowledge || waiting[0].AcknowledgedAt != nil {
		t.Fatalf("waiting: %s %v", out, err)
	}
	if out, _ := cli(t, cfg, "status"); !strings.Contains(out, "(2) Acknowledgements pending") {
		t.Fatalf("status: %s", out)
	}
	if out, _ := cli(t, cfg, "doctor", "--json"); !strings.Contains(out, `"area": "notifications"`) || !strings.Contains(out, "notifications ack "+ids[0]) {
		t.Fatalf("doctor: %s", out)
	}
	if _, err := cli(t, cfg, "notifications", "ack"); err == nil {
		t.Fatal("ack with nothing named")
	}
	if _, err := cli(t, cfg, "notifications", "ack", "--all", ids[0]); err == nil {
		t.Fatal("ack with both")
	}
	if out, err := cli(t, cfg, "notifications", "ack", ids[0]); err != nil || !strings.Contains(out, "Acknowledged 1: Ana asked") {
		t.Fatalf("ack: %s %v", out, err)
	}
	if out, _ := cli(t, cfg, "status"); !strings.Contains(out, "(1) Acknowledgement pending") {
		t.Fatalf("status: %s", out)
	}
	var acked AckOutput
	if out, err := cli(t, cfg, "notifications", "ack", "--all", "--json"); err != nil || json.Unmarshal([]byte(out), &acked) != nil ||
		len(acked.Acknowledged) != 1 || acked.Acknowledged[0].ID != ids[1] {
		t.Fatalf("ack --all: %s %v", out, err)
	}
	if out, _ := cli(t, cfg, "status"); strings.Contains(out, "Acknowledgement") {
		t.Fatalf("still waiting: %s", out)
	}
	if out, _ := cli(t, cfg, "notifications", "list", "--waiting", "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("waiting after: %s", out)
	}
}
