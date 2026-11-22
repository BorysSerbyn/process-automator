package tray

import (
	"context"
	"errors"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/app"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// View is the tray icon and its menu on the desktop.
type View interface {
	SetIcon(Frame)
	SetMenu([]MenuItem)
}

// RefreshEvery is how often the tray shows the last status again, so one
// past its validUntil goes red (the core stopped heartbeating, or the
// watch did); every fourth time it also asks for notifications (an
// update's result changes no status).
var RefreshEvery = 15 * time.Second

// Tray is the tray app: it follows run status, shows it in the icon and
// menu, delivers the app's notifications and offers updates.
type Tray struct {
	info app.Info
	cli  cli.CLI
	view View
	desk *Desk
	// openWindow is the window seam (OpenWindow).
	openWindow func(cliPath string, w Window) error
	quit       func()
	now        func() time.Time

	notifier *Notifier
	updates  *Updates

	mu     sync.Mutex
	status *cli.RunStatus
	pulse  bool
	// pulsing is closed to stop the pulse while one runs.
	pulsing   chan struct{}
	shownIcon *Frame
	shownMenu []MenuItem
}

// New is the tray for info's app, shown in view, notifying through
// notifications; openWindow opens windows, and quit ends the tray.
func New(info app.Info, view View, notifications Notifications, openWindow func(string, Window) error, quit func()) *Tray {
	t := &Tray{
		info: info, cli: cli.CLI{Path: info.CLI, Command: info.Command}, view: view, desk: NewDesk(notifications),
		openWindow: openWindow, quit: quit, now: time.Now,
	}
	t.notifier = NewNotifier(t.cli, t.desk, t.showClick, t.Alert)
	t.updates = NewUpdates(t.cli, t.desk, t.open, t.Alert, t.render)
	return t
}

// Desk is where the tray's notes are posted: the desktop's actions go to it.
func (t *Tray) Desk() *Desk { return t.desk }

// Run follows the core until ctx ends. Starting, it starts the scheduler if
// it isn't running (opening the app does, as automator-ui's launch does), so
// opening it after a Quit brings everything back.
func (t *Tray) Run(ctx context.Context) {
	go t.startScheduler()
	t.render()
	t.updates.Start(ctx.Done())
	go t.cli.Follow(ctx, t.Show)
	tick := time.NewTicker(RefreshEvery)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			t.setPulsing(false)
			return
		case <-tick.C:
			t.render()
			if n%4 == 0 {
				t.notifier.Deliver()
			}
		}
	}
}

// Show shows a run status (nil: the core's status couldn't be read) and,
// when it changed, delivers new notifications (a run's end changes it). A
// heartbeat (the same status, valid for longer) asks for none: Run's tick
// does every minute.
func (t *Tray) Show(s *cli.RunStatus) {
	t.mu.Lock()
	changed := !sameStatus(t.status, s)
	t.status = s
	t.mu.Unlock()
	t.render()
	if changed {
		t.notifier.Deliver()
	}
}

// sameStatus is whether a and b say the same, whatever their validUntil.
func sameStatus(a, b *cli.RunStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := *a, *b
	x.ValidUntil, y.ValidUntil = nil, nil
	return reflect.DeepEqual(x, y)
}

// MenuOpened is the menu being opened.
func (t *Tray) MenuOpened() { t.updates.MenuOpened() }

// render shows the icon and menu for the last status, changing only what
// changed.
func (t *Tray) render() {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Read under t.mu, so a render can't show an older update item after a
	// newer one (Updates never calls in while holding its own lock).
	update := t.updates.MenuItem()
	now := t.now()
	t.setPulsingLocked(Pulsing(t.status, now))
	frame := FrameFor(t.status, now, t.pulse)
	if t.shownIcon == nil || *t.shownIcon != frame {
		t.view.SetIcon(frame)
		t.shownIcon = &frame
	}
	menu := Menu(t.info.Name, t.info.Version, t.status, now, update)
	if !reflect.DeepEqual(menu, t.shownMenu) {
		t.view.SetMenu(menu)
		t.shownMenu = menu
	}
}

func (t *Tray) setPulsing(on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setPulsingLocked(on)
}

// setPulsingLocked starts or stops the pulse: the dot dims and brightens
// every PulseInterval while a routine runs.
func (t *Tray) setPulsingLocked(on bool) {
	switch {
	case on && t.pulsing == nil:
		stop := make(chan struct{})
		t.pulsing = stop
		go func() {
			tick := time.NewTicker(PulseInterval)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					t.mu.Lock()
					select {
					case <-stop: // stopped while this tick waited
						t.mu.Unlock()
						return
					default:
					}
					t.pulse = !t.pulse
					t.mu.Unlock()
					t.render()
				}
			}
		}()
	case !on && t.pulsing != nil:
		close(t.pulsing)
		t.pulsing, t.pulse = nil, false
	}
}

// Choose does what a menu item does.
func (t *Tray) Choose(a Action) {
	switch a.Kind {
	case OpenRun:
		go t.openRun(a.Run)
	case ShowWindow:
		t.open(a.Window)
	case CheckUpdates:
		go t.updates.Check(false)
	case InstallUpdate:
		go t.updates.Install()
	case ShowUpdate:
		t.updates.ShowProgress()
	case Quit:
		go t.quitApp()
	}
}

// startScheduler starts the scheduler if it isn't running; why it couldn't
// goes to the log (run status shows it isn't responding, doctor says why).
func (t *Tray) startScheduler() {
	if _, err := t.cli.StartScheduler(); err != nil {
		log.Printf("couldn't start the scheduler: %v", err)
	}
}

// quitApp is Quit: the scheduler stopped too (both start again at the next
// login), then the tray ends; when stopping fails, it says why, and ends
// anyway.
func (t *Tray) quitApp() {
	if _, err := t.cli.StopApp(); err != nil {
		log.Printf("Quit: couldn't stop the scheduler: %v", err)
		t.Alert("Couldn't stop the scheduler", err.Error())
	}
	t.quit()
}

// OpenApp is the app opened while the tray runs (its app menu entry): the
// scheduler started if it isn't running, and the window the core's status
// calls for (ReopenWindow) opened.
func (t *Tray) OpenApp() {
	t.startScheduler()
	var status *cli.RunStatus
	if s, err := t.cli.Status(); err == nil {
		status = &s
	} else {
		log.Printf("opening the app: the status: %v", err)
	}
	t.open(ReopenWindow(status))
}

func (t *Tray) openRun(id string) {
	if err := t.cli.OpenRun(id); err != nil {
		t.Alert("Couldn't open the run", err.Error())
	}
}

// open opens a window through the window seam; when it can't, it says how to
// do the same in a terminal.
func (t *Tray) open(w Window) {
	err := t.openWindow(t.info.CLI, w)
	switch {
	case errors.Is(err, ErrNoTerminal):
		t.Alert("No terminal to open it in",
			"Run `"+t.info.Command+" "+strings.Join(w.Command(), " ")+"` in a terminal. ("+err.Error()+")")
	case err != nil:
		t.Alert("Couldn't open it", err.Error())
	}
}

// showClick shows what a click on one of the app's notifications should:
// the run it's about, or the app (its routines).
func (t *Tray) showClick(click cli.ClickOutput) {
	if click.Action == "show-run" && click.Run != "" {
		t.openRun(click.Run)
		return
	}
	t.open(Window{Kind: RoutinesWindow})
}

// Alert tells me something, as a notification of the tray's own (the
// core's notifications are the Notifier's).
func (t *Tray) Alert(title, text string) {
	_ = t.desk.Post(Note{Summary: title, Body: text}, nil)
}
