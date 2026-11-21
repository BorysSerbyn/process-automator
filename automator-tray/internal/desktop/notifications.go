// Package desktop is the tray's side of the desktop's D-Bus services: the
// notifications service (org.freedesktop.Notifications) and the tray's own
// name on the session bus. The tray icon itself is fyne.io/systray's.
package desktop

import (
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

// The notifications service, as the Desktop Notifications Specification names it.
const (
	notificationsName  = "org.freedesktop.Notifications"
	notificationsPath  = dbus.ObjectPath("/org/freedesktop/Notifications")
	notificationsIface = "org.freedesktop.Notifications"
)

// caller is the part of a D-Bus object Notifications calls (*dbus.Object).
type caller interface {
	Call(method string, flags dbus.Flags, args ...any) *dbus.Call
}

// Notifications posts notes as the app: its name, and its icon (a PNG's path).
type Notifications struct {
	obj     caller
	appName string
	appIcon string

	once    sync.Once
	actions bool
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
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))}
	var id uint32
	err := n.obj.Call(notificationsIface+".Notify", 0,
		n.appName, uint32(0), n.appIcon, note.Summary, note.Body, actions, hints, timeout).Store(&id)
	return id, err
}

// CanAsk is whether the server shows actions (the "actions" capability);
// asked once.
func (n *Notifications) CanAsk() bool {
	n.once.Do(func() {
		var caps []string
		if n.obj.Call(notificationsIface+".GetCapabilities", 0).Store(&caps) == nil {
			for _, c := range caps {
				n.actions = n.actions || c == "actions"
			}
		}
	})
	return n.actions
}
