// Package nav is where the app's window is: a sidebar of sections, each
// with a stack of what was opened from it (a routine from the Routines
// list, a watcher from a hook). Its rules are automator-ui's
// NavigationState's (AutomatorUICore/Navigation.swift).
package nav

import (
	"slices"
	"strconv"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

// Sections are the sidebar's, in order: each is a window kind the tray
// opens (tray.Window), shown as a section's own screen.
var Sections = []tray.WindowKind{
	tray.RoutinesWindow, tray.HooksWindow, tray.AcknowledgementsWindow,
	tray.SetupWindow, tray.SettingsWindow, tray.UpdatesWindow,
}

// Groups are the sidebar's groups: what runs, then the app's own.
var Groups = [][]tray.WindowKind{Sections[:3], Sections[3:]}

var titles = map[tray.WindowKind]string{
	tray.RoutinesWindow: "Routines", tray.HooksWindow: "Hooks", tray.AcknowledgementsWindow: "Acknowledgements",
	tray.SetupWindow: "Setup", tray.SettingsWindow: "Settings", tray.UpdatesWindow: "Updates",
	// Not a section: opened by the tray as it starts, on its own.
	tray.TemplateUpdatesWindow: "Template Updates",
}

// Title is a section's name in the sidebar (or a screen's of its own,
// outside the sidebar: Template Updates).
func Title(section tray.WindowKind) string { return titles[section] }

// Badge is what the sidebar shows beside a section, from run status: how
// many notifications wait to be acknowledged, how many setup steps are left
// ("": nothing).
func Badge(section tray.WindowKind, s *cli.RunStatus) string {
	if s == nil {
		return ""
	}
	switch section {
	case tray.AcknowledgementsWindow:
		if n := len(s.Waiting); n > 0 {
			return strconv.Itoa(n)
		}
	case tray.SetupWindow:
		if n := len(s.SetupLeft); n > 0 {
			return strconv.Itoa(n) + " to do"
		}
	}
	return ""
}

// UpdatesBadge is what the sidebar shows beside Updates: a newer release,
// or one installing ("": neither).
func UpdatesBadge(u *cli.UpdateStatus) string {
	switch {
	case u == nil:
		return ""
	case u.Installing:
		return "Updating"
	case u.UpdateAvailable:
		return "New"
	}
	return ""
}

// Destination is something the window shows: a section's own screen, or
// one routine's, hook's or watcher's (Name).
type Destination struct {
	Kind tray.WindowKind
	Name string
}

// Of is what the tray asked to open (its review is the window's to do).
func Of(w tray.Window) Destination { return Destination{Kind: w.Kind, Name: w.Routine} }

// Home is the section it's shown in when opened from outside the window
// (the menu, a notification): a routine under Routines, a hook or watcher
// under Hooks.
func (d Destination) Home() tray.WindowKind {
	switch d.Kind {
	case tray.RoutineWindow:
		return tray.RoutinesWindow
	case tray.HookWindow, tray.WatcherWindow:
		return tray.HooksWindow
	}
	return d.Kind
}

// Root is whether it's a section's own screen.
func (d Destination) Root() bool { return d.Name == "" && d.Home() == d.Kind }

// Title is its name: the section's, or the routine's, hook's or watcher's.
func (d Destination) Title() string {
	if d.Root() {
		return Title(d.Kind)
	}
	return d.Name
}

// Place is a place the window has been: a section, and what was opened in
// it on top of its own screen (Path, the last shown).
type Place struct {
	Section tray.WindowKind
	Path    []Destination
}

// Shown is what it shows.
func (p Place) Shown() Destination {
	if len(p.Path) > 0 {
		return p.Path[len(p.Path)-1]
	}
	return Destination{Kind: p.Section}
}

func (p Place) equal(o Place) bool { return p.Section == o.Section && slices.Equal(p.Path, o.Path) }

// without is p with d gone, and what was opened from it.
func (p Place) without(d Destination) Place {
	if i := slices.Index(p.Path, d); i >= 0 {
		return Place{Section: p.Section, Path: p.Path[:i:i]}
	}
	return p
}

// HistoryLimit is how many places a State keeps: the oldest go first.
const HistoryLimit = 50

// State is where the window is, and where it's been: a history of places,
// as a browser's. Each move (the sidebar, a link, the menu) adds one,
// dropping those gone back from; Back and Forward move through it. Its
// moves never change a copy's places: a State is a value.
type State struct {
	history []Place
	at      int
}

// New is the window on a section's own screen.
func New(section tray.WindowKind) State { return State{history: []Place{{Section: section}}} }

// Showing is a window opened on d (from the menu, a notification, opening
// the app): it, and before it its section's own screen.
func Showing(d Destination) State {
	s := New(d.Home())
	s.Show(d)
	return s
}

// Place is where it is.
func (s State) Place() Place {
	if len(s.history) == 0 {
		return Place{Section: Sections[0]}
	}
	return s.history[s.at]
}

// History is the places it's been, oldest first.
func (s State) History() []Place { return slices.Clone(s.history) }

// Section is the section it's in.
func (s State) Section() tray.WindowKind { return s.Place().Section }

// Path is what was opened in the section, on top of its own screen.
func (s State) Path() []Destination { return slices.Clone(s.Place().Path) }

// Shown is what it shows.
func (s State) Shown() Destination { return s.Place().Shown() }

// CanGoBack is whether there's a place before this one.
func (s State) CanGoBack() bool { return s.at > 0 }

// CanGoForward is whether there's a place it went back from.
func (s State) CanGoForward() bool { return s.at < len(s.history)-1 }

// BackTitle is the name of what Back goes to ("": nothing to go back to).
func (s State) BackTitle() string {
	if !s.CanGoBack() {
		return ""
	}
	return s.history[s.at-1].Shown().Title()
}

// ForwardTitle is the name of what Forward goes to ("": nothing to go
// forward to).
func (s State) ForwardTitle() string {
	if !s.CanGoForward() {
		return ""
	}
	return s.history[s.at+1].Shown().Title()
}

// Open is everything open: the section's screen and its path.
func (s State) Open() []Destination {
	p := s.Place()
	return append([]Destination{{Kind: p.Section}}, p.Path...)
}

// Show is d opened from outside the window (the menu, a notification,
// opening the app): its section, with only it opened there; Back goes to
// the section's own screen, then to where the window was.
func (s *State) Show(d Destination) {
	p := Place{Section: d.Home()}
	if !d.Root() {
		p.Path = []Destination{d}
	}
	if p.equal(s.Place()) {
		return
	}
	if !d.Root() {
		s.goTo(Place{Section: d.Home()})
	}
	s.goTo(p)
}

// Push is d opened from inside the window (Setup's routine to fix, a
// hook's watcher): on top of what shows, staying in the section. A
// section's own screen switches to it; one already open goes back to it.
func (s *State) Push(d Destination) {
	if d.Root() {
		s.Select(d.Kind)
		return
	}
	p := s.Place()
	if i := slices.Index(p.Path, d); i >= 0 {
		s.goTo(Place{Section: p.Section, Path: p.Path[: i+1 : i+1]})
		return
	}
	s.goTo(Place{Section: p.Section, Path: append(slices.Clip(p.Path), d)})
}

// Select is a section chosen in the sidebar: its own screen.
func (s *State) Select(section tray.WindowKind) { s.goTo(Place{Section: section}) }

// Back goes to the place before.
func (s *State) Back() {
	if s.CanGoBack() {
		s.at--
	}
}

// Forward goes to the place it went back from.
func (s *State) Forward() {
	if s.CanGoForward() {
		s.at++
	}
}

// Removed is d gone (deleted): it and what was opened from it close,
// wherever they are in the history; places left the same as the one before
// them are one.
func (s *State) Removed(d Destination) {
	if len(s.history) == 0 {
		return
	}
	var kept []Place
	at := 0
	for i, p := range s.history {
		p = p.without(d)
		if len(kept) == 0 || !kept[len(kept)-1].equal(p) {
			kept = append(kept, p)
		}
		if i == s.at {
			at = len(kept) - 1
		}
	}
	s.history, s.at = kept, at
}

// goTo moves to p: after the place it's at, dropping those gone back from
// (nothing, if it's already there).
func (s *State) goTo(p Place) {
	if p.equal(s.Place()) {
		return
	}
	before := []Place{s.Place()}
	if len(s.history) > 0 {
		before = s.history[:s.at+1]
	}
	if len(before) >= HistoryLimit {
		before = before[len(before)-HistoryLimit+1:]
	}
	s.history = append(slices.Clone(before), p)
	s.at = len(s.history) - 1
}
