package ui

import (
	"reflect"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Routines is the Routines window's content: every routine (`routines
// list`) with its health, schedule in words, last run and summary from run
// status; a routine opens its window. The list is read again when what
// the routines are changes in run status (a run ending, a routine paused or
// deleted). It mirrors automator-ui's RoutinesModel and RoutinesListView.
type Routines struct {
	cli   cli.CLI
	async Async
	// open opens a routine's window, and the Setup and Settings windows.
	openRoutine  func(name string)
	turnOn       func(name string)
	openSetup    func()
	openSettings func()
	now          func() time.Time

	routines   []cli.RoutineInfo
	status     *cli.RunStatus
	loadError  string
	loaded     bool
	generation int

	scroll *container.Scroll
}

// NewRoutines makes the Routines window's content; Load reads it, Update
// gives it run status.
// turnOn opens a routine's window on its Turn On review.
func NewRoutines(c cli.CLI, async Async, openRoutine, turnOn func(string), openSetup, openSettings func()) *Routines {
	r := &Routines{cli: c, async: async, openRoutine: openRoutine, turnOn: turnOn, openSetup: openSetup, openSettings: openSettings,
		now: time.Now}
	r.scroll = container.NewVScroll(container.NewVBox())
	r.refresh()
	return r
}

// Content is what the window shows.
func (r *Routines) Content() fyne.CanvasObject { return r.scroll }

// Load reads the routines; only the latest answer counts.
func (r *Routines) Load() {
	r.generation++
	mine := r.generation
	r.async(func() func() {
		list, err := r.cli.Routines()
		return func() {
			if mine != r.generation {
				return
			}
			if err != nil {
				r.loadError = err.Error()
			} else {
				r.routines, r.loadError = list, ""
			}
			r.loaded = true
			r.refresh()
		}
	})
}

// Update shows a new run status, and reads the list again when what the
// routines are changed (their names, last outcomes, whether they're on).
func (r *Routines) Update(s *cli.RunStatus) {
	changed := !reflect.DeepEqual(routinesShape(r.status), routinesShape(s))
	r.status = s
	r.refresh()
	if changed {
		r.Load()
	}
}

func routinesShape(s *cli.RunStatus) [][3]string {
	if s == nil {
		return nil
	}
	var out [][3]string
	for _, rt := range s.Routines {
		outcome := ""
		if rt.LastRun != nil {
			outcome = rt.LastRun.Outcome
		}
		on := "off"
		if rt.Enabled {
			on = "on"
		}
		out = append(out, [3]string{rt.Name, outcome, on})
	}
	return out
}

// responding is whether the core's status is readable and not past its
// validUntil: the frontends' one judgement.
func responding(s *cli.RunStatus, now time.Time) bool {
	return s != nil && s.ValidUntil != nil && !now.After(*s.ValidUntil)
}

func (r *Routines) refresh() {
	setContent(r.scroll, padded(16, 16, 16, 16, r.render()))
}

func (r *Routines) render() fyne.CanvasObject {
	box := container.NewVBox(r.header())
	if !responding(r.status, r.now()) {
		box.Add(banner("Not responding", "The scheduler isn't updating its status, so what's shown may be out of date."))
	}
	switch {
	case r.loadError != "":
		box.Add(banner("Couldn't read the routines", r.loadError))
	case r.loaded && len(r.routines) == 0:
		box.Add(card(false, padded(24, 24, 0, 0, container.NewCenter(quiet("No routines installed.")))))
	case len(r.routines) > 0:
		var rows []fyne.CanvasObject
		for i, rt := range r.routines {
			if i > 0 {
				rows = append(rows, padded(0, 0, 38, 0, widget.NewSeparator()))
			}
			rows = append(rows, r.row(rt))
		}
		box.Add(card(false, rows...))
	default:
		box.Add(spinner())
	}
	return box
}

func (r *Routines) header() fyne.CanvasObject {
	title := container.NewVBox(heading("Routines", true))
	if r.status != nil && r.status.Summary != "" && responding(r.status, r.now()) {
		title.Add(secondary(r.status.Summary))
	}
	setup := widget.NewButtonWithIcon("Setup", theme.ListIcon(), r.openSetup)
	settings := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), r.openSettings)
	return container.NewBorder(nil, nil, nil, container.NewCenter(container.NewHBox(setup, settings)), title)
}

// row is one routine: its health and name, its schedule, its last run, its
// summary; the whole row opens it.
func (r *Routines) row(rt cli.RoutineInfo) fyne.CanvasObject {
	st := r.status.Routine(rt.Name)
	health, summary := "", ""
	last := rt.LastRun
	if st != nil {
		health, summary = st.Health, st.Summary
		if st.LastRun != nil {
			last = st.LastRun
		}
	}
	name := container.NewHBox(widget.NewLabelWithStyle(rt.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	schedule := rt.ScheduleText
	if schedule == "" {
		schedule = "No schedule"
	}
	lines := container.NewVBox(name, secondary(schedule))
	if last != nil {
		lines.Add(lastRunLine(last, r.now()))
	}
	trailing := container.NewHBox()
	if summary != "" {
		s := widget.NewLabel(summary)
		s.Truncation = fyne.TextTruncateEllipsis
		if health == "error" {
			s.Importance = widget.DangerImportance
		} else {
			s.Importance = widget.LowImportance
		}
		trailing.Add(fixedWidth(170, s))
	}
	row := container.NewBorder(nil, nil, healthDot(health, 10), container.NewCenter(trailing), lines)
	name2 := rt.Name
	return withTurnOn(rt.TurnOn, func() { r.turnOn(name2) }, newLinkRow(func() { r.openRoutine(name2) }, padded(4, 4, 0, 0, row)))
}

// withTurnOn puts Turn On… beside a list's row while it needs turning on:
// it opens the window on its review.
func withTurnOn(needed bool, turnOn func(), row fyne.CanvasObject) fyne.CanvasObject {
	if !needed {
		return row
	}
	b := widget.NewButton("Turn On…", turnOn)
	b.Importance = widget.HighImportance
	return container.NewBorder(nil, nil, nil, padded(0, 0, 0, rowInset-6, container.NewCenter(b)), row)
}

// lastRunLine is a last run in a line: when, how it ended, why.
func lastRunLine(last *cli.LastRun, now time.Time) fyne.CanvasObject {
	at := "Waiting for its turn"
	if last.StartedAt != nil {
		at = "Last run " + when(*last.StartedAt, now)
	}
	row := container.NewHBox(quiet(at), outcomeTag(last.Outcome))
	if last.Reason != "" {
		reason := quiet(last.Reason)
		reason.Truncation = fyne.TextTruncateEllipsis
		return container.NewBorder(nil, nil, row, nil, reason)
	}
	return container.NewHBox(row, layout.NewSpacer())
}
