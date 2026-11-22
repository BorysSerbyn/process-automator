package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func newSetupFor(t *testing.T, replies map[string]string) (*Setup, *fakeCLI, *[]string) {
	t.Helper()
	f := newFakeCLI(t, replies)
	var opened []string
	s := NewSetup(f.CLI, window(t), now, func(r string) { opened = append(opened, r) })
	s.Load()
	return s, f, &opened
}

// The steps render from setup --json, grouped by kind in the core's order,
// each with its title, status and question in the core's words.
func TestSetupStepsRenderFromSetupJSON(t *testing.T) {
	s, f, _ := newSetupFor(t, map[string]string{
		"setup --json":                          "@setup-claude-code.json",
		"settings list --scope app --json":      "@settings.json",
		"routines trust standup --check --json": "@trust.json",
		"routines trust digest --check --json":  "@trust.json",
	})
	c := s.Content()
	mustShow(t, c, "Setup", "4 steps to go.",
		// Templates: done, so closed, its status still showing.
		"Templates", "Routines installed: digest, standup",
		// The backend: open, its question asked.
		"Model backend", "Runs with claude-code, but Claude Code isn't installed", "Which backend should run your routines?",
		"Claude Code connection",
		// The routines to turn on: a group, how far along, each step's review.
		"Turn on routines", "0 of 2 on", "Turn on digest", "Turn on standup",
		"Trust standup to run for real with what it can reach", "Servers", "Tools",
		"Tinted tools write (they're intercepted in dry runs).")
	mustNotShow(t, c, "First, on its own: which of the templates' projects")
	if n := len(buttons(c, "Trust and Turn On")); n != 2 {
		t.Errorf("%d Turn On buttons", n)
	}
	if !slices.Contains(texts(c), "Done") {
		t.Error("no Done tag")
	}
	// The choice is the setting's value (`settings list`).
	radios := all[*widget.RadioGroup](c)
	if len(radios) != 1 || radios[0].Selected != "local" ||
		!slices.Equal(radios[0].Options, []string{"claude-code", "claude-api", "local", "codex"}) {
		t.Fatalf("backend picker: %d %q %q", len(radios), radios[0].Selected, radios[0].Options)
	}
	// The connection to Claude Code is done without asking, once: reading
	// setup again doesn't start it again.
	f.mustHaveRun(t, "mcp connect --json")
	test.Tap(button(t, c, "Check Again"))
	if n := f.count("mcp connect --json"); n != 1 {
		t.Errorf("mcp connect ran %d times", n)
	}
	if n := f.count("setup --json"); n < 3 {
		t.Errorf("setup read %d times", n)
	}
	// Its button tries again.
	test.Tap(button(t, s.Content(), "Connect"))
	if n := f.count("mcp connect --json"); n != 2 {
		t.Errorf("Connect: mcp connect ran %d times", n)
	}
}

// A step's row opens and closes it; a closed group hides its steps.
func TestStepsAndGroupsOpenAndClose(t *testing.T) {
	s, _, _ := newSetupFor(t, map[string]string{
		"setup --json":                          "@setup-claude-code.json",
		"templates list --json":                 "@templates.json",
		"routines trust standup --check --json": "@trust.json",
		"routines trust digest --check --json":  "@trust.json",
	})
	// Done: open, it shows its controls, not its question.
	test.Tap(rowOf(t, s.Content(), "Routines installed"))
	mustShow(t, s.Content(), "Templates for any project are below")
	mustNotShow(t, s.Content(), "First, on its own: which of the templates' projects")
	test.Tap(rowOf(t, s.Content(), "Turn on routines"))
	mustNotShow(t, s.Content(), "Turn on standup")
	test.Tap(rowOf(t, s.Content(), "Turn on routines"))
	test.Tap(rowOf(t, s.Content(), "Turn on standup"))
	if n := len(buttons(s.Content(), "Trust and Turn On")); n != 1 {
		t.Errorf("%d Turn On buttons with standup closed", n)
	}
}

// Turn On trusts the routine and runs it on its schedule; the review is
// routines trust --check.
func TestTurnOnTrustsAndResumes(t *testing.T) {
	s, f, _ := newSetupFor(t, map[string]string{
		"setup --json":                          "@setup-claude-code.json",
		"routines trust standup --check --json": "@trust.json",
		"routines trust digest --check --json":  "@trust.json",
	})
	f.mustHaveRun(t, "routines trust standup --check --json", "routines trust digest --check --json")
	mustShow(t, s.Content(), "notify", "notify_me")
	test.Tap(buttons(s.Content(), "Trust and Turn On")[1])
	f.mustHaveRun(t, "routines trust standup --yes --resume --json")
}

// A turn-on step's review names the sign-ins it still needs, each signed
// in from it; a routine already trusted (paused) turns on with Turn On.
func TestTurnOnSignsInFromTheReview(t *testing.T) {
	s, f, _ := newSetupFor(t, map[string]string{
		"setup --json":                          "@setup-claude-code.json",
		"routines trust standup --check --json": `{"routine":"standup","surface":{"servers":["tracker"],"tools":[]},"changes":[],"trusted":true,"signIns":["tracker"]}`,
		"routines trust digest --check --json":  "@trust.json",
	})
	mustShow(t, s.Content(), "tracker needs signing in")
	test.Tap(button(t, s.Content(), "Sign In…"))
	f.mustHaveRun(t, "auth login tracker --browser")
	if len(buttons(s.Content(), "Turn On")) != 1 {
		t.Error("a trusted routine's review doesn't just Turn On")
	}
}

// The backend picker sets the step's setting; the chosen backend's own
// settings (the step's settings) show with the Settings window's controls,
// and set and reset values.
func TestBackendPickerAndItsSettings(t *testing.T) {
	s, f, _ := newSetupFor(t, map[string]string{
		"setup --json":                     "@setup-first.json",
		"settings list --scope app --json": "@settings.json",
		"templates list --json":            "@templates.json",
	})
	c := s.Content()
	mustShow(t, c, "Runs with local (experimental, untested), but no model is set", "llm.local.url", "llm.local.model")
	radio := all[*widget.RadioGroup](c)[0]
	if radio.Selected != "local" {
		t.Fatalf("selected %q", radio.Selected)
	}
	radio.SetSelected("claude-code")
	f.mustHaveRun(t, "settings set llm.backend claude-code --json")

	var model *commitEntry
	for _, e := range all[*commitEntry](s.Content()) {
		if e.Text == "qwen3:8b" {
			model = e
		}
	}
	if model == nil {
		t.Fatal("no field for llm.local.model")
	}
	model.SetText("llama3.3")
	model.OnSubmitted(model.Text)
	f.mustHaveRun(t, "settings set llm.local.model llama3.3 --json")
	// llm.local.model isn't at its default: Reset puts it back.
	test.Tap(button(t, s.Content(), "Reset"))
	f.mustHaveRun(t, "settings reset llm.local.model --json")
}

// The template picker: projects first (none picked), then the templates of
// the picked projects and those for any project, each with its parameters
// once chosen; Install Selected installs the chosen ones with the values
// typed.
func TestTemplatePicker(t *testing.T) {
	s, f, _ := newSetupFor(t, map[string]string{
		"setup --json":                     "@setup-first.json",
		"settings list --scope app --json": "@settings.json",
		"templates list --json":            "@templates.json",
	})
	// Optional, it starts closed.
	mustNotShow(t, s.Content(), "Templates for any project are below")
	test.Tap(rowOf(t, s.Content(), "No routines installed yet"))
	f.mustHaveRun(t, "templates list --json")
	c := s.Content()
	mustShow(t, c, "Templates for any project are below", "standup", "Sends me a short standup note")
	mustNotShow(t, c, "Tells me what changed in the acme project")
	if !button(t, c, "Install Selected").Disabled() {
		t.Error("Install Selected is on with nothing chosen")
	}
	if len(buttons(c, "acme")) != 1 || len(buttons(c, "zeta")) != 1 {
		t.Fatal("no project chips")
	}
	test.Tap(button(t, c, "acme"))
	mustShow(t, s.Content(), "Tells me what changed in the acme project", "Showing templates for any project and for the projects picked.")
	mustNotShow(t, s.Content(), "Reminds me on Fridays")

	// Choose both: standup's parameters show.
	for _, check := range all[*widget.Check](s.Content()) {
		if check.Text == "" {
			test.Tap(check)
		}
	}
	mustShow(t, s.Content(), "team", "The team the note is for", "tone")
	for _, e := range all[*widget.Entry](s.Content()) {
		if e.PlaceHolder == "Required" {
			test.Type(e, "core")
		}
	}
	var tone *widget.Select
	for _, sel := range all[*widget.Select](s.Content()) {
		if sel.Selected == "Default (brief)" {
			tone = sel
		}
	}
	if tone == nil {
		t.Fatal("no picker for tone, with its default")
	}
	tone.SetSelected("detailed")
	test.Tap(button(t, s.Content(), "Install Selected"))
	f.mustHaveRun(t, "templates install projects/acme/digest --json",
		"templates install standup --set team=core --set tone=detailed --json")
}

// Sign-ins: Sign In… runs auth login with --browser; one whose plugin needs
// preparing first shows its steps, and Sign In waits for "I've done this".
// The backend's API key is Enter API Key…; a routine to fix opens its window.
func TestSignInsAndRoutinesToFix(t *testing.T) {
	s, f, opened := newSetupFor(t, map[string]string{
		"setup --json":                     "@setup-signin.json",
		"settings list --scope app --json": "@settings.json",
	})
	c := s.Content()
	mustShow(t, c, "Sign-ins", "1 of 3 signed in", "Routines to fix", "1 to fix",
		"First, set up chat", "chat needs an app of your own first.", "Open https://chat.example.com/apps and create an app.", "1.", "3.")
	var link bool
	for _, r := range all[*widget.RichText](c) {
		for _, seg := range r.Segments {
			if h, ok := seg.(*widget.HyperlinkSegment); ok && h.URL.String() == "https://chat.example.com/apps" {
				link = true
			}
		}
	}
	if !link {
		t.Error("the web address isn't a link")
	}
	signIns := buttons(c, "Sign In…")
	if len(signIns) != 2 || signIns[0].Disabled() || !signIns[1].Disabled() {
		t.Fatalf("Sign In buttons: %d, first off %v", len(signIns), len(signIns) > 0 && signIns[0].Disabled())
	}
	test.Tap(signIns[0])
	f.mustHaveRun(t, "auth login tracker --browser")
	var done *widget.Check
	for _, check := range all[*widget.Check](s.Content()) {
		if check.Text == "I've done this" {
			done = check
		}
	}
	test.Tap(done)
	test.Tap(buttons(s.Content(), "Sign In…")[1])
	f.mustHaveRun(t, "auth login chat --browser")

	test.Tap(button(t, s.Content(), "Enter API Key…"))
	f.mustHaveRun(t, "auth login anthropic --browser")
	test.Tap(button(t, s.Content(), "Open broken…"))
	if !slices.Equal(*opened, []string{"broken"}) {
		t.Errorf("opened %v", *opened)
	}
}

// Errors show in the core's words: a step's action refused, setup unreadable.
func TestErrorsInTheCoresWords(t *testing.T) {
	s, _, _ := newSetupFor(t, map[string]string{
		"setup --json":                                 "@setup-claude-code.json",
		"routines trust standup --check --json":        "@trust.json",
		"routines trust digest --check --json":         "!digest's servers can't be reached: notify doesn't answer",
		"routines trust standup --yes --resume --json": "!standup can't be trusted: notify doesn't answer",
		"mcp connect --json":                           "!Claude Code isn't installed",
	})
	mustShow(t, s.Content(), "Claude Code isn't installed", "digest's servers can't be reached: notify doesn't answer")
	test.Tap(button(t, s.Content(), "Trust and Turn On"))
	mustShow(t, s.Content(), "standup can't be trusted: notify doesn't answer")

	broken, _, _ := newSetupFor(t, map[string]string{"setup --json": "!config.yaml: line 3: not a setting"})
	mustShow(t, broken.Content(), "Couldn't read setup", "config.yaml: line 3: not a setting")
}
