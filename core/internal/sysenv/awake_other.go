//go:build !darwin

package sysenv

// KeepAwake isn't implemented on this OS yet; runs rely on the computer being awake.
func KeepAwake() (release func()) { return func() {} }
