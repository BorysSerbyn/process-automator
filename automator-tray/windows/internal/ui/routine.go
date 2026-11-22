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
	detail    *cli.RoutineInfo
	loadError string
	status    *cli.RunStatus

	settings []cli.Setting
	errors   map[string]string

	runs         []cli.RunSummary
	runsLoaded   bool
	runsError    string
	entries      map[string]cli.RunEntry
	runErrors    map[string]string
	openRuns     map[string]bool
	openedLatest bool

	memories       []cli.Memory
	memoriesLoaded bool
	memoryError    string
	memoryDraft    string

	review     *cli.TrustOutput
	trustError string
	accessOpen bool

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
		errors: map[string]string{}, entries: map[string]cli.RunEntry{}, runErrors: map[string]string{},
		openRuns: map[string]bool{}}
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

// Load reads everything again.
func (r *Routine) Load() {
	r.loadDetail()
	r.loadSettings()
	r.loadRuns()
	r.loadMemories()
}

// Update shows a new run status; when the routine's last run changed, its
// runs and detail are read again.
func (r *Routine) Update(s *cli.RunStatus) {
	var before, after *cli.LastRun
	if e := r.entry(); e != nil {
		before = e.LastRun
	}
	r.status = s
	if e := r.entry(); e != nil {
		after = e.LastRun
	}
	r.refresh()
	if !sameRun(before, after) {
		r.loadDetail()
		r.loadRuns()
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
				if !d.Trusted {
					r.accessOpen = true
					if r.review == nil {
						r.loadTrust()
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

func (r *Routine) loadRuns() {
	r.async(func() func() {
		list, err := r.cli.Runs(r.name, RunsShown)
		return func() {
			if err != nil {
				r.runsError = err.Error()
			} else {
				r.runs, r.runsError = list, ""
				// The latest run starts open: its output is what's usually wanted.
				if !r.openedLatest && len(list) > 0 {
					r.openedLatest = true
					r.openRun(list[0].ID)
				}
			}
			r.runsLoaded = true
			r.refresh()
		}
	})
}

func (r *Routine) loadMemories() {
	r.async(func() func() {
		list, err := r.cli.Memories(r.name)
		return func() {
			if err != nil {
				r.memoryError = err.Error()
			} else {
				r.memories = list
			}
			r.memoriesLoaded = true
			r.refresh()
		}
	})
}

// The runs

func (r *Routine) toggleRun(id string) {
	if r.openRuns[id] {
		delete(r.openRuns, id)
		r.refresh()
		return
	}
	r.openRun(id)
	r.refresh()
}

// openRun opens a run, reading it in full the first time.
func (r *Routine) openRun(id string) {
	r.openRuns[id] = true
	if _, ok := r.entries[id]; ok {
		return
	}
	r.async(func() func() {
		e, err := r.cli.Run(id)
		return func() {
			if err != nil {
				r.runErrors[id] = err.Error()
			} else {
				r.entries[id] = e
				delete(r.runErrors, id)
			}
			r.refresh()
		}
	})
}

// showRun shows one run: the Runs tab, with it open.
func (r *Routine) showRun(id string) {
	r.tab = RunsTab
	r.openRun(id)
	r.refresh()
}

// openReport opens a readable report of a run (the core writes and opens it).
func (r *Routine) openReport(id string) {
	r.async(func() func() {
		err := r.cli.OpenRun(id)
		return func() {
			if err != nil {
				r.actionError = err.Error()
				r.refresh()
			}
		}
	})
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
	r.confirm("Delete "+r.name+"?", "Its file, settings, trust and memories are deleted. Its run history is kept.", "Delete", func() {
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

func (r *Routine) addMemory(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	r.async(func() func() {
		err := r.cli.AddMemory(r.name, text)
		return func() {
			if err != nil {
				r.memoryError = err.Error()
			} else {
				r.memoryError, r.memoryDraft = "", ""
			}
			r.loadMemories()
		}
	})
}

func (r *Routine) removeMemory(id string) {
	r.async(func() func() {
		err := r.cli.RemoveMemory(id)
		return func() {
			r.memoryError = ""
			if err != nil {
				r.memoryError = err.Error()
			}
			r.loadMemories()
		}
	})
}

// Trust

func (r *Routine) toggleAccess() {
	r.accessOpen = !r.accessOpen
	if r.accessOpen && r.review == nil {
		r.loadTrust()
	}
	r.refresh()
}

// reviewTrust is the Not trusted banner's Review…: what it can reach, open
// in Overview.
func (r *Routine) reviewTrust() {
	r.tab = OverviewTab
	if !r.accessOpen {
		r.toggleAccess()
		return
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

// trust trusts the routine with what it can reach; turnOn also has it run
// on its schedule (a paused routine), as setup's Turn On does.
func (r *Routine) trust(turnOn bool) {
	r.working = true
	r.refresh()
	r.async(func() func() {
		var err error
		if turnOn {
			err = r.cli.TurnOn(r.name)
		} else {
			_, err = r.cli.Trust(r.name)
		}
		return func() {
			r.working = false
			if err != nil {
				r.trustError = err.Error()
			} else {
				r.trustError, r.review = "", nil
				r.Load()
			}
			r.refresh()
		}
	})
}

// The view

func (r *Routine) refresh() {
	offset := r.scroll.Offset
	r.scroll.Content = padded(16, 16, 16, 16, r.render())
	r.scroll.Refresh()
	r.scroll.ScrollToOffset(offset)
}

func (r *Routine) render() fyne.CanvasObject {
	box := container.NewVBox(r.header())
	if r.actionError != "" {
		box.Add(errorRow(r.actionError))
	}
	if r.loadError != "" && r.detail == nil {
		box.Add(banner("Couldn't read "+r.name, r.loadError))
	}
	if r.detail != nil && !r.detail.Trusted {
		review := widget.NewButton("Review…", r.reviewTrust)
		box.Add(container.NewBorder(nil, nil, nil, container.NewCenter(review), banner("Not trusted", r.detail.Untrusted)))
	}
	box.Add(segmented(routineTabs, r.tab, r.ShowTab))
	switch r.tab {
	case RunsTab:
		box.Add(r.runsView())
	case MemoriesTab:
		box.Add(r.memoriesView())
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
		run.Importance = widget.HighImportance
		buttons = append(buttons, run, widget.NewButton("Dry Run", func() { r.runNow(true) }))
	}
	if r.detail != nil {
		enabled := r.detail.Enabled
		if enabled {
			buttons = append(buttons, widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() { r.setPaused(true) }))
		} else {
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

// access is what the routine can reach, opening to its review, with Trust
// (or Turn On, for a paused routine) while it isn't trusted.
func (r *Routine) access() []fyne.CanvasObject {
	d := r.detail
	state := tag("Trusted", successColour)
	if !d.Trusted {
		state = tag("Not trusted", theme.ColorNameWarning)
	}
	objects := []fyne.CanvasObject{newDisclosure(r.accessOpen, false, r.toggleAccess,
		container.NewBorder(nil, nil, nil, state, heading("What it can reach", false)))}
	if !r.accessOpen {
		return objects
	}
	objects = append(objects, divider())
	review := cli.TrustOutput{Routine: d.Name, Surface: cli.Surface{Servers: d.Servers, Tools: d.Tools,
		BuiltinTools: d.BuiltinTools, Folder: d.Folder}}
	if r.review != nil {
		review = *r.review
	}
	var do func()
	action, note := "Trust", "Trusting it lets it run for real with what's listed above. Dry runs never need it."
	if !d.Trusted && r.review != nil {
		turnOn := !d.Enabled
		if turnOn {
			action, note = "Turn On", "Turning it on trusts it with what's listed above and runs it on its schedule. Dry runs never need it."
		}
		do = func() { r.trust(turnOn) }
	}
	objects = append(objects, trustReview(review, action, note, do, r.working)...)
	if r.trustError != "" {
		objects = append(objects, padded(0, 8, rowInset, rowInset, errorRow(r.trustError)))
	}
	return objects
}

// Runs: newest first, each opening to what it did.

func (r *Routine) runsView() fyne.CanvasObject {
	count := fyne.CanvasObject(container.NewHBox())
	if len(r.runs) > 0 {
		count = quiet(plural(len(r.runs), "run", "runs"))
	}
	objects := []fyne.CanvasObject{cardTitle("Runs", count)}
	switch {
	case r.runsError != "":
		objects = append(objects, padded(0, 10, rowInset, rowInset, errorRow(r.runsError)))
	case !r.runsLoaded:
		objects = append(objects, padded(8, 16, 0, 0, spinner()))
	case len(r.runs) == 0:
		objects = append(objects, divider(), padded(20, 20, 0, 0, container.NewCenter(quiet("It hasn't run yet."))))
	}
	for _, run := range r.runs {
		objects = append(objects, divider(), r.runRow(run))
		if r.openRuns[run.ID] {
			objects = append(objects, padded(0, 14, 34, rowInset, r.runDetail(run.ID)))
		}
	}
	return card(false, objects...)
}

// runRow is one run: when, how it ended, what started it, why; how many
// tool calls it made and what it cost.
func (r *Routine) runRow(run cli.RunSummary) fyne.CanvasObject {
	line := container.NewHBox(widget.NewLabelWithStyle(when(run.StartedAt, r.now()), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		outcomeTag(run.Outcome))
	if run.Trigger != "" {
		line.Add(tag(run.Trigger, ""))
	}
	left := container.NewVBox(line)
	if run.Reason != "" {
		reason := quiet(run.Reason)
		if !r.openRuns[run.ID] {
			reason.Truncation = fyne.TextTruncateEllipsis
		} else {
			reason.Wrapping = fyne.TextWrapWord
		}
		left.Add(reason)
	}
	numbers := container.NewVBox()
	if run.ToolCalls != nil {
		numbers.Add(quiet(plural(*run.ToolCalls, "tool call", "tool calls")))
	}
	if run.CostUSD != nil {
		numbers.Add(quiet(cost(*run.CostUSD)))
	}
	id := run.ID
	return newDisclosure(r.openRuns[id], false, func() { r.toggleRun(id) }, container.NewBorder(nil, nil, nil, numbers, left))
}

// runDetail is what a run did: its report (done, nothing to do), its final
// text, every tool call (writes tinted) and its tokens; Open Report.
func (r *Routine) runDetail(id string) fyne.CanvasObject {
	if msg, ok := r.runErrors[id]; ok {
		return errorRow(msg)
	}
	e, ok := r.entries[id]
	if !ok {
		return spinner()
	}
	box := container.NewVBox()
	report := widget.NewButton("Open Report", func() { r.openReport(id) })
	if res := e.Result; res != nil {
		tags := container.NewHBox()
		if res.Outcome != "" {
			tags.Add(tag(res.Outcome, ""))
		}
		if res.DryRun {
			tags.Add(tag("dry run", theme.ColorNamePrimary))
		}
		if len(tags.Objects) > 0 {
			box.Add(tags)
		}
		if res.Text != "" {
			out := text(res.Text)
			out.Selectable = true
			box.Add(card(true, padded(4, 4, 6, 6, out)))
		}
		box.Add(toolCalls(res))
		tokens := fmt.Sprintf("%d in · %d out · %d cached · %s", res.InputTokens, res.OutputTokens, res.CachedTokens, res.Backend)
		box.Add(container.NewBorder(nil, nil, quiet(tokens), report))
	} else {
		if t := firstNonEmpty(e.Reason, e.Error); t != "" {
			box.Add(text(t))
		}
		box.Add(right(report))
	}
	return box
}

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

func (r *Routine) memoriesView() fyne.CanvasObject {
	count := fyne.CanvasObject(container.NewHBox())
	if len(r.memories) > 0 {
		count = quiet(plural(len(r.memories), "memory", "memories"))
	}
	objects := []fyne.CanvasObject{cardTitle("Memories", count),
		padded(0, 6, rowInset-8, rowInset, secondary("What "+r.name+"'s runs are given to remember, besides the ones every routine gets."))}
	if r.memoriesLoaded && len(r.memories) == 0 {
		objects = append(objects, divider(), padded(16, 16, 0, 0, container.NewCenter(quiet("No memories yet."))))
	}
	for _, m := range r.memories {
		id := m.ID
		remove := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { r.removeMemory(id) })
		remove.Importance = widget.LowImportance
		t := text(m.Text)
		t.Selectable = true
		objects = append(objects, divider(), padded(2, 2, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, container.NewCenter(remove), t)))
	}
	field := widget.NewEntry()
	field.SetPlaceHolder("Something " + r.name + " should remember")
	field.SetText(r.memoryDraft)
	field.OnChanged = func(s string) { r.memoryDraft = s }
	field.OnSubmitted = r.addMemory
	add := widget.NewButton("Add", func() { r.addMemory(field.Text) })
	objects = append(objects, divider(), padded(8, 8, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, add, field)))
	if r.memoryError != "" {
		objects = append(objects, padded(0, 10, rowInset, rowInset, errorRow(r.memoryError)))
	}
	return card(false, objects...)
}

// Settings: its own settings, as the Settings window's rows, and its file.

func (r *Routine) settingsView() fyne.CanvasObject {
	objects := []fyne.CanvasObject{cardTitle("Settings")}
	for _, e := range routineEntries(r.settings) {
		key := e.setting.Key
		objects = append(objects, divider(), settingRow(r.win, e.setting, e.label, r.errors[key], false,
			func(v string) { r.set(key, v) }, func() { r.reset(key) }))
	}
	box := container.NewVBox(card(false, objects...))
	if d := r.detail; d != nil {
		model := firstNonEmpty(d.AgentModel, d.Model)
		var modelLabel fyne.CanvasObject = widget.NewLabel(model)
		if model == "" {
			modelLabel = quiet("The backend's default")
		}
		path := widget.NewLabelWithStyle(d.Path, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		path.Wrapping = fyne.TextWrapBreak
		box.Add(card(false, cardTitle("Routine file"), divider(), infoRow("Model", modelLabel), infoRow("File", path),
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
