package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// checks are the screen's boxes, row for row.
func checks(u *TemplateUpdates) []*widget.Check { return all[*widget.Check](u.Content()) }

// A row each, every box unticked: its name and kind, versions, what
// changed, edits it replaces, the watcher with it, why one can't be
// overridden (its box disabled). Override is disabled until one is ticked.
func TestTemplateUpdatesStartUnticked(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"templates outdated --json": "@template-updates.json"})
	u := NewTemplateUpdates(f.CLI, now, func() {})
	u.Load()
	w := window(t)
	w.SetContent(u.Content())
	mustShow(t, u.Content(), "Template Updates", "standup", "routine", "1.2 → 1.3", "Posts the summary in a thread",
		"overriding it replaces your edits", "Installed before versions were recorded", "no version → 1.1",
		"Also updates its watcher calendar.", "Can't be overridden: the new version needs the parameter dest")
	boxes := checks(u)
	if len(boxes) != 5 {
		t.Fatalf("%d boxes", len(boxes))
	}
	for i, b := range boxes {
		if b.Checked {
			t.Errorf("box %d ticked", i)
		}
		if b.Disabled() != (i == 4) {
			t.Errorf("box %d disabled: %v", i, b.Disabled())
		}
	}
	if !button(t, u.Content(), "Override").Disabled() {
		t.Error("Override can be pressed with nothing ticked")
	}
	test.Tap(checks(u)[0])
	if button(t, u.Content(), "Override").Disabled() {
		t.Error("Override can't be pressed with one ticked")
	}
}

// Select all ticks every row but the blocked one.
func TestTemplateUpdatesSelectAllSkipsBlocked(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"templates outdated --json": "@template-updates.json"})
	u := NewTemplateUpdates(f.CLI, now, func() {})
	u.Load()
	test.Tap(button(t, u.Content(), "Select all"))
	for i, b := range checks(u) {
		if b.Checked != (i != 4) {
			t.Errorf("box %d ticked: %v", i, b.Checked)
		}
	}
	if got := u.Ticked(); len(got) != 4 || got[3] != "calendar" {
		t.Errorf("ticked %q", got)
	}
	if !button(t, u.Content(), "Select all").Disabled() {
		t.Error("Select all can be pressed with all ticked")
	}
}

// Override replaces the ticked ones, then leaves the others that can be as
// they are (not the blocked one, nor the watcher overridden with its hook),
// and says what it replaced and whether each stayed trusted.
func TestTemplateUpdatesOverrideTheTickedSkipTheRest(t *testing.T) {
	f := newFakeCLI(t, map[string]string{
		"templates outdated --json":                   "@template-updates.json",
		"templates override standup open-meet --json": `{"overridden": [{"name": "calendar", "kind": "watcher", "version": "1.1", "trusted": true}, {"name": "standup", "kind": "routine", "version": "1.3", "trusted": true}, {"name": "open-meet", "kind": "hook", "version": "1.1", "trusted": false}]}`,
		"templates skip digest --json":                `{"skipped": ["digest"]}`,
	})
	closed := 0
	u := NewTemplateUpdates(f.CLI, now, func() { closed++ })
	u.Load()
	test.Tap(checks(u)[0]) // standup
	test.Tap(checks(u)[2]) // open-meet, with calendar
	test.Tap(button(t, u.Content(), "Override"))
	f.mustHaveRun(t, "templates override standup open-meet --json", "templates skip digest --json")
	mustShow(t, u.Content(), "Overridden with version 1.3, still trusted.", "Overridden with version 1.1, not trusted")
	if n := f.count("templates skip digest --json"); n != 1 {
		t.Errorf("skipped %d times", n)
	}
	test.Tap(button(t, u.Content(), "Done"))
	u.Closed() // chosen: nothing more to skip
	if closed != 1 || len(f.ran()) != 3 {
		t.Errorf("closed %d; ran %q", closed, f.ran())
	}
}

// Override failing says why and shows what's left, skipping nothing.
func TestTemplateUpdatesOverrideFailing(t *testing.T) {
	f := newFakeCLI(t, map[string]string{
		"templates outdated --json":        "@template-updates.json",
		"templates override digest --json": "!building digest failed: go not found",
	})
	u := NewTemplateUpdates(f.CLI, now, func() {})
	u.Load()
	test.Tap(checks(u)[1])
	test.Tap(button(t, u.Content(), "Override"))
	mustShow(t, u.Content(), "Couldn't override them", "building digest failed: go not found", "standup")
	if f.count("templates outdated --json") != 2 {
		t.Error("what's left wasn't read again")
	}
	for _, r := range f.ran() {
		if len(r) >= 14 && r[:14] == "templates skip" {
			t.Errorf("skipped after a failure: %q", r)
		}
	}
	if !checks(u)[1].Checked {
		t.Error("the tick was lost")
	}
}

// Not now leaves every one listed as it is (the blocked one too), then
// closes the screen; so does closing the window without choosing.
func TestTemplateUpdatesNotNowSkipsAll(t *testing.T) {
	skipAll := "templates skip standup digest open-meet calendar backup --json"
	f := newFakeCLI(t, map[string]string{"templates outdated --json": "@template-updates.json", skipAll: `{"skipped": []}`})
	closed := false
	u := NewTemplateUpdates(f.CLI, now, func() { closed = true })
	u.Load()
	test.Tap(checks(u)[0]) // ticked, but Not now leaves it too
	test.Tap(button(t, u.Content(), "Not now"))
	f.mustHaveRun(t, skipAll)
	if !closed {
		t.Error("the screen didn't close")
	}

	f = newFakeCLI(t, map[string]string{"templates outdated --json": "@template-updates.json", skipAll: `{"skipped": []}`})
	u = NewTemplateUpdates(f.CLI, now, func() {})
	u.Load()
	u.Closed()
	f.mustHaveRun(t, skipAll)

	// Not now failing stays, saying why.
	f = newFakeCLI(t, map[string]string{"templates outdated --json": "@template-updates.json", skipAll: "!the installs file is locked"})
	closed = false
	u = NewTemplateUpdates(f.CLI, now, func() { closed = true })
	u.Load()
	test.Tap(button(t, u.Content(), "Not now"))
	mustShow(t, u.Content(), "Couldn't leave them as they are", "the installs file is locked")
	if closed {
		t.Error("closed after a failure")
	}
}

// While Override runs, every control is disabled and it says it can take
// minutes.
func TestTemplateUpdatesWhileOverriding(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"templates outdated --json": "@template-updates.json"})
	var pending []func() func()
	u := NewTemplateUpdates(f.CLI, now, func() {})
	u.Load()
	u.async = func(work func() func()) { pending = append(pending, work) }
	test.Tap(checks(u)[0])
	test.Tap(button(t, u.Content(), "Override"))
	mustShow(t, u.Content(), "Overriding… This can take a few minutes")
	for _, name := range []string{"Select all", "Not now", "Override"} {
		if !button(t, u.Content(), name).Disabled() {
			t.Errorf("%s can be pressed while it overrides", name)
		}
	}
	for i, b := range checks(u) {
		if !b.Disabled() {
			t.Errorf("box %d can be ticked while it overrides", i)
		}
	}
	u.Closed() // the window closing meanwhile skips nothing
	if len(pending) != 1 || f.count("templates outdated --json") != 1 || len(f.ran()) != 1 {
		t.Errorf("ran %q", f.ran())
	}
}
