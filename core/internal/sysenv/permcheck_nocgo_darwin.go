//go:build darwin && !cgo

package sysenv

import "github.com/BorysSerbyn/process-automator/core/internal/permissions"

// CheckPermission can't ask macOS without cgo: every permission is unknown,
// never taken for missing.
func CheckPermission(name string) permissions.Status {
	if permissions.Known(name) {
		return permissions.Unknown
	}
	return permissions.Unsupported
}

// CanRequestPermission: macOS can't be asked without cgo.
func CanRequestPermission(string) (ok bool, why string) {
	return false, "this build can't ask macOS (it was built without cgo)"
}

// RequestPermission: there's nothing it can ask.
func RequestPermission(string) {}
