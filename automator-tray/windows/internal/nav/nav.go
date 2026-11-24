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
}

// Title is a section's name in the sidebar.
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

// State is where the window is: a section, and what was opened in it on
// top of its own screen (Path, the last shown; Back goes to the one before).
type State struct {
	Section tray.WindowKind
	Path    []Destination
}

// New is the window on a section's own screen.
func New(section tray.WindowKind) State { return State{Section: section} }

// Shown is what it shows.
func (s State) Shown() Destination {
	if len(s.Path) > 0 {
		return s.Path[len(s.Path)-1]
	}
	return Destination{Kind: s.Section}
}

// CanGoBack is whether anything was opened on top of the section's screen.
func (s State) CanGoBack() bool { return len(s.Path) > 0 }

// Show is d opened from outside the window (the menu, a notification,
// opening the app): its section, with only it opened there.
func (s *State) Show(d Destination) {
	s.Section = d.Home()
	s.Path = nil
	if !d.Root() {
		s.Path = []Destination{d}
	}
}

// Push is d opened from inside the window (Setup's routine to fix, a
// hook's watcher): on top of what shows, staying in the section. A
// section's own screen switches to it; one already open goes back to it.
func (s *State) Push(d Destination) {
	if d.Root() {
		s.Select(d.Kind)
		return
	}
	if i := slices.Index(s.Path, d); i >= 0 {
		s.Path = s.Path[:i+1]
		return
	}
	s.Path = append(slices.Clip(s.Path), d)
}

// Select is a section chosen in the sidebar: its own screen.
func (s *State) Select(section tray.WindowKind) {
	s.Section = section
	s.Path = nil
}

// Back goes to what the shown one was opened from.
func (s *State) Back() {
	if len(s.Path) > 0 {
		s.Path = s.Path[:len(s.Path)-1]
	}
}

// Removed is d gone (deleted): it and what was opened from it close.
func (s *State) Removed(d Destination) {
	if i := slices.Index(s.Path, d); i >= 0 {
		s.Path = s.Path[:i]
	}
}
