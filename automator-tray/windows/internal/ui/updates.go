package ui

import (
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// ProgressEvery is how often the Manage Updates window reads the update's
// progress while it's installing.
const ProgressEvery = 2 * time.Second

// Updates is the Manage Updates window's content, as the core says it:
// Install updates automatically at the top (`update status`'s setting, set
// with `settings set`), with why a newer release is waiting; the installed
// version with its release notes (`update notes`); then Check for Updates
// (`update --check`), which shows a newer release with its notes and
// Install Now (`update --detach`), or says it's up to date; and the update
// installing (`update progress`, read every ProgressEvery), or how one this
// window saw failed. It mirrors automator-ui's ManageUpdatesView.
type Updates struct {
	cli   cli.CLI
	async Async
	now   func() time.Time
	// after schedules the next read (tests read at once, or never).
	after func(time.Duration, func())

	status       *cli.UpdateStatus
	statusError  string
	notes        *cli.Notes
	notesError   string
	checking     bool
	checked      *cli.UpdateOutput
	checkError   string
	available    *cli.Notes
	availableErr string
	progress     *cli.UpdateProgress
	// sawInstalling: an update installed while the window showed, so how
	// it ended is news.
	sawInstalling bool
	following     bool
	starting      bool
	startError    string
	saving        bool
	autoError     string

	scroll *container.Scroll
}

// NewUpdates makes the window's content; Load reads it.
func NewUpdates(c cli.CLI, async Async) *Updates {
	u := &Updates{cli: c, async: async, now: time.Now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, func() { fyne.Do(f) }) }}
	u.scroll = container.NewVScroll(container.NewVBox())
	u.refresh()
	return u
}

// Content is what the window shows.
func (u *Updates) Content() fyne.CanvasObject { return u.scroll }

// Load reads where updates stand, this version's notes, and the update
// installing.
func (u *Updates) Load() {
	u.loadStatus()
	if u.notes == nil {
		u.async(func() func() {
			n, err := u.cli.ReleaseNotes("")
			return func() {
				if err != nil {
					u.notesError = err.Error()
				} else {
					u.notes, u.notesError = &n, ""
				}
				u.refresh()
			}
		})
	}
	u.readProgress()
}

func (u *Updates) loadStatus() {
	u.async(func() func() {
		st, err := u.cli.UpdateStatus()
		return func() {
			if err != nil {
				u.statusError = err.Error()
			} else {
				u.status, u.statusError = &st, ""
				if st.Installing && !u.following {
					u.readProgress()
				}
			}
			u.refresh()
		}
	})
}

// readProgress reads what the update is doing, again every ProgressEvery
// while it installs.
func (u *Updates) readProgress() {
	u.following = true
	u.async(func() func() {
		p, err := u.cli.UpdateProgress()
		return func() {
			u.following = false
			if err == nil {
				u.progress = &p
				if p.State == "installing" {
					u.sawInstalling, u.following = true, true
					u.after(ProgressEvery, u.readProgress)
				} else if u.status != nil && u.status.Installing {
					u.loadStatus()
				}
			}
			u.refresh()
		}
	})
}

func (u *Updates) installing() bool {
	if u.progress != nil {
		return u.progress.State == "installing"
	}
	return u.status != nil && u.status.Installing
}

// check asks GitHub now; a newer release comes with its notes.
func (u *Updates) check() {
	u.checking, u.checked, u.checkError, u.available, u.availableErr, u.startError = true, nil, "", nil, "", ""
	u.refresh()
	u.async(func() func() {
		out, err := u.cli.UpdateCheck()
		var notes cli.Notes
		var notesErr error
		if err == nil && out.UpdateAvailable && !out.InProgress {
			notes, notesErr = u.cli.ReleaseNotes(out.Latest)
		}
		return func() {
			u.checking = false
			switch {
			case err != nil:
				u.checkError = err.Error()
			case out.InProgress:
				u.readProgress()
			default:
				u.checked = &out
				if notesErr != nil {
					u.availableErr = notesErr.Error()
				} else if out.UpdateAvailable {
					u.available = &notes
				}
			}
			u.loadStatus()
			u.refresh()
		}
	})
}

// install starts the newer release installing (`update --detach`).
func (u *Updates) install() {
	u.starting, u.startError = true, ""
	u.refresh()
	u.async(func() func() {
		out, err := u.cli.StartUpdate()
		return func() {
			u.starting = false
			switch {
			case err != nil:
				u.startError = err.Error()
			case out.Started || out.InProgress:
				u.checked, u.available, u.sawInstalling = nil, nil, true
				u.readProgress()
			default:
				u.checked = &out
			}
			u.refresh()
		}
	})
}

// setAutomatic turns automatic updates on or off: the setting the core names.
func (u *Updates) setAutomatic(on bool) {
	if u.status == nil || u.saving {
		return
	}
	setting := u.status.Setting
	u.saving, u.autoError = true, ""
	u.refresh()
	u.async(func() func() {
		value := "false"
		if on {
			value = "true"
		}
		_, err := u.cli.SetSetting(setting, value)
		return func() {
			u.saving = false
			if err != nil {
				u.autoError = err.Error()
			}
			u.loadStatus()
		}
	})
}

func (u *Updates) refresh() {
	setContent(u.scroll, padded(16, 16, 16, 16, u.render()))
}

func (u *Updates) render() fyne.CanvasObject {
	version := ""
	if u.status != nil {
		version = u.status.Current
	}
	head := container.NewVBox(heading("Updates", true))
	if version != "" {
		head.Add(secondary(AppName + " " + version))
	}
	box := container.NewVBox(head)
	if u.statusError != "" {
		box.Add(banner("Couldn't read where updates stand", u.statusError))
	}
	box.Add(card(false, u.automaticView()))
	box.Add(card(false, u.installedView(version)))
	box.Add(card(false, u.updatesView()))
	return box
}

func (u *Updates) automaticView() fyne.CanvasObject {
	left := container.NewVBox(widget.NewLabel("Install updates automatically"), secondary(
		"New releases install by themselves once they've been out an hour, when no routine is running and "+
			"the app's window isn't open. A notification tells you when one is installed."))
	switch {
	case u.status != nil && u.status.Automatic && u.status.AutomaticBlocked != "":
		left.Add(secondary("Not on this install: " + u.status.AutomaticBlocked + "."))
	case u.status != nil && u.status.Waiting != "":
		left.Add(secondary(u.status.Waiting + "."))
	}
	if u.autoError != "" {
		left.Add(errorRow(u.autoError))
	}
	toggle := widget.NewCheck("", nil)
	toggle.Checked = u.status != nil && u.status.Automatic
	toggle.OnChanged = u.setAutomatic
	if u.status == nil || u.saving {
		toggle.Disable()
	}
	return padded(6, 6, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, container.NewCenter(toggle), left))
}

func (u *Updates) installedView(version string) fyne.CanvasObject {
	title := "Version " + version
	if version == "" {
		title = "Installed"
	}
	head := container.NewVBox(secondary("INSTALLED"), heading(title, false))
	var link fyne.CanvasObject
	if u.notes != nil {
		link = releaseLink(u.notes.URL)
	}
	notes := notesView(u.notes, u.notesError)
	return padded(6, 6, rowInset-8, rowInset-8, container.NewVBox(container.NewBorder(nil, nil, nil, link, head), notes))
}

func (u *Updates) updatesView() fyne.CanvasObject {
	objects := []fyne.CanvasObject{}
	if u.installing() {
		objects = append(objects, u.installingView()...)
	} else {
		objects = append(objects, u.checkView()...)
		if p := u.progress; u.sawInstalling && p != nil && p.State == "failed" {
			title := "The update failed"
			if p.Tag != "" {
				title = "The update to " + p.Tag + " failed"
			}
			message := p.Error
			if p.Log != "" {
				message += "\nLog: " + p.Log
			}
			objects = append(objects, banner(title, message))
		}
	}
	return padded(6, 6, rowInset-8, rowInset-8, container.NewVBox(objects...))
}

func (u *Updates) checkView() []fyne.CanvasObject {
	title, detail := "Check for a newer release", ""
	switch {
	case u.checking:
		title = "Checking for updates…"
	case u.checkError != "":
		title, detail = "Couldn't check for updates", u.checkError
	case u.checked != nil && u.checked.UpdateAvailable:
		title = "An update is available"
	case u.checked != nil:
		title, detail = AppName+" is up to date", u.checked.Current+" is the latest release."
	case u.status != nil:
		if u.status.UpdateAvailable && u.status.Latest != "" {
			title = u.status.Latest + " is available"
		}
		if u.status.CheckedAt != nil {
			detail = "Last checked " + lowerDay(when(*u.status.CheckedAt, u.now()))
			if u.status.CheckError != "" {
				detail += ", which failed: " + u.status.CheckError
			}
			detail += "."
		}
	}
	left := container.NewVBox(heading(title, false))
	if detail != "" {
		left.Add(secondary(detail))
	}
	check := widget.NewButton("Check for Updates", u.check)
	if u.checking {
		check.SetText("Checking…")
	}
	if u.checking || u.starting {
		check.Disable()
	}
	objects := []fyne.CanvasObject{container.NewBorder(nil, nil, nil, container.NewCenter(check), left)}
	if c := u.checked; c != nil && c.UpdateAvailable {
		install := widget.NewButton("Install Now", u.install)
		install.Importance = widget.HighImportance
		if u.starting {
			install.SetText("Starting…")
			install.Disable()
		}
		var link fyne.CanvasObject = container.NewCenter(install)
		if u.available != nil && u.available.URL != "" {
			link = container.NewHBox(releaseLink(u.available.URL), container.NewCenter(install))
		}
		head := container.NewVBox(secondary("AVAILABLE"), heading("Version "+strings.TrimPrefix(c.Latest, "v"), false))
		objects = append(objects, widget.NewSeparator(), container.NewBorder(nil, nil, nil, link, head),
			notesView(u.available, u.availableErr))
		if c.Note != "" {
			objects = append(objects, secondary(c.Note))
		}
		if u.startError != "" {
			objects = append(objects, banner("The update didn't start", u.startError))
		}
	}
	return objects
}

func (u *Updates) installingView() []fyne.CanvasObject {
	p := u.progress
	tag := ""
	if p != nil {
		tag = p.Tag
	}
	if tag == "" && u.status != nil {
		tag = u.status.InstallingTag
	}
	title := "Updating"
	if tag != "" {
		title += " to " + tag
	}
	head := container.NewVBox(heading(title, false))
	if p != nil && p.StartedAt != nil {
		head.Add(secondary("Started " + lowerDay(when(*p.StartedAt, u.now()))))
	}
	check := widget.NewButton("Check for Updates", nil)
	check.Disable()
	step := "Starting…"
	if p != nil && p.Step != "" {
		step = p.Step
	}
	objects := []fyne.CanvasObject{
		container.NewBorder(nil, nil, container.NewVBox(sized(32, widget.NewIcon(theme.DownloadIcon()))), container.NewCenter(check), head),
		widget.NewProgressBarInfinite(), text(step),
	}
	if p != nil && p.Detail != "" {
		d := widget.NewLabelWithStyle(p.Detail, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		d.Truncation = fyne.TextTruncateEllipsis
		d.SizeName = captionSize
		objects = append(objects, d)
	}
	return append(objects, secondary("You can close this window: the update carries on, and the app restarts when it's done."))
}

// releaseLink is View on GitHub, for a release's page.
func releaseLink(page string) fyne.CanvasObject {
	if page == "" {
		return nil
	}
	link := widget.NewHyperlink("View on GitHub", nil)
	if err := link.SetURLFromString(page); err != nil {
		return nil
	}
	return container.NewCenter(link)
}

// bareLink is a web address on its own (not already a Markdown link).
var bareLink = regexp.MustCompile(`(^|[\s])(https?://[^\s<>()]+[^\s<>().,;:!?])`)

// notesView is a release's notes, scrollable when they're long: Markdown,
// with bare web addresses as links; reading them, or why they couldn't be.
func notesView(n *cli.Notes, err string) fyne.CanvasObject {
	var body fyne.CanvasObject
	switch {
	case err != "":
		body = secondary(err)
	case n == nil:
		body = container.NewHBox(widget.NewActivity(), secondary("Reading the release notes…"))
		if a, ok := body.(*fyne.Container).Objects[0].(*widget.Activity); ok {
			a.Start()
		}
	case strings.TrimSpace(n.Notes) == "":
		body = secondary("This release has no notes.")
	default:
		md := widget.NewRichTextFromMarkdown(bareLink.ReplaceAllString(n.Notes, "$1<$2>"))
		md.Wrapping = fyne.TextWrapWord
		scroll := container.NewVScroll(md)
		scroll.SetMinSize(fyne.NewSize(0, min(md.MinSize().Height, 220)))
		body = scroll
	}
	return card(true, padded(4, 4, 8, 8, body))
}

// lowerDay is when's "Today at 09:20" in the middle of a sentence.
func lowerDay(s string) string {
	for _, day := range []string{"Today", "Yesterday", "Tomorrow"} {
		if strings.HasPrefix(s, day) {
			return strings.ToLower(s[:1]) + s[1:]
		}
	}
	return s
}
