//go:build !darwin && !linux

package sysenv

// KeepAwake isn't implemented on this OS yet; runs rely on the computer being awake.
func KeepAwake() (release func()) { return func() {} }

// FullyAwake is whether the computer is awake for work; this OS isn't asked yet.
func FullyAwake() bool { return true }
