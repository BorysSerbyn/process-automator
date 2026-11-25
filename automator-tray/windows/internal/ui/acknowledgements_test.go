package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Each notification waiting: its title, text (as is, never markup), what
// sent it and when, its run's report, Acknowledge; Acknowledge All. It's
// read again only when what waits changes, and says why one failed.
func TestTheAcknowledgementsWindow(t *testing.T) {
	f := newFakeCLI(t, map[string]string{
		"notifications list --waiting --json":      "@waiting.json",
		"notifications list --acknowledged --json": "[]",
		"notifications ack --all --json":           "!the notifications file is locked",
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
	mustNotShow(t, empty.Content(), "Past acknowledgements") // none yet: not shown
}

// Under what waits, those I've acknowledged, the last acknowledged first:
// each says when, with Open Report when it has a run, never Acknowledge;
// the most recent few, Show More for the older ones.
func TestPastAcknowledgements(t *testing.T) {
	f := newFakeCLI(t, map[string]string{
		"notifications list --waiting --json":      "[]",
		"notifications list --acknowledged --json": "@acknowledged.json",
	})
	a := NewAcknowledgements(f.CLI, now)
	a.now = noon
	a.Update(&cli.RunStatus{})
	f.mustHaveRun(t, "notifications list --waiting --json", "notifications list --acknowledged --json")
	c := a.Content()
	mustShow(t, c, "All caught up", "Past acknowledgements", "Backup finished with warnings",
		"2 files were skipped", "backup · Acknowledged today at", "Weekly check 3")
	mustNotShow(t, c, "Weekly check 2")
	if len(buttons(c, "Acknowledge")) != 0 || len(buttons(c, "Acknowledge All")) != 0 {
		t.Fatal("Acknowledge on one already acknowledged")
	}
	order := strings.Join(texts(c), "\n")
	if strings.Index(order, "Backup finished") > strings.Index(order, "Weekly check 6") ||
		strings.Index(order, "Weekly check 6") > strings.Index(order, "Weekly check 3") {
		t.Fatalf("not the last acknowledged first:\n%s", order)
	}
	test.Tap(button(t, c, "Open Report"))
	f.mustHaveRun(t, "runs show backup/2026-11-21T070000 --open")
	test.Tap(button(t, a.Content(), "Show 2 More"))
	mustShow(t, a.Content(), "Weekly check 2", "Weekly check 1")
	if len(buttons(a.Content(), "Show 2 More")) != 0 {
		t.Fatal("Show More with none hidden")
	}

	failing := NewAcknowledgements(newFakeCLI(t, map[string]string{
		"notifications list --waiting --json":      "[]",
		"notifications list --acknowledged --json": "!the notifications file is locked",
	}).CLI, now)
	failing.Load()
	mustShow(t, failing.Content(), "All caught up", "Couldn't read past acknowledgements", "the notifications file is locked")
}

// Acknowledging one moves it from what waits into Past acknowledgements.
func TestAcknowledgingMovesOneToThePast(t *testing.T) {
	dir := t.TempDir()
	n7 := `{"id":"n7","at":"2026-11-21T09:00:00Z","kind":"run","title":"Scout found a match","routine":"marco-scout","acknowledge":true`
	for name, body := range map[string]string{
		"waiting": "[" + n7 + "}]",
		"after":   "[" + n7 + `,"acknowledgedAt":"2026-11-21T11:00:00Z"}]`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\ncd '" + dir + "'\ncase \"$*\" in\n" +
		"  'notifications list --waiting --json') if [ -f acked ]; then echo '[]'; else cat waiting; fi ;;\n" +
		"  'notifications list --acknowledged --json') if [ -f acked ]; then cat after; else echo '[]'; fi ;;\n" +
		"  'notifications ack n7 --json') touch acked; echo '{}' ;;\n" +
		"  *) echo '{}' ;;\nesac\n"
	path := filepath.Join(dir, "acme-auto")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewAcknowledgements(cli.CLI{Path: path, Command: "acme-auto"}, now)
	a.now = noon
	a.Load()
	mustShow(t, a.Content(), "Scout found a match")
	mustNotShow(t, a.Content(), "Past acknowledgements")
	test.Tap(button(t, a.Content(), "Acknowledge"))
	c := a.Content()
	mustShow(t, c, "All caught up", "Past acknowledgements", "Scout found a match", "marco-scout · Acknowledged today at")
	if len(buttons(c, "Acknowledge")) != 0 {
		t.Fatal("Acknowledge on the one acknowledged")
	}
}
