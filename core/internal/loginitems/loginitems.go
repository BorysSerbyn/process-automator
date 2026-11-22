// Package loginitems is the app's items that start at login (its menu bar
// app or tray, and the scheduler) and starting and stopping them now:
// launchd on macOS, systemd and XDG autostart on Linux.
package loginitems

import (
	"errors"
)

// LoginItem is one thing that starts at login.
type LoginItem struct {
	// Name is ui (macOS's menu bar app, automator-ui; Linux's tray) or scheduler.
	Name  string `json:"name"`
	Label string `json:"label"`
	// Installed is whether it's set up to start at login at all (the
	// install puts it there).
	Installed bool `json:"installed"`
	// Enabled is whether it starts at login.
	Enabled bool `json:"enabled"`
}

// Status is `login-items --json`: whether the app starts at login.
type Status struct {
	// Installed is whether any item is set up to start at login (none before
	// the app is installed).
	Installed bool `json:"installed"`
	// Enabled is true when something is installed and every installed item
	// starts at login.
	Enabled bool        `json:"enabled"`
	Items   []LoginItem `json:"items"`
}

// RunningOutput is `login-items start --json` and `login-items stop --json`:
// whether the scheduler runs now, and what the command changed.
type RunningOutput struct {
	// Scheduler is running or stopped, as the command left it.
	Scheduler string `json:"scheduler" jsonschema:"enum=running,enum=stopped"`
	// Changed is whether the command started or stopped it (false: it
	// already was).
	Changed bool `json:"changed"`
	// UIStopped is whether stop stopped a running menu bar app or tray
	// (besides the one that ran it, if one did).
	UIStopped bool `json:"uiStopped,omitempty"`
}

const (
	schedulerRunning = "running"
	schedulerStopped = "stopped"
)

// Start starts the scheduler if it isn't running.
func Start(labels map[string]string) (RunningOutput, error) {
	label := labels["scheduler"]
	running, err := isSchedulerRunning(label)
	if err != nil {
		return RunningOutput{}, err
	}
	if running {
		return RunningOutput{Scheduler: schedulerRunning}, nil
	}
	if err := startScheduler(label); err != nil {
		return RunningOutput{}, err
	}
	return RunningOutput{Scheduler: schedulerRunning, Changed: true}, nil
}

// Stop stops the scheduler, then the frontends running from dir
// (the app's binaries' folder), except the processes spared (this one, and
// the one that ran it). Both are tried; the errors are joined.
func Stop(labels map[string]string, dir string, spare []int) (RunningOutput, error) {
	out := RunningOutput{Scheduler: schedulerStopped}
	var errs []error
	label := labels["scheduler"]
	running, err := isSchedulerRunning(label)
	switch {
	case err != nil:
		errs = append(errs, err)
	case running:
		if err := stopScheduler(label); err != nil {
			out.Scheduler = schedulerRunning
			errs = append(errs, err)
		} else {
			out.Changed = true
		}
	}
	stopped, err := stopFrontends(labels["ui"], dir, spare)
	out.UIStopped = stopped
	if err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// spared is whether pid is one of spare.
func spared(pid int, spare []int) bool {
	for _, p := range spare {
		if p == pid {
			return true
		}
	}
	return false
}

// Summarize is `login-items --json`: whether anything is installed, and
// whether every installed item starts at login.
func Summarize(items []LoginItem) Status {
	out := Status{Enabled: true, Items: items}
	for _, it := range items {
		out.Installed = out.Installed || it.Installed
		if it.Installed && !it.Enabled {
			out.Enabled = false
		}
	}
	out.Enabled = out.Enabled && out.Installed
	return out
}
