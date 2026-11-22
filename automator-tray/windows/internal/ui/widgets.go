// Package ui is the windows app's windows, automator-ui's counterparts in
// Fyne: the Setup window and the Settings window. They act only through the
// app's CLI, and show what it prints in the core's words.
package ui

import (
	"image/color"
	"net/url"
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// The windows' building blocks, as automator-ui's settings and setup
// windows have them: cards, rows that open and close, tags, the core's
// messages.

// rowInset is the horizontal padding inside a card, for headers and rows alike.
const rowInset = 14

// padded puts space around o.
func padded(top, bottom, left, right float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(top, bottom, left, right), o)
}

// tint is the theme's colour c at alpha a (0-255).
func tint(c fyne.ThemeColorName, a uint8) color.Color {
	r, g, b, _ := theme.Color(c).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: a}
}

// card is a rounded group box, like a grouped form's section; nested ones
// sit inside another card.
func card(nested bool, objects ...fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(tint(theme.ColorNameForeground, 11))
	bg.StrokeColor = tint(theme.ColorNameForeground, 24)
	bg.StrokeWidth = 1
	bg.CornerRadius = 10
	if nested {
		bg.FillColor = tint(theme.ColorNameForeground, 9)
		bg.StrokeColor = tint(theme.ColorNameForeground, 18)
		bg.CornerRadius = 8
	}
	return container.NewStack(bg, container.New(&stackLayout{}, objects...))
}

// stackLayout is a VBox without spacing: rows bring their own padding.
type stackLayout struct{}

func (*stackLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h
	}
}

func (*stackLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		s.Width = max(s.Width, m.Width)
		s.Height += m.Height
	}
	return s
}

// divider is a hairline between rows, inset from the card's edge.
func divider() fyne.CanvasObject {
	return padded(0, 0, rowInset, 0, widget.NewSeparator())
}

// text is a label that wraps.
func text(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// heading is a bold label: a card's or a step's title (prominent: bigger).
func heading(s string, prominent bool) *widget.Label {
	l := widget.NewLabel(s)
	l.TextStyle = fyne.TextStyle{Bold: true}
	if prominent {
		l.SizeName = theme.SizeNameSubHeadingText
	}
	l.Wrapping = fyne.TextWrapWord
	return l
}

// secondary is quieter text that wraps: a description, the core's status.
func secondary(s string) *widget.RichText {
	t := widget.NewRichText(&widget.TextSegment{Text: s, Style: widget.RichTextStyle{
		ColorName: theme.ColorNamePlaceHolder, SizeName: theme.SizeNameText, Inline: true,
	}})
	t.Wrapping = fyne.TextWrapWord
	return t
}

// errorRow is the core's message when it refused something.
func errorRow(message string) fyne.CanvasObject {
	l := text(message)
	l.Importance = widget.DangerImportance
	icon := widget.NewIcon(theme.NewErrorThemedResource(theme.ErrorIcon()))
	return container.NewBorder(nil, nil, container.NewVBox(padded(6, 0, 0, 0, icon)), nil, l)
}

// banner is a window-wide note above the content.
func banner(title, message string) fyne.CanvasObject {
	icon := widget.NewIcon(theme.NewErrorThemedResource(theme.ErrorIcon()))
	bg := canvas.NewRectangle(tint(theme.ColorNameError, 26))
	bg.StrokeColor = tint(theme.ColorNameError, 77)
	bg.StrokeWidth = 1
	bg.CornerRadius = 10
	body := container.NewVBox(heading(title, false))
	if message != "" {
		body.Add(secondary(message))
	}
	return container.NewStack(bg, padded(6, 6, 8, 8, container.NewBorder(nil, nil, container.NewVBox(icon), nil, body)))
}

// tag is a small capsule: Done, Optional, Installed, why a setting is inactive.
func tag(s string, colour fyne.ThemeColorName) fyne.CanvasObject {
	if colour == "" {
		colour = theme.ColorNameForeground
	}
	bg := canvas.NewRectangle(tint(colour, 36))
	bg.CornerRadius = 8
	t := canvas.NewText(s, theme.Color(colour))
	if colour == theme.ColorNameForeground {
		t.Color = theme.Color(theme.ColorNamePlaceHolder)
	}
	t.TextSize = theme.CaptionTextSize()
	t.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewCenter(container.NewStack(bg, padded(1, 1, 6, 6, t)))
}

// spinner shows the core is working on something.
func spinner() fyne.CanvasObject {
	a := widget.NewActivity()
	a.Start()
	return container.NewCenter(a)
}

// fixedWidth gives o a width of its own (labels in a column line up).
func fixedWidth(w float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&widthLayout{width: w}, o)
}

type widthLayout struct{ width float32 }

func (l *widthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(l.width, size.Height))
	}
}

func (l *widthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objects {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(l.width, h)
}

// right puts o at the right of the row.
func right(o ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewHBox(append([]fyne.CanvasObject{layout.NewSpacer()}, o...)...)
}

// disclosure is a row that opens and closes what's under it: a chevron,
// then its content; the whole row is the button, and it highlights on hover.
type disclosure struct {
	widget.BaseWidget
	open     bool
	content  fyne.CanvasObject
	toggle   func()
	hover    *canvas.Rectangle
	vertical float32
}

func newDisclosure(open, prominent bool, toggle func(), content fyne.CanvasObject) *disclosure {
	d := &disclosure{open: open, content: content, toggle: toggle, hover: canvas.NewRectangle(color.Transparent), vertical: 8}
	if prominent {
		d.vertical = 11
	}
	d.ExtendBaseWidget(d)
	return d
}

func (d *disclosure) CreateRenderer() fyne.WidgetRenderer {
	icon := theme.MenuExpandIcon()
	if d.open {
		icon = theme.MenuDropDownIcon()
	}
	chevron := widget.NewIcon(icon)
	row := container.NewBorder(nil, nil, container.NewCenter(chevron), nil, d.content)
	return widget.NewSimpleRenderer(container.NewStack(d.hover, padded(d.vertical-6, d.vertical-6, rowInset-4, rowInset, row)))
}

// Tapped opens or closes it.
func (d *disclosure) Tapped(*fyne.PointEvent) { d.toggle() }

// MouseIn highlights it.
func (d *disclosure) MouseIn(*desktop.MouseEvent) {
	d.hover.FillColor = theme.Color(theme.ColorNameHover)
	d.hover.Refresh()
}

// MouseMoved does nothing: the highlight stays while the pointer is in.
func (d *disclosure) MouseMoved(*desktop.MouseEvent) {}

// MouseOut takes the highlight away.
func (d *disclosure) MouseOut() {
	d.hover.FillColor = color.Transparent
	d.hover.Refresh()
}

// Cursor is the pointing hand: it's a button.
func (d *disclosure) Cursor() desktop.Cursor { return desktop.PointerCursor }

// flow lays objects out left to right, wrapping onto new lines: chips.
// Its height is worked out for the width it was last laid out at (at first,
// a typical card's).
type flow struct {
	gap   float32
	width float32
}

func newFlow(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&flow{gap: 6, width: 420}, objects...)
}

func (f *flow) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	f.width = size.Width
	f.place(objects, size.Width, true)
}

func (f *flow) MinSize(objects []fyne.CanvasObject) fyne.Size {
	widest := float32(0)
	for _, o := range objects {
		widest = max(widest, o.MinSize().Width)
	}
	return fyne.NewSize(widest, f.place(objects, max(f.width, widest), false))
}

// place lays objects out at width (or only measures them), returning the height.
func (f *flow) place(objects []fyne.CanvasObject, width float32, move bool) float32 {
	x, y, line := float32(0), float32(0), float32(0)
	for _, o := range objects {
		m := o.MinSize()
		if x > 0 && x+m.Width > width {
			x, y, line = 0, y+line+f.gap, 0
		}
		if move {
			o.Move(fyne.NewPos(x, y))
			o.Resize(m)
		}
		x += m.Width + f.gap
		line = max(line, m.Height)
	}
	return y + line
}

// chips are names as tags (marked ones tinted: tools that write).
func chips(names []string, marked map[string]bool) fyne.CanvasObject {
	if len(names) == 0 {
		return secondary("None")
	}
	objects := make([]fyne.CanvasObject, 0, len(names))
	for _, n := range names {
		c := fyne.ThemeColorName("")
		if marked[n] {
			c = theme.ColorNameWarning
		}
		objects = append(objects, tag(n, c))
	}
	// Level with the label beside it (a label's text sits inside its padding).
	return padded(theme.InnerPadding()-2, 0, theme.InnerPadding(), 0, newFlow(objects...))
}

// infoRow is a label in a column of its own, and what it's about.
func infoRow(label string, content ...fyne.CanvasObject) fyne.CanvasObject {
	l := secondary(label)
	return padded(0, 0, rowInset-8, rowInset, container.NewBorder(nil, nil,
		container.NewVBox(fixedWidth(110, l)), nil, container.NewVBox(content...)))
}

var (
	codeSpan = regexp.MustCompile("`[^`]+`")
	webLink  = regexp.MustCompile(`https?://[^\s<>()"']+[^\s<>()"'.,;:!?]`)
)

// rich is a line of the core's text: `code` shows as code, and web
// addresses become links.
func rich(line string) *widget.RichText {
	var segments []widget.RichTextSegment
	plain := func(s string) {
		for s != "" {
			loc := webLink.FindStringIndex(s)
			if loc == nil {
				segments = append(segments, &widget.TextSegment{Text: s, Style: widget.RichTextStyleInline})
				return
			}
			if loc[0] > 0 {
				segments = append(segments, &widget.TextSegment{Text: s[:loc[0]], Style: widget.RichTextStyleInline})
			}
			if u, err := url.Parse(s[loc[0]:loc[1]]); err == nil {
				segments = append(segments, &widget.HyperlinkSegment{Text: s[loc[0]:loc[1]], URL: u})
			}
			s = s[loc[1]:]
		}
	}
	for line != "" {
		loc := codeSpan.FindStringIndex(line)
		if loc == nil {
			plain(line)
			break
		}
		plain(line[:loc[0]])
		segments = append(segments, &widget.TextSegment{Text: strings.Trim(line[loc[0]:loc[1]], "`"), Style: widget.RichTextStyleCodeInline})
		line = line[loc[1]:]
	}
	t := widget.NewRichText(segments...)
	t.Wrapping = fyne.TextWrapWord
	return t
}

// numbered splits the core's text into lines, keeping a "1." apart.
func numbered(s string) (lines []struct{ number, text string }) {
	for _, raw := range strings.Split(s, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		line := struct{ number, text string }{text: raw}
		if dot := strings.IndexByte(raw, '.'); dot > 0 && strings.Trim(raw[:dot], "0123456789") == "" {
			line.number, line.text = raw[:dot+1], strings.TrimSpace(raw[dot+1:])
		}
		lines = append(lines, line)
	}
	return lines
}

// commitEntry is a text field that saves when Return is pressed or it
// loses focus, as automator-ui's do: an edit never just sits there.
type commitEntry struct {
	widget.Entry
	committed string
	commit    func(string)
}

func newCommitEntry(value string, commit func(string)) *commitEntry {
	e := &commitEntry{committed: value, commit: commit}
	e.ExtendBaseWidget(e)
	e.SetText(value)
	e.OnSubmitted = func(string) { e.save() }
	return e
}

func (e *commitEntry) save() {
	if e.Text == e.committed {
		return
	}
	e.committed = e.Text
	e.commit(e.Text)
}

// FocusLost saves what was typed.
func (e *commitEntry) FocusLost() {
	e.Entry.FocusLost()
	e.save()
}

// The theme's names the windows use.
const (
	successColour = theme.ColorNameSuccess
	captionSize   = theme.SizeNameCaptionText
)

// Icons, tinted as automator-ui's are.
func primaryIcon(name string) fyne.Resource { return theme.NewPrimaryThemedResource(icon(name)) }
func successIcon(name string) fyne.Resource { return theme.NewSuccessThemedResource(icon(name)) }
func refreshIcon() fyne.Resource            { return theme.ViewRefreshIcon() }
func addIcon() fyne.Resource                { return theme.ContentAddIcon() }
func checkIcon() fyne.Resource              { return theme.ConfirmIcon() }

func icon(name string) fyne.Resource {
	switch name {
	case "check":
		return theme.ConfirmIcon()
	case "list":
		return theme.ListIcon()
	case "gauge":
		return theme.InfoIcon()
	}
	return theme.QuestionIcon()
}

// sized shows o at a square size of its own (a big icon).
func sized(side float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSquareSize(side), o)
}

// mark is a step's mark: a check in a green disc when done, else a ring
// (grey when the step is optional).
func mark(step cli.SetupStep) fyne.CanvasObject {
	if step.Done {
		disc := canvas.NewCircle(theme.Color(theme.ColorNameSuccess))
		check := widget.NewIcon(theme.NewColoredResource(theme.ConfirmIcon(), theme.ColorNameForegroundOnSuccess))
		return sized(20, container.NewStack(disc, padded(-4, -4, -4, -4, check)))
	}
	ring := canvas.NewCircle(color.Transparent)
	ring.StrokeWidth = 1.5
	ring.StrokeColor = theme.Color(theme.ColorNamePrimary)
	if step.Optional {
		ring.StrokeColor = theme.Color(theme.ColorNamePlaceHolder)
	}
	return sized(20, ring)
}

// tinted is a box tinted with the theme's primary colour: what to do first.
func tinted(o fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(tint(theme.ColorNamePrimary, 18))
	bg.StrokeColor = tint(theme.ColorNamePrimary, 51)
	bg.StrokeWidth = 1
	bg.CornerRadius = 8
	return container.NewStack(bg, padded(4, 4, 6, 6, o))
}

// picker is a drop-down of options wide enough for the longest (Fyne's
// shrinks to its placeholder).
func picker(options []string, selected string, changed func(string)) (*widget.Select, fyne.CanvasObject) {
	sel := widget.NewSelect(options, nil)
	sel.Selected = selected
	sel.OnChanged = changed
	widest := float32(0)
	for _, o := range options {
		widest = max(widest, fyne.MeasureText(o, theme.TextSize(), fyne.TextStyle{}).Width)
	}
	return sel, fixedWidth(widest+theme.IconInlineSize()+4*theme.InnerPadding(), sel)
}
