package ui

import (
	"reflect"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Hooks is the Hooks window's content: every hook (`hooks list`) with its
// health, kind, what it acts on in words, whether it's on, its last run and
// summary from run status; and every watcher (`watchers list`) with how it
// runs, its trust, the hooks listening and why it's failing. A row opens
// its window. It mirrors automator-ui's HooksModel and HooksListView.
type Hooks struct {
	cli         cli.CLI
	async       Async
	openHook    func(name string)
	openWatcher func(name string)
	now         func() time.Time

	hooks         []cli.HookInfo
	watchers      []cli.WatcherInfo
	status        *cli.RunStatus
	loadError     string
	watchersError string
	loaded        bool
	generation    int

	scroll *container.Scroll
}

// NewHooks makes the Hooks window's content; Load reads it, Update gives it
// run status.
func NewHooks(c cli.CLI, async Async, openHook, openWatcher func(string)) *Hooks {
	h := &Hooks{cli: c, async: async, openHook: openHook, openWatcher: openWatcher, now: time.Now}
	h.scroll = container.NewVScroll(container.NewVBox())
	h.refresh()
	return h
}

// Content is what the window shows.
func (h *Hooks) Content() fyne.CanvasObject { return h.scroll }

// Load reads the hooks and watchers; only the latest answer counts.
func (h *Hooks) Load() {
	h.generation++
	mine := h.generation
	h.async(func() func() {
		hooks, err := h.cli.Hooks()
		watchers, werr := h.cli.Watchers()
		return func() {
			if mine != h.generation {
				return
			}
			h.loadError, h.watchersError = "", ""
			if err != nil {
				h.loadError = err.Error()
			} else {
				h.hooks = hooks
			}
			if werr != nil {
				h.watchersError = werr.Error()
			} else {
				h.watchers = watchers
			}
			h.loaded = true
			h.refresh()
		}
	})
}

// Update shows a new run status, and reads the lists again when what the
// hooks are changed (their names, last outcomes, whether they're on, their
// problems: a watcher's state).
func (h *Hooks) Update(s *cli.RunStatus) {
	changed := !reflect.DeepEqual(hooksShape(h.status), hooksShape(s))
	h.status = s
	h.refresh()
	if changed {
		h.Load()
	}
}

func hooksShape(s *cli.RunStatus) [][4]string {
	if s == nil {
		return nil
	}
	var out [][4]string
	for _, hk := range s.Hooks {
		outcome := ""
		if hk.LastRun != nil {
			outcome = hk.LastRun.Outcome
		}
		on := "off"
		if hk.Enabled {
			on = "on"
		}
		out = append(out, [4]string{hk.Name, outcome, on, hk.Problem})
	}
	return out
}

func (h *Hooks) refresh() {
	setContent(h.scroll, padded(16, 16, 16, 16, h.render()))
}

func (h *Hooks) render() fyne.CanvasObject {
	box := container.NewVBox(heading("Hooks", true),
		secondary("Prompts and programs run when something happens, instead of on a schedule."))
	if !responding(h.status, h.now()) {
		box.Add(banner("Not responding", "The scheduler isn't updating its status, so what's shown may be out of date."))
	}
	switch {
	case h.loadError != "":
		box.Add(banner("Couldn't read the hooks", h.loadError))
	case h.loaded && len(h.hooks) == 0:
		box.Add(card(false, padded(24, 24, 0, 0, container.NewCenter(quiet("No hooks.")))))
	case len(h.hooks) > 0:
		var rows []fyne.CanvasObject
		for i, hk := range h.hooks {
			if i > 0 {
				rows = append(rows, padded(0, 0, 38, 0, widget.NewSeparator()))
			}
			rows = append(rows, h.hookRow(hk))
		}
		box.Add(card(false, rows...))
	default:
		box.Add(spinner())
	}
	box.Add(padded(8, 0, 0, 0, heading("Watchers", false)))
	switch {
	case h.watchersError != "":
		box.Add(banner("Couldn't read the watchers", h.watchersError))
	case h.loaded && len(h.watchers) == 0:
		box.Add(card(false, padded(24, 24, 0, 0, container.NewCenter(quiet("No watchers.")))))
	case len(h.watchers) > 0:
		var rows []fyne.CanvasObject
		for i, w := range h.watchers {
			if i > 0 {
				rows = append(rows, divider())
			}
			rows = append(rows, h.watcherRow(w))
		}
		box.Add(card(false, rows...))
	}
	box.Add(rich("Add a hook or a watcher from a terminal: `" + h.cli.Command + " guide hooks` says how."))
	return box
}

// hookRow is one hook: its health and name, kind, whether it's on and
// trusted, what it acts on, its last run, its summary; the row opens it.
func (h *Hooks) hookRow(hk cli.HookInfo) fyne.CanvasObject {
	st := h.status.Hook(hk.Name)
	health, summary := "", ""
	last := hk.LastRun
	if st != nil {
		health, summary = st.Health, st.Summary
		if st.LastRun != nil {
			last = st.LastRun
		}
	}
	kind := "Prompt"
	if hk.IsProgram() {
		kind = "Program"
	}
	name := container.NewHBox(widget.NewLabelWithStyle(hk.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), tag(kind, ""))
	if !hk.Enabled {
		name.Add(tag("Off", ""))
	}
	if !hk.Trusted {
		name.Add(tag("Not trusted", theme.ColorNameWarning))
	}
	lines := container.NewVBox(name, secondary("On "+onText(hk.On)))
	if last != nil {
		lines.Add(lastRunLine(last, h.now()))
	}
	trailing := container.NewHBox()
	if summary != "" {
		s := widget.NewLabel(summary)
		s.Truncation = fyne.TextTruncateEllipsis
		s.Importance = widget.LowImportance
		if health == "error" {
			s.Importance = widget.DangerImportance
		}
		trailing.Add(fixedWidth(170, s))
	}
	row := container.NewBorder(nil, nil, healthDot(health, 10), container.NewCenter(trailing), lines)
	n := hk.Name
	return newLinkRow(func() { h.openHook(n) }, padded(4, 4, 0, 0, row))
}

// watcherRow is one watcher: its name and state, how it runs, the hooks
// listening, and why it's failing; the row opens it.
func (h *Hooks) watcherRow(w cli.WatcherInfo) fyne.CanvasObject {
	name := container.NewHBox(widget.NewLabelWithStyle(w.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	for _, t := range watcherTags(w) {
		name.Add(t)
	}
	heard := "No hook listens"
	if len(w.Hooks) > 0 {
		heard = "Heard by " + strings.Join(w.Hooks, ", ")
	}
	lines := container.NewVBox(name, secondary(modeText(w)), secondary(heard))
	if w.Error != "" {
		why := w.Error
		if w.ErrorAt != nil {
			why += " (since " + when(*w.ErrorAt, h.now()) + ")"
		}
		l := text(why)
		l.Importance = widget.DangerImportance
		lines.Add(l)
	}
	n := w.Name
	return newLinkRow(func() { h.openWatcher(n) }, padded(4, 4, 0, 0, lines))
}
