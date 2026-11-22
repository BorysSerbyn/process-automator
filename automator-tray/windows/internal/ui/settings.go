package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// UsageEvery is how often the Settings window reads usage again while it
// shows (the core reads Claude Code's again once its reading is 10
// minutes old).
const UsageEvery = 15 * time.Minute

// Settings is the Settings window's content: whether the app starts at
// login (`login-items`), LLM usage (`usage`), and the app-wide settings as
// the core lists them (`settings list --scope app`). Each setting is shown
// by its type and sent back as text (`settings set`); the core validates,
// and its message shows when it refuses a value. It mirrors automator-ui's
// SettingsModel and SettingsView.
type Settings struct {
	cli   cli.CLI
	win   fyne.Window
	async Async
	// appName is the app's name, as its package names it.
	appName string
	now     func() time.Time

	settings   []cli.Setting
	errors     map[string]string
	loadError  string
	loginItems *cli.LoginItems
	loginError string
	usage      *cli.Report
	usageError string
	// expanded are the groups open (their key tree ids): top-level groups
	// start open, subgroups closed.
	expanded       map[string]bool
	openedDefaults bool
	// Loads overlap when changes come quickly; only the latest one counts.
	generation, usageGeneration int

	scroll *container.Scroll
}

// NewSettings makes the Settings window's content; Load reads it.
func NewSettings(c cli.CLI, win fyne.Window, async Async, appName string) *Settings {
	s := &Settings{cli: c, win: win, async: async, appName: appName, now: time.Now,
		errors: map[string]string{}, expanded: map[string]bool{}}
	s.scroll = container.NewVScroll(container.NewVBox())
	s.refresh()
	return s
}

// Content is what the window shows.
func (s *Settings) Content() fyne.CanvasObject { return s.scroll }

// Load reads the settings and whether the app starts at login, then usage
// on its own: on a Claude plan the core may read it from Claude Code first
// (a few seconds), which mustn't hold up the settings.
func (s *Settings) Load() {
	s.generation++
	mine := s.generation
	s.async(func() func() {
		type answer struct {
			list  []cli.Setting
			err   error
			items cli.LoginItems
			iErr  error
		}
		var a answer
		done := make(chan struct{})
		go func() {
			a.items, a.iErr = s.cli.LoginItems()
			close(done)
		}()
		a.list, a.err = s.cli.AppSettings()
		<-done
		return func() {
			if mine != s.generation {
				return
			}
			if a.err != nil {
				s.loadError = "Couldn't read the settings: " + a.err.Error()
			} else {
				s.settings, s.loadError = a.list, ""
				s.openDefaults()
			}
			s.loginItems = nil
			if a.iErr == nil {
				s.loginItems = &a.items
			}
			s.refresh()
		}
	})
	s.LoadUsage()
}

// LoadUsage reads LLM usage; only the latest answer counts.
func (s *Settings) LoadUsage() {
	s.usageGeneration++
	mine := s.usageGeneration
	s.async(func() func() {
		r, err := s.cli.Usage()
		return func() {
			if mine != s.usageGeneration {
				return
			}
			if err != nil {
				s.usageError = err.Error()
			} else {
				s.usage, s.usageError = &r, ""
			}
			s.refresh()
		}
	})
}

// openDefaults opens the top-level groups the first time there are
// settings, unless something was opened already.
func (s *Settings) openDefaults() {
	if s.openedDefaults || len(s.settings) == 0 {
		return
	}
	s.openedDefaults = true
	if len(s.expanded) == 0 {
		for _, g := range keyTree(s.settings) {
			s.expanded[g.id] = true
		}
	}
}

func (s *Settings) toggle(id string) {
	s.expanded[id] = !s.expanded[id]
	s.refresh()
}

func (s *Settings) set(key, value string) {
	s.apply(key, func() error {
		_, err := s.cli.SetSetting(key, value)
		return err
	})
}

func (s *Settings) reset(key string) {
	s.apply(key, func() error {
		_, err := s.cli.ResetSetting(key)
		return err
	})
}

func (s *Settings) apply(key string, change func() error) {
	s.async(func() func() {
		err := change()
		return func() {
			if err != nil {
				s.errors[key] = err.Error()
			} else {
				delete(s.errors, key)
			}
			s.Load()
		}
	})
}

func (s *Settings) setStartAtLogin(on bool) {
	s.async(func() func() {
		items, err := s.cli.SetStartAtLogin(on)
		return func() {
			if err != nil {
				s.loginError = err.Error()
			} else {
				s.loginItems, s.loginError = &items, ""
			}
			s.refresh()
		}
	})
}

// The view

// refresh draws the window again, keeping its scroll position.
func (s *Settings) refresh() {
	setContent(s.scroll, padded(16, 16, 16, 16, s.render()))
}

// render: Start at login, LLM usage, then one card per top-level group,
// each a section that opens and closes, with its subgroups nested inside.
func (s *Settings) render() fyne.CanvasObject {
	if s.loadError != "" {
		return container.NewVBox(banner("No settings", s.loadError))
	}
	box := container.NewVBox(card(false, s.startAtLogin()), card(false, usageView(s.usage, s.usageError, s.now())...))
	for _, g := range keyTree(s.settings) {
		box.Add(s.groupView(g))
	}
	return box
}

func (s *Settings) startAtLogin() fyne.CanvasObject {
	installed := s.loginItems != nil && s.loginItems.Installed
	about := "Available once the app is installed."
	if installed {
		about = s.appName + " starts when you log in."
	}
	left := container.NewVBox(widget.NewLabel("Start at login"), secondary(about))
	if s.loginError != "" {
		left.Add(errorRow(s.loginError))
	}
	toggle := widget.NewCheck("", nil)
	toggle.Checked = s.loginItems != nil && s.loginItems.Enabled
	toggle.OnChanged = s.setStartAtLogin
	if !installed {
		toggle.Disable()
	}
	return padded(6, 6, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, container.NewCenter(toggle), left))
}

// groupView is a top-level group (llm, update, …) as a card: its header,
// then, when open, its settings and its subgroups (llm › budget) as nested
// boxes.
func (s *Settings) groupView(g *group) fyne.CanvasObject {
	objects := []fyne.CanvasObject{s.groupHeader(g, true)}
	if s.expanded[g.id] {
		objects = append(objects, s.rows(g.entries)...)
		for _, sub := range g.groups {
			inner := []fyne.CanvasObject{s.groupHeader(sub, false)}
			if s.expanded[sub.id] {
				inner = append(inner, s.rows(sub.entries)...)
			}
			objects = append(objects, padded(2, 10, 10, 10, card(true, inner...)))
		}
	}
	return card(false, objects...)
}

// groupHeader is a group's name, with how many settings it holds; the
// whole row opens and closes it.
func (s *Settings) groupHeader(g *group, prominent bool) fyne.CanvasObject {
	n := g.count()
	count := widget.NewLabel(fmt.Sprintf("%d settings", n))
	if n == 1 {
		count.SetText("1 setting")
	}
	count.Importance = widget.LowImportance
	return newDisclosure(s.expanded[g.id], prominent, func() { s.toggle(g.id) },
		container.NewBorder(nil, nil, nil, count, heading(g.name, prominent)))
}

// rows are a group's settings, one row each, with a hairline above every row.
func (s *Settings) rows(entries []entry) []fyne.CanvasObject {
	var objects []fyne.CanvasObject
	for _, e := range entries {
		key := e.setting.Key
		objects = append(objects, divider(), settingRow(s.win, e.setting, e.label, s.errors[key], false,
			func(v string) { s.set(key, v) }, func() { s.reset(key) }))
	}
	return objects
}
