package nav

import (
	"slices"
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
	if s.Section != tray.RoutinesWindow || !slices.Equal(s.Path, []Destination{routine("standup")}) || !s.CanGoBack() {
		t.Fatalf("%+v", s)
	}
	s.Show(Of(tray.Window{Kind: tray.WatcherWindow, Routine: "cal"}))
	if s.Section != tray.HooksWindow || s.Shown() != watcher("cal") {
		t.Fatalf("%+v", s)
	}
	s.Show(hook("open-meet"))
	if s.Section != tray.HooksWindow || s.Shown() != hook("open-meet") {
		t.Fatalf("%+v", s)
	}
	for _, section := range Sections {
		s.Show(Destination{Kind: section})
		if s.Section != section || s.CanGoBack() || s.Shown() != (Destination{Kind: section}) {
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
	if s.Section != tray.RoutinesWindow || s.Shown() != (Destination{Kind: tray.RoutinesWindow}) {
		t.Fatalf("%+v", s)
	}
}

// Opened from inside the window: on top of what shows, in the same
// section; one already open is gone back to, so a hook and its watcher
// opening each other don't pile up.
func TestOpeningFromInsideStaysInTheSection(t *testing.T) {
	s := New(tray.SetupWindow)
	s.Push(routine("standup"))
	if s.Section != tray.SetupWindow || s.Shown() != routine("standup") {
		t.Fatalf("%+v", s)
	}
	s.Show(hook("open-meet"))
	s.Push(watcher("cal"))
	s.Push(hook("other"))
	if !slices.Equal(s.Path, []Destination{hook("open-meet"), watcher("cal"), hook("other")}) {
		t.Fatalf("%+v", s.Path)
	}
	s.Push(watcher("cal"))
	if !slices.Equal(s.Path, []Destination{hook("open-meet"), watcher("cal")}) {
		t.Fatalf("%+v", s.Path)
	}
	// A section's own screen (Settings, say) switches to it.
	s.Push(Destination{Kind: tray.SettingsWindow})
	if s.Section != tray.SettingsWindow || s.CanGoBack() {
		t.Fatalf("%+v", s)
	}
}

func TestChoosingASectionShowsItsOwnScreen(t *testing.T) {
	s := New(tray.RoutinesWindow)
	s.Show(routine("standup"))
	s.Select(tray.AcknowledgementsWindow)
	if s.Section != tray.AcknowledgementsWindow || s.CanGoBack() {
		t.Fatalf("%+v", s)
	}
}

// Deleted: it closes, and what was opened from it.
func TestWhatsDeletedCloses(t *testing.T) {
	s := New(tray.HooksWindow)
	s.Show(hook("open-meet"))
	s.Push(watcher("cal"))
	s.Removed(hook("open-meet"))
	if s.Section != tray.HooksWindow || s.CanGoBack() {
		t.Fatalf("%+v", s)
	}
	s.Removed(routine("gone"))
	if s.Shown() != (Destination{Kind: tray.HooksWindow}) {
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
