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
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

// Clicking: a routine's notification opens its own file, else does its
// routine's onNotificationClick (default show-run, a report of the run); the
// app's own show the run whatever the routines say.
func TestNotificationClick(t *testing.T) {
	var opened []string
	orig := openExternal
	openExternal = func(target string) error { opened = append(opened, target); return nil }
	t.Cleanup(func() { openExternal = orig })

	a, _ := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	// q uses notify, so it has a click setting.
	q := "---\nautomator.servers: [notify]\n---\nGo.\n"
	if err := os.WriteFile(filepath.Join(a.Layout.Routines(), "q.prompt"), []byte(q), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"r", "q"} {
		r, _ := set.Find(name)
		if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	runID, qRun := lastRunID(t, a, "r"), lastRunID(t, a, "q")
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
	// Only routines that use notify have a click setting; there's no app-wide one.
	if _, err := cli(t, cfg, "settings", "set", "routines.r.onNotificationClick", "none"); err == nil {
		t.Fatal("a routine without notify took a click setting")
	}
	if _, err := cli(t, cfg, "settings", "set", "notifications.on-click", "none"); err == nil {
		t.Fatal("the retired app-wide click setting is still there")
	}
	// The routine's own action; a command runs, told what was clicked.
	marker := filepath.Join(t.TempDir(), "clicked")
	if _, err := cli(t, cfg, "settings", "set", "routines.q.onNotificationClick", "run echo \"$PROCAUTO_ROUTINE\" > "+marker); err != nil {
		t.Fatal(err)
	}
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
	// The app's own notifications show the run, whatever the routine's setting.
	fail := add(notify.Notification{Kind: notify.KindFailure, Title: "q failed", Routine: "q", Run: qRun})
	if out, err := cli(t, cfg, "notifications", "open", fail, "--json"); err != nil || !strings.Contains(out, `"action": "show-run"`) ||
		len(opened) != 2 || !strings.HasSuffix(opened[1], ".txt") {
		t.Fatalf("failure click: %s %v %v", out, err, opened)
	}
	// One about no run (an update) does nothing.
	upd := add(notify.Notification{Kind: notify.KindApp, Title: "Updated"})
	if out, err := cli(t, cfg, "notifications", "open", upd, "--json"); err != nil || !strings.Contains(out, `"action": "none"`) || len(opened) != 2 {
		t.Fatalf("app click: %s %v %v", out, err, opened)
	}
	// A notification's own file wins over the routine's action.
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
