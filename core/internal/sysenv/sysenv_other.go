//go:build !unix

package sysenv

// loginShellPath isn't needed here: services get the user's PATH.
func loginShellPath() (string, error) { return "", nil }
