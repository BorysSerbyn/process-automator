package engine

import (
	"path/filepath"
	"testing"
)

// The data folder's file names are what existing installs have on disk.
func TestLayoutNames(t *testing.T) {
	l := LayoutOf(filepath.Join("/d", "config.yaml"))
	for got, want := range map[string]string{
		l.Data(): "/d", l.Routines(): "/d/routines", l.RunStatus(): "/d/run-status.json",
		l.Notifications(): "/d/notifications.json", l.History(): "/d/history", l.MCPChanges(): "/d/mcp-changes.jsonl",
		l.Memories(): "/d/memories", l.Trust(): "/d/trust.json", l.Plugins(): "/d/plugins",
		l.PluginData("x"): "/d/plugin-data/x", l.SchedulerState(): "/d/scheduler-state.json",
		l.PlanUsage(): "/d/plan-usage.json", l.StatusLine(): "/d/statusline.json",
		l.LoginPath(): "/d/login-path", l.Reports(): "/d/reports", l.StartLog(): "/d/start.log",
		l.UpdateLog(): "/d/update.log", l.StopRequest("x"): "/d/stop/x", l.Locks(): "/d/locks",
		l.RoutineLock("x"): "/d/locks/routine-x.lock", l.UpdateLock(): "/d/locks/update.lock",
		l.RunQueueLock(): "/d/run.lock", l.EditLock(): "/d/edit.lock", l.DaemonLock(): "/d/daemon.lock",
	} {
		if got != filepath.FromSlash(want) {
			t.Errorf("%s, want %s", got, want)
		}
	}
}
