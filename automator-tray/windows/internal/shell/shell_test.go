package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/nav"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/ui"
)

// now runs work and what it returns at once: the tests' Async.
func now(work func() func()) { work()() }

// fakeCLI is a stand-in for the app's CLI that logs each command and
// answers {}; ran is what it ran.
func fakeCLI(t *testing.T) (cli.CLI, func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\necho '{}'\n", log)
	path := filepath.Join(dir, "acme-auto")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cli.CLI{Path: path, Command: "acme-auto"}, func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func newShell(t *testing.T) (*Shell, fyne.Window, func() []string) {
	t.Helper()
	c, ran := fakeCLI(t)
	w := test.NewTempApp(t).NewWindow("Acme")
	s := New(c, w, now, "Acme")
	w.SetContent(s.Content())
	w.Resize(fyne.NewSize(960, 760))
	return s, w, ran
}

// buttons are the buttons under o, by text.
func buttons(o fyne.CanvasObject) map[string]*widget.Button {
	found := map[string]*widget.Button{}
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Button:
			found[x.Text] = x
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		case *container.Scroll:
			walk(x.Content)
		}
	}
	walk(o)
	return found
}

// texts are the canvas texts under o.
func texts(o fyne.CanvasObject) []string {
	var found []string
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *canvas.Text:
			found = append(found, strings.TrimSpace(x.Text))
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		case *container.Scroll:
			walk(x.Content)
		}
	}
	walk(o)
	return found
}

// Every screen the tray opens shows in the one window, in its section,
// with the screen its window had.
func TestEachScreenShowsInItsSection(t *testing.T) {
	s, w, _ := newShell(t)
	for _, c := range []struct {
		w       tray.Window
		section tray.WindowKind
		view    any
		title   string
	}{
		{tray.Window{Kind: tray.SetupWindow}, tray.SetupWindow, &ui.Setup{}, "Acme"},
		{tray.Window{Kind: tray.SettingsWindow}, tray.SettingsWindow, &ui.Settings{}, "Acme"},
		{tray.Window{Kind: tray.RoutinesWindow}, tray.RoutinesWindow, &ui.Routines{}, "Acme"},
		{tray.Window{Kind: tray.RoutineWindow, Routine: "standup"}, tray.RoutinesWindow, &ui.Routine{}, "standup — Acme"},
		{tray.Window{Kind: tray.HooksWindow}, tray.HooksWindow, &ui.Hooks{}, "Acme"},
		{tray.Window{Kind: tray.HookWindow, Routine: "open-meet"}, tray.HooksWindow, &ui.Hook{}, "open-meet — Acme"},
		{tray.Window{Kind: tray.WatcherWindow, Routine: "cal"}, tray.HooksWindow, &ui.Watcher{}, "cal — Acme"},
		{tray.Window{Kind: tray.AcknowledgementsWindow}, tray.AcknowledgementsWindow, &ui.Acknowledgements{}, "Acme"},
		{tray.Window{Kind: tray.UpdatesWindow}, tray.UpdatesWindow, &ui.Updates{}, "Acme"},
		{tray.Window{Kind: tray.TemplateUpdatesWindow}, tray.TemplateUpdatesWindow, &ui.TemplateUpdates{}, "Acme"},
	} {
		s.Show(c.w)
		if fmt.Sprintf("%T", s.Shown()) != fmt.Sprintf("%T", c.view) || s.State().Section() != c.section || w.Title() != c.title {
			t.Errorf("%+v: %T in %s titled %q", c.w, s.Shown(), s.State().Section(), w.Title())
		}
		// Only what's open is kept: the screen and what it was opened from.
		if len(s.views) != len(s.State().Open()) {
			t.Errorf("%+v: %d screens kept", c.w, len(s.views))
		}
	}
}

// arrows are the bar's Back and Forward.
func arrows(t *testing.T, s *Shell) (back, forward *widget.Button) {
	t.Helper()
	if len(s.bar.Objects) != 2 {
		t.Fatalf("the bar has %d objects", len(s.bar.Objects))
	}
	return s.bar.Objects[0].(*widget.Button), s.bar.Objects[1].(*widget.Button)
}

// A routine opened from the menu shows under Routines with Back to the
// list; Back shows the list again, read already, and Forward the routine.
func TestBackGoesToTheList(t *testing.T) {
	s, w, ran := newShell(t)
	s.Show(tray.Window{Kind: tray.RoutineWindow, Routine: "standup"})
	back, forward := arrows(t, s)
	if back.Disabled() || !forward.Disabled() || !slices.Contains(ran(), "routines list --kind all --json") {
		t.Fatalf("no Back, or the list unread: %q", ran())
	}
	list := s.views[nav.Destination{Kind: tray.RoutinesWindow}]
	test.Tap(back)
	if s.Shown() != list || s.State().CanGoBack() || w.Title() != "Acme" {
		t.Fatalf("showing %T", s.Shown())
	}
	back, forward = arrows(t, s)
	if !back.Disabled() || forward.Disabled() {
		t.Fatal("Back and Forward not where the window is")
	}
	test.Tap(forward)
	if _, ok := s.Shown().(*ui.Routine); !ok || w.Title() != "standup — Acme" {
		t.Fatalf("showing %T", s.Shown())
	}
}

// Back goes through every screen shown, across sections, and Forward
// returns; Alt+Left and Alt+Right do the same. With nowhere to go, they
// do nothing.
func TestBackAndForwardGoThroughWhereTheWindowHasBeen(t *testing.T) {
	s, w, _ := newShell(t)
	s.Show(tray.Window{Kind: tray.SettingsWindow})
	s.Back() // nowhere to go
	if _, ok := s.Shown().(*ui.Settings); !ok {
		t.Fatalf("showing %T", s.Shown())
	}
	s.Push(tray.Window{Kind: tray.HooksWindow})
	s.Push(tray.Window{Kind: tray.HookWindow, Routine: "open-meet"})
	s.Push(tray.Window{Kind: tray.WatcherWindow, Routine: "cal"})

	back, _ := arrows(t, s)
	test.Tap(back)
	s.keys()[fyne.KeyLeft]()
	if _, ok := s.Shown().(*ui.Hooks); !ok {
		t.Fatalf("showing %T", s.Shown())
	}
	s.keys()[fyne.KeyLeft]()
	if _, ok := s.Shown().(*ui.Settings); !ok || w.Title() != "Acme" {
		t.Fatalf("showing %T", s.Shown())
	}
	s.keys()[fyne.KeyRight]()
	s.keys()[fyne.KeyRight]()
	if _, ok := s.Shown().(*ui.Hook); !ok || s.State().Section() != tray.HooksWindow {
		t.Fatalf("showing %T in %s", s.Shown(), s.State().Section())
	}
	// Only the place shown is kept, not every one in the history.
	if len(s.views) != len(s.State().Open()) {
		t.Errorf("%d screens kept", len(s.views))
	}
}

// Links inside the window stay in its section: Setup's routine to fix
// opens on top of Setup; Back returns there.
func TestLinksStayInTheSection(t *testing.T) {
	s, _, _ := newShell(t)
	s.Show(tray.Window{Kind: tray.SetupWindow})
	s.Push(tray.Window{Kind: tray.RoutineWindow, Routine: "standup"})
	if s.State().Section() != tray.SetupWindow || s.State().BackTitle() != "Setup" {
		t.Fatalf("%+v", s.State())
	}
	// Pushing a section's own screen (Settings) switches to it.
	s.Push(tray.Window{Kind: tray.SettingsWindow})
	if _, ok := s.Shown().(*ui.Settings); !ok || len(s.State().Path()) > 0 || s.State().BackTitle() != "standup" {
		t.Fatalf("%T %+v", s.Shown(), s.State())
	}
}

// A section chosen in the sidebar shows its own screen, marked there.
func TestTheSidebarChoosesASection(t *testing.T) {
	s, _, _ := newShell(t)
	s.Show(tray.Window{Kind: tray.RoutineWindow, Routine: "standup"})
	side := buttons(s.sidebar)
	var names []string
	for _, b := range s.sidebar.Objects {
		if b, ok := b.(*widget.Button); ok {
			names = append(names, b.Text)
		}
	}
	if want := []string{"Routines", "Hooks", "Acknowledgements", "Setup", "Settings", "Updates"}; !slices.Equal(names, want) {
		t.Fatalf("the sidebar: %q", names)
	}
	test.Tap(side["Acknowledgements"])
	if _, ok := s.Shown().(*ui.Acknowledgements); !ok || len(s.State().Path()) > 0 || s.State().BackTitle() != "standup" {
		t.Fatalf("%T", s.Shown())
	}
	if buttons(s.sidebar)["Acknowledgements"].Importance != widget.HighImportance {
		t.Fatal("not marked")
	}
}

// The sidebar is always shown: the bar holds only Back and Forward.
func TestTheSidebarIsAlwaysShown(t *testing.T) {
	s, _, _ := newShell(t)
	s.Show(tray.Window{Kind: tray.RoutinesWindow})
	back, forward := arrows(t, s)
	if !s.side.Visible() || back.Icon != ui.ChevronLeftIcon || forward.Icon != ui.ChevronRightIcon {
		t.Fatal("the sidebar hidden, or the bar isn't Back and Forward")
	}
}

// The sidebar says how many notifications wait, how many setup steps are
// left, and when an update is there.
func TestTheSidebarSaysWhatWaits(t *testing.T) {
	s, _, _ := newShell(t)
	s.Show(tray.Window{Kind: tray.SettingsWindow})
	s.Update(&cli.RunStatus{SetupLeft: []string{"backend"}, Waiting: []cli.Waiting{{ID: "a"}, {ID: "b"}}})
	s.UpdateStatus(&cli.UpdateStatus{UpdateAvailable: true})
	got := texts(s.sidebar)
	for _, want := range []string{"2", "1 to do", "New"} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
}

// A notification's run opens its routine on that run; a review opens it
// on its Turn On review.
func TestARunOrAReviewIsShown(t *testing.T) {
	s, _, ran := newShell(t)
	s.Show(tray.Window{Kind: tray.RoutineWindow, Routine: "standup", Run: "r-42"})
	if !slices.Contains(ran(), "runs show r-42 --json") {
		t.Fatalf("the run wasn't read: %q", ran())
	}
	s.Show(tray.Window{Kind: tray.HookWindow, Routine: "open-meet", Run: "r-7"})
	if !slices.Contains(ran(), "runs show r-7 --json") {
		t.Fatalf("the hook's run wasn't read: %q", ran())
	}
}

// Shown again from outside, a screen is read again.
func TestShowingAgainReadsAgain(t *testing.T) {
	s, _, ran := newShell(t)
	count := func() int {
		n := 0
		for _, r := range ran() {
			if r == "routines list --kind all --json" {
				n++
			}
		}
		return n
	}
	s.Show(tray.Window{Kind: tray.RoutinesWindow})
	s.Show(tray.Window{Kind: tray.RoutinesWindow})
	if count() != 2 {
		t.Fatalf("read %d times", count())
	}
}

// Template Updates isn't a section: opened over a screen, closing it goes
// back there; opened in a window of its own, closing it closes the window.
// The window closing tells the screens open (Template Updates' Not now).
func TestTemplateUpdatesClosesBackToWhereTheWindowWas(t *testing.T) {
	s, w, _ := newShell(t)
	s.Show(tray.Window{Kind: tray.HookWindow, Routine: "open-meet"})
	s.Show(tray.Window{Kind: tray.TemplateUpdatesWindow})
	if slices.Contains(nav.Sections, tray.TemplateUpdatesWindow) {
		t.Error("Template Updates is a sidebar section")
	}
	if _, ok := buttons(s.sidebar)["Template Updates"]; ok {
		t.Error("Template Updates is in the sidebar")
	}
	u, ok := s.Shown().(*ui.TemplateUpdates)
	if !ok {
		t.Fatalf("showing %T", s.Shown())
	}
	u.NotNow() // nothing listed (the fake answers {}): it just closes
	if s.State().Shown() != (nav.Destination{Kind: tray.HookWindow, Name: "open-meet"}) || w.Title() != "open-meet — Acme" {
		t.Fatalf("back on %+v", s.State().Shown())
	}

	s, w, _ = newShell(t)
	closed := false
	w.SetOnClosed(func() { closed = true })
	s.Show(tray.Window{Kind: tray.TemplateUpdatesWindow})
	s.Shown().(*ui.TemplateUpdates).NotNow()
	if !closed {
		t.Error("the window opened for it didn't close")
	}
}
