package tray

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a settable now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// Each status shows at once and asks for notifications (a run's end
// changes it); what didn't change isn't set again; a status past its
// validUntil goes red on the next refresh.
func TestAStatusShowsAndDelivers(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", pending)
	tr, v, n, _ := newTray(t, f)
	c := &clock{t: fresh}
	tr.now = c.now

	tr.Show(status(t, lineOK))
	frame, menu, icons, menus := v.last()
	if frame != (Frame{Light: Green}) || menu[1].Text != "All good" || icons != 1 || menus != 1 {
		t.Fatalf("%+v %+v %d %d", frame, menu, icons, menus)
	}
	eventually(t, "the notifications", func() bool { return n.count() == 2 })

	tr.render() // nothing changed
	if _, _, icons, menus := v.last(); icons != 1 || menus != 1 {
		t.Fatalf("set again: %d icons, %d menus", icons, menus)
	}
	c.set(stale)
	tr.render()
	frame, menu, _, _ = v.last()
	if frame != (Frame{Light: Red}) || menu[1].Text != "Not responding" {
		t.Fatalf("stale: %+v %+v", frame, menu[1])
	}
	tr.Show(nil) // unreadable
	if frame, _, _, _ := v.last(); frame != (Frame{Light: Red}) {
		t.Fatalf("unreadable: %+v", frame)
	}
}

// A heartbeat (the same status, valid for longer) doesn't ask for
// notifications; a change does.
func TestAHeartbeatDoesntAskForNotifications(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", "[]")
	tr, _, _, _ := newTray(t, f)
	asks := func() int {
		n := 0
		for _, c := range f.ran() {
			if c == "notifications list --pending --json" {
				n++
			}
		}
		return n
	}
	tr.Show(status(t, lineOK))
	eventually(t, "the first ask", func() bool { return asks() == 1 })
	beat := status(t, lineOK)
	later := beat.ValidUntil.Add(time.Minute)
	beat.ValidUntil = &later
	tr.Show(beat)
	time.Sleep(50 * time.Millisecond)
	if asks() != 1 {
		t.Fatalf("a heartbeat asked: %q", f.ran())
	}
	tr.Show(nil)
	eventually(t, "an ask on a change", func() bool { return asks() == 2 })
}

// While busy the dot pulses: dimmed and bright in turn; it stops when the
// core says it's done.
func TestBusyPulses(t *testing.T) {
	saved := PulseInterval
	PulseInterval = 5 * time.Millisecond
	t.Cleanup(func() { PulseInterval = saved })
	f := newFakeCLI(t)
	tr, v, _, _ := newTray(t, f)
	tr.now = func() time.Time { return fresh }

	tr.Show(status(t, lineBusy))
	eventually(t, "a dim frame and a bright one after it", func() bool {
		v.mu.Lock()
		defer v.mu.Unlock()
		for i := 1; i < len(v.icons); i++ {
			if v.icons[i-1] == (Frame{Light: Green, Dim: true}) && v.icons[i] == (Frame{Light: Green}) {
				return true
			}
		}
		return false
	})
	tr.Show(status(t, lineOK))
	time.Sleep(30 * time.Millisecond)
	_, _, before, _ := v.last()
	time.Sleep(30 * time.Millisecond)
	frame, _, after, _ := v.last()
	if frame != (Frame{Light: Green}) || after != before {
		t.Fatalf("still pulsing: %+v, %d → %d icons", frame, before, after)
	}
}

// Menu items do what automator-ui's do: a reason opens its run, windows open
// through the seam, Quit quits the tray.
func TestChoosingMenuItems(t *testing.T) {
	f := newFakeCLI(t)
	tr, _, n, w := newTray(t, f)
	quit := make(chan struct{})
	tr.quit = func() { close(quit) }

	tr.Choose(Action{Kind: OpenRun, Run: "r-42"})
	eventually(t, "the run", func() bool { return f.hasRun("runs show r-42 --open") })
	for _, win := range []Window{{Kind: SetupWindow}, {Kind: SettingsWindow}, {Kind: RoutinesWindow}, {Kind: RoutineWindow, Routine: "standup"}} {
		tr.Choose(Action{Kind: ShowWindow, Window: win})
		if !w.has(win) {
			t.Fatalf("%+v not opened", win)
		}
	}
	tr.Choose(Action{Kind: Quit})
	<-quit
	if !f.hasRun("login-items stop --json") {
		t.Fatalf("Quit didn't stop the scheduler: %q", f.ran())
	}

	// No terminal: it says what to run instead.
	w.err = ErrNoTerminal
	tr.Choose(Action{Kind: ShowWindow, Window: Window{Kind: SetupWindow}})
	id := n.find("No terminal to open it in")
	if id == 0 || !strings.Contains(n.note(id).Body, "Run `acme-auto setup` in a terminal") {
		t.Fatalf("%+v", n.notes)
	}
	// A run that doesn't open says why.
	f.fail("runs show r-1 --open", "no run r-1")
	tr.Choose(Action{Kind: OpenRun, Run: "r-1"})
	eventually(t, "the alert", func() bool { return n.find("Couldn't open the run") != 0 })
}

// Manage Updates… says what the core says: an update available, or one
// installing (followed with `update progress` until it's over); it opens
// the Manage Updates window. Opening the menu asks again and postpones
// automatic updates.
func TestTheUpdatesItem(t *testing.T) {
	savedEvery := ProgressEvery
	ProgressEvery = 5 * time.Millisecond
	t.Cleanup(func() { ProgressEvery = savedEvery })
	f := newFakeCLI(t)
	tr, _, _, w := newTray(t, f)
	u := tr.updates
	item := func(text string) MenuItem {
		return MenuItem{Text: text, Action: Action{Kind: ShowWindow, Window: Window{Kind: UpdatesWindow}}}
	}
	if got := u.MenuItem(); !reflect.DeepEqual(got, item("Manage Updates…")) {
		t.Fatalf("%+v", got)
	}
	f.answer("update status --json", `{"current":"2.1.0","development":false,"automatic":false,"setting":"update.auto","latest":"v2.2.0","updateAvailable":true,"installing":false}`)
	u.Refresh()
	if got := u.MenuItem(); !reflect.DeepEqual(got, item("Manage Updates… (v2.2.0 available)")) {
		t.Fatalf("available: %+v", got)
	}
	tr.Choose(u.MenuItem().Action)
	if !w.has(Window{Kind: UpdatesWindow}) {
		t.Fatalf("opened %+v", w.opened)
	}

	f.answer("update status --json", `{"current":"2.1.0","development":false,"automatic":true,"setting":"update.auto","latest":"v2.2.0","updateAvailable":true,"installing":true,"installingTag":"v2.2.0"}`)
	f.answer("update progress --json", `{"state":"installing","tag":"v2.2.0","step":"Downloading","log":"/x/update.log"}`)
	tr.MenuOpened()
	eventually(t, "updating to v2.2.0", func() bool {
		return reflect.DeepEqual(u.MenuItem(), item("Manage Updates… (updating to v2.2.0…)")) && f.hasRun("update progress --json")
	})
	eventually(t, "postponed", func() bool { return f.hasRun("update postpone") })
	// It ended (here, failed: the app didn't restart): the menu says what the core does again.
	f.answer("update status --json", `{"current":"2.1.0","development":false,"automatic":true,"setting":"update.auto","latest":"v2.2.0","updateAvailable":true,"installing":false}`)
	f.answer("update progress --json", `{"state":"failed","tag":"v2.2.0","error":"checksum mismatch","log":"/x/update.log"}`)
	eventually(t, "available again", func() bool { return reflect.DeepEqual(u.MenuItem(), item("Manage Updates… (v2.2.0 available)")) })
}

// When the core can't say how the update is going (it keeps failing), the
// tray stops asking after a while, and asks where updates stand again.
func TestFollowingAnUpdateStopsWhenTheCoreCantSay(t *testing.T) {
	savedEvery, savedFailures := ProgressEvery, ProgressFailures
	ProgressEvery, ProgressFailures = time.Millisecond, 3
	t.Cleanup(func() { ProgressEvery, ProgressFailures = savedEvery, savedFailures })
	f := newFakeCLI(t)
	f.fail("update progress --json", "config doesn't load")
	tr, _, _, _ := newTray(t, f)
	tr.updates.following = true
	tr.updates.follow() // returns
	asked := 0
	for _, c := range f.ran() {
		if c == "update progress --json" {
			asked++
		}
	}
	if asked != 3 || tr.updates.following || !f.hasRun("update status --json") {
		t.Fatalf("asked %d times, following %v: %q", asked, tr.updates.following, f.ran())
	}
}

// Quit stops the scheduler, then quits; when stopping fails, it says why
// and quits anyway.
func TestQuitStopsTheSchedulerThenQuits(t *testing.T) {
	f := newFakeCLI(t)
	tr, _, n, _ := newTray(t, f)
	f.fail("login-items stop --json", "launchctl bootout failed")
	quit := make(chan struct{})
	tr.quit = func() {
		if !f.hasRun("login-items stop --json") {
			t.Error("quit before stopping the scheduler")
		}
		close(quit)
	}
	tr.Choose(Action{Kind: Quit})
	<-quit
	id := n.find("Couldn't stop the scheduler")
	if id == 0 || n.note(id).Body != "launchctl bootout failed" {
		t.Fatalf("%+v", n.notes)
	}
}

// Starting, the tray starts the scheduler (opening the app does).
func TestRunStartsTheScheduler(t *testing.T) {
	f := newFakeCLI(t)
	tr, _, _, _ := newTray(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)
	eventually(t, "login-items start", func() bool { return f.hasRun("login-items start --json") })
}

// Starting, the tray asks what changed in the templates I installed from,
// and opens Template Updates when anything did; nothing changed, it opens
// nothing.
func TestRunOffersTemplateUpdates(t *testing.T) {
	for _, c := range []struct {
		answer string
		opens  bool
	}{
		{`[{"name":"standup","kind":"routine","template":"projects/acme/standup","installedVersion":"1","version":"2","edited":"no"}]`, true},
		{`[]`, false},
	} {
		f := newFakeCLI(t)
		f.answer("templates outdated --json", c.answer)
		tr, _, _, w := newTray(t, f)
		ctx, cancel := context.WithCancel(context.Background())
		go tr.Run(ctx)
		eventually(t, "templates outdated", func() bool { return f.hasRun("templates outdated --json") })
		if c.opens {
			eventually(t, "Template Updates", func() bool { return w.has(Window{Kind: TemplateUpdatesWindow}) })
		} else {
			time.Sleep(50 * time.Millisecond)
			if w.has(Window{Kind: TemplateUpdatesWindow}) {
				t.Error("opened Template Updates with nothing to offer")
			}
		}
		cancel()
	}
}

// Opening the app while the tray runs starts the scheduler and opens Setup
// while the core's status lists setup steps left, else Settings.
func TestOpeningTheAppOpensSetupOrSettings(t *testing.T) {
	f := newFakeCLI(t)
	tr, _, _, w := newTray(t, f)
	f.answer("status --json", `{"schemaVersion":1,"health":"error","busy":false,"setupLeft":["backend"],"routines":[]}`)
	tr.OpenApp()
	if !f.hasRun("login-items start --json") || !w.has(Window{Kind: SetupWindow}) {
		t.Fatalf("%q %+v", f.ran(), w.opened)
	}
	f.answer("status --json", `{"schemaVersion":1,"health":"ok","busy":false,"routines":[]}`)
	tr.OpenApp()
	if !w.has(Window{Kind: SettingsWindow}) {
		t.Fatalf("%+v", w.opened)
	}
	// The status unreadable: Settings, and the scheduler failing to start
	// doesn't stop it.
	w.opened = nil
	f.fail("status --json", "no status")
	f.fail("login-items start --json", "no systemd user session")
	tr.OpenApp()
	if !w.has(Window{Kind: SettingsWindow}) {
		t.Fatalf("%+v", w.opened)
	}
}
