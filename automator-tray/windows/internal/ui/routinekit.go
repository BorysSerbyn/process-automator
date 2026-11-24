package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The routine windows' building blocks, as automator-ui's have them: a
// routine's health as a dot, a run's outcome as a tag, a row that opens
// something, the tabs of a routine's window.

// healthColours are the lights' (automator-ui's system green, red and grey).
var healthColours = map[string]color.NRGBA{
	"ok":    {R: 0x34, G: 0xC7, B: 0x59, A: 0xFF},
	"error": {R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF},
	"none":  {R: 0x8E, G: 0x8E, B: 0x93, A: 0xFF},
}

// healthDot is a routine's health, as the core gives it (none, or unknown: grey).
func healthDot(health string, size float32) fyne.CanvasObject {
	c, ok := healthColours[health]
	if !ok {
		c = healthColours["none"]
	}
	dot := canvas.NewCircle(c)
	dot.StrokeColor = tint(theme.ColorNameForeground, 30)
	dot.StrokeWidth = 0.5
	return container.NewCenter(sized(size, dot))
}

// outcomeColour is the colour of a run's outcome tag.
func outcomeColour(outcome string) fyne.ThemeColorName {
	switch outcome {
	case "success":
		return theme.ColorNameSuccess
	case "failed", "needs-reauth", "missed", "interrupted":
		return theme.ColorNameError
	case "skipped", "stopped":
		return theme.ColorNameWarning
	case "running", "queued":
		return theme.ColorNamePrimary
	}
	return ""
}

// outcomeTag is a run's outcome, as the core names it.
func outcomeTag(outcome string) fyne.CanvasObject { return tag(outcome, outcomeColour(outcome)) }

// linkRow is a row that opens something: its content, what goes at its
// right (a summary, a button), then a chevron; the whole row is the button,
// and it highlights on hover. Its content's text starts where a list's
// divider does, whatever goes at its right.
type linkRow struct {
	widget.BaseWidget
	content  fyne.CanvasObject
	trailing []fyne.CanvasObject
	open     func()
	hover    *canvas.Rectangle
}

func newLinkRow(open func(), content fyne.CanvasObject, trailing ...fyne.CanvasObject) *linkRow {
	r := &linkRow{content: content, trailing: trailing, open: open, hover: canvas.NewRectangle(color.Transparent)}
	r.ExtendBaseWidget(r)
	return r
}

func (r *linkRow) CreateRenderer() fyne.WidgetRenderer {
	right := container.NewHBox()
	for _, t := range r.trailing {
		right.Add(container.NewCenter(t))
	}
	right.Add(container.NewCenter(chevron(false)))
	row := container.NewBorder(nil, nil, nil, right, r.content)
	return widget.NewSimpleRenderer(container.NewStack(r.hover, padded(4, 4, rowInset-theme.InnerPadding(), rowInset, row)))
}

// listInset is where a list's text starts in its row (and its dividers):
// after its health dot.
const listInset = rowInset + 10 + 10

// listRow is a routine's or a hook's row in its list: its health dot, its
// lines, its summary at the right (red when it's in error), Turn On… while
// it needs turning on (turnOn nil: not), then the chevron. Every state lines
// up the same: the dot, the text and the chevron don't move.
func listRow(health string, content fyne.CanvasObject, summary string, turnOn, open func()) *linkRow {
	var trailing []fyne.CanvasObject
	if summary != "" {
		trailing = append(trailing, fixedWidth(180, summaryText(summary, health == "error")))
	}
	if turnOn != nil {
		b := widget.NewButton("Turn On…", turnOn)
		b.Importance = widget.HighImportance
		trailing = append(trailing, b)
	}
	dot := padded(0, 0, theme.InnerPadding(), 10-theme.InnerPadding(), healthDot(health, 10))
	return newLinkRow(open, container.NewBorder(nil, nil, dot, nil, content), trailing...)
}

// listDivider is the hairline between a list's rows, from where their text starts.
func listDivider() fyne.CanvasObject {
	return padded(0, 0, listInset, 0, widget.NewSeparator())
}

// Tapped opens it.
func (r *linkRow) Tapped(*fyne.PointEvent) { r.open() }

// MouseIn highlights it.
func (r *linkRow) MouseIn(*desktop.MouseEvent) {
	r.hover.FillColor = theme.Color(theme.ColorNameHover)
	r.hover.Refresh()
}

// MouseMoved does nothing: the highlight stays while the pointer is in.
func (r *linkRow) MouseMoved(*desktop.MouseEvent) {}

// MouseOut takes the highlight away.
func (r *linkRow) MouseOut() {
	r.hover.FillColor = color.Transparent
	r.hover.Refresh()
}

// Cursor is the pointing hand: it's a button.
func (r *linkRow) Cursor() desktop.Cursor { return desktop.PointerCursor }

// segmented is a row of buttons, one per choice, the chosen one prominent:
// a routine window's tabs.
func segmented(choices []string, chosen string, choose func(string)) fyne.CanvasObject {
	row := container.NewHBox()
	for _, c := range choices {
		b := widget.NewButton(c, func() { choose(c) })
		if c == chosen {
			b.Importance = widget.HighImportance
		}
		row.Add(b)
	}
	return container.NewCenter(row)
}

// cardTitle is a card's title row, with what goes at its right.
func cardTitle(title string, trailing ...fyne.CanvasObject) fyne.CanvasObject {
	right := container.NewHBox()
	for _, t := range trailing {
		right.Add(container.NewCenter(t))
	}
	return padded(4, 0, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, right, heading(title, false)))
}

// quiet is a small grey label.
func quiet(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Importance = widget.LowImportance
	return l
}

// plural is n things in words: "1 run", "3 runs".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}
