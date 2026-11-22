//go:build !darwin && !linux

package sysenv

import "github.com/BorysSerbyn/process-automator/core/internal/permissions"

// CheckPermission: permissions aren't checked on this OS.
func CheckPermission(string) permissions.Status { return permissions.Unsupported }

// CanRequestPermission: there is nothing to ask on this OS.
func CanRequestPermission(string) (ok bool, why string) {
	return false, "permissions aren't checked on this OS"
}

// RequestPermission: there is nothing to ask on this OS.
func RequestPermission(string) {}

// PermissionPane: there's none on this OS.
func PermissionPane(string) (where, url string) { return "", "" }
