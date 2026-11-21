package engine

import (
	"fmt"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/notify"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// notifyFailure is the app's own notification for a run that failed, needs a
// sign-in, or was skipped or missed (unless runs.failure-notifications is
// off). It's the app's, not the notify plugin's: no routine's notification
// settings apply to it, and clicking it shows the run.
func notifyFailure(a *App, routine string, run runstatus.Run) {
	var title string
	switch run.Outcome {
	case runstatus.OutcomeFailed:
		title = routine + " failed"
	case runstatus.OutcomeNeedsReauth:
		title = routine + " needs you to sign in again"
	case runstatus.OutcomeSkipped:
		title = routine + " was skipped"
	case runstatus.OutcomeMissed:
		title = routine + " missed its run"
		if run.For != nil {
			title = fmt.Sprintf("%s missed its %s run", routine, run.For.Local().Format("15:04"))
		}
	default:
		return
	}
	if vals, err := a.Settings(); err == nil && !vals.Bool("runs.failure-notifications") {
		return
	}
	body := run.Reason
	if r := []rune(body); len(r) > 240 {
		body = string(r[:240]) + "…"
	}
	_, err := notify.Add(a.Layout.Notifications(), notify.Notification{
		Kind: notify.KindFailure, Title: title, Body: body,
		Routine: routine, Run: history.RunID(routine, run.StartedAt),
	}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record a notification:", err)
	}
}
