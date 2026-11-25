package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

var hooksReplies = map[string]string{
	"hooks list --json":    "@hooks.json",
	"watchers list --json": "@watchers.json",
}

// Every hook with its health, kind, what it acts on in words, whether it's
// on and its summary; every watcher with how it runs, its state, the hooks
// listening and why it's failing. A row opens its window.
func TestHooksListsHooksAndWatchers(t *testing.T) {
	f := newFakeCLI(t, hooksReplies)
	var opened []string
	h := NewHooks(f.CLI, now, func(n string) { opened = append(opened, "hook "+n) }, func(n string) { opened = append(opened, "turn on "+n) },
		func(n string) { opened = append(opened, "watcher "+n) })
	h.now = noon
	h.Load()
	h.Update(statusFrom(t, "status-hooks.json"))
	c := h.Content()
	mustShow(t, c, "Hooks", "brief", "Prompt", "When A meeting starts (from cal)", "Paused",
		"open-meet", "Program", "When any event (from cal)", "Waiting for events", "Last run Today at",
		"Watchers", "cal", "Polled every 30s", "Heard by brief, open-meet",
		"inbox", "Streams events", "Failing", "exit 1: no token (since Today at", "No hook listens", "acme-auto guide hooks")
	mustNotShow(t, c, "standup")
	rows := all[*linkRow](c)
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	test.Tap(rows[1])
	test.Tap(rows[3])
	test.Tap(button(t, c, "Turn On…"))
	if len(buttons(c, "Turn On…")) != 1 {
		t.Error("Turn On… isn't only on the hook that needs it")
	}
	if strings.Join(opened, ",") != "hook open-meet,watcher inbox,turn on brief" {
		t.Fatalf("opened %q", opened)
	}
}

// The lists are read again when what the hooks are changes in run status,
// not on every status.
func TestHooksFollowRunStatus(t *testing.T) {
	f := newFakeCLI(t, hooksReplies)
	h := NewHooks(f.CLI, now, func(string) {}, func(string) {}, func(string) {})
	h.Load()
	mustShow(t, h.Content(), "Not responding")
	h.Update(statusFrom(t, "status-hooks.json"))
	h.Update(statusFrom(t, "status-hooks.json"))
	if n := f.count("hooks list --json"); n != 2 {
		t.Fatalf("read %d times", n)
	}
	s := statusFrom(t, "status-hooks.json")
	s.Hooks[1].Problem = "its watcher cal is paused, so it hears nothing"
	h.Update(s)
	if n := f.count("watchers list --json"); n != 3 {
		t.Fatalf("read %d times", n)
	}
	empty := newFakeCLI(t, map[string]string{"hooks list --json": "[]", "watchers list --json": "!no watchers folder"})
	h2 := NewHooks(empty.CLI, now, func(string) {}, func(string) {}, func(string) {})
	h2.Load()
	mustShow(t, h2.Content(), "No hooks.", "Couldn't read the watchers", "no watchers folder")
}

var openMeetReplies = map[string]string{
	"hooks show open-meet --json":                  "@hook-open-meet.json",
	"settings list --routine open-meet --json":     "@hook-settings-open-meet.json",
	"runs open-meet --limit 50 --json":             "@runs-open-meet.json",
	"runs show open-meet/2026-11-21T093000 --json": "@run-open-meet.json",
	"hooks trust open-meet --check --json":         "@trust-hook-open-meet.json",
}

func newHookFor(t *testing.T, name string, replies map[string]string) (*Hook, *fakeCLI, *[]string) {
	t.Helper()
	f := newFakeCLI(t, replies)
	var events []string
	h := NewHook(f.CLI, window(t), now, name, func() { events = append(events, "deleted") },
		func(w string) { events = append(events, "watcher "+w) })
	h.now = noon
	h.confirm = func(_, message, _ string, yes func()) {
		events = append(events, "asked: "+message)
		yes()
	}
	h.Update(statusFrom(t, "status-hooks.json"))
	h.Load()
	return h, f, &events
}

// A program hook's window: no Run Now, schedule or Memories; what it acts
// on, with its watcher's state, linking to it; what it runs, with Sign
// In…; Pause and Delete… run their commands.
func TestAProgramHooksWindow(t *testing.T) {
	h, f, events := newHookFor(t, "open-meet", openMeetReplies)
	c := h.Content()
	mustShow(t, c, "open-meet", "Waiting for events", "Status", "Last run", "success", "event", "When it runs",
		"When a meeting.started event (from cal)", "cal paused", "cal failing", "exit 1: calendar unreachable", "What it runs", "Trusted")
	for _, gone := range []string{"Run Now", "Next run", "Memories", "Batches", "Not trusted", "Turn On…", "Staleness"} {
		mustNotShow(t, c, gone)
	}
	// When it runs opens to the rest: its watcher (opening it), type, staleness, rate.
	test.Tap(rowOf(t, c, "When it runs"))
	mustShow(t, h.Content(), "Polled every 30s", "meeting.started", "Events older than 2m are dropped.",
		"At most 20 runs an hour; far past it, it's paused.")
	test.Tap(button(t, h.Content(), "cal"))
	test.Tap(rowOf(t, h.Content(), "What it runs"))
	mustShow(t, h.Content(), "/home/me/.config/acme/hooks/open-meet/act", "meet-token", "Not stored")
	test.Tap(button(t, h.Content(), "Sign In…"))
	test.Tap(button(t, h.Content(), "Pause"))
	// "Pausing…" until it reads as paused, which these replies never say.
	if len(buttons(h.Content(), "Pausing…")) != 1 || !button(t, h.Content(), "Delete…").Disabled() {
		t.Error("Pause doesn't say it's pausing, or another control can be tapped meanwhile")
	}
	release(h.pending)
	test.Tap(button(t, h.Content(), "Delete…"))
	f.mustHaveRun(t, "hooks trust open-meet --check --json", "auth login open-meet --browser", "hooks pause open-meet --json",
		"hooks delete open-meet --json")
	want := "watcher cal,asked: Its copy, settings, trust and secrets are deleted. Its run history is kept.,deleted"
	if got := strings.Join(*events, ","); got != want {
		t.Fatalf("got %q", got)
	}
	// While it runs, Stop replaces Fire….
	s := statusFrom(t, "status-hooks.json")
	s.Hooks[1].LastRun.Outcome = "running"
	h.Update(s)
	if len(buttons(h.Content(), "Fire…")) != 0 {
		t.Fatal("Fire… while it runs")
	}
	test.Tap(button(t, h.Content(), "Stop"))
	f.mustHaveRun(t, "stop open-meet --json")
}

// Fire with a sample event: an editor for one event, starting from the
// type it acts on; Dry Run and Fire run hooks fire with it, and say how
// it went; a real one is read into its runs.
func TestFiringAHook(t *testing.T) {
	event := `{"id":"1","type":"meeting.started"}`
	replies := map[string]string{
		"hooks fire open-meet --event-json " + event + " --dry-run --json": `{"kind":"program","program":{"exitCode":0,"output":"would open it","dryRun":true}}`,
		"hooks fire open-meet --event-json " + event + " --json":           "!open-meet isn't trusted to run for real",
	}
	for k, v := range openMeetReplies {
		replies[k] = v
	}
	h, f, _ := newHookFor(t, "open-meet", replies)
	test.Tap(button(t, h.Content(), "Fire…"))
	c := h.Content()
	mustShow(t, c, "Fire with a sample event", "A program hook needs trusting even for a dry run.")
	editors := all[*widget.Entry](c)
	if len(editors) != 1 || !strings.Contains(editors[0].Text, `"type": "meeting.started"`) {
		t.Fatalf("the sample: %v", editors)
	}
	editors[0].SetText(event)
	test.Tap(button(t, h.Content(), "Dry Run"))
	mustShow(t, h.Content(), "How it went", "exit 0", "dry run", "would open it")
	runs := f.count("runs open-meet --limit 50 --json")
	test.Tap(button(t, h.Content(), "Fire"))
	mustShow(t, h.Content(), "open-meet isn't trusted to run for real")
	if f.count("runs open-meet --limit 50 --json") != runs+1 {
		t.Error("its runs weren't read after a real fire")
	}
	test.Tap(button(t, h.Content(), "Close"))
	mustNotShow(t, h.Content(), "Fire with a sample event")
}

var briefReplies = map[string]string{
	"hooks show brief --json":              "@hook-brief.json",
	"settings list --routine brief --json": "[]",
	"runs brief --limit 50 --json":         "[]",
	"memory list --routine brief --json":   "@memories-standup.json",
	"hooks trust brief --check --json":     "@trust-hook-brief.json",
}

// An LLM hook that's off and untrusted, as is its watcher: one Turn On…
// opens one review covering the watcher, the warning, and the sign-in its
// watcher needs (done from the review); its one confirm trusts both and
// turns it on. It has Memories and batches.
func TestTurningOnAHookAndItsWatcher(t *testing.T) {
	replies := map[string]string{"hooks trust brief --yes --resume --json": "{}"}
	for k, v := range briefReplies {
		replies[k] = v
	}
	h, f, events := newHookFor(t, "brief", replies)
	c := h.Content()
	mustShow(t, c, "Paused")
	mustNotShow(t, c, "brief isn't on")
	mustNotShow(t, c, "Its watcher cal isn't trusted")
	if len(buttons(c, "Turn On…")) != 1 || len(buttons(c, "Resume")) != 0 || len(buttons(c, "Pause")) != 0 {
		t.Fatal("not one action, Turn On…, for a hook that's off")
	}
	test.Tap(button(t, c, "Turn On…"))
	c = h.Content()
	mustShow(t, c, "Turn on brief", "Briefs me. It runs when A meeting starts (from cal).", "Before you turn it on",
		"it can write (Bash) and acts on outside event data", "cal needs signing in",
		"It can be turned on now; it works once signed in.", "Its watcher, cal, is trusted with it")
	test.Tap(button(t, c, "Sign In…"))
	f.mustHaveRun(t, "auth login cal --browser")
	if f.count("hooks trust brief --check --json") != 2 {
		t.Error("the review wasn't read again after signing in")
	}
	test.Tap(rowOf(t, h.Content(), "Details"))
	mustShow(t, h.Content(), "Built-in tools", "Bash", "Its watcher, cal", "Watches", "My calendar",
		"/home/me/.config/acme/watchers/cal/watch", "cal-token")
	test.Tap(button(t, h.Content(), "Trust and Turn On"))
	f.mustHaveRun(t, "hooks trust brief --yes --resume --json")
	mustNotShow(t, h.Content(), "Turn on brief")
	if len(*events) != 0 {
		t.Fatalf("it sent me elsewhere: %q", *events)
	}
	h.ShowTab(MemoriesTab)
	mustShow(t, h.Content(), "The team channel is #team.")
	test.Tap(button(t, h.Content(), "Fire…"))
	mustShow(t, h.Content(), "A dry run intercepts its writes, and needs no trust.")
	h.ShowTab(OverviewTab)
	test.Tap(rowOf(t, h.Content(), "When it runs"))
	mustShow(t, h.Content(), "One run gets every event that waited for it.")
}

// A hook's runs: each with what started it, and the events it ran for,
// their data plain monospace text, never markup.
func TestAHooksRunsShowTheirEvents(t *testing.T) {
	h, _, _ := newHookFor(t, "open-meet", openMeetReplies)
	h.ShowTab(RunsTab)
	c := h.Content()
	mustShow(t, c, "1 run", "Opened Meet", "event", "For this event", "meeting.started", "from cal", "m1", "exit 0",
		"opening https://meet.google.com/abc")
	found := false
	for _, l := range all[*widget.Label](c) {
		if strings.Contains(l.Text, `"title": "**Standup** <b>now</b>"`) {
			found = l.TextStyle.Monospace
		}
	}
	if !found {
		t.Error("its event's data isn't shown as plain monospace text")
	}
	// Nothing reads it as markup: wherever it shows, the asterisks and tags
	// are still there.
	for _, r := range all[*widget.RichText](c) {
		if strings.Contains(r.String(), "Standup") && !strings.Contains(r.String(), "**Standup** <b>now</b>") {
			t.Errorf("its event's data is read as markup: %q", r.String())
		}
	}
}

// A hook's settings: its own, labelled without hooks.<name>., and its file,
// read-only.
func TestAHooksSettings(t *testing.T) {
	h, f, _ := newHookFor(t, "open-meet", openMeetReplies)
	h.ShowTab(SettingsTab)
	c := h.Content()
	mustShow(t, c, "enabled", "staleness", "rate", "Program file", "Opens Meet", "/home/me/.config/acme/hooks/open-meet/hook.yaml",
		"Data folder", "on: {watcher: cal, type: meeting.started}", "acme-auto hooks update open-meet --from <file>")
	mustNotShow(t, c, "hooks.open-meet.rate")
	for _, e := range all[*commitEntry](c) {
		if e.Text == "20" {
			e.SetText("30")
			e.save()
		}
	}
	f.mustHaveRun(t, "settings set hooks.open-meet.rate 30 --json")
}

var inboxReplies = map[string]string{
	"watchers show inbox --json":          "@watcher-inbox.json",
	"watchers trust inbox --check --json": "@trust-watcher-inbox.json",
}

// A watcher's window: how it runs and last went, why it's failing, the
// hooks listening (each opening), what it runs (no writes), with Sign In…
// and Trust, its file; Resume and Delete… run their commands.
func TestAWatchersWindow(t *testing.T) {
	f := newFakeCLI(t, inboxReplies)
	var events []string
	w := NewWatcher(f.CLI, window(t), now, "inbox", func() { events = append(events, "deleted") },
		func(h string) { events = append(events, "hook "+h) })
	w.now = noon
	w.confirm = func(_, message, _ string, yes func()) {
		events = append(events, "asked: "+message)
		yes()
	}
	w.Update(statusFrom(t, "status-hooks.json"))
	w.Load()
	c := w.Content()
	mustShow(t, c, "inbox", "Streams events", "Not trusted", "Paused", "Failing", "It doesn't run, and the hooks listening hear nothing",
		"Started", "Today at", "Last event", "Never", "exit 1: no token", "Since Today at", "Hooks listening", "1 hook", "triage",
		"What it runs", "/home/me/.config/acme/watchers/inbox/watch", "SHA-256 1234567890ab…", "--all", "mail-token", "as $MAIL_TOKEN",
		"Not stored", "Changed", "its executable changed", "Watcher file", "mode: stream",
		"Events it prints", "An email arrives", "email.received", "Heard by triage", "An email is read", "No hook acts on it",
		"acme-auto watchers update inbox --from <folder>/watcher.yaml")
	mustNotShow(t, c, "Writes")
	test.Tap(all[*linkRow](c)[0])
	test.Tap(button(t, w.Content(), "Sign In…"))
	test.Tap(button(t, w.Content(), "Trust"))
	release(w.pending)
	test.Tap(button(t, w.Content(), "Resume"))
	release(w.pending)
	test.Tap(button(t, w.Content(), "Delete…"))
	f.mustHaveRun(t, "auth login inbox --browser", "watchers trust inbox --yes --json", "watchers resume inbox --json",
		"watchers delete inbox --json")
	want := "hook triage,asked: Its copy, settings, trust, secrets and state are deleted. The hooks listening to it stay, and hear nothing.,deleted"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("got %q", got)
	}
}

// A watcher written before watchers declared their events: the core's hint
// as a sentence, never a problem.
func TestAWatchersHint(t *testing.T) {
	f := newFakeCLI(t, map[string]string{"watchers show cal --json": `{"name":"cal","path":"/w/cal/watcher.yaml","mode":"poll",` +
		`"interval":"30s","executable":"/w/cal/watch","args":[],"secrets":[],"enabled":true,"trusted":true,"hooks":[],` +
		`"hint":"it doesn't declare the events it prints","source":""}`})
	w := NewWatcher(f.CLI, window(t), now, "cal", func() {}, func(string) {})
	w.Load()
	mustShow(t, w.Content(), "Events it prints", "It doesn't declare the events it prints.")
	mustNotShow(t, w.Content(), "Heard by")
}
