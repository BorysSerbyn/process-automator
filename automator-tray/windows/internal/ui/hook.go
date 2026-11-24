package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Hook is a hook's window's content, modelled on a routine's: the hook
// (`hooks show`), its entry in run status, its controls (fire with a sample
// event, stop, pause or resume, delete), what it acts on, its trust review,
// runs (with the events each was for), memories (an LLM hook's) and
// settings, all as the core gives them. No schedule and no Run Now: a hook
// runs on its events. It mirrors automator-ui's HookModel and HookDetailView.
type Hook struct {
	cli   cli.CLI
	win   fyne.Window
	async Async
	name  string
	now   func() time.Time
	// confirm asks before something that can't be undone (a dialog).
	confirm func(title, message, action string, yes func())
	// deleted is called once the hook is deleted (its window closes);
	// openWatcher opens a watcher's window.
	deleted     func()
	openWatcher func(string)

	tab       string
	detail    *cli.HookDetail
	loadError string
	status    *cli.RunStatus

	settings []cli.Setting
	errors   map[string]string

	history  *runHistory
	memories *memoryList

	review     *cli.TrustOutput
	trustError string
	accessOpen bool
	// triggerOpen shows the details of when it runs.
	triggerOpen bool

	// turnOn is its one review and confirm; reviewWhenLoaded opens it once
	// the hook is read, if it needs turning on (from the menu, a reason).
	turnOn           *turnOn
	reviewWhenLoaded bool
	// granter is Grant… for the permissions it needs; watchingPermissions,
	// whether it's read again while one isn't allowed.
	granter             *granter
	watchingPermissions bool

	signingIn   bool
	signInError string

	actionError string
	pending     *pendingAction

	// The sample event Fire runs it with: whether its editor shows, the
	// event, the fire waiting for its run ("dry run" or "fire"), and how
	// the last one went.
	fireOpen   bool
	sample     string
	firing     string
	fireResult *cli.FireOutput
	fireError  string

	scroll *container.Scroll
}

// NewHook makes a hook's window's content; Load reads it, Update gives it
// run status.
func NewHook(c cli.CLI, win fyne.Window, async Async, name string, deleted func(), openWatcher func(string)) *Hook {
	h := &Hook{cli: c, win: win, async: async, name: name, now: time.Now, deleted: deleted, openWatcher: openWatcher,
		tab: OverviewTab, errors: map[string]string{}}
	h.history = newRunHistory(c, async, name, func() time.Time { return h.now() }, h.refresh,
		func(msg string) { h.actionError = msg; h.refresh() })
	h.memories = newMemoryList(c, async, name, h.refresh)
	h.turnOn = newTurnOn(c, async, name, true, h.summary, h.refresh, h.Load)
	h.pending = newPendingAction(async, h.refresh)
	h.granter = newGranter(c, async, h.refresh, h.loadDetail)
	h.confirm = func(title, message, action string, yes func()) {
		d := dialog.NewConfirm(title, message, func(ok bool) {
			if ok {
				yes()
			}
		}, win)
		d.SetConfirmText(action)
		d.SetConfirmImportance(widget.DangerImportance)
		d.Show()
	}
	h.scroll = container.NewVScroll(container.NewVBox())
	h.refresh()
	return h
}

// Content is what the window shows.
func (h *Hook) Content() fyne.CanvasObject { return h.scroll }

func (h *Hook) entry() *cli.RoutineStatus { return h.status.Hook(h.name) }

func (h *Hook) running() bool {
	e := h.entry()
	return e != nil && e.LastRun.InProgress()
}

func (h *Hook) isProgram() bool { return h.detail != nil && h.detail.IsProgram() }

// tabs are the tabs it has: a program hook has no memories.
func (h *Hook) tabs() []string {
	if !h.isProgram() {
		return routineTabs
	}
	return []string{OverviewTab, RunsTab, SettingsTab}
}

// Load reads everything again.
func (h *Hook) Load() {
	h.loadDetail()
	h.loadSettings()
	h.history.load()
}

// Update shows a new run status; when the hook's last run or problem
// changed, its runs and detail are read again.
func (h *Hook) Update(s *cli.RunStatus) {
	var before, after cli.RoutineStatus
	if e := h.entry(); e != nil {
		before = *e
	}
	h.status = s
	if e := h.entry(); e != nil {
		after = *e
	}
	h.pending.settle()
	h.refresh()
	if !sameRun(before.LastRun, after.LastRun) || before.Problem != after.Problem {
		h.loadDetail()
		h.history.load()
		h.turnOn.statusChanged()
	}
}

// ShowTab shows one of the window's tabs.
func (h *Hook) ShowTab(tab string) {
	h.tab = tab
	h.refresh()
}

func (h *Hook) loadDetail() {
	h.async(func() func() {
		d, err := h.cli.Hook(h.name)
		return func() {
			if err != nil {
				h.loadError = err.Error()
			} else {
				if h.detail == nil && h.sample == "" {
					h.sample = sampleEvent(d.On)
				}
				h.detail, h.loadError = &d, ""
				h.pending.settle()
				h.granter.saw(d.Permissions)
				h.watchPermissions()
				if !d.IsProgram() && !h.memories.loaded {
					h.memories.load()
				}
				if h.reviewWhenLoaded {
					h.reviewWhenLoaded = false
					if d.TurnOn {
						h.turnOn.start()
					}
				}
			}
			h.refresh()
		}
	})
}

func (h *Hook) loadSettings() {
	h.async(func() func() {
		list, err := h.cli.RoutineSettings(h.name)
		return func() {
			if err != nil {
				h.loadError = err.Error()
			} else {
				h.settings = list
			}
			h.refresh()
		}
	})
}

// ShowRun shows one of its runs (a notification's): the Runs tab, with it open.
func (h *Hook) ShowRun(id string) { h.showRun(id) }

func (h *Hook) showRun(id string) {
	h.tab = RunsTab
	h.history.open(id)
	h.refresh()
}

// The controls

// control runs a control's command (`action`, its button saying `label`
// meanwhile), then `then`; it stays pending until `until` shows in its
// state (nil: until the command returns). Its failure shows as before.
func (h *Hook) control(action, label string, until func() bool, work func() error, then func()) {
	h.pending.run(action, label, until, work, func(err error) {
		if err != nil {
			h.actionError = err.Error()
			return
		}
		h.actionError = ""
		if then != nil {
			then()
		}
	})
}

// stop is Stop: "Stopping…" until it no longer runs.
func (h *Hook) stop() {
	h.control("stop", "Stopping…", func() bool { return !h.running() }, func() error { return h.cli.StopRun(h.name) }, nil)
}

func (h *Hook) setPaused(paused bool) {
	action, label := "resume", "Resuming…"
	if paused {
		action, label = "pause", "Pausing…"
	}
	h.control(action, label, func() bool { return h.detail != nil && h.detail.Enabled != paused },
		func() error { return h.cli.SetHookPaused(h.name, paused) }, h.Load)
}

func (h *Hook) askDelete() {
	h.confirm("Delete "+h.name+"?", "Its copy, settings, trust and secrets are deleted. Its run history is kept.", "Delete",
		func() {
			h.control("delete", "Deleting…", nil, func() error { return h.cli.DeleteHook(h.name) }, h.deleted)
		})
}

func (h *Hook) toggleFire() {
	h.fireOpen = !h.fireOpen
	h.refresh()
}

// fire runs it with the sample event, as a dry run or for real; a real
// one is in its runs after.
func (h *Hook) fire(dryRun bool) {
	h.firing, h.fireResult, h.fireError = "fire", nil, ""
	if dryRun {
		h.firing = "dry run"
	}
	event := h.sample
	h.refresh()
	h.async(func() func() {
		out, err := h.cli.FireHook(h.name, event, dryRun)
		return func() {
			h.firing = ""
			if err != nil {
				h.fireError = err.Error()
			} else {
				h.fireResult = &out
			}
			if !dryRun {
				h.history.load()
			}
			h.refresh()
		}
	})
}

// Settings

func (h *Hook) set(key, value string) {
	h.apply(key, func() error {
		_, err := h.cli.SetSetting(key, value)
		return err
	})
}

func (h *Hook) reset(key string) {
	h.apply(key, func() error {
		_, err := h.cli.ResetSetting(key)
		return err
	})
}

func (h *Hook) apply(key string, change func() error) {
	h.async(func() func() {
		err := change()
		return func() {
			if err != nil {
				h.errors[key] = err.Error()
			} else {
				delete(h.errors, key)
			}
			h.loadSettings()
			h.loadDetail()
		}
	})
}

// Trust

func (h *Hook) toggleAccess() {
	h.accessOpen = !h.accessOpen
	if h.accessOpen && h.review == nil {
		h.loadTrust()
	}
	h.refresh()
}

func (h *Hook) loadTrust() {
	h.async(func() func() {
		review, err := h.cli.HookTrustCheck(h.name)
		return func() {
			if err != nil {
				h.trustError = err.Error()
			} else {
				h.review, h.trustError = &review, ""
			}
			h.refresh()
		}
	})
}

func (h *Hook) signIn() {
	h.signingIn = true
	h.refresh()
	h.async(func() func() {
		err := h.cli.SignIn(h.name)
		return func() {
			h.signingIn, h.signInError = false, ""
			if err != nil {
				h.signInError = err.Error()
			}
			h.loadDetail()
			if h.review != nil {
				h.loadTrust()
			}
			h.refresh()
		}
	})
}

// Review opens its Turn On review, once it's read, if it needs turning on
// (the window opened, or raised, from the menu or a reason).
func (h *Hook) Review() {
	if h.detail == nil {
		h.reviewWhenLoaded = true
		return
	}
	if h.detail.TurnOn {
		h.turnOn.start()
	}
}

// watchPermissions reads it again now and then while a permission it needs
// isn't allowed (the scheduler records it within a pass).
func (h *Hook) watchPermissions() {
	if h.watchingPermissions || len(cli.BlockingPermissions(h.detail.Permissions)) == 0 {
		return
	}
	h.watchingPermissions = true
	pollPermissions(h.async, func() bool {
		h.watchingPermissions = h.detail != nil && len(cli.BlockingPermissions(h.detail.Permissions)) > 0
		return h.watchingPermissions
	}, h.loadDetail)
}

// summary is what it does, in a line, for its review.
func (h *Hook) summary() string {
	d := h.detail
	if d == nil {
		return ""
	}
	s := "It runs when " + onPhrase(d.On) + "."
	if d.Description != "" {
		s = d.Description + ". " + s
	}
	return s
}

// The view

func (h *Hook) refresh() {
	setContent(h.scroll, padded(16, 16, 16, 16, h.render()))
}

func (h *Hook) render() fyne.CanvasObject {
	box := container.NewVBox(h.header())
	if h.fireOpen {
		box.Add(h.fireView())
	}
	if h.actionError != "" {
		box.Add(errorRow(h.actionError))
	}
	if h.loadError != "" && h.detail == nil {
		box.Add(banner("Couldn't read "+h.name, h.loadError))
	}
	if h.turnOn.showing {
		box.Add(h.turnOn.view())
	} else if d := h.detail; d != nil && d.TurnOn {
		why := d.Untrusted
		if why == "" && d.Enabled {
			why = d.Problem
		}
		if why == "" {
			why = "It's off."
		}
		on := widget.NewButton("Turn On…", h.turnOn.start)
		box.Add(container.NewBorder(nil, nil, nil, container.NewCenter(on), banner(h.name+" isn't on", why)))
	}
	if d := h.detail; d != nil && !h.turnOn.showing {
		if b := h.granter.banner(h.name, d.Permissions); b != nil {
			box.Add(b)
		}
	}
	box.Add(segmented(h.tabs(), h.tab, h.ShowTab))
	switch h.tab {
	case RunsTab:
		box.Add(h.history.view())
	case MemoriesTab:
		if h.isProgram() {
			box.Add(h.overview())
			break
		}
		box.Add(h.memories.view())
	case SettingsTab:
		box.Add(h.settingsView())
	default:
		box.Add(h.overview())
	}
	return box
}

func (h *Hook) header() fyne.CanvasObject {
	e := h.entry()
	health := ""
	title := container.NewVBox(heading(h.name, true))
	if e != nil {
		health = e.Health
		if e.Summary != "" {
			s := text(e.Summary)
			s.Importance = widget.LowImportance
			if e.Health == "error" {
				s.Importance = widget.DangerImportance
			}
			title.Add(s)
		}
	}
	return container.NewBorder(nil, nil, healthDot(health, 12), container.NewCenter(h.controls()), title)
}

func (h *Hook) controls() fyne.CanvasObject {
	var buttons []*widget.Button
	if h.running() {
		stop := widget.NewButtonWithIcon(h.pending.title("Stop", "stop"), theme.MediaStopIcon(), h.stop)
		stop.Importance = widget.DangerImportance
		buttons = append(buttons, stop)
	} else {
		buttons = append(buttons, widget.NewButtonWithIcon("Fire…", theme.MediaPlayIcon(), h.toggleFire))
	}
	if d := h.detail; d != nil {
		if d.TurnOn {
			on := widget.NewButtonWithIcon("Turn On…", theme.ConfirmIcon(), h.turnOn.start)
			on.Importance = widget.HighImportance
			buttons = append(buttons, on)
		} else {
			buttons = append(buttons, widget.NewButtonWithIcon(h.pending.title("Pause", "pause"), theme.MediaPauseIcon(), func() { h.setPaused(true) }))
		}
	}
	buttons = append(buttons, widget.NewButtonWithIcon("Delete…", theme.DeleteIcon(), h.askDelete))
	row := container.NewHBox()
	for _, b := range buttons {
		if h.pending.busy() || h.detail == nil {
			b.Disable()
		}
		row.Add(b)
	}
	return row
}

// fireView is Fire with a sample event: one event as JSON, as its watcher
// would print it, run as a dry run or for real; then how the run went.
func (h *Hook) fireView() fyne.CanvasObject {
	editor := widget.NewMultiLineEntry()
	editor.TextStyle = fyne.TextStyle{Monospace: true}
	editor.SetText(h.sample)
	editor.SetMinRowsVisible(5)
	editor.OnChanged = func(s string) { h.sample = s }
	dry := widget.NewButton("Dry Run", func() { h.fire(true) })
	fire := widget.NewButton("Fire", func() { h.fire(false) })
	fire.Importance = widget.HighImportance
	closeButton := widget.NewButton("Close", h.toggleFire)
	if h.firing != "" {
		dry.Disable()
		fire.Disable()
	}
	dryRun := "A dry run intercepts its writes, and needs no trust."
	if h.isProgram() {
		dryRun = "A program hook needs trusting even for a dry run."
	}
	objects := []fyne.CanvasObject{cardTitle("Fire with a sample event"), divider(),
		padded(0, 0, rowInset-8, rowInset-8, secondary("One event as its watcher would print it: an id, a type, and optionally at and data. It runs whatever its staleness and rate.")),
		padded(4, 4, rowInset-8, rowInset-8, editor),
		padded(0, 0, rowInset-8, rowInset-8, secondary(dryRun))}
	var status fyne.CanvasObject = container.NewHBox()
	if h.firing != "" {
		what := "Running…"
		if h.firing == "dry run" {
			what = "Dry run going…"
		}
		status = container.NewHBox(spinner(), quiet(what))
	}
	objects = append(objects, padded(4, 4, rowInset-8, rowInset-8, container.NewBorder(nil, nil, status, container.NewHBox(closeButton, dry, fire))))
	switch {
	case h.fireError != "":
		objects = append(objects, padded(0, 8, rowInset, rowInset, errorRow(h.fireError)))
	case h.fireResult != nil:
		objects = append(objects, divider(), padded(4, 8, rowInset-8, rowInset-8, fireResult(*h.fireResult, h.now())))
	}
	return card(false, objects...)
}

// fireResult is how a fire went: its report and text (an LLM hook's), or
// what the program did.
func fireResult(out cli.FireOutput, now time.Time) fyne.CanvasObject {
	tags := container.NewHBox(quiet("How it went"))
	if out.Outcome != "" {
		tags.Add(tag(out.Outcome, ""))
	}
	if out.DryRun {
		tags.Add(tag("dry run", theme.ColorNamePrimary))
	}
	box := container.NewVBox(tags)
	if t := firstNonEmpty(out.Text, out.Reason); t != "" {
		l := text(t)
		l.Selectable = true
		box.Add(l)
	}
	if out.Program != nil {
		box.Add(programRun(*out.Program, nil, now))
	}
	return box
}

// sampleEvent is the event Fire starts from: one line as its watcher would
// print it, of the type the hook acts on.
func sampleEvent(on cli.On) string {
	t := on.Type
	if t == "" {
		t = "example"
	}
	return "{\n  \"id\": \"sample-1\",\n  \"type\": \"" + t + "\",\n  \"data\": {}\n}"
}

// Overview: the status, what it acts on, and what it can reach or runs.

func (h *Hook) overview() fyne.CanvasObject {
	box := container.NewVBox(card(false, h.statusRows()...))
	if h.detail != nil {
		box.Add(card(false, h.trigger()...))
		box.Add(card(false, h.access()...))
		if c := h.granter.card(h.detail.Permissions); c != nil {
			box.Add(c)
		}
	}
	return box
}

func (h *Hook) statusRows() []fyne.CanvasObject {
	e := h.entry()
	rows := []fyne.CanvasObject{cardTitle("Status"), divider()}
	problem := ""
	if e != nil {
		problem = e.Problem
	}
	if problem == "" && h.detail != nil {
		problem = h.detail.Problem
	}
	if problem != "" {
		rows = append(rows, infoRow("Can't run", errorRow(problem)))
	}
	var last *cli.LastRun
	if e != nil && e.LastRun != nil {
		last = e.LastRun
	} else if h.detail != nil {
		last = h.detail.LastRun
	}
	if last == nil {
		rows = append(rows, infoRow("Last run", quiet("Never")))
	} else {
		at := "Waiting for its turn"
		if last.StartedAt != nil {
			at = when(*last.StartedAt, h.now())
		}
		line := container.NewHBox(widget.NewLabel(at), outcomeTag(last.Outcome))
		if last.Trigger != "" {
			line.Add(tag(last.Trigger, ""))
		}
		var show fyne.CanvasObject = container.NewHBox()
		if last.ID != "" {
			id := last.ID
			b := widget.NewButton("Show Run", func() { h.showRun(id) })
			b.Importance = widget.LowImportance
			show = b
		}
		content := []fyne.CanvasObject{container.NewBorder(nil, nil, line, show)}
		if last.Reason != "" {
			content = append(content, secondary(last.Reason))
		}
		rows = append(rows, infoRow("Last run", content...))
	}
	return append(rows, padded(0, 6, 0, 0, container.NewHBox()))
}

// trigger is when it runs, in a line ("When End key pressed (from nav-keys)"),
// with its watcher's state when it needs me (paused, failing); the rest
// (its watcher, the event type, batches, staleness, rate) opens under it.
func (h *Hook) trigger() []fyne.CanvasObject {
	d := h.detail
	line := container.NewVBox(heading("When it runs", false), secondary(onText(d.On)))
	tags := container.NewHBox()
	if w := d.Watcher; w != nil {
		if !w.Enabled {
			tags.Add(tag(w.Name+" paused", ""))
		}
		if w.Error != "" {
			tags.Add(tag(w.Name+" failing", theme.ColorNameError))
		}
	} else {
		tags.Add(tag("No watcher "+d.On.Watcher, theme.ColorNameError))
	}
	rows := []fyne.CanvasObject{newDisclosure(h.triggerOpen, false, func() {
		h.triggerOpen = !h.triggerOpen
		h.refresh()
	}, container.NewBorder(nil, nil, nil, container.NewCenter(tags), line))}
	if w := d.Watcher; w != nil && w.Error != "" {
		failing := []fyne.CanvasObject{errorRow(w.Error)}
		if w.ErrorAt != nil {
			failing = append(failing, secondary("Since "+when(*w.ErrorAt, h.now())))
		}
		rows = append(rows, infoRow("Watcher", failing...))
	}
	if !h.triggerOpen {
		return rows
	}
	rows = append(rows, divider())
	if w := d.Watcher; w != nil {
		name := w.Name
		link := widget.NewButton(name, func() { h.openWatcher(name) })
		link.Importance = widget.LowImportance
		line := container.NewHBox(link)
		for _, t := range watcherTags(*w) {
			line.Add(t)
		}
		rows = append(rows, infoRow("Watcher", line, secondary(modeText(*w))))
	} else {
		missing := text("There's no watcher by that name.")
		missing.Importance = widget.DangerImportance
		rows = append(rows, infoRow("Watcher", mono(d.On.Watcher), missing))
	}
	switch {
	case d.On.Type == "":
		rows = append(rows, infoRow("Events", text("Every type")))
	case d.On.Label != "":
		rows = append(rows, infoRow("Events", container.NewVBox(text(d.On.Label), secondary(d.On.Type))))
	default:
		rows = append(rows, infoRow("Events", mono(d.On.Type)))
	}
	if !d.IsProgram() {
		batch := "One run per event."
		if d.Batch {
			batch = "One run gets every event that waited for it."
		}
		rows = append(rows, infoRow("Batches", text(batch)))
	}
	rate := plural(d.Rate, "run", "runs")
	return append(rows,
		infoRow("Staleness", text("Events older than "+d.Staleness+" are dropped.")),
		infoRow("Rate", text("At most "+rate+" an hour; far past it, it's paused.")),
		padded(0, 6, 0, 0, container.NewHBox()))
}

// onPhrase is what a hook acts on, in words, with the label its watcher
// declares for the type: "End key pressed (from nav-keys)"; "a key.end
// event (from nav-keys)" when it declares none; "any event (from
// nav-keys)" with no type.
func onPhrase(on cli.On) string {
	switch {
	case on.Type == "":
		return "any event (from " + on.Watcher + ")"
	case on.Label == "":
		return "a " + on.Type + " event (from " + on.Watcher + ")"
	}
	return on.Label + " (from " + on.Watcher + ")"
}

// onText is when a hook runs: "When End key pressed (from nav-keys)".
func onText(on cli.On) string { return "When " + onPhrase(on) }

// access is what the hook can reach (an LLM hook's) or runs (a program
// hook's), read only, opening to its review.
func (h *Hook) access() []fyne.CanvasObject {
	d := h.detail
	state := tag("Trusted", successColour)
	if !d.Trusted {
		state = tag("Not trusted", theme.ColorNameWarning)
	}
	title := "What it can reach"
	if d.IsProgram() {
		title = "What it runs"
	}
	objects := []fyne.CanvasObject{newDisclosure(h.accessOpen, false, h.toggleAccess,
		container.NewBorder(nil, nil, nil, state, heading(title, false)))}
	if !h.accessOpen {
		return objects
	}
	objects = append(objects, divider())
	review := cli.TrustOutput{Routine: d.Name, Surface: cli.Surface{Servers: d.Servers, Tools: d.Tools,
		BuiltinTools: d.BuiltinTools, Folder: d.Folder}, Program: d.Program}
	if h.review != nil {
		review = *h.review
	}
	if review.ToolsError != "" {
		objects = append(objects, infoRow("Tools", errorRow(review.ToolsError)))
	}
	var signIn fyne.CanvasObject
	if review.Program != nil {
		signIn = h.signInRow(review.Program)
	}
	// Read only: Turn On… is where it's trusted.
	objects = append(objects, trustReview(review, "", "", nil, h.pending.busy(), signIn)...)
	if h.trustError != "" {
		objects = append(objects, padded(0, 8, rowInset, rowInset, errorRow(h.trustError)))
	}
	return objects
}

// signInRow is Sign In… under a program hook's secrets while one isn't
// stored (or the wait for it, and why it failed); nil when none is missing.
func (h *Hook) signInRow(p *cli.ProgramInfo) fyne.CanvasObject {
	return signInRow(p, h.signingIn, h.signInError, h.signIn)
}

// Settings: its own settings, as the Settings window's rows, and its file
// (read-only).

func (h *Hook) settingsView() fyne.CanvasObject {
	objects := []fyne.CanvasObject{cardTitle("Settings")}
	for _, e := range routineEntries(h.settings) {
		key := e.setting.Key
		objects = append(objects, divider(), settingRow(h.win, e.setting, e.label, h.errors[key], false,
			func(v string) { h.set(key, v) }, func() { h.reset(key) }))
	}
	box := container.NewVBox(card(false, objects...))
	if d := h.detail; d != nil {
		f := sourceFile{title: "Prompt file", description: d.Description, path: d.Path, source: d.Source,
			update: h.cli.Command + " hooks update " + d.Name + " --from <file>"}
		if d.IsProgram() {
			f.title, f.folder = "Program file", d.Folder
		}
		box.Add(descriptionFile(f))
	}
	return box
}
