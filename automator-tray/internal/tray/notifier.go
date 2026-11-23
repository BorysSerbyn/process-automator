package tray

import (
	"sync"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Notifier delivers the app's own notifications (runs that failed,
// updates), as automator-ui's Notifier does: it asks the core for the ones
// no frontend delivered yet, shows them, and tells the core which were
// delivered. A click asks the core what to show (`notifications open`).
type Notifier struct {
	cli  cli.CLI
	desk *Desk
	// show shows what a click on one should: its run, or the app.
	show func(cli.ClickOutput)
	// alert says a click couldn't be followed.
	alert func(title, text string)

	mu sync.Mutex
	// posted are the notifications already shown, until the core no longer
	// lists them as pending (they were marked delivered).
	posted map[string]bool
	// asking is whether the core is being asked for pending ones (one ask
	// at a time).
	asking bool
}

// NewNotifier delivers c's notifications on desk.
func NewNotifier(c cli.CLI, desk *Desk, show func(cli.ClickOutput), alert func(title, text string)) *Notifier {
	return &Notifier{cli: c, desk: desk, show: show, alert: alert, posted: map[string]bool{}}
}

// Deliver shows the notifications the core says are pending, in the
// background; while it's asking, another Deliver does nothing.
func (n *Notifier) Deliver() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.asking {
		return
	}
	n.asking = true
	go n.deliver()
}

// deliver asks for the pending notifications, shows the ones not shown yet
// and marks those shown delivered. Those the desktop didn't take stay pending.
func (n *Notifier) deliver() {
	pending, err := n.cli.PendingNotifications()
	n.mu.Lock()
	n.asking = false
	var fresh []cli.Notification
	if err == nil {
		// Those no longer pending were marked delivered: forget them, so
		// posted holds only what's still pending.
		still := map[string]bool{}
		for _, note := range pending {
			still[note.ID] = true
		}
		for id := range n.posted {
			if !still[id] {
				delete(n.posted, id)
			}
		}
	}
	for _, note := range pending {
		if err == nil && !n.posted[note.ID] {
			n.posted[note.ID] = true // claimed: the next delivery won't post it again
			fresh = append(fresh, note)
		}
	}
	n.mu.Unlock()
	var delivered []string
	for _, note := range fresh {
		id := note.ID
		posted := Note{Summary: note.Title, Body: note.Body, Actions: []string{"default", "Open"}, Urgent: note.Urgent}
		if note.Waiting() {
			// One waiting for me has Acknowledge (where the desktop shows
			// buttons; elsewhere the menu's acknowledgement line), and stays.
			posted.Actions = append(posted.Actions, "acknowledge", "Acknowledge")
			posted.Persistent = true
		}
		err := n.desk.Post(posted, func(action string) {
			switch action {
			case "default":
				n.open(id)
			case "acknowledge":
				if err := n.cli.Acknowledge(id); err != nil {
					n.alert("Couldn't acknowledge it", err.Error())
				}
			}
		})
		if err != nil {
			n.mu.Lock()
			delete(n.posted, id)
			n.mu.Unlock()
			continue
		}
		delivered = append(delivered, id)
	}
	if len(delivered) > 0 {
		_ = n.cli.MarkDelivered(delivered)
	}
}

// open follows a click on a notification: the core says what to show.
func (n *Notifier) open(id string) {
	click, err := n.cli.OpenNotification(id)
	if err != nil {
		n.alert("Couldn't open the notification", err.Error())
		return
	}
	n.show(click)
}
