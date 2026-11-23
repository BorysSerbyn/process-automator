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

// Acknowledgements is the Acknowledgements window's content: each
// notification a routine or hook asked me to acknowledge (`notifications
// list --waiting`): what sent it and when, its title and text (plain text),
// its run's report, and Acknowledge (`notifications ack <id>`); Acknowledge
// All (`--all`). It's read again when run status says what waits changed.
// It mirrors automator-ui's Acknowledgements and AcknowledgementsView.
type Acknowledgements struct {
	cli   cli.CLI
	async Async
	now   func() time.Time

	waiting       []cli.Notification
	loaded        bool
	loadError     string
	err           string
	acknowledging map[string]bool
	shownIDs      []string

	scroll *container.Scroll
}

// NewAcknowledgements makes the window's content; Load reads it, Update
// gives it run status.
func NewAcknowledgements(c cli.CLI, async Async) *Acknowledgements {
	a := &Acknowledgements{cli: c, async: async, now: time.Now, acknowledging: map[string]bool{}}
	a.scroll = container.NewVScroll(container.NewVBox())
	a.refresh()
	return a
}

// Content is what the window shows.
func (a *Acknowledgements) Content() fyne.CanvasObject { return a.scroll }

// Load reads what waits.
func (a *Acknowledgements) Load() {
	a.async(func() func() {
		list, err := a.cli.WaitingNotifications()
		return func() {
			if err != nil {
				a.loadError = err.Error()
			} else {
				a.waiting, a.loadError = list, ""
			}
			a.loaded = true
			a.refresh()
		}
	})
}

// Update reads the list again when what waits changed.
func (a *Acknowledgements) Update(s *cli.RunStatus) {
	ids := []string{}
	if s != nil {
		for _, w := range s.Waiting {
			ids = append(ids, w.ID)
		}
	}
	if reflect.DeepEqual(ids, a.shownIDs) {
		return
	}
	a.shownIDs = ids
	a.Load()
}

// acknowledge acknowledges one ("": every one waiting).
func (a *Acknowledgements) acknowledge(id string) {
	key := id
	if key == "" {
		key = "*"
	}
	a.acknowledging[key], a.err = true, ""
	a.refresh()
	a.async(func() func() {
		var err error
		if id == "" {
			err = a.cli.Acknowledge()
		} else {
			err = a.cli.Acknowledge(id)
		}
		return func() {
			delete(a.acknowledging, key)
			if err != nil {
				a.err = err.Error()
			}
			a.Load()
		}
	})
}

func (a *Acknowledgements) openReport(run string) {
	a.async(func() func() {
		err := a.cli.OpenRun(run)
		return func() {
			if err != nil {
				a.err = err.Error()
				a.refresh()
			}
		}
	})
}

func (a *Acknowledgements) refresh() {
	setContent(a.scroll, padded(16, 16, 16, 16, a.render()))
}

func (a *Acknowledgements) render() fyne.CanvasObject {
	title := container.NewVBox(heading("Acknowledgements", true),
		secondary("Notifications that wait for you to acknowledge them; the dot stays red until you do."))
	var all fyne.CanvasObject = container.NewHBox()
	if len(a.waiting) > 1 {
		b := widget.NewButton("Acknowledge All", func() { a.acknowledge("") })
		b.Importance = widget.HighImportance
		if a.acknowledging["*"] {
			b.Disable()
		}
		all = container.NewCenter(b)
	}
	box := container.NewVBox(container.NewBorder(nil, nil, nil, all, title))
	if a.err != "" {
		box.Add(errorRow(a.err))
	}
	switch {
	case a.loadError != "":
		box.Add(banner("Couldn't read what's waiting", a.loadError))
	case a.loaded && len(a.waiting) == 0:
		box.Add(card(false, padded(24, 24, 0, 0, container.NewCenter(quiet("Nothing waits for you.")))))
	case len(a.waiting) > 0:
		var rows []fyne.CanvasObject
		for i, n := range a.waiting {
			if i > 0 {
				rows = append(rows, divider())
			}
			rows = append(rows, a.row(n))
		}
		box.Add(card(false, rows...))
	default:
		box.Add(spinner())
	}
	return box
}

// row is one notification waiting: its title and text (the routine's
// words, plain text), what sent it and when, its run's report, and
// Acknowledge.
func (a *Acknowledgements) row(n cli.Notification) fyne.CanvasObject {
	lines := container.NewVBox(widget.NewLabelWithStyle(n.Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	if n.Body != "" {
		lines.Add(text(n.Body))
	}
	var about []string
	if n.Routine != "" {
		about = append(about, n.Routine)
	}
	about = append(about, when(n.At, a.now()))
	meta := container.NewHBox(quiet(strings.Join(about, " · ")))
	if n.Run != "" {
		run := n.Run
		b := widget.NewButton("Open Report", func() { a.openReport(run) })
		b.Importance = widget.LowImportance
		meta.Add(b)
	}
	lines.Add(meta)
	var action fyne.CanvasObject
	if a.acknowledging[n.ID] {
		action = spinner()
	} else {
		id := n.ID
		action = container.NewCenter(widget.NewButton("Acknowledge", func() { a.acknowledge(id) }))
	}
	icon := widget.NewIcon(theme.NewWarningThemedResource(theme.WarningIcon()))
	if n.Urgent {
		icon = widget.NewIcon(theme.NewErrorThemedResource(theme.ErrorIcon()))
	}
	return padded(4, 4, rowInset-8, rowInset-8, container.NewBorder(nil, nil, container.NewVBox(icon), action, lines))
}
