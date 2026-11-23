// Package desktop is the tray's side of the desktop's D-Bus services: the
// notifications service (org.freedesktop.Notifications) and the tray's own
// name on the session bus. The tray icon itself is fyne.io/systray's.
package desktop

import (
	"context"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

// The notifications service, as the Desktop Notifications Specification names it.
const (
	notificationsName  = "org.freedesktop.Notifications"
	notificationsPath  = dbus.ObjectPath("/org/freedesktop/Notifications")
	notificationsIface = "org.freedesktop.Notifications"
)

// callTimeout bounds each call to the notifications service: one that's
// hung, or that D-Bus is starting and that never comes up (the bus allows
// that minutes), mustn't hold up the tray (Desk holds its lock across Notify).
const callTimeout = 10 * time.Second

// caller is the part of a D-Bus object Notifications calls (*dbus.Object).
type caller interface {
	CallWithContext(ctx context.Context, method string, flags dbus.Flags, args ...any) *dbus.Call
}

func (n *Notifications) call(method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return n.obj.CallWithContext(ctx, notificationsIface+"."+method, 0, args...)
}

// Notifications posts notes as the app: its name, and its icon (a PNG's path).
type Notifications struct {
	obj     caller
	appName string
	appIcon string

	mu sync.Mutex
	// actions is the server's answer about actions, once it gave one.
	actions *bool
}

// NewNotifications posts on conn's notifications service.
func NewNotifications(conn *dbus.Conn, appName, appIcon string) *Notifications {
	return &Notifications{obj: conn.Object(notificationsName, notificationsPath), appName: appName, appIcon: appIcon}
}

// Notify shows a note (Notify) and returns its ID.
func (n *Notifications) Notify(note tray.Note) (uint32, error) {
	timeout := int32(-1) // the server's default
	if note.Persistent {
		timeout = 0 // until dismissed
	}
	actions := note.Actions
	if actions == nil {
		actions = []string{}
	}
	urgency := byte(1)
	if note.Urgent {
		urgency = 2 // critical
	}
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(urgency)}
	var id uint32
	err := n.call("Notify", n.appName, uint32(0), n.appIcon, note.Summary, note.Body, actions, hints, timeout).Store(&id)
	return id, err
}

// CanAsk is whether the server shows actions (the "actions" capability):
// asked until it answers (it may not be up yet), then remembered.
func (n *Notifications) CanAsk() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.actions == nil {
		var caps []string
		if n.call("GetCapabilities").Store(&caps) != nil {
			return false
		}
		actions := false
		for _, c := range caps {
			actions = actions || c == "actions"
		}
		n.actions = &actions
	}
	return *n.actions
}
