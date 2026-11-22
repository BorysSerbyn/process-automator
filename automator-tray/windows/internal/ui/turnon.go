package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// turnOn is turning something on (an LLM routine, a program, a hook): one
// review, read from `trust --check`, and one confirm, `trust --yes
// --resume` (a hook's also trusts its watcher), with the sign-ins the
// review says are still needed done from it. Every failure shows in it:
// nothing fails silently. It mirrors automator-ui's TurnOn and TurnOnSheet;
// a window shows it above its tabs while it's open.
type turnOn struct {
	cli   cli.CLI
	async Async
	name  string
	hook  bool
	// summary is what it does, in a line (the window's); redraw draws the
	// window again; turnedOn reads it again once it's on.
	summary  func() string
	redraw   func()
	turnedOn func()

	showing     bool
	review      *cli.TrustOutput
	loadError   string
	working     bool
	err         string
	signingIn   string
	signInError string
	detailsOpen bool
}

func newTurnOn(c cli.CLI, async Async, name string, hook bool, summary func() string, redraw, turnedOn func()) *turnOn {
	return &turnOn{cli: c, async: async, name: name, hook: hook, summary: summary, redraw: redraw, turnedOn: turnedOn}
}

// start is Turn On…: the review shows, read afresh.
func (t *turnOn) start() {
	t.showing, t.err = true, ""
	t.load()
	t.redraw()
}

func (t *turnOn) cancel() {
	t.showing = false
	t.redraw()
}

func (t *turnOn) load() {
	t.loadError = ""
	t.async(func() func() {
		var review cli.TrustOutput
		var err error
		if t.hook {
			review, err = t.cli.HookTrustCheck(t.name)
		} else {
			review, err = t.cli.TrustCheck(t.name)
		}
		return func() {
			if err != nil {
				t.loadError = err.Error()
			} else {
				t.review = &review
			}
			t.redraw()
		}
	})
}

// confirm trusts what the review showed and turns it on; the review closes
// once it's on, and why it couldn't stays in it.
func (t *turnOn) confirm() {
	t.working, t.err = true, ""
	t.redraw()
	t.async(func() func() {
		var err error
		if t.hook {
			err = t.cli.TrustHook(t.name, true)
		} else {
			err = t.cli.TurnOn(t.name)
		}
		return func() {
			t.working = false
			if err != nil {
				t.err = err.Error()
			} else {
				t.showing, t.review = false, nil
				t.turnedOn()
			}
			t.redraw()
		}
	})
}

// signIn signs in where the review says one is still needed (`auth login
// <name> --browser`, waiting while I do), then reads the review again.
func (t *turnOn) signIn(name string) {
	t.signingIn = name
	t.redraw()
	t.async(func() func() {
		err := t.cli.SignIn(name)
		return func() {
			t.signingIn, t.signInError = "", ""
			if err != nil {
				t.signInError = err.Error()
			}
			t.load()
			t.redraw()
		}
	})
}

// view is the review: what it does in a line, what to weigh, what it still
// needs before it works, its details folded underneath, and Cancel and the
// one confirm.
func (t *turnOn) view() fyne.CanvasObject {
	objects := []fyne.CanvasObject{cardTitle("Turn on " + t.name), divider()}
	inset := func(o fyne.CanvasObject) fyne.CanvasObject { return padded(2, 2, rowInset-8, rowInset-8, o) }
	if s := t.summary(); s != "" {
		objects = append(objects, inset(text(s)))
	}
	r := t.review
	switch {
	case r == nil && t.loadError != "":
		objects = append(objects, inset(errorRow(t.loadError)), inset(right(widget.NewButton("Try Again", t.load))))
	case r == nil:
		objects = append(objects, inset(container.NewHBox(spinner(), quiet("Reading what it can reach…"))))
	default:
		for _, w := range r.Warnings {
			objects = append(objects, padded(4, 2, rowInset-8, rowInset-8, warning("Before you turn it on", w)))
		}
		// What it needs before it works. Sign-ins go here; so would
		// anything else it needs from me first.
		for _, name := range r.SignIns {
			n := name
			var action fyne.CanvasObject
			if t.signingIn == n {
				action = container.NewHBox(spinner(), quiet("In your browser…"))
			} else {
				b := widget.NewButton("Sign In…", func() { t.signIn(n) })
				if t.signingIn != "" {
					b.Disable()
				}
				action = b
			}
			objects = append(objects, inset(container.NewBorder(nil, nil, widget.NewIcon(theme.AccountIcon()), action,
				widget.NewLabel(n+" needs signing in"))))
		}
		if len(r.SignIns) > 0 {
			if t.signInError != "" {
				objects = append(objects, inset(errorRow(t.signInError)))
			}
			objects = append(objects, inset(secondary("It can be turned on now; it works once signed in.")))
		}
		if w := r.WatcherToTrust(); w != nil {
			objects = append(objects, inset(secondary("Its watcher, "+w.Name+", is trusted with it: it's what tells "+
				r.Routine+" something happened.")))
		}
		if w := r.MissingWatcher(); w != nil {
			why := w.Untrusted
			if why == "" {
				why = "no such watcher"
			}
			objects = append(objects, inset(errorRow("It listens to "+w.Name+": "+why+". It hears nothing until there is one.")))
		}
		if r.ToolsError != "" {
			objects = append(objects, inset(errorRow("Couldn't check which of its tools write: "+r.ToolsError)))
		}
		objects = append(objects, newDisclosure(t.detailsOpen, false, func() {
			t.detailsOpen = !t.detailsOpen
			t.redraw()
		}, widget.NewLabel("Details")))
		if t.detailsOpen {
			details := trustReview(*r, "", "", nil, false, nil)
			if w := r.WatcherToTrust(); w != nil {
				details = append(details, divider(), cardTitle("Its watcher, "+w.Name))
				if w.Description != "" {
					details = append(details, infoRow("Watches", text(w.Description)))
				}
				details = append(details, executableRows(*w.Program, nil, false)...)
				details = append(details, changedRows(w.Changes)...)
			}
			objects = append(objects, padded(0, 4, rowInset-8, rowInset-8, card(true, details...)))
		}
	}
	if t.err != "" {
		objects = append(objects, padded(4, 0, rowInset, rowInset, errorRow(t.err)))
	}
	title := "Trust and Turn On"
	if r != nil {
		title = r.ConfirmTitle()
	}
	confirm := widget.NewButton(title, t.confirm)
	confirm.Importance = widget.HighImportance
	if r == nil || t.working {
		confirm.Disable()
	}
	var status fyne.CanvasObject = container.NewHBox()
	if t.working {
		status = spinner()
	}
	objects = append(objects, divider(), padded(4, 8, rowInset-8, rowInset-8,
		container.NewBorder(nil, nil, status, container.NewHBox(widget.NewButton("Cancel", t.cancel), confirm))))
	return card(false, objects...)
}
