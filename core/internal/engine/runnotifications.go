package engine

import (
	"errors"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// A run's own notifications: a routine or hook asks for one in its run (a
// plugin through Host.Notify, a program through $PA_NOTIFY), and it's the
// app's, which frontends show: a click opens its link, if it has one, and
// one to acknowledge has an Acknowledge button and is in run status (so
// health is error) until I acknowledge it (Acknowledge). Each change to
// what waits rewrites run status at once, which is what frontends follow.

// Notify records n as the app's notification about the run the session
// serves.
func (h *host) Notify(n pluginapi.Notice) (string, error) {
	run := h.run.Run
	note, err := addRunNotification(h.files, run.Routine, run.Routine != "" && hooks.Exists(h.files.Hooks(), run.Routine), run.ID, n, time.Now())
	return note.ID, err
}

// addRunNotification records n as the app's notification about a run of a
// routine or hook (named; "" when there's none), and puts it in run status
// when it waits.
func addRunNotification(files Layout, name string, hook bool, runID string, n pluginapi.Notice, now time.Time) (notifications.Notification, error) {
	note, err := notifications.Add(files.Notifications(), notifications.Notification{
		Kind: notifications.KindRun, Title: n.Title, Body: n.Body, URL: n.URL, Urgent: n.Urgent, Acknowledge: n.Acknowledge,
		Routine: name, Run: runID, Hook: hook,
	}, now)
	if err != nil || !note.Acknowledge {
		return note, err
	}
	return note, syncWaiting(files, now)
}

// Acknowledge acknowledges the notifications named (all: every one
// waiting), takes them out of run status, and returns those it
// acknowledged (see notifications.Acknowledge).
func (a *App) Acknowledge(ids []string, all bool, now time.Time) ([]notifications.Notification, error) {
	if len(ids) == 0 && !all {
		return nil, errors.New("name the notifications to acknowledge, or all of them")
	}
	done, err := notifications.Acknowledge(a.Layout.Notifications(), ids, all, now)
	if err != nil {
		return nil, err
	}
	return done, syncWaiting(a.Layout, now)
}

// syncWaiting puts the notifications waiting to be acknowledged in run
// status, as the notifications file has them.
func syncWaiting(files Layout, now time.Time) error {
	return runstatus.SetWaiting(files.RunStatus(), func() ([]runstatus.Waiting, error) {
		f, err := notifications.Load(files.Notifications())
		if err != nil {
			return nil, err
		}
		var out []runstatus.Waiting
		for _, n := range f.Waiting() {
			out = append(out, runstatus.Waiting{ID: n.ID, Title: n.Title})
		}
		return out, nil
	}, now)
}
