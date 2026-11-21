package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// notifyTimeout bounds asking the notification service: one that's hung, or
// being started by D-Bus and never coming up (the bus gives that minutes).
const notifyTimeout = 15 * time.Second

// deliver shows n through the freedesktop notification service on the
// session bus (what notify-send uses), as the app.
func deliver(n Note) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("desktop notifications need a session bus (a desktop session's): %w", err)
	}
	defer func() { _ = conn.Close() }()
	urgency := byte(1) // normal
	if n.Urgent {
		urgency = 2 // critical: stays until dismissed
	}
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(urgency)}
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
		n.App, uint32(0), "", n.Title, n.Body, []string{}, hints, int32(-1))
	if call.Err != nil {
		var de dbus.Error
		if errors.As(call.Err, &de) && de.Name == "org.freedesktop.DBus.Error.ServiceUnknown" {
			return errors.New("no desktop notification service is running (a desktop session runs one)")
		}
		return fmt.Errorf("the desktop's notification service: %w", call.Err)
	}
	return nil
}
