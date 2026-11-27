package nav

import (
	"slices"
	"strconv"
	"testing"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

func routine(n string) Destination { return Destination{Kind: tray.RoutineWindow, Name: n} }
func hook(n string) Destination    { return Destination{Kind: tray.HookWindow, Name: n} }
func watcher(n string) Destination { return Destination{Kind: tray.WatcherWindow, Name: n} }

// Opened from outside the window (the menu, a notification, opening the
// app): the section it belongs to, with only it opened there.
func TestOpeningFromOutsideShowsItInItsSection(t *testing.T) {
	s := New(tray.SettingsWindow)
	s.Show(routine("standup"))
	if s.Section() != tray.RoutinesWindow || !slices.Equal(s.Path(), []Destination{routine("standup")}) || !s.CanGoBack() {
		t.Fatalf("%+v", s)
	}
	s.Show(Of(tray.Window{Kind: tray.WatcherWindow, Routine: "cal"}))
	if s.Section() != tray.HooksWindow || s.Shown() != watcher("cal") {
		t.Fatalf("%+v", s)
	}
	s.Show(hook("open-meet"))
	if s.Section() != tray.HooksWindow || s.Shown() != hook("open-meet") {
		t.Fatalf("%+v", s)
	}
	for _, section := range Sections {
		s.Show(Destination{Kind: section})
		if s.Section() != section || len(s.Path()) > 0 || s.Shown() != (Destination{Kind: section}) {
			t.Fatalf("%s: %+v", section, s)
		}
	}
}

// Back from what was opened from a list is the list.
func TestBackGoesToWhatItWasOpenedFrom(t *testing.T) {
	s := New(tray.RoutinesWindow)
	s.Show(routine("standup"))
	if s.BackTitle() != "Routines" {
		t.Fatal(s.BackTitle())
	}
	s.Push(routine("digest"))
	if s.BackTitle() != "standup" || len(s.Open()) != 3 {
		t.Fatal(s.BackTitle(), s.Open())
	}
	s.Back()
	s.Back()
	s.Back() // nothing to go back to
	if s.BackTitle() != "" {
		t.Fatal(s.BackTitle())
	}
	if s.Section() != tray.RoutinesWindow || s.Shown() != (Destination{Kind: tray.RoutinesWindow}) {
		t.Fatalf("%+v", s)
	}
}

// Opened from inside the window: on top of what shows, in the same
// section; one already open is gone back to, so a hook and its watcher
// opening each other don't pile up.
func TestOpeningFromInsideStaysInTheSection(t *testing.T) {
	s := New(tray.SetupWindow)
	s.Push(routine("standup"))
	if s.Section() != tray.SetupWindow || s.Shown() != routine("standup") {
		t.Fatalf("%+v", s)
	}
	s.Show(hook("open-meet"))
	s.Push(watcher("cal"))
	s.Push(hook("other"))
	if !slices.Equal(s.Path(), []Destination{hook("open-meet"), watcher("cal"), hook("other")}) {
		t.Fatalf("%+v", s.Path())
	}
	s.Push(watcher("cal"))
	if !slices.Equal(s.Path(), []Destination{hook("open-meet"), watcher("cal")}) {
		t.Fatalf("%+v", s.Path())
	}
	// A section's own screen (Settings, say) switches to it.
	s.Push(Destination{Kind: tray.SettingsWindow})
	if s.Section() != tray.SettingsWindow || len(s.Path()) > 0 {
		t.Fatalf("%+v", s)
	}
}

func TestChoosingASectionShowsItsOwnScreen(t *testing.T) {
	s := New(tray.RoutinesWindow)
	s.Show(routine("standup"))
	s.Select(tray.AcknowledgementsWindow)
	if s.Section() != tray.AcknowledgementsWindow || len(s.Path()) > 0 {
		t.Fatalf("%+v", s)
	}
}

// Deleted: it closes, and what was opened from it.
func TestWhatsDeletedCloses(t *testing.T) {
	s := New(tray.HooksWindow)
	s.Show(hook("open-meet"))
	s.Push(watcher("cal"))
	s.Removed(hook("open-meet"))
	if s.Section() != tray.HooksWindow || len(s.Path()) > 0 {
		t.Fatalf("%+v", s)
	}
	s.Removed(routine("gone"))
	if s.Shown() != (Destination{Kind: tray.HooksWindow}) {
		t.Fatalf("%+v", s)
	}
}

// Deleted, it's gone from where the window has been too: Back doesn't
// return to it, and a place it leaves the same as the one before is one.
func TestWhatsDeletedLeavesTheHistory(t *testing.T) {
	s := New(tray.RoutinesWindow)
	s.Push(routine("standup"))
	s.Select(tray.SettingsWindow)
	s.Show(routine("standup"))
	s.Push(routine("digest"))
	s.Back()
	s.Removed(routine("standup"))
	var shown []Destination
	for _, p := range s.History() {
		shown = append(shown, p.Shown())
	}
	routines, settings := Destination{Kind: tray.RoutinesWindow}, Destination{Kind: tray.SettingsWindow}
	if !slices.Equal(shown, []Destination{routines, settings, routines}) {
		t.Fatalf("%+v", shown)
	}
	if s.Shown() != routines || s.BackTitle() != "Settings" || s.CanGoForward() {
		t.Fatalf("%+v", s)
	}
}

// A window opened on something (the menu, a notification) has Back to its
// section's own screen, and nothing before it; opened on a section's own
// screen, nothing to go back to.
func TestAWindowOpenedOnSomethingGoesBackToItsSection(t *testing.T) {
	s := Showing(watcher("cal"))
	if s.Section() != tray.HooksWindow || s.Shown() != watcher("cal") || s.BackTitle() != "Hooks" {
		t.Fatalf("%+v", s)
	}
	s.Back()
	if s.Shown() != (Destination{Kind: tray.HooksWindow}) || s.CanGoBack() || s.ForwardTitle() != "cal" {
		t.Fatalf("%+v", s)
	}
	if Showing(Destination{Kind: tray.SettingsWindow}).CanGoBack() {
		t.Fatal("Back on a window opened on Settings")
	}
}

// Back goes through everywhere the window has been, across sections, and
// Forward returns; a move after going back drops what was ahead.
func TestBackAndForwardGoThroughWhereItsBeen(t *testing.T) {
	s := New(tray.RoutinesWindow)
	s.Push(routine("standup"))
	s.Select(tray.HooksWindow)
	s.Push(hook("open-meet"))
	s.Push(watcher("cal"))
	if s.BackTitle() != "open-meet" || s.ForwardTitle() != "" {
		t.Fatalf("%+v", s)
	}
	var titles []string
	for s.CanGoBack() {
		titles = append(titles, s.BackTitle())
		s.Back()
	}
	if !slices.Equal(titles, []string{"open-meet", "Hooks", "standup", "Routines"}) || s.Shown() != (Destination{Kind: tray.RoutinesWindow}) {
		t.Fatalf("%q %+v", titles, s)
	}
	s.Forward()
	s.Forward()
	if s.Section() != tray.HooksWindow || s.Shown() != (Destination{Kind: tray.HooksWindow}) || s.ForwardTitle() != "open-meet" {
		t.Fatalf("%+v", s)
	}
	s.Select(tray.SettingsWindow)
	if s.CanGoForward() || s.BackTitle() != "Hooks" {
		t.Fatalf("%+v", s)
	}
	s.Forward() // nothing ahead
	if s.Shown() != (Destination{Kind: tray.SettingsWindow}) {
		t.Fatalf("%+v", s)
	}
}

// Showing what already shows, or choosing the section shown, adds nothing
// to go back through.
func TestGoingWhereItIsAddsNothing(t *testing.T) {
	s := New(tray.RoutinesWindow)
	s.Show(routine("standup"))
	s.Show(routine("standup"))
	s.Push(routine("standup"))
	if n := len(s.History()); n != 2 {
		t.Fatal(n)
	}
	s.Select(tray.RoutinesWindow)
	s.Select(tray.RoutinesWindow)
	if n := len(s.History()); n != 3 {
		t.Fatal(n)
	}
}

// It keeps the last places it's been, not all of them.
func TestTheHistoryIsLimited(t *testing.T) {
	s := New(tray.RoutinesWindow)
	for i := range 100 {
		s.Push(routine(strconv.Itoa(i)))
	}
	if len(s.History()) != HistoryLimit || s.Shown() != routine("99") {
		t.Fatal(len(s.History()), s.Shown())
	}
}

// A State is a value: moving a copy leaves the original where it was.
func TestACopyMovesOnItsOwn(t *testing.T) {
	s := New(tray.HooksWindow)
	s.Push(hook("open-meet"))
	s.Back()
	c := s
	c.Push(watcher("cal"))
	c.Removed(hook("open-meet"))
	if s.Shown() != (Destination{Kind: tray.HooksWindow}) || s.ForwardTitle() != "open-meet" {
		t.Fatalf("%+v", s)
	}
}

// The sections and their names are automator-ui's.
func TestTheSectionsAreNamedAsOnTheMac(t *testing.T) {
	var names []string
	for _, s := range Sections {
		names = append(names, Title(s))
	}
	want := []string{"Routines", "Hooks", "Acknowledgements", "Setup", "Settings", "Updates"}
	if !slices.Equal(names, want) || !slices.Equal(slices.Concat(Groups...), Sections) {
		t.Fatal(names)
	}
	if routine("standup").Title() != "standup" || (Destination{Kind: tray.UpdatesWindow}).Title() != "Updates" {
		t.Fatal("titles")
	}
}

// The sidebar says how many notifications wait and how many setup steps are left.
func TestTheSidebarCountsWhatWaits(t *testing.T) {
	s := &cli.RunStatus{SetupLeft: []string{"backend", "turn-on"}, Waiting: []cli.Waiting{{ID: "a"}}}
	if Badge(tray.AcknowledgementsWindow, s) != "1" || Badge(tray.SetupWindow, s) != "2 to do" ||
		Badge(tray.RoutinesWindow, s) != "" || Badge(tray.SetupWindow, nil) != "" {
		t.Fatal(Badge(tray.AcknowledgementsWindow, s), Badge(tray.SetupWindow, s))
	}
}

// Updates shows a newer release, or one installing.
func TestTheSidebarSaysWhenAnUpdateIsThere(t *testing.T) {
	if UpdatesBadge(nil) != "" || UpdatesBadge(&cli.UpdateStatus{}) != "" ||
		UpdatesBadge(&cli.UpdateStatus{UpdateAvailable: true}) != "New" ||
		UpdatesBadge(&cli.UpdateStatus{UpdateAvailable: true, Installing: true}) != "Updating" {
		t.Fatal("badges")
	}
}
