package desktop

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Session is the tray's own connection to the session bus. It's apart from
// fyne.io/systray's (the shared one): systray's handler for the
// StatusNotifierWatcher's signals stops at a signal with fewer than three
// arguments, and the notifications service's have two.
type Session struct {
	Conn *dbus.Conn
}

// Connect connects to the session bus; err: there's no desktop session.
func Connect() (*Session, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("no D-Bus session bus (the tray needs a desktop session): %w", err)
	}
	return &Session{Conn: conn}, nil
}

var busNamePart = regexp.MustCompile(`^[A-Za-z_-][A-Za-z0-9_-]*$`)

// BusName is the tray's name on the session bus, from the app's bundle ID
// ("" when the bundle ID makes no bus name).
func BusName(bundleID string) string {
	name := bundleID + ".Tray"
	for _, part := range strings.Split(name, ".") {
		if !busNamePart.MatchString(part) {
			return ""
		}
	}
	return name
}

// Claim makes this the app's one tray: it takes the app's bus name from a
// tray already running, which then quits (Listen's onLost), so the newest
// one started is the one left.
func (s *Session) Claim(name string) error {
	if name == "" {
		return nil
	}
	reply, err := s.Conn.RequestName(name, dbus.NameFlagAllowReplacement|dbus.NameFlagReplaceExisting|dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner && reply != dbus.RequestNameReplyAlreadyOwner {
		return fmt.Errorf("%s is taken by a tray that won't let go", name)
	}
	return nil
}

// Events are what the desktop tells the tray.
type Events struct {
	// Invoked: a notification's action, "default" for a click on it.
	Invoked func(id uint32, action string)
	// Closed: a notification is gone.
	Closed func(id uint32)
	// Lost: the tray's bus name went to another tray.
	Lost func()
	// Gone: the session bus went away (the desktop session ended).
	Gone func()
}

// Listen hands the notifications service's signals, and losing the bus
// name, to ev until the connection closes, which is Gone.
func (s *Session) Listen(ev Events) error {
	for _, member := range []string{"ActionInvoked", "NotificationClosed"} {
		if err := s.Conn.AddMatchSignal(
			dbus.WithMatchObjectPath(notificationsPath),
			dbus.WithMatchInterface(notificationsIface),
			dbus.WithMatchMember(member),
		); err != nil {
			return err
		}
	}
	ch := make(chan *dbus.Signal, 16)
	s.Conn.Signal(ch)
	go Dispatch(ch, ev)
	return nil
}

// Dispatch hands each signal from ch to ev, until ch closes (the
// connection did: Gone).
func Dispatch(ch <-chan *dbus.Signal, ev Events) {
	defer func() {
		if ev.Gone != nil {
			ev.Gone()
		}
	}()
	for sig := range ch {
		switch sig.Name {
		case notificationsIface + ".ActionInvoked":
			if len(sig.Body) == 2 {
				id, ok1 := sig.Body[0].(uint32)
				action, ok2 := sig.Body[1].(string)
				if ok1 && ok2 && ev.Invoked != nil {
					ev.Invoked(id, action)
				}
			}
		case notificationsIface + ".NotificationClosed":
			if len(sig.Body) >= 1 {
				if id, ok := sig.Body[0].(uint32); ok && ev.Closed != nil {
					ev.Closed(id)
				}
			}
		case "org.freedesktop.DBus.NameLost":
			if ev.Lost != nil {
				ev.Lost()
			}
		}
	}
}
