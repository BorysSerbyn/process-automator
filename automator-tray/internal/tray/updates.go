package tray

import (
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// How often Updates asks: whether there's a release (as automator-ui, on
// launch and daily), and what an update installing is doing.
var (
	CheckEvery    = 24 * time.Hour
	ProgressEvery = 2 * time.Second
)

// Updates are updates as the core reports them, as automator-ui's: `update
// --check` on launch and daily, installing with `update --detach`, and while
// one installs, what it's doing (`update progress`, every 2 seconds), in the
// menu and the update window. Whether one is available or installing is the
// core's answer, never guessed here.
type Updates struct {
	cli  cli.CLI
	desk *Desk
	// open opens a window (the update window, or asking in a terminal).
	open func(Window)
	// alert tells me something (a note with no action).
	alert func(title, text string)
	// changed says the menu item changed.
	changed func()

	mu sync.Mutex
	// update is what the core last said (`update --check`, or the update it started).
	update *cli.UpdateOutput
	// progress is the update installing (or the one that last ended), while it's followed.
	progress  *cli.UpdateProgress
	following bool
}

// NewUpdates asks c about updates.
func NewUpdates(c cli.CLI, desk *Desk, open func(Window), alert func(title, text string), changed func()) *Updates {
	return &Updates{cli: c, desk: desk, open: open, alert: alert, changed: changed}
}

// Start checks quietly now and every CheckEvery, until stop is closed.
func (u *Updates) Start(stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(CheckEvery)
		defer t.Stop()
		for {
			u.Check(true)
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
}

func (u *Updates) installing() bool { return u.progress != nil && u.progress.State == "installing" }

// MenuItem is the menu's update item: installing, "Update to X…", or
// "Check for Updates".
func (u *Updates) MenuItem() MenuItem {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case u.update != nil && u.update.InProgress || u.installing():
		tag := ""
		if u.progress != nil {
			tag = u.progress.Tag
		}
		if tag == "" && u.update != nil {
			tag = u.update.Latest
		}
		text := "Updating…"
		if tag != "" {
			text = "Updating to " + tag + "…"
		}
		return MenuItem{Text: text, Action: Action{Kind: ShowUpdate}}
	case u.update != nil && u.update.UpdateAvailable:
		return MenuItem{Text: "Update to " + u.update.Latest + "…", Action: Action{Kind: InstallUpdate}}
	}
	return MenuItem{Text: "Check for Updates", Action: Action{Kind: CheckUpdates}}
}

// MenuOpened asks again while an update installs (it may have failed: it
// never restarts the app then).
func (u *Updates) MenuOpened() {
	u.mu.Lock()
	inProgress := u.update != nil && u.update.InProgress
	u.mu.Unlock()
	if inProgress {
		go u.Check(true)
	}
}

// Check asks the core whether a newer release exists; quietly, only the
// menu changes. Otherwise it says what it found, or installs it.
func (u *Updates) Check(quietly bool) {
	answer, err := u.cli.UpdateCheck()
	if err == nil {
		u.show(answer)
	}
	if quietly {
		return
	}
	switch {
	case err != nil:
		u.alert("Couldn't check for updates", err.Error())
	case answer.UpdateAvailable && !answer.InProgress:
		u.Install()
	case answer.InProgress:
		u.ShowProgress()
	default:
		u.alert("You're up to date", "This is "+answer.Current+".")
	}
}

func (u *Updates) show(answer cli.UpdateOutput) {
	u.mu.Lock()
	u.update = &answer
	follow := answer.InProgress && !u.following
	if follow {
		u.following = true
	}
	u.mu.Unlock()
	if follow {
		go u.follow()
	}
	u.changed()
}

// follow asks the core what the update is doing every ProgressEvery, until
// it says it's over; then checks again (the menu: up to date, or the update
// again).
func (u *Updates) follow() {
	for {
		if p, err := u.cli.UpdateProgress(); err == nil {
			u.mu.Lock()
			u.progress = &p
			over := !u.installing()
			if over {
				u.following = false
			}
			u.mu.Unlock()
			u.changed()
			if over {
				u.Check(true)
				return
			}
		}
		time.Sleep(ProgressEvery)
	}
}

// ShowProgress opens the update window: what the update is doing, or how it
// ended.
func (u *Updates) ShowProgress() { u.open(Window{Kind: UpdateWindow}) }

// Install asks before installing the newer release (with the core's note
// on what installing involves), then starts it. Where notifications can't
// ask, the terminal asks (the CLI's own update).
func (u *Updates) Install() {
	u.mu.Lock()
	update := u.update
	u.mu.Unlock()
	if update == nil || !update.UpdateAvailable {
		return
	}
	if !u.desk.CanAsk() {
		u.open(Window{Kind: ConfirmUpdateWindow})
		return
	}
	note := Note{
		Summary: "Update to " + update.Latest + "?", Body: update.Note,
		Actions: []string{"update", "Update", "later", "Later"}, Persistent: true,
	}
	err := u.desk.Post(note, func(action string) {
		if action == "update" {
			u.start()
		}
	})
	if err != nil {
		u.open(Window{Kind: ConfirmUpdateWindow})
	}
}

// start starts the update in the background (`update --detach`).
func (u *Updates) start() {
	answer, err := u.cli.StartUpdate()
	if err != nil {
		u.alert("The update didn't start", err.Error())
		return
	}
	u.show(answer)
	if answer.Started {
		u.ShowProgress()
	} else {
		u.alert("Nothing to update", "This is "+answer.Current+", the latest release.")
	}
}
