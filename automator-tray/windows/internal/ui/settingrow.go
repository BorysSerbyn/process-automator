package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// Async runs work off the UI's thread, then what it returns on it. Every
// CLI call goes through it: they block until the command exits.
type Async func(work func() (apply func()))

// Background is Async for a running app: a goroutine, then fyne.Do.
func Background(work func() func()) {
	go func() {
		apply := work()
		fyne.Do(apply)
	}()
}

// settingRow is one setting: its label on the left, its control (by type)
// on the right, its description and the core's message under them
// (automator-ui's SettingRow). win is for the path control's folder dialog;
// busy disables it while the core works on a change.
func settingRow(win fyne.Window, s cli.Setting, label, problem string, busy bool, set func(string), reset func()) fyne.CanvasObject {
	name := widget.NewLabel(label)
	tags := container.NewHBox(name)
	if s.NotImplemented {
		tags.Add(tag("Not implemented", theme.ColorNameWarning))
	}
	// Not applying to the backend in use: dimmed, its tag says why.
	if s.Inactive != "" {
		name.Importance = widget.LowImportance
		tags.Add(tag(s.Inactive, ""))
	}
	// Its words take the row's width, under the label and the control.
	words := container.NewVBox()
	if s.Description != "" {
		words.Add(secondary(s.Description))
	}
	// A choice's own line (e.g. the backend's), for the one selected.
	if help := s.EnumHelp[s.ValueText]; help != "" {
		words.Add(secondary(help))
	}
	if problem != "" {
		words.Add(errorRow(problem))
	}

	controls := container.NewHBox()
	if !s.IsDefault {
		r := widget.NewButton("Reset", reset)
		r.Importance = widget.LowImportance
		if busy {
			r.Disable()
		}
		controls.Add(r)
	}
	for _, c := range control(win, s, label, busy || s.NotImplemented || s.Inactive != "", set) {
		controls.Add(c)
	}
	if s.Unit != "" {
		u := widget.NewLabel(s.Unit)
		u.Importance = widget.LowImportance
		controls.Add(u)
	}
	return padded(4, 4, rowInset-8, rowInset-8, container.NewVBox(
		container.NewBorder(nil, nil, nil, controls, container.NewVBox(tags)),
		words))
}

// control is a setting's control by its type: a toggle, a picker, a field
// (a path's with Choose…).
func control(win fyne.Window, s cli.Setting, label string, disabled bool, set func(string)) []fyne.CanvasObject {
	off := func(d fyne.Disableable) {
		if disabled {
			d.Disable()
		}
	}
	switch s.Type {
	case "bool":
		c := widget.NewCheck("", nil)
		c.Checked = s.ValueText == "true"
		c.OnChanged = func(on bool) {
			if on {
				set("true")
			} else {
				set("false")
			}
		}
		off(c)
		return []fyne.CanvasObject{c}
	case "enum":
		sel, sized := picker(s.Enum, s.ValueText, set)
		sel.PlaceHolder = label
		off(sel)
		return []fyne.CanvasObject{sized}
	}
	field := newCommitEntry(s.ValueText, set)
	field.PlaceHolder = s.DefaultText
	if field.PlaceHolder == "" {
		field.PlaceHolder = "unset"
	}
	width := float32(200)
	switch s.Type {
	case "int", "number", "percent", "duration":
		width = 72
	case "cron":
		width = 140
	}
	off(field)
	objects := []fyne.CanvasObject{fixedWidth(width, field)}
	if s.Type == "path" {
		choose := widget.NewButton("Choose…", func() {
			dialog.ShowFolderOpen(func(dir fyne.ListableURI, err error) {
				if err == nil && dir != nil {
					set(dir.Path())
				}
			}, win)
		})
		off(choose)
		objects = append(objects, choose)
	}
	return objects
}
