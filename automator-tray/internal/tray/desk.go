package tray

import "sync"

// Note is a desktop notification: the app's name and icon are the
// desktop's (Notifications).
type Note struct {
	Summary string
	Body    string
	// Actions are pairs of an action's key and its label; "default" is a
	// click on the notification itself.
	Actions []string
	// Persistent notes stay until dismissed or acted on.
	Persistent bool
}

// Notifications is the desktop's notifications service
// (org.freedesktop.Notifications).
type Notifications interface {
	// Notify shows a note and returns its ID.
	Notify(Note) (uint32, error)
	// CanAsk is whether notes can have buttons (the "actions" capability).
	CanAsk() bool
}

// Desk posts notes and hands what's done with them (an action invoked) to
// the one that posted them.
type Desk struct {
	notifications Notifications
	mu            sync.Mutex
	handlers      map[uint32]func(action string)
}

// NewDesk posts through n.
func NewDesk(n Notifications) *Desk {
	return &Desk{notifications: n, handlers: map[uint32]func(string){}}
}

// Post shows a note; onAction (if any) gets each of its actions invoked.
func (d *Desk) Post(n Note, onAction func(action string)) error {
	d.mu.Lock()
	defer d.mu.Unlock() // held across Notify, so an action can't come before its handler
	id, err := d.notifications.Notify(n)
	if err != nil {
		return err
	}
	if onAction != nil {
		d.handlers[id] = onAction
	}
	return nil
}

// CanAsk is whether notes can have buttons.
func (d *Desk) CanAsk() bool { return d.notifications.CanAsk() }

// Invoked is an action invoked on note id (the desktop's ActionInvoked).
// Its handler runs on a goroutine of its own (it runs the CLI).
func (d *Desk) Invoked(id uint32, action string) {
	d.mu.Lock()
	h := d.handlers[id]
	d.mu.Unlock()
	if h != nil {
		go h(action)
	}
}

// Closed is note id gone (the desktop's NotificationClosed): nothing more
// comes of it.
func (d *Desk) Closed(id uint32) {
	d.mu.Lock()
	delete(d.handlers, id)
	d.mu.Unlock()
}
