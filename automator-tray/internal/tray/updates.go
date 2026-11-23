package tray

import (
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// How often Updates asks: where updates stand (no network: the scheduler
// checks every hour), and what an update installing is doing.
// ProgressFailures is how many asks for the progress may fail in a row
// before it's no longer followed (the CLI can't say: a broken config).
var (
	StatusEvery      = time.Minute
	ProgressEvery    = 2 * time.Second
	ProgressFailures = 30
)

// Updates are updates as the core reports them, as automator-ui's: where
// they stand (`update status`) on launch, every StatusEvery and when the
// menu opens; while one installs, what it's doing (`update progress`,
// every ProgressEvery). The menu's Manage Updates… line says when one is
// available or installing, and opens the Manage Updates window, which
// checks, installs and turns automatic updates on and off. Opening the menu
// holds automatic updates back (`update postpone`), as the windows do while
// they're open. Whether one is available or installing is the core's
// answer, never guessed here.
type Updates struct {
	cli cli.CLI
	// changed says the menu item changed.
	changed func()

	mu sync.Mutex
	// status is what the core last said (`update status`).
	status *cli.UpdateStatus
	// progress is the update installing (or the one that last ended), while it's followed.
	progress  *cli.UpdateProgress
	following bool
	asking    bool
}

// NewUpdates asks c about updates.
func NewUpdates(c cli.CLI, changed func()) *Updates {
	return &Updates{cli: c, changed: changed}
}

// Start asks now and every StatusEvery, until stop is closed.
func (u *Updates) Start(stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(StatusEvery)
		defer t.Stop()
		for {
			u.Refresh()
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
}

func (u *Updates) installing() bool {
	if u.progress != nil {
		return u.progress.State == "installing"
	}
	return u.status != nil && u.status.Installing
}

// MenuItem is the menu's Manage Updates… line: it says when an update is
// installing or available, and opens the Manage Updates window.
func (u *Updates) MenuItem() MenuItem {
	u.mu.Lock()
	defer u.mu.Unlock()
	text := "Manage Updates…"
	switch {
	case u.installing():
		tag := ""
		if u.progress != nil {
			tag = u.progress.Tag
		}
		if tag == "" && u.status != nil {
			tag = u.status.InstallingTag
		}
		if tag == "" {
			text += " (updating…)"
		} else {
			text += " (updating to " + tag + "…)"
		}
	case u.status != nil && u.status.UpdateAvailable && u.status.Latest != "":
		text += " (" + u.status.Latest + " available)"
	}
	return MenuItem{Text: text, Action: Action{Kind: ShowWindow, Window: Window{Kind: UpdatesWindow}}}
}

// MenuOpened asks again, and holds automatic updates back: I'm using the app.
func (u *Updates) MenuOpened() {
	go u.Refresh()
	go func() { _ = u.cli.PostponeUpdate() }()
}

// Refresh asks the core where updates stand; while one installs, it's
// followed.
func (u *Updates) Refresh() {
	u.mu.Lock()
	if u.asking {
		u.mu.Unlock()
		return
	}
	u.asking = true
	u.mu.Unlock()
	answer, err := u.cli.UpdateStatus()
	u.mu.Lock()
	u.asking = false
	if err == nil {
		u.status = &answer
	}
	follow := err == nil && answer.Installing && !u.following
	if follow {
		u.following = true
		u.progress = nil
	}
	u.mu.Unlock()
	if follow {
		go u.follow()
	}
	u.changed()
}

// follow asks the core what the update is doing every ProgressEvery, until
// it says it's over, or it can't say for ProgressFailures asks in a row;
// then asks where updates stand again.
func (u *Updates) follow() {
	for failures := 0; failures < ProgressFailures; {
		p, err := u.cli.UpdateProgress()
		if err != nil {
			failures++
			time.Sleep(ProgressEvery)
			continue
		}
		failures = 0
		u.mu.Lock()
		u.progress = &p
		over := !u.installing()
		u.mu.Unlock()
		u.changed()
		if over {
			break
		}
		time.Sleep(ProgressEvery)
	}
	u.mu.Lock()
	u.following, u.progress = false, nil
	u.mu.Unlock()
	u.Refresh()
}
