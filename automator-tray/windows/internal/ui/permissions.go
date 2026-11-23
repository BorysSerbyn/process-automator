package ui

import (
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// AppName is the app's name, in what to switch on for a permission (the
// windows app sets it from the package).
var AppName = ""

// PermissionPollEvery is how often a review, or a window, showing a
// permission that isn't allowed yet reads it again, so it turns allowed by
// itself (0: never; tests).
var PermissionPollEvery = 3 * time.Second

// granter is Grant… for a permission, wherever it shows: asks through the
// scheduler (`permissions request`) and says what to do; while it's still
// not allowed and the OS didn't ask, it opens the OS's page for it. Why it failed
// shows where the button is. It mirrors automator-ui's PermissionGranter.
type granter struct {
	cli   cli.CLI
	async Async
	// redraw draws the window again; asked reads what needs it again.
	redraw func()
	asked  func()
	// openURL opens the OS's settings page.
	openURL func(string) error

	granting string
	notes    map[string]string
	errors   map[string]string
}

func newGranter(c cli.CLI, async Async, redraw, asked func()) *granter {
	return &granter{cli: c, async: async, redraw: redraw, asked: asked, openURL: openURL,
		notes: map[string]string{}, errors: map[string]string{}}
}

// openURL opens an address with the desktop, through Fyne.
func openURL(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return err
	}
	if app := fyne.CurrentApp(); app != nil {
		return app.OpenURL(u)
	}
	return nil
}

func (g *granter) grant(permission string) {
	g.granting = permission
	delete(g.errors, permission)
	g.redraw()
	g.async(func() func() {
		r, err := g.cli.RequestPermission(permission)
		return func() {
			g.granting = ""
			if err != nil {
				g.errors[permission] = err.Error()
			} else {
				g.notes[permission] = r.Instructions(AppName)
				// Only when the OS didn't ask (refused before, or it can't be
				// asked, as on Linux): over its prompt, the settings page
				// would hide it. While the prompt is up the note says to
				// answer it, and the row turns allowed by itself.
				if r.State != "granted" && !r.Asked && r.SettingsURL != "" {
					if err := g.openURL(r.SettingsURL); err != nil {
						g.errors[permission] = err.Error()
					}
				}
			}
			g.redraw()
			g.asked()
		}
	})
}

// rows are the permissions given, one row each: its plain name, why it's
// needed, its state, and Grant… while it isn't allowed; after asking, what
// to switch on and where.
func (g *granter) rows(list []cli.PermissionNeed) []fyne.CanvasObject {
	var out []fyne.CanvasObject
	for _, p := range list {
		icon := widget.NewIcon(theme.NewSuccessThemedResource(theme.ConfirmIcon()))
		if p.Blocking() {
			icon = widget.NewIcon(theme.NewWarningThemedResource(theme.WarningIcon()))
		}
		about := p.StateText()
		if r := p.Reason(); r != "" {
			about = r + " · " + about
		}
		name := widget.NewLabelWithStyle(p.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: p.Blocking()})
		var action fyne.CanvasObject = container.NewHBox()
		if p.Blocking() {
			if g.granting == p.Permission {
				action = spinner()
			} else {
				perm := p.Permission
				b := widget.NewButton("Grant…", func() { g.grant(perm) })
				if g.granting != "" {
					b.Disable()
				}
				action = container.NewCenter(b)
			}
		}
		row := container.NewVBox(container.NewBorder(nil, nil, container.NewCenter(icon), action,
			container.NewVBox(name, padded(-10, 0, 0, 0, quiet(about)))))
		if note := g.notes[p.Permission]; note != "" && p.Blocking() {
			row.Add(text(note))
		}
		if e := g.errors[p.Permission]; e != "" {
			row.Add(errorRow(e))
		}
		out = append(out, padded(2, 2, rowInset-8, rowInset-8, row))
	}
	return out
}

// banner is a window's permissions banner while one it needs isn't
// allowed: it won't run until it is, with Grant… for each (nil: none).
func (g *granter) banner(name string, list []cli.PermissionNeed) fyne.CanvasObject {
	blocking := cli.BlockingPermissions(list)
	if len(blocking) == 0 {
		return nil
	}
	title := name + " needs a permission"
	if len(blocking) > 1 {
		title = name + " needs permissions"
	}
	objects := []fyne.CanvasObject{cardTitle(title), padded(0, 0, rowInset-8, rowInset-8, secondary(cli.WontRunText(list)))}
	return card(false, append(objects, g.rows(blocking)...)...)
}

// card is a window's permissions card: each one it needs with its state
// (allowed ones too), and Grant… for one that isn't (nil: none shown).
func (g *granter) card(list []cli.PermissionNeed) fyne.CanvasObject {
	var shown []cli.PermissionNeed
	for _, p := range list {
		if p.Shown() {
			shown = append(shown, p)
		}
	}
	if len(shown) == 0 {
		return nil
	}
	objects := []fyne.CanvasObject{cardTitle("Permissions"), divider()}
	objects = append(objects, g.rows(shown)...)
	return card(false, append(objects, padded(0, 6, 0, 0, container.NewHBox()))...)
}

// pollPermissions reads something again every PermissionPollEvery while
// waiting says it waits for a permission (and stop doesn't): load reads it.
func pollPermissions(async Async, waiting func() bool, load func()) {
	if PermissionPollEvery <= 0 {
		return
	}
	async(func() func() {
		time.Sleep(PermissionPollEvery)
		return func() {
			if waiting() {
				load()
				pollPermissions(async, waiting, load)
			}
		}
	})
}
