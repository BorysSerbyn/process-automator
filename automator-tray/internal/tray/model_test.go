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
		`{"text":"Setup isn't finished"}]}`
	lineNone = `{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","validUntil":"2026-11-17T08:06:00Z","health":"none","busy":false,` +
		`"summary":"No routines enabled","routines":[]}`
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

var checkItem = MenuItem{Text: "Check for Updates", Action: Action{Kind: CheckUpdates}}

func window(k WindowKind, routine string) Action {
	return Action{Kind: ShowWindow, Window: Window{Kind: k, Routine: routine}}
}

// The rest of the menu after the status: Routines ›, the update item,
// Settings, Quit.
func tail(routines ...MenuItem) []MenuItem {
	sub := append([]MenuItem{{Text: "All Routines…", Action: window(RoutinesWindow, "")}, {Separator: true}}, routines...)
	return []MenuItem{
		{Separator: true},
		{Text: "Routines", Submenu: sub},
		checkItem,
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
			MenuItem{Text: "weekly_report — Next: tomorrow 09:20", Health: "ok", Action: window(RoutineWindow, "weekly_report")},
			MenuItem{Text: "standup — Paused", Health: "none", Action: window(RoutineWindow, "standup")},
		)...)},
		{"error: each reason (one about a run opens it), and Finish Setup… while setup is left", lineError, fresh, append([]MenuItem{
			version,
			{Text: "standup needs a sign-in", Action: Action{Kind: OpenRun, Run: "r-42"}},
			{Text: "Setup isn't finished"},
			{Text: "Finish Setup…", Action: window(SetupWindow, "")},
		}, tail(
			MenuItem{Text: "standup — standup failed", Health: "error", Action: window(RoutineWindow, "standup")},
		)...)},
		{"stale: not responding, no Finish Setup…, the last routines stand", lineError, stale, append([]MenuItem{
			version, {Text: "Not responding"},
		}, tail(
			MenuItem{Text: "standup — standup failed", Health: "error", Action: window(RoutineWindow, "standup")},
		)...)},
		{"no routines", lineNone, fresh, append([]MenuItem{version, {Text: "No routines enabled"}}, tail(MenuItem{Text: "No routines"})...)},
		{"no status", "", fresh, append([]MenuItem{version, {Text: "Not responding"}}, tail(MenuItem{Text: "Not responding"})...)},
	} {
		var s *cli.RunStatus
		if c.line != "" {
			s = status(t, c.line)
		}
		got := Menu("Acme Automator", "2.1.0", s, c.now, checkItem)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// A long text ends in an ellipsis rather than widening the menu.
func TestLongTextIsShortened(t *testing.T) {
	long := ""
	for range 30 {
		long += "word "
	}
	s := status(t, lineNone)
	s.Summary = long
	got := Menu("A", "1", s, fresh, checkItem)[1].Text
	if r := []rune(got); len(r) != MenuText || r[len(r)-1] != '…' {
		t.Fatalf("%q", got)
	}
}
