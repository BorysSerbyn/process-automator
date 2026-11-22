package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// The tabs of a routine's window.
const (
	OverviewTab = "Overview"
	RunsTab     = "Runs"
	MemoriesTab = "Memories"
	SettingsTab = "Settings"
)

var routineTabs = []string{OverviewTab, RunsTab, MemoriesTab, SettingsTab}

// RunsShown is how many of a routine's latest runs its window lists.
const RunsShown = 50

// Routine is a routine's window's content: the routine (`routines show`),
// its entry in run status, its controls (run now or a dry run, stop, pause
// or resume, delete), its trust review, runs, memories and settings, all
// as the core gives them. Every change goes through the CLI, then what
// changed is read again. It mirrors automator-ui's RoutineModel and
// RoutineDetailView.
type Routine struct {
	cli   cli.CLI
	win   fyne.Window
	async Async
	name  string
	now   func() time.Time
	// confirm asks before something that can't be undone (a dialog).
	confirm func(title, message, action string, yes func())
	// deleted is called once the routine is deleted (its window closes).
	deleted func()

	tab       string
	detail    *cli.RoutineDetail
	loadError string
	status    *cli.RunStatus

	settings []cli.Setting
	errors   map[string]string

	history  *runHistory
	memories *memoryList

	review     *cli.TrustOutput
	trustError string
	accessOpen bool

	// turnOn is its one review and confirm; reviewWhenLoaded opens it once
	// the routine is read, if it needs turning on (from the menu, a reason).
	turnOn           *turnOn
	reviewWhenLoaded bool
	// granter is Grant… for the permissions it needs; watchingPermissions,
	// whether it's read again while one isn't allowed.
	granter             *granter
	watchingPermissions bool

	// signingIn is a program's sign-in to its secrets, waiting for me.
	signingIn   bool
	signInError string

	// actionError is why the last control failed; working, that one waits
	// for the core.
	actionError string
	working     bool

	scroll *container.Scroll
}

// NewRoutine makes a routine's window's content; Load reads it, Update gives
// it run status.
func NewRoutine(c cli.CLI, win fyne.Window, async Async, name string, deleted func()) *Routine {
	r := &Routine{cli: c, win: win, async: async, name: name, now: time.Now, deleted: deleted, tab: OverviewTab,
		errors: map[string]string{}}
	r.history = newRunHistory(c, async, name, func() time.Time { return r.now() }, r.refresh,
		func(msg string) { r.actionError = msg; r.refresh() })
	r.memories = newMemoryList(c, async, name, r.refresh)
	r.turnOn = newTurnOn(c, async, name, false, r.summary, r.refresh, r.Load)
	r.granter = newGranter(c, async, r.refresh, r.loadDetail)
	r.confirm = func(title, message, action string, yes func()) {
		d := dialog.NewConfirm(title, message, func(ok bool) {
			if ok {
				yes()
			}
		}, win)
		d.SetConfirmText(action)
		d.SetConfirmImportance(widget.DangerImportance)
		d.Show()
	}
	r.scroll = container.NewVScroll(container.NewVBox())
	r.refresh()
	return r
}

// Content is what the window shows.
func (r *Routine) Content() fyne.CanvasObject { return r.scroll }

// entry is the routine's entry in run status.
func (r *Routine) entry() *cli.RoutineStatus { return r.status.Routine(r.name) }

// running is whether a run is going or waiting its turn: Stop replaces Run Now.
func (r *Routine) running() bool {
	e := r.entry()
	return e != nil && e.LastRun.InProgress()
}

// Load reads everything again (its memories once it's known to be an LLM
// routine: a program has none).
func (r *Routine) Load() {
	r.loadDetail()
	r.loadSettings()
	r.history.load()
}

// isProgram is whether it's a program, not an LLM routine.
func (r *Routine) isProgram() bool { return r.detail != nil && r.detail.IsProgram() }

// tabs are the tabs it has: a program has no memories.
func (r *Routine) tabs() []string {
	if !r.isProgram() {
		return routineTabs
	}
	return []string{OverviewTab, RunsTab, SettingsTab}
}

// Update shows a new run status; when the routine's last run changed, its
// runs and detail are read again.
func (r *Routine) Update(s *cli.RunStatus) {
	var before, after cli.RoutineStatus
	if e := r.entry(); e != nil {
		before = *e
	}
	r.status = s
	if e := r.entry(); e != nil {
		after = *e
	}
	r.refresh()
	if !sameRun(before.LastRun, after.LastRun) || before.Problem != after.Problem {
		r.loadDetail()
		r.history.load()
		r.turnOn.statusChanged()
	}
}

func sameRun(a, b *cli.LastRun) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Outcome == b.Outcome
}

// ShowTab shows one of the window's tabs.
func (r *Routine) ShowTab(tab string) {
	r.tab = tab
	r.refresh()
}

func (r *Routine) loadDetail() {
	r.async(func() func() {
		d, err := r.cli.Routine(r.name)
		return func() {
			if err != nil {
				r.loadError = err.Error()
			} else {
				r.detail, r.loadError = &d, ""
				r.watchPermissions()
				if !d.IsProgram() && !r.memories.loaded {
					r.memories.load()
				}
				if r.reviewWhenLoaded {
					r.reviewWhenLoaded = false
					if d.TurnOn {
						r.turnOn.start()
					}
				}
			}
			r.refresh()
		}
	})
}

func (r *Routine) loadSettings() {
	r.async(func() func() {
		list, err := r.cli.RoutineSettings(r.name)
		return func() {
			if err != nil {
				r.loadError = err.Error()
			} else {
				r.settings = list
			}
			r.refresh()
		}
	})
}

// The runs

// showRun shows one run: the Runs tab, with it open.
func (r *Routine) showRun(id string) {
	r.tab = RunsTab
	r.history.open(id)
	r.refresh()
}

// The controls

func (r *Routine) control(work func() error, then func()) {
	r.working = true
	r.refresh()
	r.async(func() func() {
		err := work()
		return func() {
			r.working = false
			if err != nil {
				r.actionError = err.Error()
			} else {
				r.actionError = ""
				if then != nil {
					then()
				}
			}
			r.refresh()
		}
	})
}

func (r *Routine) runNow(dryRun bool) {
	r.control(func() error { return r.cli.StartRun(r.name, dryRun) }, nil)
}

func (r *Routine) stop() { r.control(func() error { return r.cli.StopRun(r.name) }, nil) }

func (r *Routine) setPaused(paused bool) {
	r.control(func() error { return r.cli.SetPaused(r.name, paused) }, r.Load)
}

func (r *Routine) askDelete() {
	what := "Its file, settings, trust and memories are deleted. Its run history is kept."
	if r.isProgram() {
		what = "Its copy, settings, trust and secrets are deleted. Its run history and data folder are kept."
	}
	r.confirm("Delete "+r.name+"?", what, "Delete", func() {
		r.control(func() error { return r.cli.DeleteRoutine(r.name) }, r.deleted)
	})
}

// Settings

func (r *Routine) set(key, value string) {
	r.apply(key, func() error {
		_, err := r.cli.SetSetting(key, value)
		return err
	})
}

func (r *Routine) reset(key string) {
	r.apply(key, func() error {
		_, err := r.cli.ResetSetting(key)
		return err
	})
}

func (r *Routine) apply(key string, change func() error) {
	r.async(func() func() {
		err := change()
		return func() {
			if err != nil {
				r.errors[key] = err.Error()
			} else {
				delete(r.errors, key)
			}
			r.loadSettings()
			r.loadDetail()
		}
	})
}

// Memories

// Trust

func (r *Routine) toggleAccess() {
	r.accessOpen = !r.accessOpen
	if r.accessOpen && r.review == nil {
		r.loadTrust()
	}
	r.refresh()
}

func (r *Routine) loadTrust() {
	r.async(func() func() {
		review, err := r.cli.TrustCheck(r.name)
		return func() {
			if err != nil {
				r.trustError = err.Error()
			} else {
				r.review, r.trustError = &review, ""
			}
			r.refresh()
		}
	})
}

// signIn signs in to a program's secrets (`auth login <program>
// --browser`): the core opens a page in the browser to enter them, as
// setup's sign-in step does. Then what's stored is read again.
func (r *Routine) signIn() {
	r.signingIn = true
	r.refresh()
	r.async(func() func() {
		err := r.cli.SignIn(r.name)
		return func() {
			r.signingIn, r.signInError = false, ""
			if err != nil {
				r.signInError = err.Error()
			}
			r.loadDetail()
			if r.review != nil {
				r.loadTrust()
			}
			r.refresh()
		}
	})
}

// signInRow is Sign In… under a program's secrets while one isn't stored
// (or the wait for it, and why it failed); nil when none is missing.
func (r *Routine) signInRow(p *cli.ProgramInfo) fyne.CanvasObject {
	return signInRow(p, r.signingIn, r.signInError, r.signIn)
}

// signInRow is Sign In… under an executable's secrets (a program's, a
// hook's, a watcher's) while one isn't stored, or the wait for it
// (signingIn), and why it failed; nil when none is missing.
func signInRow(p *cli.ProgramInfo, signingIn bool, problem string, start func()) fyne.CanvasObject {
	if !needsSignIn(p) && problem == "" {
		return nil
	}
	box := container.NewVBox()
	switch {
	case signingIn:
		box.Add(container.NewHBox(spinner(), quiet("Enter them on the page in your browser…")))
	case needsSignIn(p):
		box.Add(container.NewHBox(widget.NewButton("Sign In…", start), quiet("Opens your browser to enter them.")))
	}
	if problem != "" {
		box.Add(errorRow(problem))
	}
	return box
}

// Review opens its Turn On review, once it's read, if it needs turning on
// (the window opened, or raised, from the menu or a reason).
func (r *Routine) Review() {
	if r.detail == nil {
		r.reviewWhenLoaded = true
		return
	}
	if r.detail.TurnOn {
		r.turnOn.start()
	}
}

// watchPermissions reads it again now and then while a permission it needs
// isn't allowed (the scheduler records it within a pass).
func (r *Routine) watchPermissions() {
	if r.watchingPermissions || len(cli.BlockingPermissions(r.detail.Permissions)) == 0 {
		return
	}
	r.watchingPermissions = true
	pollPermissions(r.async, func() bool {
		r.watchingPermissions = r.detail != nil && len(cli.BlockingPermissions(r.detail.Permissions)) > 0
		return r.watchingPermissions
	}, r.loadDetail)
}

// summary is what it does, in a line, for its review.
func (r *Routine) summary() string {
	d := r.detail
	if d == nil {
		return ""
	}
	var parts []string
	if d.Description != "" {
		parts = append(parts, d.Description)
	}
	if d.ScheduleText != "" {
		parts = append(parts, "Runs: "+d.ScheduleText+".")
	}
	return strings.Join(parts, " ")
}

// The view

func (r *Routine) refresh() {
	setContent(r.scroll, padded(16, 16, 16, 16, r.render()))
}

func (r *Routine) render() fyne.CanvasObject {
	box := container.NewVBox(r.header())
	if d := r.detail; d != nil && d.Program != nil && !r.running() {
		// What Dry Run does with a program depends on whether it writes.
		note := widget.NewRichText(&widget.TextSegment{Text: dryRunNote(*d.Program), Style: widget.RichTextStyle{
			ColorName: theme.ColorNamePlaceHolder, SizeName: theme.SizeNameCaptionText, Alignment: fyne.TextAlignTrailing}})
		note.Wrapping = fyne.TextWrapWord
		box.Add(note)
	}
	if r.actionError != "" {
		box.Add(errorRow(r.actionError))
	}
	if r.loadError != "" && r.detail == nil {
		box.Add(banner("Couldn't read "+r.name, r.loadError))
	}
	if r.turnOn.showing {
		box.Add(r.turnOn.view())
	} else if d := r.detail; d != nil && d.TurnOn {
		why := d.Untrusted
		if why == "" {
			why = "It's paused."
		}
		on := widget.NewButton("Turn On…", r.turnOn.start)
		box.Add(container.NewBorder(nil, nil, nil, container.NewCenter(on), banner(r.name+" isn't on", why)))
	}
	if d := r.detail; d != nil && !r.turnOn.showing {
		if b := r.granter.banner(r.name, d.Permissions); b != nil {
			box.Add(b)
		}
	}
	box.Add(segmented(r.tabs(), r.tab, r.ShowTab))
	switch r.tab {
	case RunsTab:
		box.Add(r.history.view())
	case MemoriesTab:
		if r.isProgram() {
			box.Add(r.overview())
			break
		}
		box.Add(r.memories.view())
	case SettingsTab:
		box.Add(r.settingsView())
	default:
		box.Add(r.overview())
	}
	return box
}

func (r *Routine) header() fyne.CanvasObject {
	e := r.entry()
	health := ""
	title := container.NewVBox(heading(r.name, true))
	if e != nil {
		health = e.Health
		if e.Summary != "" {
			s := text(e.Summary)
			if e.Health == "error" {
				s.Importance = widget.DangerImportance
			} else {
				s.Importance = widget.LowImportance
			}
			title.Add(s)
		}
	}
	return container.NewBorder(nil, nil, healthDot(health, 12), container.NewCenter(r.controls()), title)
}

func (r *Routine) controls() fyne.CanvasObject {
	var buttons []*widget.Button
	if r.running() {
		stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), r.stop)
		stop.Importance = widget.DangerImportance
		buttons = append(buttons, stop)
	} else {
		run := widget.NewButtonWithIcon("Run Now", theme.MediaPlayIcon(), func() { r.runNow(false) })
		buttons = append(buttons, run, widget.NewButton("Dry Run", func() { r.runNow(true) }))
	}
	if d := r.detail; d != nil {
		switch {
		case d.TurnOn:
			on := widget.NewButtonWithIcon("Turn On…", theme.ConfirmIcon(), r.turnOn.start)
			on.Importance = widget.HighImportance
			buttons = append(buttons, on)
		case d.Enabled:
			buttons = append(buttons, widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() { r.setPaused(true) }))
		default:
			buttons = append(buttons, widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() { r.setPaused(false) }))
		}
	}
	buttons = append(buttons, widget.NewButtonWithIcon("Delete…", theme.DeleteIcon(), r.askDelete))
	row := container.NewHBox()
	for _, b := range buttons {
		if r.working || r.detail == nil {
			b.Disable()
		}
		row.Add(b)
	}
	return row
}

// Overview: the status, and what it can reach.

func (r *Routine) overview() fyne.CanvasObject {
	box := container.NewVBox(card(false, r.statusRows()...))
	if r.detail != nil {
		box.Add(card(false, r.access()...))
		if c := r.granter.card(r.detail.Permissions); c != nil {
			box.Add(c)
		}
	}
	return box
}

func (r *Routine) statusRows() []fyne.CanvasObject {
	e := r.entry()
	rows := []fyne.CanvasObject{cardTitle("Status"), divider()}
	if e != nil && e.Problem != "" {
		rows = append(rows, infoRow("Can't schedule", errorRow(e.Problem)))
	}
	var last *cli.LastRun
	if e != nil && e.LastRun != nil {
		last = e.LastRun
	} else if r.detail != nil {
		last = r.detail.LastRun
	}
	if last == nil {
		rows = append(rows, infoRow("Last run", quiet("Never")))
	} else {
		at := "Waiting for its turn"
		if last.StartedAt != nil {
			at = when(*last.StartedAt, r.now())
		}
		line := container.NewHBox(widget.NewLabel(at), outcomeTag(last.Outcome))
		if last.Trigger != "" {
			line.Add(tag(last.Trigger, ""))
		}
		var show fyne.CanvasObject = container.NewHBox()
		if last.ID != "" {
			id := last.ID
			b := widget.NewButton("Show Run", func() { r.showRun(id) })
			b.Importance = widget.LowImportance
			show = b
		}
		content := []fyne.CanvasObject{container.NewBorder(nil, nil, line, show)}
		if last.Reason != "" {
			content = append(content, secondary(last.Reason))
		}
		rows = append(rows, infoRow("Last run", content...))
	}
	var next *time.Time
	if e != nil && e.NextRunAt != nil {
		next = e.NextRunAt
	} else if r.detail != nil {
		next = r.detail.Next
	}
	if next != nil {
		rows = append(rows, infoRow("Next run", widget.NewLabel(when(*next, r.now()))))
	} else {
		rows = append(rows, infoRow("Next run", quiet("None")))
	}
	return append(rows, padded(0, 6, 0, 0, container.NewHBox()))
}

// access is what the routine can reach (a program's: what it runs), read
// only, opening to its review.
func (r *Routine) access() []fyne.CanvasObject {
	d := r.detail
	state := tag("Trusted", successColour)
	if !d.Trusted {
		state = tag("Not trusted", theme.ColorNameWarning)
	}
	title := "What it can reach"
	if d.IsProgram() {
		title = "What it runs"
	}
	objects := []fyne.CanvasObject{newDisclosure(r.accessOpen, false, r.toggleAccess,
		container.NewBorder(nil, nil, nil, state, heading(title, false)))}
	if !r.accessOpen {
		return objects
	}
	objects = append(objects, divider())
	review := cli.TrustOutput{Routine: d.Name, Surface: cli.Surface{Servers: d.Servers, Tools: d.Tools,
		BuiltinTools: d.BuiltinTools, Folder: d.Folder}, Program: d.Program}
	if r.review != nil {
		review = *r.review
	}
	var signIn fyne.CanvasObject
	if review.Program != nil {
		signIn = r.signInRow(review.Program)
	}
	// Read only: Turn On… is where it's trusted.
	objects = append(objects, trustReview(review, "", "", nil, r.working, signIn)...)
	if r.trustError != "" {
		objects = append(objects, padded(0, 8, rowInset, rowInset, errorRow(r.trustError)))
	}
	return objects
}

// Runs: newest first, each opening to what it did.

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// toolCalls are every call the run made, in order (older records: only its
// writes), with their arguments and errors; writes are tinted.
func toolCalls(res *cli.RunResult) fyne.CanvasObject {
	calls := res.Calls
	if len(calls) == 0 {
		for _, w := range res.Writes {
			calls = append(calls, cli.ToolCall{Tool: w.Tool, Args: w.Args, Write: true, Error: w.Error})
		}
	}
	if len(calls) == 0 {
		return container.NewHBox()
	}
	writes := 0
	for _, c := range calls {
		if c.Write {
			writes++
		}
	}
	title := plural(len(calls), "tool call", "tool calls")
	if writes > 0 {
		title += ", " + plural(writes, "write", "writes")
	}
	var rows []fyne.CanvasObject
	for i, c := range calls {
		if i > 0 {
			rows = append(rows, divider())
		}
		name := widget.NewLabelWithStyle(fmt.Sprintf("%d  %s", i+1, c.Tool), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		line := container.NewHBox(name)
		if c.Write {
			name.Importance = widget.WarningImportance
			line.Add(tag("write", theme.ColorNameWarning))
		}
		row := container.NewVBox(line)
		for _, a := range arguments(c.Args) {
			arg := widget.NewLabelWithStyle(a, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
			arg.Wrapping = fyne.TextWrapBreak
			arg.SizeName = captionSize
			row.Add(padded(-8, -4, 24, 0, arg))
		}
		if c.Error != "" {
			row.Add(padded(0, 0, 24, 0, errorRow(c.Error)))
		}
		rows = append(rows, padded(2, 2, 6, 6, row))
	}
	return container.NewVBox(quiet(title), card(true, rows...))
}

// arguments are a call's arguments by name: text as is, anything else as JSON.
func arguments(args map[string]any) []string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		v, ok := args[k].(string)
		if !ok {
			b, _ := json.Marshal(args[k])
			v = string(b)
		}
		out = append(out, k+": "+v)
	}
	return out
}

// Memories: what its runs are given to remember; added and removed here.

// Settings: its own settings, as the Settings window's rows, and its file
// (a program's: its description file, read-only).

func (r *Routine) settingsView() fyne.CanvasObject {
	objects := []fyne.CanvasObject{cardTitle("Settings")}
	for _, e := range routineEntries(r.settings) {
		key := e.setting.Key
		objects = append(objects, divider(), settingRow(r.win, e.setting, e.label, r.errors[key], false,
			func(v string) { r.set(key, v) }, func() { r.reset(key) }))
	}
	box := container.NewVBox(card(false, objects...))
	if d := r.detail; d != nil {
		if d.IsProgram() {
			box.Add(programFile(*d, r.cli.Command))
			return box
		}
		model := firstNonEmpty(d.AgentModel, d.Model)
		var modelLabel fyne.CanvasObject = widget.NewLabel(model)
		if model == "" {
			modelLabel = quiet("The backend's default")
		}
		box.Add(card(false, cardTitle("Routine file"), divider(), infoRow("Model", modelLabel), infoRow("File", mono(d.Path)),
			padded(0, 6, 0, 0, container.NewHBox())))
	}
	return box
}

// routineEntries are a routine's settings with their labels: the key
// without "routines.<name>." (keyTree puts them two groups deep).
func routineEntries(settings []cli.Setting) []entry {
	var out []entry
	for _, g := range keyTree(settings) {
		out = append(out, g.entries...)
		for _, sub := range g.groups {
			out = append(out, sub.entries...)
		}
	}
	return out
}
