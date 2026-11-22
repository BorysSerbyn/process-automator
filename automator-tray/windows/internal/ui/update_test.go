package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// While it installs: its release, step and latest line, read again every
// UpdateEvery; then how it ended.
func TestUpdatingFollowsTheUpdate(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"update progress --json": "@update-installing.json"})
	closed := false
	u := NewUpdate(f.CLI, now, false, func() { closed = true })
	var next []func()
	u.after = func(d time.Duration, fn func()) {
		if d != UpdateEvery {
			t.Errorf("read again after %s", d)
		}
		next = append(next, fn)
	}
	u.now = noon
	u.Load()
	mustShow(t, u.Content(), "Updating to v3.0.0", "Started Today at", "Checking it's genuine", "shasum: OK", "You can close this window")
	if len(next) != 1 {
		t.Fatalf("%d reads scheduled", len(next))
	}
	test.Tap(button(t, u.Content(), "Hide"))
	if !closed {
		t.Error("Hide didn't close it")
	}

	f2 := newFakeCLI(t, map[string]string{"update progress --json": "@update-failed.json"})
	u2 := NewUpdate(f2.CLI, now, false, func() {})
	u2.after = func(time.Duration, func()) { t.Error("read again after it ended") }
	u2.Load()
	mustShow(t, u2.Content(), "The update to v3.0.0 failed", "the checksum doesn't match", "Log: /home/me/.config/acme/update.log")
	button(t, u2.Content(), "Close")
}

// Asking first: what's available and the core's note; Update starts it
// and follows it in the same window.
func TestConfirmingAnUpdate(t *testing.T) {
	f := newFakeCLI(t, map[string]string{
		"update --check --json":  "@update-check.json",
		"update --detach --json": `{"current": "2.9.0", "latest": "v3.0.0", "updateAvailable": true, "inProgress": true, "started": true}`,
		"update progress --json": "@update-installing.json",
	})
	u := NewUpdate(f.CLI, now, true, func() {})
	u.after = func(time.Duration, func()) {}
	u.Load()
	mustShow(t, u.Content(), "Update to v3.0.0?", "This is 2.9.0.", "It downloads about 30 MB.")
	button(t, u.Content(), "Later")
	test.Tap(button(t, u.Content(), "Update"))
	f.mustHaveRun(t, "update --detach --json", "update progress --json")
	mustShow(t, u.Content(), "Checking it's genuine")

	f2 := newFakeCLI(t, map[string]string{"update --check --json": `{"current": "3.0.0", "latest": "v3.0.0", "updateAvailable": false, "inProgress": false}`})
	u2 := NewUpdate(f2.CLI, now, true, func() {})
	u2.Load()
	mustShow(t, u2.Content(), "Nothing to update", "This is 3.0.0, the latest release.")
}
