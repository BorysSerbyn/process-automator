package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// noon is the tests' now: the testdata's runs are this morning's.
func noon() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) }

// statusFrom is a run status from testdata.
func statusFrom(t *testing.T, file string) *cli.RunStatus {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	var s cli.RunStatus
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// Every routine with its health, schedule in words, last run and summary;
// a row opens its routine; Setup and Settings open theirs.
func TestRoutinesListsEachRoutine(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"routines list --kind all --json": "@routines.json"})
	var opened []string
	r := NewRoutines(f.CLI, now, func(n string) { opened = append(opened, n) },
		func() { opened = append(opened, "setup") }, func() { opened = append(opened, "settings") })
	r.now = noon
	r.Load()
	r.Update(statusFrom(t, "status-routines.json"))
	c := r.Content()
	mustShow(t, c, "Routines", "1 routine needs you", "standup", "At 09:20, Monday through Friday", "Last run Today at",
		"success", "Next: Monday 09:20", "digest", "No schedule", "Not trusted", "digest can't be scheduled")
	mustNotShow(t, c, "Not responding")
	rows := all[*linkRow](c)
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	test.Tap(rows[1])
	test.Tap(button(t, c, "Setup"))
	test.Tap(button(t, c, "Settings"))
	if strings.Join(opened, ",") != "digest,setup,settings" {
		t.Fatalf("opened %q", opened)
	}
}

// The list is read again when what the routines are changes in run status
// (a run ended), not on every status; no status is not responding.
func TestRoutinesFollowRunStatus(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"routines list --kind all --json": "@routines.json"})
	r := NewRoutines(f.CLI, now, func(string) {}, func() {}, func() {})
	r.now = noon
	r.Load()
	mustShow(t, r.Content(), "Not responding")
	s := statusFrom(t, "status-routines.json")
	r.Update(s)
	r.Update(statusFrom(t, "status-routines.json")) // a heartbeat
	if n := f.count("routines list --kind all --json"); n != 2 {
		t.Fatalf("read %d times", n)
	}
	s2 := statusFrom(t, "status-routines.json")
	s2.Routines[0].LastRun.Outcome = "running"
	r.Update(s2)
	if n := f.count("routines list --kind all --json"); n != 3 {
		t.Fatalf("read %d times", n)
	}
	mustShow(t, r.Content(), "running")
}

func TestRoutinesSayWhatWentWrong(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"routines list --kind all --json": "!the routines folder can't be read"})
	r := NewRoutines(f.CLI, now, func(string) {}, func() {}, func() {})
	r.Load()
	mustShow(t, r.Content(), "Couldn't read the routines", "the routines folder can't be read")
	f2 := newFakeCLI(t, map[string]string{"routines list --kind all --json": "[]"})
	r2 := NewRoutines(f2.CLI, now, func(string) {}, func() {}, func() {})
	r2.Load()
	mustShow(t, r2.Content(), "No routines installed.")
}

var standupReplies = map[string]string{
	"routines show standup --json":               "@routine-standup.json",
	"settings list --routine standup --json":     "@routine-settings-standup.json",
	"runs standup --limit 50 --json":             "@runs-standup.json",
	"runs show standup/2026-11-21T092001 --json": "@run-standup.json",
	"memory list --routine standup --json":       "@memories-standup.json",
	"routines trust standup --check --json":      "@trust-standup.json",
}

func newRoutineFor(t *testing.T, name string, replies map[string]string) (*Routine, *fakeCLI, *bool) {
	t.Helper()
	f := newFakeCLI(t, replies)
	deleted := false
	r := NewRoutine(f.CLI, window(t), now, name, func() { deleted = true })
	r.now = noon
	r.confirm = func(_, _, _ string, yes func()) { yes() }
	r.Update(statusFrom(t, "status-routines.json"))
	r.Load()
	return r, f, &deleted
}

// A trusted, enabled routine: its status, schedule and what it can reach;
// Run Now, Dry Run, Pause and Delete… run their commands.
func TestARoutinesOverviewAndControls(t *testing.T) {
	r, f, deleted := newRoutineFor(t, "standup", standupReplies)
	c := r.Content()
	mustShow(t, c, "standup", "Next: Monday 09:20", "Status", "Last run", "Today at", "success", "schedule",
		"Next run", "Monday", "What it can reach", "Trusted")
	mustNotShow(t, c, "Not trusted")
	test.Tap(button(t, c, "Run Now"))
	test.Tap(button(t, r.Content(), "Dry Run"))
	test.Tap(button(t, r.Content(), "Pause"))
	test.Tap(button(t, r.Content(), "Delete…"))
	f.mustHaveRun(t, "start standup --json", "start standup --dry-run --json", "routines pause standup --json",
		"routines delete standup --json")
	if !*deleted {
		t.Error("the window didn't close once deleted")
	}
	// What it can reach opens to its servers and tools.
	test.Tap(rowOf(t, r.Content(), "What it can reach"))
	mustShow(t, r.Content(), "Servers", "slack", "post_message", "Built-in tools", "Read", "/home/me/work")
}

// While it runs (or waits), Stop replaces Run Now; a control that fails
// says why in the core's words.
func TestStoppingARunAndErrors(t *testing.T) {
	replies := map[string]string{"stop standup --json": "!standup isn't running"}
	for k, v := range standupReplies {
		replies[k] = v
	}
	r, f, _ := newRoutineFor(t, "standup", replies)
	s := statusFrom(t, "status-routines.json")
	s.Routines[0].LastRun.Outcome = "queued"
	r.Update(s)
	if len(buttons(r.Content(), "Run Now")) != 0 {
		t.Fatal("Run Now while it waits its turn")
	}
	test.Tap(button(t, r.Content(), "Stop"))
	f.mustHaveRun(t, "stop standup --json")
	mustShow(t, r.Content(), "standup isn't running")
}

// Not trusted: the banner's Review… shows what it can reach; a paused
// routine is turned on (trusted and resumed) in one step.
func TestReviewingAndTurningOnARoutine(t *testing.T) {
	r, f, _ := newRoutineFor(t, "digest", map[string]string{
		"routines show digest --json":           "@routine-digest.json",
		"routines trust digest --check --json":  "@trust-digest.json",
		"settings list --routine digest --json": "[]",
		"runs digest --limit 50 --json":         "[]",
		"memory list --routine digest --json":   "[]",
	})
	c := r.Content()
	mustShow(t, c, "Not trusted", "it's never been trusted", "Can't schedule", "it isn't trusted", "Never", "None")
	if len(buttons(c, "Resume")) != 1 {
		t.Error("no Resume for a paused routine")
	}
	r.ShowTab(RunsTab)
	mustShow(t, r.Content(), "It hasn't run yet.")
	test.Tap(button(t, r.Content(), "Review…"))
	mustShow(t, r.Content(), "notify_me", "Tinted tools write")
	test.Tap(button(t, r.Content(), "Turn On"))
	f.mustHaveRun(t, "routines trust digest --check --json", "routines trust digest --yes --resume --json")
}

// Runs: newest first, the latest open with its report, final text and
// every tool call; Open Report opens the core's report.
func TestARoutinesRuns(t *testing.T) {
	r, f, _ := newRoutineFor(t, "standup", standupReplies)
	r.ShowTab(RunsTab)
	c := r.Content()
	mustShow(t, c, "2 runs", "2 tool calls", "$0.04", "Slack said: not_in_channel", "Posted the standup to #team.",
		"done", "2 tool calls, 1 write", "post_message", "channel: #team", "1200 in · 340 out · 800 cached · claude-api")
	test.Tap(button(t, c, "Open Report"))
	f.mustHaveRun(t, "runs show standup/2026-11-21T092001 --open")
	// Closing it hides what it did.
	test.Tap(rowOf(t, r.Content(), "Today at"))
	mustNotShow(t, r.Content(), "Posted the standup")
	// The overview's Show Run opens it again, in Runs.
	r.ShowTab(OverviewTab)
	test.Tap(button(t, r.Content(), "Show Run"))
	mustShow(t, r.Content(), "Posted the standup")
}

// Memories: listed, added and removed through the core.
func TestARoutinesMemories(t *testing.T) {
	r, f, _ := newRoutineFor(t, "standup", standupReplies)
	r.ShowTab(MemoriesTab)
	mustShow(t, r.Content(), "1 memory", "The team channel is #team.")
	entries := all[*widget.Entry](r.Content())
	if len(entries) != 1 {
		t.Fatalf("%d fields", len(entries))
	}
	test.Type(entries[0], "-Fridays are quiet")
	test.Tap(button(t, r.Content(), "Add"))
	f.mustHaveRun(t, "memory add --routine standup --json -- -Fridays are quiet")
	for _, b := range all[*widget.Button](r.Content()) {
		if b.Text == "" {
			test.Tap(b) // the memory's remove button
			break
		}
	}
	f.mustHaveRun(t, "memory rm m1")
}

// Settings: its own settings as the Settings window's rows, labelled
// without routines.<name>., and its file.
func TestARoutinesSettings(t *testing.T) {
	r, f, _ := newRoutineFor(t, "standup", standupReplies)
	r.ShowTab(SettingsTab)
	c := r.Content()
	mustShow(t, c, "schedule", "timeout", "How long a run may take.", "Routine file", "claude-sonnet-4-5",
		"/home/me/.config/acme/routines/standup.prompt")
	mustNotShow(t, c, "routines.standup.timeout")
	for _, e := range all[*commitEntry](c) {
		if e.Text == "10m" {
			e.SetText("20m")
			e.save()
		}
	}
	f.mustHaveRun(t, "settings set routines.standup.timeout 20m --json")
}

var backupReplies = map[string]string{
	"routines show backup --json":               "@routine-backup.json",
	"routines trust backup --check --json":      "@trust-backup.json",
	"settings list --routine backup --json":     "[]",
	"runs backup --limit 50 --json":             "@runs-backup.json",
	"runs show backup/2026-11-21T090000 --json": "@run-backup.json",
}

// A program's window: no Memories tab or model; what Dry Run does with it,
// by the controls; what it runs, in place of what it can reach, with its
// checksum shortened and copied whole, and Sign In… for a secret that isn't
// stored; its delete says what's kept.
func TestAProgramsWindow(t *testing.T) {
	r, f, _ := newRoutineFor(t, "backup", backupReplies)
	c := r.Content()
	mustShow(t, c, "What it runs", "Executable", "/home/me/.config/acme/programs/backup/backup", "SHA-256 9f86d081884c…",
		"a dry run passes it --dry-run", "api-token", "as $API_TOKEN", "Not stored", "--quiet",
		"Dry Run passes it --dry-run: it's told to change nothing. Like a real run, a dry run needs it trusted.")
	mustNotShow(t, c, "Memories")
	mustNotShow(t, c, "Servers")
	test.Tap(button(t, c, "Copy"))
	if got := fyne.CurrentApp().Clipboard().Content(); got != "9f86d081884c7d65" {
		t.Errorf("copied %q", got)
	}
	test.Tap(button(t, r.Content(), "Sign In…"))
	f.mustHaveRun(t, "auth login backup --browser")
	if f.count("routines show backup --json") < 2 || f.count("routines trust backup --check --json") < 2 {
		t.Error("what's stored wasn't read again after signing in")
	}
	var asked string
	r.confirm = func(_, message, _ string, _ func()) { asked = message }
	test.Tap(button(t, r.Content(), "Delete…"))
	if asked != "Its copy, settings, trust and secrets are deleted. Its run history and data folder are kept." {
		t.Errorf("asked %q", asked)
	}
	if f.count("memory list --routine backup --json") != 0 {
		t.Error("asked for a program's memories")
	}
}

// A program that doesn't write: Dry Run runs it as it is; its secrets are
// stored, so there's nothing to sign in to.
func TestAProgramThatDoesntWrite(t *testing.T) {
	reads := strings.NewReplacer(`"writes": true`, `"writes": false`, `"stored": false`, `"stored": true`)
	replies := map[string]string{}
	for k, v := range backupReplies {
		replies[k] = v
	}
	for _, k := range []string{"routines show backup --json", "routines trust backup --check --json"} {
		b, err := os.ReadFile(filepath.Join("testdata", strings.TrimPrefix(replies[k], "@")))
		if err != nil {
			t.Fatal(err)
		}
		replies[k] = reads.Replace(string(b))
	}
	r, _, _ := newRoutineFor(t, "backup", replies)
	c := r.Content()
	mustShow(t, c, "It doesn't write: Dry Run runs it as it is.", "No: a dry run runs it as it is", "Stored")
	if len(buttons(c, "Sign In…")) != 0 {
		t.Error("Sign In… with every secret stored")
	}
}

// A program's runs: its summary, exit code, whether it was a dry run, when
// it finished, and its output, saying when it was cut down.
func TestAProgramsRuns(t *testing.T) {
	r, _, _ := newRoutineFor(t, "backup", backupReplies)
	r.ShowTab(RunsTab)
	c := r.Content()
	mustShow(t, c, "1 run", "Copied 3 files", "exit 0", "dry run", "Finished Today at", "copying notes.md",
		"[… 40 KiB of output left out …]", "It printed more than is kept")
	mustNotShow(t, c, "tool call")
}

// A program's settings: its description file, read-only, with what it
// says it does and its data folder; no model.
func TestAProgramsFile(t *testing.T) {
	r, _, _ := newRoutineFor(t, "backup", backupReplies)
	r.ShowTab(SettingsTab)
	c := r.Content()
	mustShow(t, c, "Program file", "Backs up my notes", "/home/me/.config/acme/programs/backup/program.yaml",
		"Data folder", "/home/me/.config/acme/program-data/backup", "executable: ./backup", "Read-only",
		"acme-auto routines update")
	mustNotShow(t, c, "Model")
	if len(all[*widget.Entry](c)) != 0 {
		t.Error("its file can be edited")
	}
}

// Long output scrolls in a box of its own; short output doesn't.
func TestAProgramsLongOutputScrolls(t *testing.T) {
	window(t) // a test app, for the theme
	scrolls := func(output string) bool {
		return len(all[*container.Scroll](programRun(cli.ProgramRun{Output: output}, nil, noon()))) > 0
	}
	if scrolls("one line") {
		t.Error("a line scrolls")
	}
	if !scrolls(strings.Repeat("line\n", outputLines+1)) {
		t.Error("long output doesn't scroll")
	}
}
