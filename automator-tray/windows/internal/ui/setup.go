package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Setup is the Setup window's content: first-run setup as the core sees it
// (`setup --json`: every step, what's done, what to ask), and doing each
// step through the CLI. After each, the steps are read again: the core
// decides what's done. It mirrors automator-ui's SetupViewModel and
// SetupView.
type Setup struct {
	cli   cli.CLI
	win   fyne.Window
	async Async
	// openRoutine opens a routine's window (a routine to fix).
	openRoutine func(routine string)

	steps     []cli.SetupStep
	done      bool
	remaining int
	loaded    bool
	loadError string
	// errors are the core's messages when a step's action failed, by step id.
	errors map[string]string
	// working are steps waiting for the core (a sign-in waits for the browser).
	working map[string]bool
	// toggled are steps and groups opened or closed by hand; the others
	// are open while not done.
	toggled map[string]bool

	templates       []cli.TemplateInfo
	templatesLoaded bool
	chosen          map[string]bool
	// projects are the projects picked (none at first): only their
	// templates, and those for any project, are offered.
	projects map[string]bool
	// prepared are sign-ins whose preparation I said is done (by step id).
	prepared map[string]bool
	// values are the parameter values typed for each template.
	values map[string]map[string]string
	// backend is the model backend setting (its choices come from the core),
	// and backendSettings what the chosen backend needs set (the step's
	// settings, e.g. the local backend's server and model).
	backend         *cli.Setting
	backendSettings []cli.Setting
	reviews         map[string]cli.TrustOutput
	// autoStarted are auto steps started once already: a failed one waits
	// for its button.
	autoStarted map[string]bool
	generation  int

	scroll *container.Scroll
}

// NewSetup makes the Setup window's content; Load reads where setup stands.
func NewSetup(c cli.CLI, win fyne.Window, async Async, openRoutine func(string)) *Setup {
	s := &Setup{
		cli: c, win: win, async: async, openRoutine: openRoutine,
		errors: map[string]string{}, working: map[string]bool{}, toggled: map[string]bool{},
		chosen: map[string]bool{}, projects: map[string]bool{}, prepared: map[string]bool{},
		values: map[string]map[string]string{}, reviews: map[string]cli.TrustOutput{}, autoStarted: map[string]bool{},
	}
	s.scroll = container.NewVScroll(container.NewVBox())
	s.refresh()
	return s
}

// Content is what the window shows.
func (s *Setup) Content() fyne.CanvasObject { return s.scroll }

func stepID(step cli.SetupStep) string { return step.Step + ":" + step.Subject }

// isOpen: steps start open while they're to do; optional ones start closed.
func (s *Setup) isOpen(step cli.SetupStep) bool {
	if open, ok := s.toggled[stepID(step)]; ok {
		return open
	}
	return !step.Done && !step.Optional
}

func (s *Setup) toggle(step cli.SetupStep) {
	open := !s.isOpen(step)
	s.toggled[stepID(step)] = open
	if open {
		s.prepare(step)
	}
	s.refresh()
}

// Load reads where setup stands, then reads what open steps show and does
// the steps the core does without asking (once each).
func (s *Setup) Load() {
	s.generation++
	mine := s.generation
	s.async(func() func() {
		out, err := s.cli.Setup()
		return func() {
			if mine != s.generation {
				return
			}
			if err != nil {
				s.loadError = err.Error()
			} else {
				s.steps, s.done, s.remaining, s.loadError = out.Steps, out.Done, out.Remaining, ""
				// Reviews are read again: one taken before a sign-in is out of date.
				s.reviews = map[string]cli.TrustOutput{}
				for _, step := range out.Steps {
					if s.isOpen(step) {
						s.prepare(step)
					}
				}
				for _, step := range out.Steps {
					if step.Auto && !step.Done && !s.autoStarted[stepID(step)] {
						s.autoStarted[stepID(step)] = true
						s.act(step)
					}
				}
			}
			s.loaded = true
			s.refresh()
		}
	})
}

// prepare reads what an open step shows: the templates, the backend's
// choices, a routine's trust review.
func (s *Setup) prepare(step cli.SetupStep) {
	switch step.Step {
	case "templates":
		if !s.templatesLoaded {
			s.loadTemplates(step)
		}
	case "backend":
		s.loadBackend(step)
	case "turn-on":
		if _, ok := s.reviews[step.Subject]; step.Subject != "" && !ok {
			s.review(step)
		}
	}
}

// act does a step that's one action with no answers to give (an auto one).
func (s *Setup) act(step cli.SetupStep) {
	switch step.Step {
	case "mcp":
		s.connectClaudeCode(step)
	case "usage":
		s.setUpUsage(step)
	}
}

// run does a step's action through the CLI, shows the core's message if it
// fails, then reads the steps again.
func (s *Setup) run(step cli.SetupStep, work func() error, then func()) {
	id := stepID(step)
	s.working[id] = true
	s.refresh()
	s.async(func() func() {
		err := work()
		return func() {
			delete(s.working, id)
			s.setError(id, err)
			if then != nil {
				then()
			}
			s.Load()
		}
	})
}

func (s *Setup) setError(id string, err error) {
	if err != nil {
		s.errors[id] = err.Error()
	} else {
		delete(s.errors, id)
	}
}

// Templates

func (s *Setup) loadTemplates(step cli.SetupStep) {
	s.async(func() func() {
		list, err := s.cli.Templates()
		return func() {
			if err != nil {
				s.errors[stepID(step)] = err.Error()
			} else {
				s.templates, s.templatesLoaded = list, true
			}
			s.refresh()
		}
	})
}

// allProjects are the projects templates are for, in the order they're listed.
func (s *Setup) allProjects() []string {
	var seen []string
	for _, t := range s.templates {
		if t.Project != "" && !slices.Contains(seen, t.Project) {
			seen = append(seen, t.Project)
		}
	}
	return seen
}

// offered are the templates for any project, and the chosen projects'.
func (s *Setup) offered() []cli.TemplateInfo {
	var out []cli.TemplateInfo
	for _, t := range s.templates {
		if t.Project == "" || s.projects[t.Project] {
			out = append(out, t)
		}
	}
	return out
}

// install installs the chosen templates, each with the values typed for it
// (empty ones left to the template's defaults); the core says what's missing.
func (s *Setup) install(step cli.SetupStep) {
	type pick struct {
		id     string
		values []cli.Value
	}
	var picked []pick
	for _, t := range s.offered() {
		if !s.chosen[t.ID] {
			continue
		}
		p := pick{id: t.ID}
		for name, v := range s.values[t.ID] {
			if v != "" {
				p.values = append(p.values, cli.Value{Name: name, Value: v})
			}
		}
		slices.SortFunc(p.values, func(a, b cli.Value) int { return strings.Compare(a.Name, b.Name) })
		picked = append(picked, p)
	}
	s.run(step, func() error {
		var failures []string
		for _, p := range picked {
			if err := s.cli.InstallTemplate(p.id, p.values); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", p.id, err))
			}
		}
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "\n"))
		}
		return nil
	}, func() {
		s.chosen = map[string]bool{}
		s.loadTemplates(step)
	})
}

// Backend

func (s *Setup) loadBackend(step cli.SetupStep) {
	s.async(func() func() {
		list, err := s.cli.AppSettings()
		return func() {
			if err != nil {
				s.errors[stepID(step)] = err.Error()
				s.refresh()
				return
			}
			s.backend, s.backendSettings = nil, nil
			for i := range list {
				if list[i].Key == step.Setting {
					s.backend = &list[i]
				}
			}
			for _, key := range step.Settings {
				for _, setting := range list {
					if setting.Key == key {
						s.backendSettings = append(s.backendSettings, setting)
					}
				}
			}
			if s.backend == nil {
				s.errors[stepID(step)] = "The core lists no setting for this step."
			}
			s.refresh()
		}
	})
}

func (s *Setup) setBackend(step cli.SetupStep, value string) {
	if s.backend == nil {
		return
	}
	key := s.backend.Key
	s.run(step, func() error {
		_, err := s.cli.SetSetting(key, value)
		return err
	}, func() { s.loadBackend(step) })
}

// setBackendSetting sets (or, with reset, resets) one of the backend's own settings.
func (s *Setup) setBackendSetting(step cli.SetupStep, key, value string, reset bool) {
	s.run(step, func() error {
		var err error
		if reset {
			_, err = s.cli.ResetSetting(key)
		} else {
			_, err = s.cli.SetSetting(key, value)
		}
		return err
	}, func() { s.loadBackend(step) })
}

// Sign-in, Claude Code, turning routines on

// signIn signs in where the step says (a server, or the API key): OAuth in
// the browser, keys on a local page there.
func (s *Setup) signIn(step cli.SetupStep) {
	if step.SignIn == "" {
		return
	}
	s.run(step, func() error { return s.cli.SignIn(step.SignIn) }, nil)
}

func (s *Setup) review(step cli.SetupStep) {
	s.async(func() func() {
		r, err := s.cli.TrustCheck(step.Subject)
		return func() {
			if err != nil {
				s.errors[stepID(step)] = err.Error()
			} else {
				s.reviews[step.Subject] = r
			}
			s.refresh()
		}
	})
}

// turnOn trusts the routine with what it can reach and lets it run on its
// schedule, in one step (after its review).
func (s *Setup) turnOn(step cli.SetupStep) {
	s.run(step, func() error { return s.cli.TurnOn(step.Subject) }, nil)
}

// connectClaudeCode registers the core's MCP server with Claude Code.
func (s *Setup) connectClaudeCode(step cli.SetupStep) {
	s.run(step, s.cli.ConnectClaudeCode, nil)
}

// setUpUsage has the core record the plan's usage from Claude Code's status line.
func (s *Setup) setUpUsage(step cli.SetupStep) {
	s.run(step, s.cli.InstallStatusLine, nil)
}

// The view

// refresh draws the window again from where things stand, keeping its
// scroll position.
func (s *Setup) refresh() {
	setContent(s.scroll, padded(16, 16, 16, 16, s.render()))
}

func (s *Setup) render() fyne.CanvasObject {
	box := container.NewVBox(s.header())
	if s.loadError != "" {
		box.Add(banner("Couldn't read setup", s.loadError))
	}
	for _, g := range groupSteps(s.steps) {
		if g.single() {
			box.Add(card(false, s.stepView(g.steps[0], true)))
		} else {
			box.Add(card(false, s.groupView(g)...))
		}
	}
	return box
}

func (s *Setup) header() fyne.CanvasObject {
	icon := widget.NewIcon(primaryIcon("list"))
	if s.done {
		icon = widget.NewIcon(successIcon("check"))
	}
	titles := container.NewVBox(heading("Setup", true))
	if s.loaded {
		subtitle := "Everything is set up."
		switch {
		case s.done:
		case s.remaining == 1:
			subtitle = "1 step to go."
		default:
			subtitle = fmt.Sprintf("%d steps to go.", s.remaining)
		}
		titles.Add(secondary(subtitle))
	}
	again := widget.NewButtonWithIcon("Check Again", refreshIcon(), s.Load)
	return container.NewBorder(nil, nil, container.NewCenter(sized(32, icon)), container.NewCenter(again), titles)
}

// stepGroup is the steps of one kind.
type stepGroup struct {
	kind  string
	steps []cli.SetupStep
}

// groupSteps groups the steps by kind, in the order the core lists them.
func groupSteps(steps []cli.SetupStep) []stepGroup {
	var groups []stepGroup
	for _, step := range steps {
		if n := len(groups); n > 0 && groups[n-1].kind == step.Step {
			groups[n-1].steps = append(groups[n-1].steps, step)
		} else {
			groups = append(groups, stepGroup{kind: step.Step, steps: []cli.SetupStep{step}})
		}
	}
	return groups
}

func (g stepGroup) id() string { return "group:" + g.kind }

func (g stepGroup) allDone() bool {
	for _, step := range g.steps {
		if !step.Done {
			return false
		}
	}
	return true
}

// single: templates, the model backend, plan usage and Claude Code are one
// step each, a row of their own, not a group of one.
func (g stepGroup) single() bool {
	return len(g.steps) == 1 && slices.Contains([]string{"templates", "backend", "usage", "mcp"}, g.kind)
}

// title is the UI's name for the core's kind of step.
func (g stepGroup) title() string {
	switch g.kind {
	case "templates":
		return "Templates"
	case "backend":
		return "Model backend"
	case "usage":
		return "Plan usage"
	case "mcp":
		return "Claude Code"
	case "routine":
		return "Routines to fix"
	case "sign-in":
		return "Sign-ins"
	case "turn-on":
		return "Turn on routines"
	}
	return g.kind
}

// progress says how far along it is: "4 of 5 signed in".
func (g stepGroup) progress() string {
	done := 0
	for _, step := range g.steps {
		if step.Done {
			done++
		}
	}
	of := fmt.Sprintf("%d of %d", done, len(g.steps))
	switch g.kind {
	case "routine":
		switch left := len(g.steps) - done; left {
		case 0:
			return "All fixed"
		case 1:
			return "1 to fix"
		default:
			return fmt.Sprintf("%d to fix", left)
		}
	case "sign-in":
		return of + " signed in"
	case "turn-on":
		return of + " on"
	}
	return of + " done"
}

// groupView is a group's header showing how far along it is (the whole row
// opens and closes it), then, when open, a row per step.
func (s *Setup) groupView(g stepGroup) []fyne.CanvasObject {
	open, ok := s.toggled[g.id()]
	if !ok {
		open = !g.allDone()
	}
	progress := widget.NewLabel(g.progress())
	if g.allDone() {
		progress.Importance = widget.LowImportance
	}
	trailing := container.NewHBox(progress)
	if g.allDone() {
		trailing.Objects = append([]fyne.CanvasObject{container.NewCenter(widget.NewIcon(successIcon("check")))}, trailing.Objects...)
	}
	objects := []fyne.CanvasObject{newDisclosure(open, true, func() {
		s.toggled[g.id()] = !open
		s.refresh()
	}, container.NewBorder(nil, nil, nil, trailing, heading(g.title(), true)))}
	if !open {
		return objects
	}
	for _, step := range g.steps {
		objects = append(objects, divider())
		if step.Step == "turn-on" {
			objects = append(objects, s.stepView(step, false))
		} else {
			objects = append(objects, s.stepRow(step))
		}
	}
	return objects
}

// stepView is a step that opens to what it needs: the templates to pick,
// the backend's choice, a routine's review before turning it on.
func (s *Setup) stepView(step cli.SetupStep, prominent bool) fyne.CanvasObject {
	open := s.isOpen(step)
	row := newDisclosure(open, prominent, func() { s.toggle(step) },
		container.NewBorder(nil, nil, container.NewCenter(mark(step)), container.NewCenter(s.stepTags(step)), stepText(step, prominent, open)))
	if !open {
		return row
	}
	body := container.NewVBox()
	// The question is for doing the step; a done one shows its controls only.
	if step.Ask != "" && !step.Done {
		body.Add(text(step.Ask))
	}
	if a := s.action(step); a != nil {
		body.Add(a)
	}
	if step.SignIn != "" {
		body.Add(s.signInButton(step, true))
	}
	if msg := s.errors[stepID(step)]; msg != "" {
		body.Add(errorRow(msg))
	}
	return container.NewVBox(row, padded(0, 12, 48, rowInset, body))
}

func (s *Setup) action(step cli.SetupStep) fyne.CanvasObject {
	id := stepID(step)
	switch step.Step {
	case "templates":
		return s.templatePicker(step)
	case "backend":
		return s.backendPicker(step)
	case "turn-on":
		// What it can reach, then one button: trusting it and running it
		// on its schedule (the question above says which it needs).
		if r, ok := s.reviews[step.Subject]; ok {
			var turnOn func()
			if !step.Done {
				turnOn = func() { s.turnOn(step) }
			}
			// The sign-ins it still needs, from the review, as a window's
			// Turn On… shows them; then the one confirm.
			var rows []fyne.CanvasObject
			for _, name := range r.SignIns {
				n := name
				signIn := s.button("Sign In…", step, func() { s.run(step, func() error { return s.cli.SignIn(n) }, nil) })
				rows = append(rows, padded(4, 0, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, signIn,
					widget.NewLabel(n+" needs signing in"))))
			}
			return card(true, append(rows, trustReview(r, r.ConfirmTitle(), "", turnOn, s.working[id], nil)...)...)
		}
		if s.errors[id] == "" {
			return spinner()
		}
	case "usage":
		// The core wraps my Claude Code status line to record usage.
		if !step.Done {
			return s.button("Set Up", step, func() { s.setUpUsage(step) })
		}
	case "mcp":
		// Done without asking when the window shows it; the button is for
		// trying again after a failure.
		if !step.Done {
			return s.button("Connect", step, func() { s.connectClaudeCode(step) })
		}
	}
	return nil
}

// button is a step's button, off while the core works on the step.
func (s *Setup) button(label string, step cli.SetupStep, tapped func()) fyne.CanvasObject {
	b := widget.NewButton(label, tapped)
	if s.working[stepID(step)] {
		b.Disable()
	}
	return container.NewHBox(b)
}

// stepRow is a step done with one button, right on its row: a sign-in, a
// routine to fix (its window shows what's wrong).
func (s *Setup) stepRow(step cli.SetupStep) fyne.CanvasObject {
	prepare := ""
	if !step.Done {
		prepare = step.Prepare
	}
	trailing := container.NewHBox()
	if !step.Done && prepare == "" {
		switch {
		case step.Step == "sign-in" && step.SignIn != "":
			trailing.Add(s.signInButton(step, true))
		case step.Step == "routine" && step.Subject != "":
			routine := step.Subject
			trailing.Add(container.NewCenter(widget.NewButton("Open "+routine+"…", func() { s.openRoutine(routine) })))
		}
	}
	trailing.Add(container.NewCenter(s.stepTags(step)))
	box := container.NewVBox(container.NewBorder(nil, nil, container.NewCenter(mark(step)), trailing, stepText(step, false, true)))
	if prepare != "" {
		box.Add(padded(4, 0, 28, 0, s.preparation(step, prepare)))
	}
	if msg := s.errors[stepID(step)]; msg != "" {
		box.Add(padded(0, 0, 28, 0, errorRow(msg)))
	}
	return padded(4, 4, rowInset+20, rowInset, box)
}

// stepText is a step's title and the core's status under it.
func stepText(step cli.SetupStep, prominent, expanded bool) fyne.CanvasObject {
	status := secondary(step.Status)
	if !expanded {
		status.Wrapping, status.Truncation = fyne.TextWrapOff, fyne.TextTruncateEllipsis
	}
	return container.NewVBox(heading(step.Title, prominent), status)
}

// stepTags is Done, Optional, or a spinner while the core works on it.
func (s *Setup) stepTags(step cli.SetupStep) fyne.CanvasObject {
	switch {
	case s.working[stepID(step)]:
		return spinner()
	case step.Done:
		return tag("Done", successColour)
	case step.Optional:
		return tag("Optional", "")
	}
	return container.NewHBox()
}

// signInButton is Sign In… (or Enter API Key… for the backend), where the
// step's signIn says; it waits for the browser.
func (s *Setup) signInButton(step cli.SetupStep, enabled bool) fyne.CanvasObject {
	busy := s.working[stepID(step)]
	label := "Sign In…"
	if step.Step == "backend" {
		label = "Enter API Key…"
	}
	b := widget.NewButton(label, func() { s.signIn(step) })
	if busy || !enabled {
		b.Disable()
	}
	box := container.NewHBox()
	if busy {
		box.Add(secondary("Finish in your browser."))
	}
	box.Add(b)
	return box
}

// templatePicker is the templates to install as routines: none chosen at
// first; a chosen one shows its parameters (blank ones take the
// template's default).
func (s *Setup) templatePicker(step cli.SetupStep) fyne.CanvasObject {
	if !s.templatesLoaded {
		if s.errors[stepID(step)] == "" {
			return spinner()
		}
		return nil
	}
	box := container.NewVBox()
	if projects := s.allProjects(); len(projects) > 0 {
		box.Add(s.projectPicker(projects))
	}
	if offered := s.offered(); len(offered) > 0 {
		var rows []fyne.CanvasObject
		for i, t := range offered {
			if i > 0 {
				rows = append(rows, padded(0, 0, 12, 0, widget.NewSeparator()))
			}
			rows = append(rows, s.templateRow(t))
		}
		box.Add(card(true, rows...))
	}
	if len(s.templates) == 0 {
		box.Add(secondary("No templates to install."))
		return box
	}
	install := widget.NewButton("Install Selected", func() { s.install(step) })
	install.Importance = widget.HighImportance
	picked := false
	for _, t := range s.offered() {
		picked = picked || s.chosen[t.ID]
	}
	if !picked || s.working[stepID(step)] {
		install.Disable()
	}
	box.Add(right(install))
	return box
}

// projectPicker is which projects I work on, as chips (none picked at
// first): the templates offered follow.
func (s *Setup) projectPicker(projects []string) fyne.CanvasObject {
	var chips []fyne.CanvasObject
	for _, p := range projects {
		on := s.projects[p]
		icon := addIcon()
		if on {
			icon = checkIcon()
		}
		b := widget.NewButtonWithIcon(p, icon, func() {
			if on {
				delete(s.projects, p)
			} else {
				s.projects[p] = true
			}
			s.refresh()
		})
		if on {
			b.Importance = widget.HighImportance
		}
		chips = append(chips, b)
	}
	note := "Templates for any project are below; pick your projects to see theirs too."
	if len(s.projects) > 0 {
		note = "Showing templates for any project and for the projects picked."
	}
	return container.NewVBox(newFlow(chips...), secondary(note))
}

func (s *Setup) templateRow(t cli.TemplateInfo) fyne.CanvasObject {
	check := widget.NewCheck("", nil)
	check.Checked = s.chosen[t.ID]
	check.OnChanged = func(on bool) {
		if on {
			s.chosen[t.ID] = true
		} else {
			delete(s.chosen, t.ID)
		}
		s.refresh()
	}
	if t.Installed {
		check.Disable()
	}
	id := widget.NewLabel(t.ID)
	id.TextStyle = fyne.TextStyle{Monospace: true}
	id.SizeName = captionSize
	id.Importance = widget.LowImportance
	name := heading(t.Name, false)
	name.Wrapping = fyne.TextWrapOff // on a line with its id
	title := container.NewHBox(name, id)
	if t.Installed {
		title.Add(tag("Installed", ""))
	}
	about := container.NewVBox(title)
	if t.Description != "" {
		about.Add(secondary(t.Description))
	}
	box := container.NewVBox(container.NewBorder(nil, nil, container.NewVBox(check), nil, about))
	if s.chosen[t.ID] && len(t.Params) > 0 {
		params := container.NewVBox()
		for _, p := range t.Params {
			params.Add(s.paramField(t.ID, p))
		}
		box.Add(padded(0, 0, 36, 0, params))
	}
	return padded(2, 2, 4, 8, box)
}

// paramField is a template parameter: its name (marked when required), a
// field showing its default, its description.
func (s *Setup) paramField(template string, p cli.Param) fyne.CanvasObject {
	placeholder := p.DefaultText
	if placeholder == "" && p.Required {
		placeholder = "Required"
	}
	value := s.values[template][p.Name]
	keep := func(v string) {
		if s.values[template] == nil {
			s.values[template] = map[string]string{}
		}
		s.values[template][p.Name] = v
	}
	name := widget.NewLabel(p.Name)
	label := container.NewHBox(name)
	if p.Required {
		star := widget.NewLabel("*")
		star.Importance = widget.DangerImportance
		star.TextStyle = fyne.TextStyle{Bold: true}
		label.Add(star)
	}
	var field fyne.CanvasObject
	if len(p.Enum) > 0 {
		first := "Default"
		if placeholder != "" {
			first = "Default (" + placeholder + ")"
		}
		selected := first
		if value != "" {
			selected = value
		}
		_, field = picker(append([]string{first}, p.Enum...), selected, func(v string) {
			if v == first {
				v = ""
			}
			keep(v)
		})
	} else {
		e := widget.NewEntry()
		e.SetText(value)
		e.PlaceHolder = placeholder
		e.OnChanged = keep
		field = fixedWidth(260, e)
	}
	box := container.NewVBox(container.NewBorder(nil, nil, fixedWidth(120, label), nil, container.NewHBox(field)))
	if p.Description != "" {
		box.Add(padded(0, 0, 124, 0, secondary(p.Description)))
	}
	return box
}

// backendPicker is the model backend: the setting's choices as the core
// gives them (the step's question says what each is), then what the chosen
// backend needs set (local: its server and model), each with the Settings
// window's control.
func (s *Setup) backendPicker(step cli.SetupStep) fyne.CanvasObject {
	if s.backend == nil {
		if s.errors[stepID(step)] == "" {
			return spinner()
		}
		return nil
	}
	busy := s.working[stepID(step)]
	choices := widget.NewRadioGroup(s.backend.Enum, nil)
	choices.Horizontal = true
	choices.Required = true
	choices.Selected = s.backend.ValueText
	choices.OnChanged = func(v string) {
		if v != "" && v != s.backend.ValueText {
			s.setBackend(step, v)
		}
	}
	if busy {
		choices.Disable()
	}
	box := container.NewVBox(choices)
	for _, setting := range s.backendSettings {
		key := setting.Key
		box.Add(card(true, settingRow(s.win, setting, key, "", busy,
			func(v string) { s.setBackendSetting(step, key, v, false) },
			func() { s.setBackendSetting(step, key, "", true) })))
	}
	return box
}

// preparation is what to do outside the app before signing in (e.g.
// creating a Slack app), as the core words it: its intro, then numbered
// steps (links open in the browser). Sign In waits for "I've done this".
func (s *Setup) preparation(step cli.SetupStep, prepare string) fyne.CanvasObject {
	id := stepID(step)
	subject := step.Subject
	if subject == "" {
		subject = "this"
	}
	lines := container.NewVBox()
	for _, l := range numbered(prepare) {
		number := widget.NewLabel(l.number)
		number.Importance = widget.LowImportance
		number.TextStyle = fyne.TextStyle{Bold: true}
		number.Alignment = fyne.TextAlignTrailing
		lines.Add(container.NewBorder(nil, nil, container.NewVBox(fixedWidth(28, number)), nil, rich(l.text)))
	}
	done := widget.NewCheck("I've done this", nil)
	done.Checked = s.prepared[id]
	done.OnChanged = func(on bool) {
		if on {
			s.prepared[id] = true
		} else {
			delete(s.prepared, id)
		}
		s.refresh()
	}
	title := container.NewBorder(nil, nil, widget.NewIcon(primaryIcon("list")), nil, heading("First, set up "+subject, false))
	return tinted(container.NewVBox(title, lines,
		container.NewBorder(nil, nil, done, s.signInButton(step, s.prepared[id]))))
}

// trustReview is what a routine can reach: its servers, tools (writes
// tinted), built-in tools, folder, and what changed since it was trusted;
// with a button (action: Turn On, Trust) when do is given, and the note
// beside it, if any. A program's is what it runs, with signIn (when given)
// under its secrets.
func trustReview(r cli.TrustOutput, action, note string, do func(), working bool, signIn fyne.CanvasObject) []fyne.CanvasObject {
	if r.Program != nil {
		return reviewButton(append(programRows(*r.Program, signIn), changedRows(r.Changes)...), action, note, do, working)
	}
	writes := map[string]bool{}
	for _, t := range r.Tools {
		if !t.ReadOnly {
			writes[t.Name] = true
		}
	}
	tools := []fyne.CanvasObject{chips(r.Surface.Tools, writes)}
	if len(writes) > 0 {
		tools = append(tools, secondary("Tinted tools write (they're intercepted in dry runs)."))
	}
	objects := []fyne.CanvasObject{
		padded(6, 0, 0, 0, infoRow("Servers", chips(r.Surface.Servers, nil))),
		infoRow("Tools", tools...),
	}
	if len(r.Surface.BuiltinTools) > 0 {
		objects = append(objects, infoRow("Built-in tools", chips(r.Surface.BuiltinTools, nil)))
	}
	if r.Surface.Folder != "" {
		folder := widget.NewLabel(r.Surface.Folder)
		folder.TextStyle = fyne.TextStyle{Monospace: true}
		folder.Wrapping = fyne.TextWrapBreak
		objects = append(objects, infoRow("Folder", folder))
	}
	return reviewButton(append(objects, changedRows(r.Changes)...), action, note, do, working)
}

// changedRows say what changed since it was trusted.
func changedRows(changed []string) []fyne.CanvasObject {
	if len(changed) == 0 {
		return nil
	}
	var changes []fyne.CanvasObject
	for _, c := range changed {
		changes = append(changes, text(c))
	}
	return []fyne.CanvasObject{infoRow("Changed", changes...)}
}

// reviewButton adds the review's button (when do is given) and the note
// beside it.
func reviewButton(objects []fyne.CanvasObject, action, note string, do func(), working bool) []fyne.CanvasObject {
	if do != nil {
		b := widget.NewButton(action, do)
		b.Importance = widget.HighImportance
		if working {
			b.Disable()
		}
		row := right(b)
		if note != "" {
			row = container.NewBorder(nil, nil, nil, container.NewCenter(b), secondary(note))
		}
		objects = append(objects, divider(), padded(4, 4, rowInset, rowInset-8, row))
	}
	return append(objects, padded(0, 6, 0, 0, container.NewHBox()))
}
