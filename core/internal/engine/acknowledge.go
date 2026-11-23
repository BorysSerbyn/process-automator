package engine

import (
	"errors"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Notifications waiting to be acknowledged: a plugin asks for one in a run
// (AskToAcknowledge); it's the app's own notification, which frontends show
// with an Acknowledge button, and run status lists it (so health is error)
// until I acknowledge it (Acknowledge). Each change rewrites run status at
// once, which is what frontends follow.

// AskToAcknowledge records n as a notification waiting to be acknowledged,
// about the run the session serves, and puts it in run status.
func (h *host) AskToAcknowledge(n pluginapi.Notice) (string, error) {
	if strings.TrimSpace(n.Title) == "" {
		return "", errors.New("a notification needs a title")
	}
	run, now := h.run.Run, time.Now()
	note, err := notifications.Add(h.files.Notifications(), notifications.Notification{
		Kind: notifications.KindAcknowledge, Title: n.Title, Body: n.Body, Urgent: n.Urgent,
		Routine: run.Routine, Run: run.ID, Hook: run.Routine != "" && hooks.Exists(h.files.Hooks(), run.Routine),
	}, now)
	if err != nil {
		return "", err
	}
	return note.ID, syncWaiting(h.files, now)
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
