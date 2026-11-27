//go:build !unix

package engine

// appRunning: processes can't be looked up here; an app counts as running.
var appRunning = func(string) bool { return true }
