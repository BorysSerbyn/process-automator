package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// A plugin's notice to acknowledge is the app's notification about the
// run, in run status (so health is error) until it's acknowledged.
func TestAskToAcknowledge(t *testing.T) {
	files := LayoutOf(filepath.Join(t.TempDir(), "config.yaml"))
	a := &App{Layout: files}
	h := &host{files: files, run: mcp.Run{Run: pluginapi.Run{Routine: "scout", ID: "scout/2026-11-23T090000"}}}
	if _, err := h.AskToAcknowledge(pluginapi.Notice{Title: " "}); err == nil {
		t.Fatal("no title accepted")
	}
	id, err := h.AskToAcknowledge(pluginapi.Notice{Title: "Ana asked for the report", Body: "in #team", Urgent: true})
	if err != nil {
		t.Fatal(err)
	}
	n, err := notifications.Get(files.Notifications(), id)
	if err != nil || n.Kind != notifications.KindAcknowledge || !n.Waiting() || !n.Urgent ||
		n.Routine != "scout" || n.Run != "scout/2026-11-23T090000" || n.Hook || n.Body != "in #team" {
		t.Fatalf("%+v %v", n, err)
	}
	s, _ := runstatus.Load(files.RunStatus())
	if s.Health != runstatus.HealthError || len(s.Waiting) != 1 || s.Waiting[0].ID != id {
		t.Fatalf("status: %+v", s)
	}
	if _, err := a.Acknowledge(nil, false, time.Now()); err == nil {
		t.Fatal("acknowledged nothing named")
	}
	if done, err := a.Acknowledge([]string{id}, false, time.Now()); err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	if s, _ := runstatus.Load(files.RunStatus()); s.Health == runstatus.HealthError || len(s.Waiting) != 0 {
		t.Fatalf("still red: %+v", s)
	}
}
