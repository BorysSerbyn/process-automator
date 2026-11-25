package ui

import (
	"reflect"
	"slices"
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
// All (`--all`). Under them, Past acknowledgements (`notifications list
// --acknowledged`): those I've acknowledged, the last acknowledged first,
// as the same cards saying when I acknowledged each, with Open Report but
// no Acknowledge; the most recent few, Show More for older ones; not shown
// while there are none. Both are read again when run status says what
// waits changed, so acknowledging one moves it into the past.
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
	// past are those I've acknowledged, the last acknowledged first; why
	// they couldn't be read; how many of them show.
	past      []cli.Notification
	pastError string
	pastShown int
	// open are the notifications whose whole text shows.
	open     map[string]bool
	shownIDs []string

	scroll *container.Scroll
}

// NewAcknowledgements makes the window's content; Load reads it, Update
// gives it run status.
func NewAcknowledgements(c cli.CLI, async Async) *Acknowledgements {
	a := &Acknowledgements{cli: c, async: async, now: time.Now, acknowledging: map[string]bool{}, open: map[string]bool{},
		pastShown: pastPage}
	a.scroll = container.NewVScroll(container.NewVBox())
	a.refresh()
	return a
}

// Content is what the window shows.
func (a *Acknowledgements) Content() fyne.CanvasObject { return a.scroll }

// pastPage is how many past acknowledgements show at first, pastMore how
// many more each Show More adds.
const (
	pastPage = 5
	pastMore = 20
)

// Load reads what waits, and what I've acknowledged.
func (a *Acknowledgements) Load() {
	a.async(func() func() {
		list, err := a.cli.WaitingNotifications()
		past, pastErr := a.cli.AcknowledgedNotifications()
		return func() {
			if err != nil {
				a.loadError = err.Error()
			} else {
				a.waiting, a.loadError = list, ""
			}
			if pastErr != nil {
				a.pastError = pastErr.Error()
			} else {
				slices.Reverse(past)
				a.past, a.pastError = past, ""
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
	title := lines(pageTitle("Acknowledgements"), secondary(subtitle))
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
	if a.loaded {
		a.renderPast(box)
	}
	return box
}

// renderPast adds those I've acknowledged under what waits: a heading, the
// most recent cards, and Show More while older ones are hidden. Nothing
// while there are none.
func (a *Acknowledgements) renderPast(box *fyne.Container) {
	if a.pastError != "" {
		box.Add(banner("Couldn't read past acknowledgements", a.pastError))
		return
	}
	if len(a.past) == 0 {
		return
	}
	box.Add(padded(16, 4, 0, 0, lines(heading("Past acknowledgements", true),
		secondary("What you've acknowledged, the most recent first."))))
	shown := a.past[:min(a.pastShown, len(a.past))]
	for _, n := range shown {
		box.Add(padded(4, 4, 0, 0, a.card(n)))
	}
	if older := len(a.past) - len(shown); older > 0 {
		b := widget.NewButtonWithIcon("Show "+itoa(min(older, pastMore))+" More", theme.MenuExpandIcon(), func() {
			a.pastShown += pastMore
			a.refresh()
		})
		b.Importance = widget.LowImportance
		box.Add(container.NewCenter(b))
	}
}

// foldedLines is how many lines a long text shows while folded.
const foldedLines = 5

// card is one notification waiting, as a card of its own: an icon (red
// when urgent), its title, what sent it and when; its text under them,
// lines and paragraphs as written (plain text, selectable), a long one
// folded to its first lines and opening in place, bulleted lines hanging
// after their bullet; and a footer under a divider: Show More at the left,
// Open Report and Acknowledge at the right, apart from the text. One I've
// acknowledged has a muted check for its icon, says when I acknowledged
// it, and has no Acknowledge (and no footer when there's nothing for one).
func (a *Acknowledgements) card(n cli.Notification) fyne.CanvasObject {
	acked := n.AcknowledgedAt != nil
	icon := widget.NewIcon(theme.NewWarningThemedResource(theme.InfoIcon()))
	switch {
	case acked:
		icon = widget.NewIcon(theme.NewDisabledResource(theme.ConfirmIcon()))
	case n.Urgent:
		icon = widget.NewIcon(theme.NewErrorThemedResource(theme.WarningIcon()))
	}
	var about []string
	if n.Routine != "" {
		about = append(about, n.Routine)
	}
	if acked {
		about = append(about, acknowledgedWhen(*n.AcknowledgedAt, a.now()))
	} else {
		about = append(about, when(n.At, a.now()))
	}
	meta := secondary(strings.Join(about, " · "))
	title := widget.NewLabelWithStyle(n.Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Wrapping = fyne.TextWrapWord
	var urgent fyne.CanvasObject = container.NewHBox()
	if n.Urgent {
		urgent = container.NewVBox(padded(8, 0, 0, 0, tag("Urgent", theme.ColorNameError)))
	}
	head := container.NewBorder(nil, nil, container.NewVBox(padded(4, 0, 0, 0, sized(24, icon))), urgent,
		lines(title, meta))
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
	switch {
	case acked:
	case a.acknowledging[id]:
		actions.Add(spinner())
	default:
		ack := widget.NewButton("Acknowledge", func() { a.acknowledge(id) })
		ack.Importance = widget.HighImportance
		actions.Add(ack)
	}
	if acked && n.Run == "" && !folds(body) {
		return card(false, padded(10, 8, 12, 12, content))
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

// acknowledgedWhen is when I acknowledged one, as a phrase: "Acknowledged
// today at 09:05", "Acknowledged 20 Nov 2026 at 09:05".
func acknowledgedWhen(t, now time.Time) string {
	w := when(t, now)
	for _, day := range []string{"Today", "Yesterday", "Tomorrow"} {
		if strings.HasPrefix(w, day) {
			w = strings.ToLower(day) + w[len(day):]
		}
	}
	return "Acknowledged " + w
}
