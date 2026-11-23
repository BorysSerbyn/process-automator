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
	// open are the notifications whose whole text shows.
	open     map[string]bool
	shownIDs []string

	scroll *container.Scroll
}

// NewAcknowledgements makes the window's content; Load reads it, Update
// gives it run status.
func NewAcknowledgements(c cli.CLI, async Async) *Acknowledgements {
	a := &Acknowledgements{cli: c, async: async, now: time.Now, acknowledging: map[string]bool{}, open: map[string]bool{}}
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
	content := padded(16, 16, 16, 16, a.render())
	setContent(a.scroll, content)
	// A wrapping label's height is first guessed before it has its width:
	// laid out again once it has, a long text's card fits it exactly.
	content.Refresh()
	a.scroll.Refresh()
}

func (a *Acknowledgements) render() fyne.CanvasObject {
	subtitle := "Notifications a routine or hook asks you to acknowledge wait here."
	switch n := len(a.waiting); {
	case n == 1:
		subtitle = "1 waiting. The dot stays red until you acknowledge it."
	case n > 1:
		subtitle = itoa(n) + " waiting. The dot stays red until you acknowledge them."
	}
	title := container.NewVBox(heading("Acknowledgements", true), padded(-8, 0, 0, 0, secondary(subtitle)))
	var all fyne.CanvasObject = container.NewHBox()
	if len(a.waiting) > 1 {
		b := widget.NewButton("Acknowledge All", func() { a.acknowledge("") })
		if a.acknowledging["*"] {
			b.Disable()
		}
		all = container.NewCenter(b)
	}
	box := container.NewVBox(container.NewBorder(nil, nil, nil, all, title))
	if a.err != "" {
		box.Add(banner("Couldn't acknowledge", a.err))
	}
	switch {
	case a.loadError != "":
		box.Add(banner("Couldn't read what's waiting", a.loadError))
	case a.loaded && len(a.waiting) == 0:
		check := widget.NewIcon(theme.NewSuccessThemedResource(theme.ConfirmIcon()))
		caught := widget.NewLabelWithStyle("All caught up", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
		hint := secondary("When a routine or hook asks you to acknowledge something, it shows here.")
		hint.Segments[0].(*widget.TextSegment).Style.Alignment = fyne.TextAlignCenter
		box.Add(card(false, padded(28, 28, 16, 16, container.NewVBox(container.NewCenter(sized(36, check)), caught, hint))))
	case len(a.waiting) > 0:
		for _, n := range a.waiting {
			box.Add(padded(4, 4, 0, 0, a.card(n)))
		}
	default:
		box.Add(spinner())
	}
	return box
}

// foldedLines is how many lines a long text shows while folded.
const foldedLines = 5

// card is one notification waiting, as a card of its own: an icon (red
// when urgent), its title, what sent it and when; its text under them,
// lines and paragraphs as written (plain text, selectable), a long one
// folded to its first lines and opening in place, bulleted lines hanging
// after their bullet; and a footer under a divider: Show More at the left,
// Open Report and Acknowledge at the right, apart from the text.
func (a *Acknowledgements) card(n cli.Notification) fyne.CanvasObject {
	icon := widget.NewIcon(theme.NewWarningThemedResource(theme.InfoIcon()))
	if n.Urgent {
		icon = widget.NewIcon(theme.NewErrorThemedResource(theme.WarningIcon()))
	}
	var about []string
	if n.Routine != "" {
		about = append(about, n.Routine)
	}
	about = append(about, when(n.At, a.now()))
	meta := secondary(strings.Join(about, " · "))
	title := widget.NewLabelWithStyle(n.Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Wrapping = fyne.TextWrapWord
	var urgent fyne.CanvasObject = container.NewHBox()
	if n.Urgent {
		urgent = container.NewVBox(padded(8, 0, 0, 0, tag("Urgent", theme.ColorNameError)))
	}
	head := container.NewBorder(nil, nil, container.NewVBox(padded(4, 0, 0, 0, sized(24, icon))), urgent,
		container.NewVBox(title, padded(-12, 0, 0, 0, meta)))
	content := container.NewVBox(head)
	id := n.ID
	body := strings.TrimSpace(n.Body)
	if body != "" {
		content.Add(padded(-4, 0, 32, 0, a.text(id, body)))
	}
	var more fyne.CanvasObject = container.NewHBox()
	if folds(body) {
		label, icon := "Show More", theme.MenuExpandIcon()
		if a.open[id] {
			label, icon = "Show Less", theme.MenuDropDownIcon()
		}
		b := widget.NewButtonWithIcon(label, icon, func() { a.toggle(id) })
		b.Importance = widget.LowImportance
		more = b
	}
	actions := container.NewHBox()
	if n.Run != "" {
		run := n.Run
		actions.Add(widget.NewButton("Open Report", func() { a.openReport(run) }))
	}
	if a.acknowledging[id] {
		actions.Add(spinner())
	} else {
		ack := widget.NewButton("Acknowledge", func() { a.acknowledge(id) })
		ack.Importance = widget.HighImportance
		actions.Add(ack)
	}
	footer := container.NewBorder(nil, nil, more, actions)
	return card(false, padded(10, 8, 12, 12, content), widget.NewSeparator(), padded(4, 4, 8, 8, footer))
}

// text is a notification's text, as one selectable block (copied whole):
// folded to its first lines while it's long and closed; else all of it,
// its lines and paragraphs as written, bulleted lines (•, -, *) with a
// bullet of their own.
func (a *Acknowledgements) text(id, body string) fyne.CanvasObject {
	if folds(body) && !a.open[id] {
		return selectable(firstLines(body))
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		for _, b := range []string{"•", "-", "*", "–"} {
			if strings.HasPrefix(line, b+" ") {
				line = "•  " + strings.TrimSpace(line[len(b)+1:])
				break
			}
		}
		lines[i] = line
	}
	return selectable(strings.Join(lines, "\n"))
}

// selectable is wrapping text that can be selected and copied.
func selectable(s string) *widget.Label {
	l := text(s)
	l.Selectable = true
	return l
}

func (a *Acknowledgements) toggle(id string) {
	a.open[id] = !a.open[id]
	a.refresh()
}

// folds is whether a text is long enough to fold to its first lines.
func folds(s string) bool {
	return len(s) > 280 || strings.Count(s, "\n") >= foldedLines
}

// firstLines is a text folded: its first lines, at most 280 characters,
// ending in an ellipsis.
func firstLines(s string) string {
	lines := strings.SplitN(s, "\n", foldedLines+1)
	if len(lines) > foldedLines {
		lines = lines[:foldedLines]
	}
	out := strings.Join(lines, "\n")
	if r := []rune(out); len(r) > 280 {
		out = string(r[:279])
	}
	return strings.TrimRight(out, " \n") + "…"
}
