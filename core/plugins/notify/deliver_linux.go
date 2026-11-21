package notify

import (
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// deliver shows n through the freedesktop notification service on the
// session bus (what notify-send uses), as the app.
func deliver(n Note) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("desktop notifications need a session bus: %w", err)
	}
	defer func() { _ = conn.Close() }()
	urgency := byte(1) // normal
	if n.Urgent {
		urgency = 2 // critical: stays until dismissed
	}
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(urgency)}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		product.Name, uint32(0), "", n.Title, n.Body, []string{}, hints, int32(-1))
	if call.Err != nil {
		return fmt.Errorf("the desktop's notification service: %w", call.Err)
	}
	return nil
}
