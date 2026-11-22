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
	return container.NewCenter(sized(size, canvas.NewCircle(c)))
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

// linkRow is a row that opens something: its content, then a chevron; the
// whole row is the button, and it highlights on hover.
type linkRow struct {
	widget.BaseWidget
	content fyne.CanvasObject
	open    func()
	hover   *canvas.Rectangle
}

func newLinkRow(open func(), content fyne.CanvasObject) *linkRow {
	r := &linkRow{content: content, open: open, hover: canvas.NewRectangle(color.Transparent)}
	r.ExtendBaseWidget(r)
	return r
}

func (r *linkRow) CreateRenderer() fyne.WidgetRenderer {
	chevron := widget.NewIcon(theme.NavigateNextIcon())
	row := container.NewBorder(nil, nil, nil, container.NewCenter(chevron), r.content)
	return widget.NewSimpleRenderer(container.NewStack(r.hover, padded(4, 4, rowInset-6, rowInset-6, row)))
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
	return padded(4, 0, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, container.NewHBox(trailing...), heading(title, false)))
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
