package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// TemplateUpdates is the Template Updates screen: what I installed from a
// template that changed since (`templates outdated`), offered when the tray
// starts after an update. A row each, unticked: its name and kind, its
// installed and new versions, what changed, whether I edited my copy (which
// overriding replaces), the watcher overridden with a hook, and why one
// can't be overridden (its box disabled). Select all ticks every one that
// can be; Override replaces the ticked ones (`templates override`, minutes
// when one builds from source), then leaves the others as they are
// (`templates skip`), and says what it replaced and whether each stayed
// trusted; Not now leaves them all as they are and closes the screen, as
// closing the window without choosing does (Closed). It mirrors
// automator-ui's Template Updates.
type TemplateUpdates struct {
	cli   cli.CLI
	async Async
	// done closes the screen.
	done func()

	updates   []cli.TemplateUpdate
	loaded    bool
	loadError string
	ticked    map[string]bool
	// working is what's running: "override" or "skip" ("": nothing).
	working string
	// err is why Override or Not now failed, errTitle which.
	err, errTitle string
	// result is what Override replaced, once it has; skipError why leaving
	// the others as they are failed then.
	result    *cli.OverrideOutput
	skipError string
	// chosen: Override or Not now was chosen and done, so closing the window
	// leaves nothing to skip.
	chosen bool

	scroll *container.Scroll
}

// NewTemplateUpdates makes the screen; Load reads it, done closes it.
func NewTemplateUpdates(c cli.CLI, async Async, done func()) *TemplateUpdates {
	u := &TemplateUpdates{cli: c, async: async, done: done, ticked: map[string]bool{}}
	u.scroll = container.NewVScroll(container.NewVBox())
	u.refresh()
	return u
}

// Content is what the screen shows.
func (u *TemplateUpdates) Content() fyne.CanvasObject { return u.scroll }

// Load reads what changed; ticks stay on the rows still listed.
func (u *TemplateUpdates) Load() {
	u.async(func() func() {
		list, err := u.cli.TemplateUpdates()
		return func() {
			if err != nil {
				u.loadError = err.Error()
			} else {
				u.updates, u.loadError = list, ""
				for name := range u.ticked {
					if u.find(name) == nil {
						delete(u.ticked, name)
					}
				}
			}
			u.loaded = true
			u.refresh()
		}
	})
}

func (u *TemplateUpdates) find(name string) *cli.TemplateUpdate {
	for i := range u.updates {
		if u.updates[i].Name == name {
			return &u.updates[i]
		}
	}
	return nil
}

// Ticked are the rows ticked, in the list's order.
func (u *TemplateUpdates) Ticked() []string {
	var out []string
	for _, up := range u.updates {
		if u.ticked[up.Name] && up.Blocked == "" {
			out = append(out, up.Name)
		}
	}
	return out
}

// unticked are the rows Override leaves as they are: not ticked, not
// blocked, and not a watcher overridden with a hook ticked.
func (u *TemplateUpdates) unticked() []string {
	with := map[string]bool{}
	for _, up := range u.updates {
		if u.ticked[up.Name] {
			for _, w := range up.With {
				with[w] = true
			}
		}
	}
	var out []string
	for _, up := range u.updates {
		if !u.ticked[up.Name] && up.Blocked == "" && !with[up.Name] {
			out = append(out, up.Name)
		}
	}
	return out
}

// names are every row listed.
func (u *TemplateUpdates) names() []string {
	out := make([]string, 0, len(u.updates))
	for _, up := range u.updates {
		out = append(out, up.Name)
	}
	return out
}

// SelectAll ticks every row that can be overridden.
func (u *TemplateUpdates) SelectAll() {
	for _, up := range u.updates {
		if up.Blocked == "" {
			u.ticked[up.Name] = true
		}
	}
	u.refresh()
}

// Override replaces the ticked ones, then leaves the others as they are.
// On an error, it says so and reads what's left.
func (u *TemplateUpdates) Override() {
	names, others := u.Ticked(), u.unticked()
	if len(names) == 0 || u.working != "" {
		return
	}
	u.working, u.err = "override", ""
	u.refresh()
	u.async(func() func() {
		out, err := u.cli.OverrideTemplates(names...)
		var skipErr error
		if err == nil && len(others) > 0 {
			skipErr = u.cli.SkipTemplateUpdates(others...)
		}
		return func() {
			u.working = ""
			if err != nil {
				u.err, u.errTitle = err.Error(), "Couldn't override them"
				u.Load()
				return
			}
			u.result, u.chosen = &out, true
			if skipErr != nil {
				u.skipError = skipErr.Error()
			}
			u.refresh()
		}
	})
}

// NotNow leaves every one listed as it is, then closes the screen.
func (u *TemplateUpdates) NotNow() {
	names := u.names()
	if u.working != "" {
		return
	}
	if len(names) == 0 {
		u.chosen = true
		u.done()
		return
	}
	u.working, u.err = "skip", ""
	u.refresh()
	u.async(func() func() {
		err := u.cli.SkipTemplateUpdates(names...)
		return func() {
			u.working = ""
			if err != nil {
				u.err, u.errTitle = err.Error(), "Couldn't leave them as they are"
				u.refresh()
				return
			}
			u.chosen = true
			u.done()
		}
	})
}

// Closed is the window closing: without a choice made, it's Not now (the
// ones listed are left as they are), at once, as the process ends after.
// While Override runs, it's left to finish.
func (u *TemplateUpdates) Closed() {
	if u.chosen || u.working != "" || len(u.updates) == 0 {
		return
	}
	u.chosen = true
	if err := u.cli.SkipTemplateUpdates(u.names()...); err != nil {
		fyne.LogError("leaving the template updates as they are", err)
	}
}

func (u *TemplateUpdates) refresh() {
	content := padded(16, 16, 16, 16, u.render())
	setContent(u.scroll, content)
	content.Refresh()
	u.scroll.Refresh()
}

func (u *TemplateUpdates) render() fyne.CanvasObject {
	header := lines(pageTitle("Template Updates"),
		secondary("Templates you installed from have changed. Tick the ones to replace with the new version: "+
			"the others stay as they are, and these changes aren't offered again."))
	box := container.NewVBox(header)
	if u.err != "" {
		box.Add(banner(u.errTitle, u.err))
	}
	switch {
	case u.result != nil:
		box.Add(u.overridden())
	case u.loadError != "":
		box.Add(banner("Couldn't read the template updates", u.loadError))
	case !u.loaded:
		box.Add(spinner())
	case len(u.updates) == 0:
		box.Add(card(false, padded(20, 20, rowInset, rowInset,
			container.NewCenter(secondary("Everything you installed from a template is up to date with it.")))))
		closeButton := widget.NewButton("Close", u.done)
		box.Add(container.NewHBox(layout.NewSpacer(), closeButton))
	default:
		rows := []fyne.CanvasObject{}
		for i, up := range u.updates {
			if i > 0 {
				rows = append(rows, divider())
			}
			rows = append(rows, u.row(up))
		}
		box.Add(card(false, rows...))
		box.Add(u.buttons())
	}
	return box
}

// row is one update: its box, name and kind; versions; what changed; and
// what to know before overriding it.
func (u *TemplateUpdates) row(up cli.TemplateUpdate) fyne.CanvasObject {
	name := up.Name
	check := widget.NewCheck("", func(on bool) {
		if on {
			u.ticked[name] = true
		} else {
			delete(u.ticked, name)
		}
		u.refresh()
	})
	check.Checked = u.ticked[name]
	if up.Blocked != "" || u.working != "" {
		check.Disable()
	}
	title := nameAndKind(up.Name, up.Kind)
	about := lines(title)
	if v := versions(up); v != "" {
		about.Add(secondary(v))
	}
	if up.Changes != "" {
		about.Add(text(up.Changes))
	}
	switch up.Edited {
	case "yes":
		l := text("You edited your copy: overriding it replaces your edits.")
		l.Importance = widget.WarningImportance
		about.Add(l)
	case "unknown":
		about.Add(secondary("Installed before versions were recorded: it may have been edited, and overriding it replaces any edits."))
	}
	if len(up.With) > 0 {
		word := "watcher"
		if len(up.With) > 1 {
			word = "watchers"
		}
		about.Add(secondary("Also updates its " + word + " " + strings.Join(up.With, ", ") + "."))
	}
	if up.Blocked != "" {
		about.Add(errorRow("Can't be overridden: " + up.Blocked))
	}
	return padded(6, 6, rowInset-4, rowInset, container.NewBorder(nil, nil, container.NewVBox(check), nil, about))
}

// nameAndKind is a row's title: its name, bold, and its kind as a tag.
func nameAndKind(name, kind string) fyne.CanvasObject {
	l := heading(name, false)
	l.Wrapping = fyne.TextWrapOff
	return container.NewHBox(l, tag(kind, ""))
}

// versions is "installed → new" ("" when neither has one).
func versions(up cli.TemplateUpdate) string {
	if up.InstalledVersion == "" && up.Version == "" {
		return ""
	}
	or := func(v string) string {
		if v == "" {
			return "no version"
		}
		return v
	}
	return or(up.InstalledVersion) + " → " + or(up.Version)
}

// buttons are Select all at the left; Not now and Override at the right,
// or what's running.
func (u *TemplateUpdates) buttons() fyne.CanvasObject {
	all := widget.NewButton("Select all", u.SelectAll)
	notNow := widget.NewButton("Not now", u.NotNow)
	override := widget.NewButton("Override", u.Override)
	override.Importance = widget.HighImportance
	selectable := false
	for _, up := range u.updates {
		if up.Blocked == "" && !u.ticked[up.Name] {
			selectable = true
		}
	}
	if !selectable || u.working != "" {
		all.Disable()
	}
	if u.working != "" {
		notNow.Disable()
	}
	if len(u.Ticked()) == 0 || u.working != "" {
		override.Disable()
	}
	right := container.NewHBox(notNow, override)
	bar := container.NewBorder(nil, nil, all, right)
	switch u.working {
	case "override":
		return container.NewVBox(bar, container.NewHBox(spinnerInline(),
			secondaryLine("Overriding… This can take a few minutes when one is built from source.")))
	case "skip":
		return container.NewVBox(bar, container.NewHBox(spinnerInline(), secondaryLine("Leaving them as they are…")))
	}
	return bar
}

// spinnerInline is a spinner beside a line of text.
func spinnerInline() fyne.CanvasObject {
	a := widget.NewActivity()
	a.Start()
	return a
}

// overridden is what Override replaced, each saying whether it stayed trusted.
func (u *TemplateUpdates) overridden() fyne.CanvasObject {
	rows := []fyne.CanvasObject{}
	for i, o := range u.result.Overridden {
		if i > 0 {
			rows = append(rows, divider())
		}
		icon := widget.NewIcon(theme.NewSuccessThemedResource(theme.ConfirmIcon()))
		title := nameAndKind(o.Name, o.Kind)
		about := lines(title)
		line := "Overridden"
		if o.Version != "" {
			line += " with version " + o.Version
		}
		if o.Trusted {
			about.Add(secondary(line + ", still trusted."))
		} else {
			l := text(line + ", not trusted: turn it on from its screen when you're ready.")
			l.Importance = widget.WarningImportance
			about.Add(l)
		}
		rows = append(rows, padded(6, 6, rowInset, rowInset,
			container.NewBorder(nil, nil, leadingIcon(icon), nil, about)))
	}
	box := container.NewVBox()
	if len(rows) > 0 {
		box.Add(card(false, rows...))
	} else {
		box.Add(secondary("Nothing was overridden."))
	}
	if u.skipError != "" {
		box.Add(banner("Couldn't leave the others as they are", u.skipError+" They'll be offered again."))
	}
	done := widget.NewButton("Done", u.done)
	done.Importance = widget.HighImportance
	box.Add(container.NewHBox(layout.NewSpacer(), done))
	return box
}
