package ui

import (
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Watcher is a watcher's window's content (`watchers show`): how it runs
// and last went, the hooks listening, what it runs (Trust while it isn't
// trusted, Sign In… for its secrets) and its file; Pause/Resume and
// Delete…. It mirrors automator-ui's WatcherModel and WatcherView.
type Watcher struct {
	cli   cli.CLI
	async Async
	name  string
	now   func() time.Time
	// confirm asks before deleting it; deleted closes its window; openHook
	// opens a hook's window.
	confirm  func(title, message, action string, yes func())
	deleted  func()
	openHook func(string)

	detail      *cli.WatcherDetail
	loadError   string
	status      *cli.RunStatus
	review      *cli.WatcherTrustOutput
	trustError  string
	signingIn   bool
	signInError string
	actionError string
	working     bool

	scroll *container.Scroll
}

// NewWatcher makes a watcher's window's content; Load reads it, Update
// gives it run status.
func NewWatcher(c cli.CLI, win fyne.Window, async Async, name string, deleted func(), openHook func(string)) *Watcher {
	w := &Watcher{cli: c, async: async, name: name, now: time.Now, deleted: deleted, openHook: openHook}
	w.confirm = func(title, message, action string, yes func()) {
		d := dialog.NewConfirm(title, message, func(ok bool) {
			if ok {
				yes()
			}
		}, win)
		d.SetConfirmText(action)
		d.SetConfirmImportance(widget.DangerImportance)
		d.Show()
	}
	w.scroll = container.NewVScroll(container.NewVBox())
	w.refresh()
	return w
}

// Content is what the window shows.
func (w *Watcher) Content() fyne.CanvasObject { return w.scroll }

// Load reads it again (and its review, while it isn't trusted).
func (w *Watcher) Load() {
	w.async(func() func() {
		d, err := w.cli.Watcher(w.name)
		return func() {
			if err != nil {
				w.loadError = err.Error()
			} else {
				w.detail, w.loadError = &d, ""
				if !d.Trusted && w.review == nil {
					w.loadTrust()
				}
			}
			w.refresh()
		}
	})
}

// Update shows a new run status; it's read again when the hooks listening
// changed (a hook's problem is its watcher's state).
func (w *Watcher) Update(s *cli.RunStatus) {
	changed := !sameHooks(w.status, s)
	w.status = s
	w.refresh()
	if changed {
		w.Load()
	}
}

func sameHooks(a, b *cli.RunStatus) bool {
	shape := func(s *cli.RunStatus) string {
		if s == nil {
			return ""
		}
		var out []string
		for _, h := range s.Hooks {
			id := ""
			if h.LastRun != nil {
				id = h.LastRun.ID + h.LastRun.Outcome
			}
			out = append(out, h.Name+"|"+h.Problem+"|"+id)
		}
		return strings.Join(out, "\n")
	}
	return shape(a) == shape(b)
}

func (w *Watcher) loadTrust() {
	w.async(func() func() {
		review, err := w.cli.WatcherTrustCheck(w.name)
		return func() {
			if err != nil {
				w.trustError = err.Error()
			} else {
				w.review, w.trustError = &review, ""
			}
			w.refresh()
		}
	})
}

func (w *Watcher) trust() {
	w.working = true
	w.refresh()
	w.async(func() func() {
		err := w.cli.TrustWatcher(w.name)
		return func() {
			w.working = false
			if err != nil {
				w.trustError = err.Error()
			} else {
				w.trustError, w.review = "", nil
				w.Load()
			}
			w.refresh()
		}
	})
}

func (w *Watcher) signIn() {
	w.signingIn = true
	w.refresh()
	w.async(func() func() {
		err := w.cli.SignIn(w.name)
		return func() {
			w.signingIn, w.signInError = false, ""
			if err != nil {
				w.signInError = err.Error()
			}
			w.Load()
			if w.review != nil {
				w.loadTrust()
			}
			w.refresh()
		}
	})
}

func (w *Watcher) control(work func() error, then func()) {
	w.working = true
	w.refresh()
	w.async(func() func() {
		err := work()
		return func() {
			w.working = false
			if err != nil {
				w.actionError = err.Error()
			} else {
				w.actionError = ""
				then()
			}
			w.refresh()
		}
	})
}

func (w *Watcher) setPaused(paused bool) {
	w.control(func() error { return w.cli.SetWatcherPaused(w.name, paused) }, w.Load)
}

func (w *Watcher) askDelete() {
	w.confirm("Delete "+w.name+"?",
		"Its copy, settings, trust, secrets and state are deleted. The hooks listening to it stay, and hear nothing.", "Delete",
		func() { w.control(func() error { return w.cli.DeleteWatcher(w.name) }, w.deleted) })
}

// The view

func (w *Watcher) refresh() {
	setContent(w.scroll, padded(16, 16, 16, 16, w.render()))
}

func (w *Watcher) render() fyne.CanvasObject {
	box := container.NewVBox(w.header())
	if w.actionError != "" {
		box.Add(errorRow(w.actionError))
	}
	if w.loadError != "" && w.detail == nil {
		box.Add(banner("Couldn't read "+w.name, w.loadError))
	}
	d := w.detail
	if d == nil {
		if w.loadError == "" {
			box.Add(spinner())
		}
		return box
	}
	if !d.Trusted {
		why := "It doesn't run, and the hooks listening hear nothing, until it's trusted."
		if d.Untrusted != "" {
			why = d.Untrusted + ". " + why
		}
		box.Add(banner("Not trusted", why))
	}
	for _, o := range []fyne.CanvasObject{card(false, w.state()...), card(false, w.hooks()...), card(false, w.access()...),
		descriptionFile(sourceFile{title: "Watcher file", description: d.Description, path: d.Path, source: d.Source,
			update: w.cli.Command + " watchers update " + d.Name + " --from <folder>/watcher.yaml"})} {
		box.Add(o)
	}
	return box
}

func (w *Watcher) header() fyne.CanvasObject {
	title := container.NewVBox(heading(w.name, true))
	if d := w.detail; d != nil {
		line := container.NewHBox(quiet(modeText(d.WatcherInfo)))
		for _, t := range watcherTags(d.WatcherInfo) {
			line.Add(t)
		}
		title.Add(line)
	}
	var buttons []*widget.Button
	if d := w.detail; d != nil {
		if d.Enabled {
			buttons = append(buttons, widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() { w.setPaused(true) }))
		} else {
			buttons = append(buttons, widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() { w.setPaused(false) }))
		}
	}
	buttons = append(buttons, widget.NewButtonWithIcon("Delete…", theme.DeleteIcon(), w.askDelete))
	row := container.NewHBox()
	for _, b := range buttons {
		if w.working || w.detail == nil {
			b.Disable()
		}
		row.Add(b)
	}
	return container.NewBorder(nil, nil, nil, container.NewCenter(row), title)
}

// state is how it runs and last went.
func (w *Watcher) state() []fyne.CanvasObject {
	d := w.detail
	at := func(t *time.Time) fyne.CanvasObject {
		if t == nil {
			return quiet("Never")
		}
		return widget.NewLabel(when(*t, w.now()))
	}
	mode := []fyne.CanvasObject{text(modeText(d.WatcherInfo))}
	if d.Mode == "poll" && d.Timeout != "" {
		mode = append(mode, secondary("A poll may take "+d.Timeout+" before it's stopped."))
	}
	rows := []fyne.CanvasObject{cardTitle("How it runs"), divider(), infoRow("Mode", mode...)}
	if d.Mode == "poll" {
		rows = append(rows, infoRow("Last poll", at(d.LastPollAt)))
	} else {
		rows = append(rows, infoRow("Started", at(d.StartedAt)))
	}
	rows = append(rows, infoRow("Last event", at(d.LastEventAt)))
	if d.Error != "" {
		failing := []fyne.CanvasObject{errorRow(d.Error)}
		if d.ErrorAt != nil {
			failing = append(failing, secondary("Since "+when(*d.ErrorAt, w.now())))
		}
		rows = append(rows, infoRow("Failing", failing...))
	}
	return append(rows, padded(0, 6, 0, 0, container.NewHBox()))
}

// hooks are the hooks listening, each with its health and summary,
// opening its window.
func (w *Watcher) hooks() []fyne.CanvasObject {
	d := w.detail
	rows := []fyne.CanvasObject{cardTitle("Hooks listening", quiet(plural(len(d.Hooks), "hook", "hooks")))}
	if len(d.Hooks) == 0 {
		rows = append(rows, divider(), padded(16, 16, 0, 0, container.NewCenter(quiet("No hook listens to it, so it doesn't run."))))
	}
	for _, name := range d.Hooks {
		health, summary := "", ""
		if e := w.status.Hook(name); e != nil {
			health, summary = e.Health, e.Summary
		}
		var trailing fyne.CanvasObject = container.NewHBox()
		if summary != "" {
			s := quiet(summary)
			if health == "error" {
				s.Importance = widget.DangerImportance
			}
			trailing = s
		}
		n := name
		row := container.NewBorder(nil, nil, healthDot(health, 10), trailing, widget.NewLabel(name))
		rows = append(rows, divider(), newLinkRow(func() { w.openHook(n) }, row))
	}
	return rows
}

// access is what it runs, as trust approves it, with Trust while it isn't
// trusted.
func (w *Watcher) access() []fyne.CanvasObject {
	d := w.detail
	state := tag("Trusted", successColour)
	if !d.Trusted {
		state = tag("Not trusted", theme.ColorNameWarning)
	}
	program := d.Program()
	var changes []string
	if w.review != nil {
		program, changes = w.review.Program, w.review.Changes
	}
	objects := []fyne.CanvasObject{cardTitle("What it runs", state), divider()}
	objects = append(objects, executableRows(program, signInRow(&program, w.signingIn, w.signInError, w.signIn), false)...)
	objects = append(objects, changedRows(changes)...)
	switch {
	case w.trustError != "" && w.review == nil:
		objects = append(objects, padded(0, 8, rowInset, rowInset, container.NewBorder(nil, nil, nil,
			widget.NewButton("Try Again", w.loadTrust), errorRow(w.trustError))))
	case w.trustError != "":
		objects = append(objects, padded(0, 8, rowInset, rowInset, errorRow(w.trustError)))
	case !d.Trusted && w.review == nil:
		objects = append(objects, padded(0, 8, rowInset, rowInset, container.NewHBox(spinner(), quiet("Reading what it runs…"))))
	}
	var do func()
	if !d.Trusted && w.review != nil {
		do = w.trust
	}
	return reviewButton(objects, "Trust", "Turning on a hook that listens to it trusts it too; this trusts it alone.", do, w.working)
}

// modeText is how a watcher runs, in words: "Polled every 30s", "Streams events".
func modeText(w cli.WatcherInfo) string {
	if w.Mode != "poll" {
		return "Streams events"
	}
	if w.Interval == "" {
		return "Polled"
	}
	return "Polled every " + w.Interval
}

// watcherTags are a watcher's state: trusted, paused, failing.
func watcherTags(w cli.WatcherInfo) []fyne.CanvasObject {
	tags := []fyne.CanvasObject{tag("Trusted", successColour)}
	if !w.Trusted {
		tags[0] = tag("Not trusted", theme.ColorNameWarning)
	}
	if !w.Enabled {
		tags = append(tags, tag("Paused", ""))
	}
	if w.Error != "" {
		tags = append(tags, tag("Failing", theme.ColorNameError))
	}
	return tags
}

// warning is a note to weigh before doing something (trusting a hook that
// writes and acts on outside data), tinted as one.
func warning(title, message string) fyne.CanvasObject {
	icon := widget.NewIcon(theme.NewWarningThemedResource(theme.WarningIcon()))
	bg := canvas.NewRectangle(tint(theme.ColorNameWarning, 26))
	bg.StrokeColor = tint(theme.ColorNameWarning, 77)
	bg.StrokeWidth = 1
	bg.CornerRadius = 10
	body := container.NewVBox(heading(title, false), secondary(message))
	return container.NewStack(bg, padded(6, 6, 8, 8, container.NewBorder(nil, nil, container.NewVBox(icon), nil, body)))
}
