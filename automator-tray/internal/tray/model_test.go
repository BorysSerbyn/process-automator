package tray

import (
	"reflect"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Lines as `status --watch` prints them.
const (
	lineOK = `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","validUntil":"2026-11-17T08:06:00Z","health":"ok","busy":false,` +
		`"summary":"All good","routines":[{"name":"weekly_report","enabled":true,"health":"ok","summary":"Next: tomorrow 09:20"},` +
		`{"name":"standup","enabled":false,"health":"none","summary":"Paused"}]}`
	lineBusy = `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","validUntil":"2026-11-17T08:06:00Z","health":"ok","busy":true,` +
		`"summary":"Running standup","routines":[]}`
	lineError = `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","validUntil":"2026-11-17T08:06:00Z","health":"error","busy":true,` +
		`"setupLeft":["Choose the model backend"],"routines":[{"name":"standup","enabled":true,"health":"error","summary":"standup failed"}],` +
		`"reasons":[{"routine":"standup","text":"standup needs a sign-in","detail":"tracker: token expired","run":"r-42"},` +
		`{"text":"Finish Setup (1 to do)…","setup":true}]}`
	lineNone = `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","validUntil":"2026-11-17T08:06:00Z","health":"none","busy":false,` +
		`"summary":"No routines enabled","routines":[]}`
	// lineHooks has hooks beside the routines, reasons about them, and one
	// about the watcher they listen to.
	lineHooks = `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","validUntil":"2026-11-17T08:06:00Z","health":"error","busy":false,` +
		`"routines":[{"name":"standup","enabled":true,"health":"ok","summary":"Next: tomorrow 09:20"}],` +
		`"hooks":[{"name":"brief","enabled":false,"health":"none","summary":"Paused"},` +
		`{"name":"open-meet","enabled":true,"health":"error","summary":"open-meet can't run"}],` +
		`"reasons":[{"routine":"open-meet","hook":true,"text":"open-meet can't run","detail":"it isn't trusted"},` +
		`{"routine":"brief","hook":true,"text":"brief failed","run":"brief/r1"},` +
		`{"watcher":"cal","text":"cal is failing: no network (open-meet and brief listen to it)"},` +
		`{"routine":"standup","text":"standup can't be scheduled","detail":"it isn't trusted"},` +
		`{"text":"(2) Acknowledgements pending","detail":"a\nb","acknowledge":true}]}`
	// lineNoHeartbeat has no validUntil: the scheduler never said it's up.
	lineNoHeartbeat = `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","health":"ok","busy":true,"routines":[]}`
)

var (
	fresh = time.Date(2026, 11, 17, 8, 5, 30, 0, time.UTC)
	// stale is past every line's validUntil.
	stale = time.Date(2026, 11, 17, 8, 6, 1, 0, time.UTC)
)

func status(t *testing.T, line string) *cli.RunStatus {
	t.Helper()
	s := cli.ParseStatus([]byte(line))
	if s == nil {
		t.Fatalf("unreadable: %s", line)
	}
	return s
}

// The light is the core's health as is; no status, or one past its
// validUntil, is red. Busy pulses, only while the status holds.
func TestTheLight(t *testing.T) {
	for _, c := range []struct {
		name    string
		line    string
		now     time.Time
		light   Light
		pulsing bool
	}{
		{"ok", lineOK, fresh, Green, false},
		{"busy", lineBusy, fresh, Green, true},
		{"error, busy", lineError, fresh, Red, true},
		{"none", lineNone, fresh, Grey, false},
		{"ok, stale", lineOK, stale, Red, false},
		{"busy, stale", lineBusy, stale, Red, false},
		{"none, stale", lineNone, stale, Red, false},
		{"no heartbeat", lineNoHeartbeat, fresh, Red, false},
		{"no status", "", fresh, Red, false},
	} {
		var s *cli.RunStatus
		if c.line != "" {
			s = status(t, c.line)
		}
		if got := LightFor(s, c.now); got != c.light {
			t.Errorf("%s: light %s, want %s", c.name, got, c.light)
		}
		if got := Pulsing(s, c.now); got != c.pulsing {
			t.Errorf("%s: pulsing %v, want %v", c.name, got, c.pulsing)
		}
		// The pulse dims the dot only while pulsing.
		if got := FrameFor(s, c.now, true); got != (Frame{Light: c.light, Dim: c.pulsing}) {
			t.Errorf("%s: frame %+v", c.name, got)
		}
		if got := FrameFor(s, c.now, false); got.Dim {
			t.Errorf("%s: dimmed off the pulse", c.name)
		}
	}
}

func TestFramesNameTheirIcons(t *testing.T) {
	var files []string
	for _, f := range Frames() {
		files = append(files, f.File())
	}
	want := []string{"status-green.png", "status-green-dim.png", "status-red.png", "status-red-dim.png", "status-grey.png", "status-grey-dim.png"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("%v", files)
	}
}

var updatesItem = MenuItem{Text: "Manage Updates…", Action: Action{Kind: ShowWindow, Window: Window{Kind: UpdatesWindow}}}

func window(k WindowKind, routine string) Action {
	return Action{Kind: ShowWindow, Window: Window{Kind: k, Routine: routine}}
}

// review is a routine's or hook's window, on its Turn On review when it
// needs one.
func review(k WindowKind, name string) Action {
	return Action{Kind: ShowWindow, Window: Window{Kind: k, Routine: name, Review: true}}
}

// The rest of the menu after the status: Manage Updates… between
// separators, Routines ›, Settings, Quit.
func tail(routines ...MenuItem) []MenuItem {
	sub := append([]MenuItem{{Text: "All Routines…", Action: window(RoutinesWindow, "")}, {Separator: true}}, routines...)
	return []MenuItem{
		{Separator: true},
		updatesItem,
		{Separator: true},
		{Text: "Routines", Submenu: sub},
		{Text: "Settings", Action: window(SettingsWindow, "")},
		{Separator: true},
		{Text: "Quit", Action: Action{Kind: Quit}},
	}
}

func TestTheMenu(t *testing.T) {
	version := MenuItem{Text: "Acme Automator 2.1.0"}
	for _, c := range []struct {
		name string
		line string
		now  time.Time
		want []MenuItem
	}{
		{"ok: the summary, and each routine with its health and summary", lineOK, fresh, append([]MenuItem{
			version, {Text: "All good"},
		}, tail(
			MenuItem{Text: "weekly_report — Next: tomorrow 09:20", Health: "ok", Action: review(RoutineWindow, "weekly_report")},
			MenuItem{Text: "standup — Paused", Health: "none", Action: review(RoutineWindow, "standup")},
		)...)},
		{"error: each reason (one about a run opens it; setup's, Setup), and no Finish Setup… of its own", lineError, fresh, append([]MenuItem{
			version,
			{Text: "standup needs a sign-in", Action: Action{Kind: OpenRun, Run: "r-42"}},
			{Text: "Finish Setup (1 to do)…", Action: window(SetupWindow, "")},
		}, tail(
			MenuItem{Text: "standup — standup failed", Health: "error", Action: review(RoutineWindow, "standup")},
		)...)},
		{"stale: not responding, the last routines stand", lineError, stale, append([]MenuItem{
			version, {Text: "Not responding"},
		}, tail(
			MenuItem{Text: "standup — standup failed", Health: "error", Action: review(RoutineWindow, "standup")},
		)...)},
		{"hooks: their own submenu after Routines ›; a reason about one opens its window, or its run; one about a watcher, the watcher's window", lineHooks, fresh, append([]MenuItem{
			version,
			{Text: "open-meet can't run", Action: review(HookWindow, "open-meet")},
			{Text: "brief failed", Action: Action{Kind: OpenRun, Run: "brief/r1"}},
			{Text: "cal is failing: no network (open-meet and brief listen to it)", Action: window(WatcherWindow, "cal")},
			{Text: "standup can't be scheduled", Action: review(RoutineWindow, "standup")},
			{Text: "(2) Acknowledgements pending", Action: window(AcknowledgementsWindow, "")},
		}, withHooks(tail(
			MenuItem{Text: "standup — Next: tomorrow 09:20", Health: "ok", Action: review(RoutineWindow, "standup")},
		),
			MenuItem{Text: "brief — Paused", Health: "none", Action: review(HookWindow, "brief")},
			MenuItem{Text: "open-meet — open-meet can't run", Health: "error", Action: review(HookWindow, "open-meet")},
		)...)},
		{"no routines", lineNone, fresh, append([]MenuItem{version, {Text: "No routines enabled"}}, tail(MenuItem{Text: "No routines"})...)},
		{"no status", "", fresh, append([]MenuItem{version, {Text: "Not responding"}}, tail(MenuItem{Text: "Not responding"})...)},
	} {
		var s *cli.RunStatus
		if c.line != "" {
			s = status(t, c.line)
		}
		got := Menu("Acme Automator", "2.1.0", s, c.now, updatesItem)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// withHooks puts Hooks › after Routines › in a menu's tail.
func withHooks(tail []MenuItem, hooks ...MenuItem) []MenuItem {
	sub := append([]MenuItem{{Text: "All Hooks…", Action: window(HooksWindow, "")}, {Separator: true}}, hooks...)
	out := append([]MenuItem{}, tail[:4]...)
	out = append(out, MenuItem{Text: "Hooks", Submenu: sub})
	return append(out, tail[4:]...)
}

// A long text ends in an ellipsis rather than widening the menu.
func TestLongTextIsShortened(t *testing.T) {
	long := ""
	for range 30 {
		long += "word "
	}
	s := status(t, lineNone)
	s.Summary = long
	got := Menu("A", "1", s, fresh, updatesItem)[1].Text
	if r := []rune(got); len(r) != MenuText || r[len(r)-1] != '…' {
		t.Fatalf("%q", got)
	}
}

func TestReopenWindowFollowsSetupLeft(t *testing.T) {
	if w := ReopenWindow(&cli.RunStatus{SetupLeft: []string{"sign-in"}}); w.Kind != SetupWindow {
		t.Fatal(w)
	}
	if w := ReopenWindow(&cli.RunStatus{}); w.Kind != SettingsWindow {
		t.Fatal(w)
	}
	if w := ReopenWindow(nil); w.Kind != SettingsWindow {
		t.Fatal(w)
	}
}
