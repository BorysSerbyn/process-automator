package sysenv

import "github.com/BorysSerbyn/process-automator/core/internal/permissions"

// Where each permission is turned on: System Settings' privacy panes, opened
// by x-apple.systempreferences: addresses.
const privacyPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_"

var privacyPanes = map[string]string{
	"input-monitoring": "ListenEvent",
	"accessibility":    "Accessibility",
	"screen-recording": "ScreenCapture",
	"full-disk-access": "AllFiles",
	"calendars":        "Calendars",
	"contacts":         "Contacts",
	"reminders":        "Reminders",
	"microphone":       "Microphone",
	"camera":           "Camera",
	"automation":       "Automation",
}

// PermissionPane is where a permission is turned on for the app, in words
// that follow "turn it on", and the address that opens it ("" when there's
// none).
func PermissionPane(name string) (where, url string) {
	if name == "notifications" {
		return "in System Settings > Notifications", "x-apple.systempreferences:com.apple.Notifications-Settings.extension"
	}
	if pane, ok := privacyPanes[name]; ok {
		return "in System Settings > Privacy & Security > " + permissions.Label(name), privacyPane + pane
	}
	return "", ""
}
