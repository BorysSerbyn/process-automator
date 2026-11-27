package ui

import (
	"errors"
	"maps"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// with is replies and more.
func with(replies map[string]string, more map[string]string) map[string]string {
	out := maps.Clone(replies)
	maps.Copy(out, more)
	return out
}

// shownDialog is the dialog a window shows (nil: none).
func shownDialog(w fyne.Window) fyne.CanvasObject {
	return w.Canvas().Overlays().Top()
}

const standupLogs = `{"path":"/home/me/.acme/reports/standup-logs-2026-11-21T120000.zip","name":"standup",
	"runs":["standup/2026-11-21T092001","standup/2026-11-20T092001"],"files":[{"name":"about.txt","bytes":300}]}`

// Export Logs… has the core write the zip, opens its folder (never in a
// test: openURL is a stand-in) and says what's in it.
func TestExportLogs(t *testing.T) {
	r, f, _ := newRoutineFor(t, "standup", with(standupReplies, map[string]string{"routines logs standup --json": standupLogs}))
	var opened []string
	r.bugs.openURL = func(u string) error { opened = append(opened, u); return nil }
	test.Tap(button(t, r.Content(), "Export Logs…"))
	f.mustHaveRun(t, "routines logs standup --json")
	if len(opened) != 1 || opened[0] != "file:///home/me/.acme/reports" {
		t.Fatalf("opened %q", opened)
	}
	d := shownDialog(r.win)
	if d == nil {
		t.Fatal("no dialog")
	}
	mustShow(t, d, "Logs Exported", "now open in your file manager", "standup-logs-2026-11-21T120000.zip",
		"the routine's file, its latest 2 runs", "read it before sharing it")
	test.Tap(button(t, d, "Done"))
	if shownDialog(r.win) != nil {
		t.Fatal("the dialog stayed")
	}

	// Its folder not opening is said in it; the path is still shown.
	r.bugs.openURL = func(string) error { return errors.New("no file manager") }
	test.Tap(button(t, r.Content(), "Export Logs…"))
	mustShow(t, shownDialog(r.win), "Couldn't open its folder: no file manager", "Saved here:", "standup-logs-")
}

// Why export failed shows where the window's other controls' errors do,
// with no dialog.
func TestExportLogsFailing(t *testing.T) {
	h, f, _ := newHookFor(t, "open-meet", with(openMeetReplies, map[string]string{"hooks logs open-meet --json": "!no runs of open-meet, and no hook by that name"}))
	h.bugs.openURL = func(string) error { t.Fatal("opened a folder"); return nil }
	test.Tap(button(t, h.Content(), "Export Logs…"))
	f.mustHaveRun(t, "hooks logs open-meet --json")
	mustShow(t, h.Content(), "no runs of open-meet, and no hook by that name")
	if shownDialog(h.win) != nil {
		t.Fatal("a dialog for a failed export")
	}
}

// reportForm opens Report a Bug… and is its dialog, its entries and check.
func reportForm(t *testing.T, content fyne.CanvasObject, win fyne.Window) (fyne.CanvasObject, []*widget.Entry, *widget.Check) {
	t.Helper()
	test.Tap(button(t, content, "Report a Bug…"))
	d := shownDialog(win)
	if d == nil {
		t.Fatal("no dialog")
	}
	entries := all[*widget.Entry](d)
	checks := all[*widget.Check](d)
	if len(entries) != 2 || len(checks) != 1 {
		t.Fatalf("%d entries, %d checks", len(entries), len(checks))
	}
	return d, entries, checks[0]
}

// Report a Bug…: a title, what happened, Attach logs (off, and saying what
// the logs hold when on); Open on GitHub has the core open the issue; then
// what's left: drag the logs onto it.
func TestReportABug(t *testing.T) {
	report := `{"repo":"acme/auto","url":"https://github.com/acme/auto/issues/new?title=x","title":"Posts twice","body":"b",
		"logs":{"path":"/home/me/.acme/reports/standup-logs-2026-11-21T120000.zip","name":"standup","runs":[],"files":[]},"opened":true}`
	r, f, _ := newRoutineFor(t, "standup", with(standupReplies, map[string]string{
		"routines report standup --title=Posts twice --description=It posted the report twice. --include-logs --json": report}))
	d, entries, attach := reportForm(t, r.Content(), r.win)
	mustShow(t, d, "Report a Bug in standup", "Title", "What happened", "Attach logs",
		"filed from your own GitHub account, and is public if the repository is")
	mustNotShow(t, d, "read them before attaching them")
	if attach.Checked {
		t.Fatal("Attach logs is on by default")
	}
	attach.SetChecked(true)
	mustShow(t, d, "The logs may hold the routine's text and tool results", "read them before attaching them")
	entries[0].SetText("Posts twice")
	entries[1].SetText("It posted the report twice.")
	test.Tap(button(t, d, "Open on GitHub"))
	f.mustHaveRun(t, "routines report standup --title=Posts twice --description=It posted the report twice. --include-logs --json")
	mustShow(t, d, "The issue is open in your browser", "Drag standup-logs-2026-11-21T120000.zip onto the issue before submitting it")
	test.Tap(button(t, d, "Done"))
	if shownDialog(r.win) != nil || r.bugs.report != nil {
		t.Fatal("the dialog stayed")
	}
}

// Without logs, once the issue is open the dialog closes; when the browser
// didn't open, it's a link.
func TestReportABugWithoutLogs(t *testing.T) {
	h, f, _ := newHookFor(t, "open-meet", with(openMeetReplies, map[string]string{
		"hooks report open-meet --title= --description= --json": `{"repo":"acme/auto","url":"https://github.com/acme/auto/issues/new","title":"open-meet: ","body":"b","opened":true}`}))
	d, _, attach := reportForm(t, h.Content(), h.win)
	attach.SetChecked(true)
	mustShow(t, d, "The logs may hold the hook's text")
	attach.SetChecked(false)
	test.Tap(button(t, d, "Open on GitHub"))
	f.mustHaveRun(t, "hooks report open-meet --title= --description= --json")
	if shownDialog(h.win) != nil {
		t.Fatal("the dialog stayed")
	}

	f2 := newFakeCLI(t, with(openMeetReplies, map[string]string{
		"hooks report open-meet --title= --description= --json": `{"repo":"acme/auto","url":"https://github.com/acme/auto/issues/new","title":"open-meet: ","body":"b","opened":false}`}))
	h.cli, h.bugs.cli = f2.CLI, f2.CLI
	d, _, _ = reportForm(t, h.Content(), h.win)
	test.Tap(button(t, d, "Open on GitHub"))
	mustShow(t, d, "Your browser didn't open")
	if links := all[*widget.Hyperlink](d); len(links) != 1 || links[0].URL.String() != "https://github.com/acme/auto/issues/new" {
		t.Fatalf("links %v", links)
	}
}

// Why a report failed shows in its dialog, as the core says it, and the
// form stays to try again.
func TestReportABugFailing(t *testing.T) {
	refused := "Acme takes no bug reports (its build names nowhere to send them): export the logs with `acme-auto routines logs standup` and send them to whoever maintains it"
	r, _, _ := newRoutineFor(t, "standup", with(standupReplies, map[string]string{
		"routines report standup --title=x --description= --json": "!" + refused}))
	d, entries, _ := reportForm(t, r.Content(), r.win)
	entries[0].SetText("x")
	test.Tap(button(t, d, "Open on GitHub"))
	if shownDialog(r.win) == nil {
		t.Fatal("the dialog closed")
	}
	mustShow(t, d, refused)
	if b := button(t, d, "Open on GitHub"); b.Disabled() {
		t.Fatal("Open on GitHub stays disabled")
	}
	test.Tap(button(t, d, "Cancel"))
	if shownDialog(r.win) != nil {
		t.Fatal("Cancel didn't close it")
	}
}
