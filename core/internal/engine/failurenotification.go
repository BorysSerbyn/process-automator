package engine

import (
	"fmt"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// addFailureNotification records a notification for a run that failed,
// needs a sign-in, or was skipped or missed (unless
// runs.failure-notifications is off); clicking it shows the run.
func addFailureNotification(a *App, routine string, run runstatus.Run) {
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
	_, err := notifications.Add(a.Layout.Notifications(), notifications.Notification{
		Kind: notifications.KindFailure, Title: title, Body: body,
		Routine: routine, Run: history.RunID(routine, run.StartedAt),
	}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record a notification:", err)
	}
}
