package ui

// The tests' Async runs at once: a poll that sleeps would hold them up.
func init() { PermissionPollEvery = 0 }
