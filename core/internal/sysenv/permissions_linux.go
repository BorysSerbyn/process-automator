package sysenv

import (
	"path/filepath"
	"syscall"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// inputDevices are the devices reading the keyboard and mouse needs (a
// variable for tests).
var inputDevices = "/dev/input/event*"

// CheckPermission checks what Linux has of a permission: input-monitoring
// is read access to the input devices; the rest isn't checked on Linux,
// never taken for missing.
func CheckPermission(name string) permissions.Status {
	if name != "input-monitoring" {
		return permissions.Unsupported
	}
	devs, _ := filepath.Glob(inputDevices)
	if len(devs) == 0 {
		return permissions.Unknown // no input devices here (a server, a container)
	}
	const readable = 4 // R_OK
	for _, d := range devs {
		if syscall.Access(d, readable) == nil {
			return permissions.Granted
		}
	}
	return permissions.Missing
}

// CanRequestPermission: there is nothing to ask on this OS.
func CanRequestPermission(string) (ok bool, why string) {
	return false, "Linux has no prompt for it"
}

// RequestPermission: there is nothing to ask on this OS.
func RequestPermission(string) {}

// PermissionPane is how a permission is given on Linux, in words that
// follow "turn it on"; there's no address to open.
func PermissionPane(name string) (where, url string) {
	if name == "input-monitoring" {
		return "by giving it read access to /dev/input/event* (usually: add me to the input group, then log in again)", ""
	}
	return "", ""
}
