package engine

import (
	"path/filepath"

	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

// Layout is where the app keeps its files: all of them in the data folder,
// which is the config file's folder. `paths` shows the ones frontends use.
type Layout struct{ config string }

// LayoutOf is the layout of the data folder holding the config file at path.
func LayoutOf(configPath string) Layout { return Layout{config: configPath} }

// Config is the config file.
func (l Layout) Config() string { return l.config }

// Data is the data folder.
func (l Layout) Data() string { return filepath.Dir(l.config) }

func (l Layout) in(elem ...string) string {
	return filepath.Join(append([]string{l.Data()}, elem...)...)
}

// Routines is the routines folder.
func (l Layout) Routines() string { return routines.Dir(l.Data()) }

// RunStatus is the run-status file frontends watch.
func (l Layout) RunStatus() string { return l.in("run-status.json") }

// Notifications is the notifications file frontends deliver from.
func (l Layout) Notifications() string { return l.in("notifications.json") }

// History is the run history's folder.
func (l Layout) History() string { return l.in("history") }

// MCPChanges logs every side-effecting call made through `mcp serve`.
func (l Layout) MCPChanges() string { return l.in("mcp-changes.jsonl") }

// Memories is the memories folder.
func (l Layout) Memories() string { return l.in("memories") }

// Trust records which routines are trusted with what.
func (l Layout) Trust() string { return l.in("trust.json") }

// Plugins holds the plugin folders `plugins add --dir` installed.
func (l Layout) Plugins() string { return l.in("plugins") }

// PluginData is an in-process plugin's own folder.
func (l Layout) PluginData(plugin string) string { return l.in("plugin-data", plugin) }

// SessionFolder is where a routine that saves its sessions but sets no
// folder works: its own, kept, so the sessions can be resumed from it.
func (l Layout) SessionFolder(routine string) string { return l.in("session-folders", routine) }

// SchedulerState records which slots the scheduler handled.
func (l Layout) SchedulerState() string { return l.in("scheduler-state.json") }

// PlanUsage is what's known of the Claude plan's usage (claude-code).
func (l Layout) PlanUsage() string { return l.in("plan-usage.json") }

// StatusLine is my Claude Code status line as it was before the app wrapped
// it (to run it, and to put it back).
func (l Layout) StatusLine() string { return l.in("statusline.json") }

// LoginPath caches the login shell's PATH.
func (l Layout) LoginPath() string { return l.in("login-path") }

// Reports holds run reports written for opening.
func (l Layout) Reports() string { return l.in("reports") }

// StartLog is where runs started with `start` log.
func (l Layout) StartLog() string { return l.in("start.log") }

// UpdateLog is where a detached update logs.
func (l Layout) UpdateLog() string { return l.in("update.log") }

// UpdateProgress is how the latest update went, or is going.
func (l Layout) UpdateProgress() string { return l.in("update-progress.json") }

// StopRequests holds the stop requests `stop` writes, one file per routine.
func (l Layout) StopRequests() string { return l.in("stop") }

// StopRequest is the file `stop` writes to stop a routine's run, whichever
// process runs it.
func (l Layout) StopRequest(routine string) string { return filepath.Join(l.StopRequests(), routine) }

// Locks holds the per-credential OAuth refresh locks, each routine's lock
// and the update lock.
func (l Layout) Locks() string { return l.in("locks") }

// RoutineLock is held by a routine's run (a routine never overlaps itself).
func (l Layout) RoutineLock(routine string) string {
	return filepath.Join(l.Locks(), "routine-"+routine+".lock")
}

// UpdateLock is held while an update installs.
func (l Layout) UpdateLock() string { return filepath.Join(l.Locks(), "update.lock") }

// RunQueueLock is the device-wide run queue.
func (l Layout) RunQueueLock() string { return l.in("run.lock") }

// EditLock serializes changes to routines and plugins.
func (l Layout) EditLock() string { return l.in("edit.lock") }

// DaemonLock is held by the one running daemon.
func (l Layout) DaemonLock() string { return l.in("daemon.lock") }
