// Package shell is the app's window on Linux, automator-ui's MainWindow's
// counterpart: a sidebar of sections (☰ shows and hides it), a bar with
// Back to what the screen shown was opened from, and the screen itself.
// Every screen is the ui package's content, as it was in a window of its
// own; where the window is follows nav's rules (automator-ui's).
package shell

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/nav"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/ui"
)

// View is a screen: what it shows, and reading it again.
type View interface {
	Content() fyne.CanvasObject
	Load()
}

// statusFollower is a screen that shows run status as it changes.
type statusFollower interface{ Update(*cli.RunStatus) }

// reviewer is a screen that can open its Turn On review.
type reviewer interface{ Review() }

// runShower is a screen that can show one of its runs.
type runShower interface{ ShowRun(id string) }

// SidebarWidth is the sidebar's width.
const SidebarWidth = 210

// Shell is the window's content and where it is. A screen's data is read
// when it's shown and let go when it's left.
type Shell struct {
	cli     cli.CLI
	win     fyne.Window
	async   ui.Async
	appName string

	state   nav.State
	views   map[nav.Destination]View
	status  *cli.RunStatus
	updates *cli.UpdateStatus

	sidebarShown bool
	side         *fyne.Container
	sidebar      *fyne.Container
	bar          *fyne.Container
	body         *fyne.Container
	root         *fyne.Container
}

// New is the window's content in win: it shows nothing until Show.
func New(c cli.CLI, win fyne.Window, async ui.Async, appName string) *Shell {
	s := &Shell{
		cli: c, win: win, async: async, appName: appName,
		state: nav.New(tray.RoutinesWindow), views: map[nav.Destination]View{}, sidebarShown: true,
		sidebar: container.NewVBox(), bar: container.NewHBox(), body: container.NewStack(),
	}
	// The sidebar's background, a shade off the window's, sets its width.
	width := canvas.NewRectangle(sidebarShade())
	width.SetMinSize(fyne.NewSize(SidebarWidth, 0))
	s.side = container.NewBorder(nil, nil, nil, widget.NewSeparator(),
		container.NewStack(width, container.NewThemeOverride(
			container.NewVScroll(container.New(layout.NewCustomPaddedLayout(10, 10, 8, 8), s.sidebar)),
			sidebarTheme{fyne.CurrentApp().Settings().Theme()})))
	main := container.NewBorder(container.NewVBox(container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6), s.bar), widget.NewSeparator()), nil, nil, nil, s.body)
	s.root = container.NewBorder(nil, nil, s.side, nil, main)
	s.drawBar()
	s.drawSidebar()
	return s
}

// Content is what the window shows.
func (s *Shell) Content() fyne.CanvasObject { return s.root }

// State is where the window is.
func (s *Shell) State() nav.State { return s.state }

// Shown is the screen shown (nil before Show).
func (s *Shell) Shown() View { return s.views[s.state.Shown()] }

// SidebarShown is whether the sidebar shows.
func (s *Shell) SidebarShown() bool { return s.sidebarShown }

// Show opens what the tray asked for, from outside the window (the menu, a
// notification, opening the app): in its section, on its run or Turn On
// review when it asks for one.
func (s *Shell) Show(w tray.Window) {
	d := nav.Of(w)
	s.navigate(d, w, func(st *nav.State) { st.Show(d) })
}

// Push opens a screen from inside the window: on top of what shows.
func (s *Shell) Push(w tray.Window) {
	d := nav.Of(w)
	s.navigate(d, w, func(st *nav.State) { st.Push(d) })
}

// Select is a section chosen in the sidebar.
func (s *Shell) Select(section tray.WindowKind) {
	if section == s.state.Section && !s.state.CanGoBack() {
		return
	}
	d := nav.Destination{Kind: section}
	s.navigate(d, tray.Window{Kind: section}, func(st *nav.State) { st.Select(section) })
}

// Back goes to what the shown screen was opened from.
func (s *Shell) Back() {
	next := s.state
	next.Back()
	s.apply(next, nav.Destination{})
}

// ToggleSidebar shows the sidebar, or hides it (☰).
func (s *Shell) ToggleSidebar() {
	s.sidebarShown = !s.sidebarShown
	s.drawBar()
	if s.sidebarShown {
		s.side.Show()
	} else {
		s.side.Hide()
	}
	s.root.Refresh()
}

// Update shows a new run status: in the screens that follow it, and the
// sidebar's counts.
func (s *Shell) Update(st *cli.RunStatus) {
	s.status = st
	for _, v := range s.views {
		if f, ok := v.(statusFollower); ok {
			f.Update(st)
		}
	}
	s.drawSidebar()
}

// UpdateStatus shows where updates stand in the sidebar.
func (s *Shell) UpdateStatus(u *cli.UpdateStatus) {
	s.updates = u
	s.drawSidebar()
}

// LoadUsage reads the plan's usage again while Settings shows.
func (s *Shell) LoadUsage() {
	if v, ok := s.views[nav.Destination{Kind: tray.SettingsWindow}].(*ui.Settings); ok {
		v.LoadUsage()
	}
}

// navigate moves the window, then reads what it opens (again, if it was
// open), with its run or review.
func (s *Shell) navigate(d nav.Destination, w tray.Window, change func(*nav.State)) {
	next := s.state
	next.Path = append([]nav.Destination(nil), next.Path...)
	change(&next)
	s.apply(next, d)
	s.load(d, w)
}

// apply moves to next: the screens open there made (and read, but
// except, which the caller reads), the others let go.
func (s *Shell) apply(next nav.State, except nav.Destination) {
	open := map[nav.Destination]bool{}
	for _, d := range next.Open() {
		open[d] = true
		if _, ok := s.views[d]; ok {
			continue
		}
		v := s.makeView(d)
		s.views[d] = v
		if f, ok := v.(statusFollower); ok && s.status != nil {
			f.Update(s.status)
		}
		if d != except {
			v.Load()
		}
	}
	for d := range s.views {
		if !open[d] {
			delete(s.views, d)
		}
	}
	s.state = next
	s.body.Objects = []fyne.CanvasObject{s.views[next.Shown()].Content()}
	s.body.Refresh()
	title := s.appName
	if shown := next.Shown(); !shown.Root() {
		title = shown.Title() + " — " + s.appName
	}
	s.win.SetTitle(title)
	s.drawBar()
	s.drawSidebar()
}

// load reads what d shows, as opening its window did, on w's run or review.
func (s *Shell) load(d nav.Destination, w tray.Window) {
	v := s.views[d]
	if v == nil {
		return
	}
	v.Load()
	if r, ok := v.(reviewer); ok && w.Review {
		r.Review()
	}
	if r, ok := v.(runShower); ok && w.Run != "" {
		r.ShowRun(w.Run)
	}
}

// deleted: the screen closes (Back to what it was opened from), and the
// lists are read again.
func (s *Shell) deleted(d nav.Destination) {
	next := s.state
	next.Path = append([]nav.Destination(nil), next.Path...)
	next.Removed(d)
	s.apply(next, nav.Destination{})
	for _, k := range []tray.WindowKind{tray.RoutinesWindow, tray.HooksWindow} {
		if v := s.views[nav.Destination{Kind: k}]; v != nil {
			v.Load()
		}
	}
}

// makeView is the screen d shows: the content its window had, its links
// opening their screens here.
func (s *Shell) makeView(d nav.Destination) View {
	c, win, async := s.cli, s.win, s.async
	push := func(kind tray.WindowKind) func(string) {
		return func(name string) { s.Push(tray.Window{Kind: kind, Routine: name}) }
	}
	review := func(kind tray.WindowKind) func(string) {
		return func(name string) { s.Push(tray.Window{Kind: kind, Routine: name, Review: true}) }
	}
	deleted := func() { s.deleted(d) }
	switch d.Kind {
	case tray.SetupWindow:
		return ui.NewSetup(c, win, async, push(tray.RoutineWindow))
	case tray.SettingsWindow:
		return ui.NewSettings(c, win, async, s.appName)
	case tray.RoutinesWindow:
		return ui.NewRoutines(c, async, push(tray.RoutineWindow), review(tray.RoutineWindow))
	case tray.RoutineWindow:
		return ui.NewRoutine(c, win, async, d.Name, deleted)
	case tray.AcknowledgementsWindow:
		return ui.NewAcknowledgements(c, async)
	case tray.HooksWindow:
		return ui.NewHooks(c, async, push(tray.HookWindow), review(tray.HookWindow), push(tray.WatcherWindow))
	case tray.HookWindow:
		return ui.NewHook(c, win, async, d.Name, deleted, push(tray.WatcherWindow))
	case tray.WatcherWindow:
		return ui.NewWatcher(c, win, async, d.Name, deleted, push(tray.HookWindow))
	}
	return ui.NewUpdates(c, async)
}

// drawBar is ☰, and Back naming what it goes to while there's one.
func (s *Shell) drawBar() {
	menu := widget.NewButtonWithIcon("", theme.MenuIcon(), s.ToggleSidebar)
	menu.Importance = widget.LowImportance
	objects := []fyne.CanvasObject{menu}
	if back := s.state.BackTitle(); back != "" {
		b := widget.NewButtonWithIcon(back, ui.ChevronLeftIcon, s.Back)
		b.Importance = widget.LowImportance
		objects = append(objects, b)
	}
	s.bar.Objects = objects
	s.bar.Refresh()
}

// drawSidebar is each section with what waits in it, the one shown marked.
func (s *Shell) drawSidebar() {
	var objects []fyne.CanvasObject
	for i, group := range nav.Groups {
		if i > 0 {
			objects = append(objects, container.New(layout.NewCustomPaddedLayout(6, 6, 8, 8), widget.NewSeparator()))
		}
		for _, section := range group {
			b := widget.NewButtonWithIcon(nav.Title(section), icon(section), func() { s.Select(section) })
			b.Alignment = widget.ButtonAlignLeading
			b.Importance = widget.LowImportance
			if section == s.state.Section {
				b.Importance = widget.HighImportance
			}
			badge := nav.Badge(section, s.status)
			if section == tray.UpdatesWindow {
				badge = nav.UpdatesBadge(s.updates)
			}
			row := fyne.CanvasObject(b)
			if badge != "" {
				t := canvas.NewText(badge+"  ", theme.Color(theme.ColorNamePlaceHolder))
				t.TextSize = theme.CaptionTextSize()
				t.TextStyle = fyne.TextStyle{Bold: true}
				row = container.NewStack(b, container.NewHBox(layout.NewSpacer(), container.NewCenter(t)))
			}
			objects = append(objects, row)
		}
	}
	s.sidebar.Objects = objects
	s.sidebar.Refresh()
}

// icon is a section's, as automator-ui's sidebar has them: a clock, a
// bolt, a bubble with a check, a checklist, a gear, an arrow down.
func icon(section tray.WindowKind) fyne.Resource {
	switch section {
	case tray.RoutinesWindow:
		return ui.ClockIcon
	case tray.HooksWindow:
		return ui.BoltIcon
	case tray.AcknowledgementsWindow:
		return ui.AcknowledgeIcon
	case tray.SetupWindow:
		return ui.ChecklistIcon
	case tray.SettingsWindow:
		return theme.SettingsIcon()
	}
	return ui.DownloadCircleIcon
}

// sidebarShade is the sidebar's background: the window's, a shade darker.
func sidebarShade() color.Color {
	r, g, b, _ := theme.Color(theme.ColorNameForeground).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 8}
}

// sidebarTheme is the window's theme in the sidebar: the section shown is
// marked with a tint of the accent colour, its name and icon in the accent
// (a sidebar's selection), not a solid button; rows are compact.
type sidebarTheme struct{ fyne.Theme }

func (t sidebarTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNamePrimary:
		r, g, b, _ := t.Theme.Color(theme.ColorNamePrimary, v).RGBA()
		return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0x30}
	case theme.ColorNameForegroundOnPrimary:
		return t.Theme.Color(theme.ColorNamePrimary, v)
	}
	return t.Theme.Color(n, v)
}

func (t sidebarTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == theme.SizeNameInnerPadding {
		return 7
	}
	return t.Theme.Size(n)
}
