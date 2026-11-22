package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

func newSettingsFor(t *testing.T, replies map[string]string) (*Settings, *fakeCLI) {
	t.Helper()
	f := newFakeCLI(t, replies)
	s := NewSettings(f.CLI, window(t), now, "Acme Automator")
	s.now = func() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local) }
	s.Load()
	return s, f
}

// The app's settings, as a tree from their dotted keys: top-level groups
// open, subgroups closed; a control per type; a selected choice's line;
// inactive settings dimmed with the core's reason.
func TestSettingsControlsPerType(t *testing.T) {
	s, _ := newSettingsFor(t, map[string]string{"settings list --scope app --json": "@settings.json"})
	c := s.Content()
	mustShow(t, c, "llm", "history", "runs", "update", "memories", "budget", "3 settings",
		"backend", "What runs your routines.",
		// The selected backend's own line.
		"Your own model server, e.g. Ollama (experimental, untested)",
		"usage-limit", "only used with llm.backend claude-code", "retention-days", "failure-notifications")
	// llm › budget starts closed.
	mustNotShow(t, c, "window-usd")
	test.Tap(rowOf(t, c, "budget"))
	mustShow(t, s.Content(), "window-usd", "weekly-usd")

	selects := all[*widget.Select](s.Content())
	if len(selects) != 1 || selects[0].Selected != "local" {
		t.Fatalf("pickers: %d", len(selects))
	}
	checks := 0
	for _, ch := range all[*widget.Check](s.Content()) {
		if ch.Text == "" && ch.Checked {
			checks++
		}
	}
	if checks != 1 {
		t.Errorf("%d toggles on (runs.failure-notifications)", checks)
	}
	fields := map[string]*commitEntry{}
	disabled := map[string]int{}
	for _, e := range all[*commitEntry](s.Content()) {
		fields[e.Text] = e
		if e.Disabled() {
			disabled[e.Text]++
		}
	}
	// Inactive: off, with the core's reason beside it (llm.usage-limit is
	// 90, as history.retention-days is, which is on).
	if disabled["90"] != 1 {
		t.Errorf("%d fields at 90 off", disabled["90"])
	}
	if f := fields["4000"]; f == nil || f.Disabled() {
		t.Errorf("memories.max-chars: %v", f)
	}
	if f := fields[""]; f == nil || f.PlaceHolder != "unset" {
		t.Errorf("update.source-dir: %+v", f)
	}
	// Only settings away from their default have Reset: llm.backend, and
	// llm.local.model once llm › local is open.
	if n := len(buttons(s.Content(), "Reset")); n != 1 {
		t.Errorf("%d Reset buttons", n)
	}
	test.Tap(rowOf(t, s.Content(), "local"))
	if n := len(buttons(s.Content(), "Reset")); n != 2 {
		t.Errorf("%d Reset buttons with llm › local open", n)
	}
}

// Each control sends its value back as text; Reset resets; the core's
// message shows when it refuses a value.
func TestSettingsSetAndReset(t *testing.T) {
	s, f := newSettingsFor(t, map[string]string{
		"settings list --scope app --json":                     "@settings.json",
		"settings set memories.max-chars lots --json":          "!memories.max-chars: \"lots\" isn't a whole number",
		"settings set runs.failure-notifications false --json": `{"key":"runs.failure-notifications"}`,
	})
	all[*widget.Select](s.Content())[0].SetSelected("codex")
	f.mustHaveRun(t, "settings set llm.backend codex --json")
	for _, ch := range all[*widget.Check](s.Content()) {
		if ch.Checked {
			test.Tap(ch)
		}
	}
	f.mustHaveRun(t, "settings set runs.failure-notifications false --json")
	for _, e := range all[*commitEntry](s.Content()) {
		if e.Text == "4000" {
			e.SetText("lots")
			e.FocusLost() // leaving a field saves it, as Return does
		}
	}
	f.mustHaveRun(t, "settings set memories.max-chars lots --json")
	mustShow(t, s.Content(), `memories.max-chars: "lots" isn't a whole number`)
	test.Tap(button(t, s.Content(), "Reset"))
	f.mustHaveRun(t, "settings reset llm.backend --json")
}

// Start at login is login-items: off until the app is installed.
func TestStartAtLogin(t *testing.T) {
	s, _ := newSettingsFor(t, map[string]string{
		"settings list --scope app --json": "@settings.json",
		"login-items status --json":        `{"installed":false,"enabled":false,"items":[]}`,
	})
	mustShow(t, s.Content(), "Start at login", "Available once the app is installed.")
	if toggle := loginToggle(t, s); !toggle.Disabled() {
		t.Error("Start at login is on before the app is installed")
	}
	s, f := newSettingsFor(t, map[string]string{
		"settings list --scope app --json": "@settings.json",
		"login-items status --json":        `{"installed":true,"enabled":false,"items":[]}`,
		"login-items enable --json":        `{"installed":true,"enabled":true,"items":[]}`,
	})
	mustShow(t, s.Content(), "Acme Automator starts when you log in.")
	test.Tap(loginToggle(t, s))
	f.mustHaveRun(t, "login-items enable --json")
	if !loginToggle(t, s).Checked {
		t.Error("not shown on")
	}
}

func loginToggle(t *testing.T, s *Settings) *widget.Check {
	t.Helper()
	for _, ch := range all[*widget.Check](s.Content()) {
		if ch.Text == "" && hasText(s.startAtLogin(), "Start at login") {
			return ch // the first toggle is Start at login's
		}
	}
	t.Fatal("no Start at login toggle")
	return nil
}

// The usage card shows usage --json: a plan's windows against its limit,
// budgets' spend, or no limit; the core's reasons and read errors.
func TestUsageCard(t *testing.T) {
	newSettingsFor := func(t *testing.T, replies map[string]string) (*Settings, *fakeCLI) {
		replies["settings list --scope app --json"] = "@settings.json"
		return newSettingsFor(t, replies)
	}
	s, _ := newSettingsFor(t, map[string]string{"usage --json": "@usage-plan.json"})
	mustShow(t, s.Content(), "LLM usage", "Limit: 90% of the plan", "Not read yet.",
		"Couldn't read usage just now: claude-code backend: the `claude` CLI isn't on PATH")

	s, _ = newSettingsFor(t, map[string]string{"usage --json": `{"kind":"plan","limitPercent":80,"over":true,
		"reason":"The session window is past the usage limit (85% of 80%)",
		"fetchedAt":"2026-11-21T11:50:00Z",
		"windows":[{"name":"session","length":"5h","usedPercent":85,"resetsAt":"2026-11-21T14:00:00Z","over":true},
		           {"name":"weekly","length":"7d","over":false}],
		"models":[{"model":"opus","usedPercent":12.4,"resetsAt":"2026-11-25T09:00:00Z"}]}`})
	mustShow(t, s.Content(), "Session (5h)", "85% used", "Weekly (7d)", "Not known", "opus weekly: 12% used",
		"Single-model limits are shown only", "The session window is past the usage limit (85% of 80%)", "As of Today at")
	if len(all[*usageBar](s.Content())) != 2 {
		t.Error("no bars")
	}

	s, _ = newSettingsFor(t, map[string]string{"usage --json": "@usage-budget.json"})
	mustShow(t, s.Content(), "Window (5h)", "$0.00 of $2.00", "Week (7d)", "$0.00 of $20.00")

	s, _ = newSettingsFor(t, map[string]string{"usage --json": `{"kind":"none","windows":[],"over":false}`})
	mustShow(t, s.Content(), "No usage limit with this backend")

	s, _ = newSettingsFor(t, map[string]string{"usage --json": "!can't read the usage file"})
	mustShow(t, s.Content(), "can't read the usage file")
}

// Settings that can't be read: the core's message instead.
func TestSettingsUnreadable(t *testing.T) {
	s, _ := newSettingsFor(t, map[string]string{"settings list --scope app --json": "!config.yaml: line 3: not a setting"})
	mustShow(t, s.Content(), "No settings", "Couldn't read the settings: config.yaml: line 3: not a setting")
}

func TestKeyTree(t *testing.T) {
	var settings []cli.Setting
	for _, k := range []string{"llm.backend", "llm.budget.window", "history.retention-days", "llm.budget.weekly-usd", "llm.local.url", "solo", "llm.usage-limit"} {
		settings = append(settings, cli.Setting{Key: k})
	}
	type flat struct {
		id      string
		entries []string
		groups  []flat
	}
	var walk func(gs []*group) []flat
	walk = func(gs []*group) []flat {
		var out []flat
		for _, g := range gs {
			f := flat{id: g.id}
			for _, e := range g.entries {
				f.entries = append(f.entries, e.label)
			}
			f.groups = walk(g.groups)
			out = append(out, f)
		}
		return out
	}
	want := []flat{
		{id: "llm", entries: []string{"backend", "usage-limit"}, groups: []flat{
			{id: "llm.budget", entries: []string{"window", "weekly-usd"}},
			{id: "llm.local", entries: []string{"url"}},
		}},
		{id: "history", entries: []string{"retention-days"}},
		{id: "solo", entries: []string{"solo"}},
	}
	tree := keyTree(settings)
	if got := walk(tree); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if tree[0].count() != 5 {
		t.Errorf("llm holds %d", tree[0].count())
	}
}

func TestWhen(t *testing.T) {
	base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local)
	for d, want := range map[time.Duration]string{
		-3 * time.Hour:      "Today at 09:00",
		-24 * time.Hour:     "Yesterday at 12:00",
		20 * time.Hour:      "Tomorrow at 08:00",
		-48 * time.Hour:     "19 Nov 2026 at 12:00",
		5*24*time.Hour + 30: "26 Nov 2026 at 12:00",
	} {
		if got := when(base.Add(d), base); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestPreparationText(t *testing.T) {
	lines := numbered("Intro line.\n\n1. Open https://x.example/apps.\n12. Set `a=b` there\n")
	if len(lines) != 3 || lines[0].number != "" || lines[1].number != "1." || lines[2].number != "12." || lines[2].text != "Set `a=b` there" {
		t.Fatalf("%+v", lines)
	}
	r := rich(lines[1].text)
	var got []string
	for _, s := range r.Segments {
		switch v := s.(type) {
		case *widget.HyperlinkSegment:
			got = append(got, "link:"+v.URL.String())
		case *widget.TextSegment:
			got = append(got, v.Text)
		}
	}
	if strings.Join(got, "|") != "Open |link:https://x.example/apps|." {
		t.Errorf("%q", got)
	}
	r = rich(lines[2].text)
	if seg, ok := r.Segments[1].(*widget.TextSegment); !ok || seg.Text != "a=b" || seg.Style != widget.RichTextStyleCodeInline {
		t.Errorf("%+v", r.Segments)
	}
}
