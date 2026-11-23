package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// The window: automatic updates on, with why the newer release waits; the
// installed version with its notes (links clickable); Check for Updates
// shows the newer release with its notes and Install Now, which starts it
// and follows it until it's over.
func TestManageUpdates(t *testing.T) {
	AppName = "Acme"
	t.Cleanup(func() { AppName = "" })
	f := newFakeCLI(t, map[string]string{
		"update status --json":                  "@update-status.json",
		"update notes --json":                   "@update-notes.json",
		"update progress --json":                `{"state": "none", "log": "/x/update.log"}`,
		"update --check --json":                 "@update-check.json",
		"update notes v3.0.0 --json":            `{"tag": "v3.0.0", "notes": "- automatic updates", "url": "https://github.com/acme/app/releases/tag/v3.0.0"}`,
		"settings set update.auto false --json": `{"key": "update.auto", "value": false}`,
	})
	u := NewUpdates(f.CLI, now)
	var next []func()
	u.after = func(d time.Duration, fn func()) {
		if d != ProgressEvery {
			t.Errorf("read again after %s", d)
		}
		next = append(next, fn)
	}
	u.now = noon
	u.Load()
	w := window(t)
	w.SetContent(u.Content())
	mustShow(t, u.Content(), "Install updates automatically", "v3.0.0 installs automatically after today 13:00", "Version 2.9.0",
		"What's new", "Faster", "v3.0.0 is available", "Last checked")
	if len(all[*widget.Hyperlink](u.Content())) == 0 {
		t.Error("no View on GitHub link")
	}
	toggle := all[*widget.Check](u.Content())
	if len(toggle) != 1 || !toggle[0].Checked {
		t.Fatalf("the toggle: %+v", toggle)
	}
	toggle[0].SetChecked(false)
	f.mustHaveRun(t, "settings set update.auto false --json")

	test.Tap(button(t, u.Content(), "Check for Updates"))
	f.mustHaveRun(t, "update --check --json", "update notes v3.0.0 --json")
	mustShow(t, u.Content(), "An update is available", "Version 3.0.0", "automatic updates", "It downloads about 30 MB.")

	// From here the core says it's installing.
	f2 := newFakeCLI(t, map[string]string{
		"update --detach --json": `{"current": "2.9.0", "latest": "v3.0.0", "updateAvailable": true, "inProgress": true, "started": true}`,
		"update progress --json": "@update-installing.json",
		"update status --json":   `{"current": "2.9.0", "development": false, "automatic": true, "setting": "update.auto", "latest": "v3.0.0", "updateAvailable": true, "installing": true, "installingTag": "v3.0.0"}`,
	})
	u.cli = f2.CLI
	test.Tap(button(t, u.Content(), "Install Now"))
	f2.mustHaveRun(t, "update --detach --json")
	mustShow(t, u.Content(), "Updating to v3.0.0", "Checking it's genuine", "shasum: OK", "You can close this window")
	if b := button(t, u.Content(), "Check for Updates"); !b.Disabled() {
		t.Error("Check for Updates can be pressed while it installs")
	}
	if len(next) != 1 {
		t.Fatalf("%d reads scheduled", len(next))
	}
	u.cli = newFakeCLI(t, map[string]string{"update progress --json": "@update-failed.json"}).CLI
	next[0]()
	mustShow(t, u.Content(), "The update to v3.0.0 failed", "the checksum doesn't match", "Log: /home/me/.config/acme/update.log")
}

// Up to date, and checking failing, each said plainly; a development build
// says it has no notes.
func TestManageUpdatesUpToDate(t *testing.T) {
	f := newFakeCLI(t, map[string]string{
		"update status --json":   `{"current": "3.0.0", "development": false, "automatic": false, "setting": "update.auto", "updateAvailable": false, "installing": false}`,
		"update notes --json":    `!this is a development build (3.0.1-dev+abc): it has no release notes`,
		"update progress --json": `{"state": "none", "log": "/x/update.log"}`,
		"update --check --json":  `{"current": "3.0.0", "latest": "v3.0.0", "updateAvailable": false, "inProgress": false}`,
	})
	u := NewUpdates(f.CLI, now)
	u.Load()
	mustShow(t, u.Content(), "no release notes", "Check for a newer release")
	if toggle := all[*widget.Check](u.Content()); len(toggle) != 1 || toggle[0].Checked {
		t.Fatalf("the toggle: %+v", toggle)
	}
	test.Tap(button(t, u.Content(), "Check for Updates"))
	mustShow(t, u.Content(), "is up to date", "3.0.0 is the latest release.")
	mustNotShow(t, u.Content(), "Install Now")

	u.cli = newFakeCLI(t, map[string]string{"update --check --json": "!can't reach GitHub"}).CLI
	test.Tap(button(t, u.Content(), "Check for Updates"))
	mustShow(t, u.Content(), "Couldn't check for updates", "can't reach GitHub")
}
