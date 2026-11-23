// Package tray is the tray's behaviour, as automator-ui's indicator,
// notifier and updates behave: what the icon and menu show for a run
// status, delivering the app's notifications, and updates. It shows what the
// core says and runs what the core offers, through its CLI only; the desktop
// (the tray icon, notifications, windows) is behind interfaces.
package tray

import (
	"time"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Light is what the status dot shows.
type Light string

// The lights: the core's health as is (ok, error, none), and red when it
// isn't responding.
const (
	Green Light = "green"
	Red   Light = "red"
	Grey  Light = "grey"
)

// Responding is whether the core is responding: its status is readable and
// not past validUntil. The tray's only judgement; everything else it shows
// is the core's (health, reasons).
func Responding(s *cli.RunStatus, now time.Time) bool {
	return s != nil && s.ValidUntil != nil && !now.After(*s.ValidUntil)
}

// LightFor is the dot for a status: the core's verdict as is, and red when
// there's no readable status or it's past its validUntil.
func LightFor(s *cli.RunStatus, now time.Time) Light {
	if !Responding(s, now) {
		return Red
	}
	switch s.Health {
	case "ok":
		return Green
	case "none":
		return Grey
	}
	return Red
}

// Pulsing is whether the dot pulses: the core says a routine is running,
// and its status is still valid (an expired status shows red, not activity).
func Pulsing(s *cli.RunStatus, now time.Time) bool {
	return Responding(s, now) && s.Busy
}

// Frame is one tray icon: the glyph with its dot in a light, dimmed for the
// second half of a pulse.
type Frame struct {
	Light Light
	Dim   bool
}

// File is the frame's icon in the tray's icons folder.
func (f Frame) File() string {
	if f.Dim {
		return "status-" + string(f.Light) + "-dim.png"
	}
	return "status-" + string(f.Light) + ".png"
}

// Frames are every icon the tray shows, made when the app is packaged.
func Frames() []Frame {
	var out []Frame
	for _, l := range []Light{Green, Red, Grey} {
		out = append(out, Frame{Light: l}, Frame{Light: l, Dim: true})
	}
	return out
}

// PulseInterval is how long each half of a pulse lasts (automator-ui fades
// the dot over 0.8s and back).
var PulseInterval = 800 * time.Millisecond

// FrameFor is the icon to show: pulse is which half of a pulse it is in
// (only used while pulsing).
func FrameFor(s *cli.RunStatus, now time.Time, pulse bool) Frame {
	return Frame{Light: LightFor(s, now), Dim: pulse && Pulsing(s, now)}
}

// ActionKind is what choosing a menu item does.
type ActionKind string

// What menu items do.
const (
	None ActionKind = ""
	// OpenRun opens a run's report (`runs show <id> --open`).
	OpenRun ActionKind = "open-run"
	// ShowWindow opens one of the app's windows (through the window seam).
	ShowWindow ActionKind = "show-window"
	// CheckUpdates, InstallUpdate and ShowUpdate are the update item's.
	CheckUpdates  ActionKind = "check-updates"
	InstallUpdate ActionKind = "install-update"
	ShowUpdate    ActionKind = "show-update"
	Quit          ActionKind = "quit"
)

// Action is a menu item's action and what it's about.
type Action struct {
	Kind   ActionKind
	Run    string
	Window Window
}

// MenuItem is one row of the menu. A row with no action is shown disabled
// (text); a separator has neither text nor action.
type MenuItem struct {
	Text      string
	Separator bool
	// Health is a routine's own health (ok, error, none), shown as a dot.
	Health  string
	Action  Action
	Submenu []MenuItem
}

func separator() MenuItem { return MenuItem{Separator: true} }

// MenuText is the longest a row's text gets (automator-ui wraps at the
// menu's width, then ends in an ellipsis; a tray menu can't wrap).
const MenuText = 80

// Menu is the tray's menu for a status, as automator-ui's: the version, the
// status in the core's words, Finish Setup… while setup isn't done,
// Routines › (each routine with its health and summary), Hooks › (each
// hook, while there are any), the update item, Settings, Quit.
func Menu(name, version string, s *cli.RunStatus, now time.Time, update MenuItem) []MenuItem {
	items := []MenuItem{{Text: name + " " + version}}
	items = append(items, statusItems(s, now)...)
	if Responding(s, now) && len(s.SetupLeft) > 0 {
		items = append(items, MenuItem{Text: "Finish Setup…", Action: Action{Kind: ShowWindow, Window: Window{Kind: SetupWindow}}})
	}
	items = append(items, separator(), routinesItem(s))
	if s != nil && len(s.Hooks) > 0 {
		items = append(items, hooksItem(s))
	}
	return append(items,
		update,
		MenuItem{Text: "Settings", Action: Action{Kind: ShowWindow, Window: Window{Kind: SettingsWindow}}},
		separator(),
		MenuItem{Text: "Quit", Action: Action{Kind: Quit}},
	)
}

// statusItems are what the status is, in the core's words: why it's red
// (each reason opens the run it's about; one about a routine or hook with
// no run, its window on its Turn On review; one about a watcher, its
// window), or its summary.
func statusItems(s *cli.RunStatus, now time.Time) []MenuItem {
	if !Responding(s, now) {
		return []MenuItem{{Text: "Not responding"}}
	}
	if len(s.Reasons) == 0 {
		return []MenuItem{{Text: shorten(s.Summary)}}
	}
	var items []MenuItem
	for _, r := range s.Reasons {
		item := MenuItem{Text: shorten(r.Text)}
		switch {
		case r.Acknowledge:
			// "(2) Acknowledgements pending": the window to acknowledge them.
			item.Action = Action{Kind: ShowWindow, Window: Window{Kind: AcknowledgementsWindow}}
		case r.Watcher != "":
			item.Action = Action{Kind: ShowWindow, Window: Window{Kind: WatcherWindow, Routine: r.Watcher}}
		case r.Run != "":
			item.Action = Action{Kind: OpenRun, Run: r.Run}
		case r.Hook && r.Routine != "":
			item.Action = Action{Kind: ShowWindow, Window: Window{Kind: HookWindow, Routine: r.Routine, Review: true}}
		case r.Routine != "":
			// Nothing ran (it isn't trusted, say): its window, on its review.
			item.Action = Action{Kind: ShowWindow, Window: Window{Kind: RoutineWindow, Routine: r.Routine, Review: true}}
		}
		items = append(items, item)
	}
	return items
}

// routinesItem is Routines ›: All Routines…, then every routine with its
// health and summary as the core gives them; choosing one opens it.
func routinesItem(s *cli.RunStatus) MenuItem {
	sub := []MenuItem{
		{Text: "All Routines…", Action: Action{Kind: ShowWindow, Window: Window{Kind: RoutinesWindow}}},
		separator(),
	}
	switch {
	case s == nil:
		sub = append(sub, MenuItem{Text: "Not responding"})
	case len(s.Routines) == 0:
		sub = append(sub, MenuItem{Text: "No routines"})
	}
	if s != nil {
		for _, r := range s.Routines {
			text := r.Name
			if r.Summary != "" {
				text += " — " + r.Summary
			}
			sub = append(sub, MenuItem{
				Text: shorten(text), Health: healthOrNone(r.Health),
				Action: Action{Kind: ShowWindow, Window: Window{Kind: RoutineWindow, Routine: r.Name, Review: true}},
			})
		}
	}
	return MenuItem{Text: "Routines", Submenu: sub}
}

// hooksItem is Hooks ›: All Hooks…, then every hook with its health and
// summary as the core gives them; choosing one opens its window. Hooks
// aren't routines: they never show under Routines ›.
func hooksItem(s *cli.RunStatus) MenuItem {
	sub := []MenuItem{
		{Text: "All Hooks…", Action: Action{Kind: ShowWindow, Window: Window{Kind: HooksWindow}}},
		separator(),
	}
	for _, h := range s.Hooks {
		text := h.Name
		if h.Summary != "" {
			text += " — " + h.Summary
		}
		sub = append(sub, MenuItem{
			Text: shorten(text), Health: healthOrNone(h.Health),
			Action: Action{Kind: ShowWindow, Window: Window{Kind: HookWindow, Routine: h.Name, Review: true}},
		})
	}
	return MenuItem{Text: "Hooks", Submenu: sub}
}

// healthOrNone is a routine's dot: ok green, error red, anything else grey.
func healthOrNone(h string) string {
	if h == "ok" || h == "error" {
		return h
	}
	return "none"
}

func shorten(s string) string {
	r := []rune(s)
	if len(r) <= MenuText {
		return s
	}
	return string(r[:MenuText-1]) + "…"
}

// ReopenWindow is the window opening the app shows while it runs: Setup
// while the core's status lists setup steps still to do, else Settings
// (automator-ui's rule too). An unreadable status lists none.
func ReopenWindow(s *cli.RunStatus) Window {
	if s != nil && len(s.SetupLeft) > 0 {
		return Window{Kind: SetupWindow}
	}
	return Window{Kind: SettingsWindow}
}
