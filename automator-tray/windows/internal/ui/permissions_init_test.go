package ui

// The tests' Async runs at once: a poll or a timeout that sleeps would hold
// them up.
func init() {
	PermissionPollEvery = 0
	PendingTimeout = 0
}

// release lets a pending control go, as its timeout would.
func release(p *pendingAction) {
	p.end()
	p.redraw()
}
