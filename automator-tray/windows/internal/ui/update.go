package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// UpdateEvery is how often the Updating window reads the update's progress
// while it's installing.
const UpdateEvery = 2 * time.Second

// Update is the Updating window's content: the update installing, as the
// core describes it (`update progress`: its release, what it's doing and the
// latest line it printed), read every UpdateEvery until it isn't
// installing; then how it ended. Asking first (where notifications can't),
// it says what's available (`update --check`: the release and the core's
// note) and starts it on Update (`update --detach`). It mirrors
// automator-ui's UpdateProgressView.
type Update struct {
	cli   cli.CLI
	async Async
	close func()
	now   func() time.Time
	// after schedules the next read (tests read at once, or never).
	after func(time.Duration, func())

	asking   bool
	check    *cli.UpdateOutput
	progress *cli.UpdateProgress
	err      string
	starting bool

	box *fyne.Container
}

// NewUpdate makes the Updating window's content (ask: asking before it
// installs); Load reads it. done closes the window.
func NewUpdate(c cli.CLI, async Async, ask bool, done func()) *Update {
	u := &Update{cli: c, async: async, asking: ask, close: done, now: time.Now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) }}
	u.box = container.NewVBox()
	u.refresh()
	return u
}

// Content is what the window shows.
func (u *Update) Content() fyne.CanvasObject { return padded(20, 20, 20, 20, u.box) }

// Load reads what's available (asking) or the update's progress.
func (u *Update) Load() {
	if u.asking {
		u.async(func() func() {
			out, err := u.cli.UpdateCheck()
			return func() {
				u.err = ""
				if err != nil {
					u.err = err.Error()
				} else {
					u.check = &out
					if out.InProgress {
						u.asking = false
						u.Load()
					}
				}
				u.refresh()
			}
		})
		return
	}
	u.async(func() func() {
		p, err := u.cli.UpdateProgress()
		return func() {
			if err != nil {
				u.err = err.Error()
			} else {
				u.progress, u.err = &p, ""
			}
			u.refresh()
			if err == nil && p.State == "installing" {
				u.after(UpdateEvery, func() { fyne.Do(u.Load) })
			}
		}
	})
}

// start installs the update (`update --detach`), then follows it.
func (u *Update) start() {
	u.starting = true
	u.refresh()
	u.async(func() func() {
		out, err := u.cli.StartUpdate()
		return func() {
			u.starting = false
			switch {
			case err != nil:
				u.err = err.Error()
			case out.Started || out.InProgress:
				u.asking, u.err = false, ""
				u.Load()
			default:
				u.check = &out
			}
			u.refresh()
		}
	})
}

func (u *Update) refresh() {
	u.box.Objects = u.render()
	u.box.Refresh()
}

func (u *Update) render() []fyne.CanvasObject {
	if u.asking {
		return u.askView()
	}
	p := u.progress
	state := ""
	if p != nil {
		state = p.State
	}
	tagText := ""
	if p != nil && p.Tag != "" {
		tagText = " to " + p.Tag
	}
	icon, title := theme.DownloadIcon(), "Updating"+tagText
	switch state {
	case "failed":
		icon, title = theme.NewErrorThemedResource(theme.ErrorIcon()), "The update"+tagText+" failed"
	case "done":
		icon, title = theme.NewSuccessThemedResource(theme.ConfirmIcon()), "Update finished"
	case "none":
		icon, title = theme.InfoIcon(), "No update is installing"
	}
	head := container.NewVBox(heading(title, false))
	if p != nil && p.StartedAt != nil {
		head.Add(secondary("Started " + when(*p.StartedAt, u.now())))
	}
	objects := []fyne.CanvasObject{container.NewBorder(nil, nil, container.NewVBox(sized(32, widget.NewIcon(icon))), nil, head)}
	closeText := "Close"
	switch state {
	case "failed":
		objects = append(objects, text(p.Error))
		if p.Log != "" {
			objects = append(objects, secondary("Log: "+p.Log))
		}
	case "done":
		objects = append(objects, text(p.Step))
	case "none":
		objects = append(objects, secondary("Check for updates in the tray's menu."))
	default:
		closeText = "Hide"
		bar := widget.NewProgressBarInfinite()
		step := "Starting…"
		if p != nil && p.Step != "" {
			step = p.Step
		}
		objects = append(objects, bar, text(step))
		if p != nil && p.Detail != "" {
			d := widget.NewLabelWithStyle(p.Detail, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
			d.Truncation = fyne.TextTruncateEllipsis
			d.SizeName = captionSize
			objects = append(objects, d)
		}
		objects = append(objects, secondary("You can close this window: the update carries on, and the app restarts when it's done."))
	}
	if u.err != "" {
		objects = append(objects, errorRow(u.err))
	}
	b := widget.NewButton(closeText, u.close)
	b.Importance = widget.HighImportance
	return append(objects, right(b))
}

// askView says what's available and asks before installing it.
func (u *Update) askView() []fyne.CanvasObject {
	c := u.check
	var objects []fyne.CanvasObject
	later := widget.NewButton("Later", u.close)
	switch {
	case c == nil && u.err == "":
		objects = append(objects, spinner())
	case c != nil && c.UpdateAvailable:
		objects = append(objects, container.NewBorder(nil, nil, container.NewVBox(sized(32, widget.NewIcon(theme.DownloadIcon()))), nil,
			container.NewVBox(heading("Update to "+c.Latest+"?", false), secondary("This is "+c.Current+"."))))
		if c.Note != "" {
			objects = append(objects, text(c.Note))
		}
		update := widget.NewButton("Update", u.start)
		update.Importance = widget.HighImportance
		if u.starting {
			update.Disable()
		}
		if u.err != "" {
			objects = append(objects, errorRow(u.err))
		}
		return append(objects, right(later, update))
	case c != nil:
		objects = append(objects, heading("Nothing to update", false), secondary("This is "+c.Current+", the latest release."))
		later.SetText("Close")
	}
	if u.err != "" {
		objects = append(objects, errorRow(u.err))
	}
	return append(objects, right(later))
}
