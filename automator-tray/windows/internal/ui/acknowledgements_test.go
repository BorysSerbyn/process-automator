package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Each notification waiting: its title, text (as is, never markup), what
// sent it and when, its run's report, Acknowledge; Acknowledge All. It's
// read again only when what waits changes, and says why one failed.
func TestTheAcknowledgementsWindow(t *testing.T) {
	f := newFakeCLI(t, map[string]string{
		"notifications list --waiting --json": "@waiting.json",
		"notifications ack --all --json":      "!the notifications file is locked",
	})
	a := NewAcknowledgements(f.CLI, now)
	a.now = noon
	waiting := &cli.RunStatus{Waiting: []cli.Waiting{{ID: "n7"}, {ID: "n8"}}}
	a.Update(waiting)
	a.Update(&cli.RunStatus{Waiting: []cli.Waiting{{ID: "n7"}, {ID: "n8"}}}) // the same: not read again
	if n := f.count("notifications list --waiting --json"); n != 1 {
		t.Fatalf("read %d times", n)
	}
	c := a.Content()
	mustShow(t, c, "Acknowledgements", "Scout found a match", "**3** new listings <b>now</b>", "marco-scout · Today at",
		"Door open", "front-door")
	if len(buttons(c, "Acknowledge")) != 2 || len(buttons(c, "Open Report")) != 1 {
		t.Fatal("not Acknowledge on each, and Open Report on the one with a run")
	}
	test.Tap(buttons(c, "Acknowledge")[0])
	test.Tap(button(t, a.Content(), "Open Report"))
	test.Tap(button(t, a.Content(), "Acknowledge All"))
	f.mustHaveRun(t, "notifications ack n7 --json", "runs show marco-scout/2026-11-21T090000 --open", "notifications ack --all --json")
	mustShow(t, a.Content(), "the notifications file is locked")

	// A long text is folded to its first lines, and opens to all of it.
	mustShow(t, a.Content(), "line 4 of the scout's report…")
	mustNotShow(t, a.Content(), "line 39 of the scout's report")
	test.Tap(button(t, a.Content(), "Show More"))
	mustShow(t, a.Content(), "line 39 of the scout's report")
	test.Tap(button(t, a.Content(), "Show Less"))
	mustNotShow(t, a.Content(), "line 39 of the scout's report")

	empty := NewAcknowledgements(newFakeCLI(t, map[string]string{"notifications list --waiting --json": "[]"}).CLI, now)
	empty.Load()
	mustShow(t, empty.Content(), "All caught up")
}
