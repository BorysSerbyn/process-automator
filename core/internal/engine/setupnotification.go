package engine

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// setupNotifier tells me, with a notification whose click shows setup, when
// setup has a step left that it hasn't told me about since the daemon
// started: so once when the daemon starts (at login) with setup unfinished,
// and again whenever a step is added (a new routine to turn on, a sign-in it
// needs), but not on every pass while the same steps are left. A step done
// and later back again counts as new.
type setupNotifier struct {
	told []string
}

func (n *setupNotifier) record(a *App, left []string, now time.Time) {
	news := slices.ContainsFunc(left, func(step string) bool { return !slices.Contains(n.told, step) })
	n.told = slices.Clone(left)
	if !news {
		return
	}
	_, err := notifications.Add(a.Layout.Notifications(), setupNotification(left), now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record a notification:", err)
	}
}

// setupNotification is the notification for setup with these steps left.
func setupNotification(left []string) notifications.Notification {
	return notifications.Notification{
		Kind: notifications.KindApp, Setup: true,
		Title: "Finish setting up " + product.Name,
		Body:  "Still to do: " + strings.Join(left, ", "),
	}
}
