package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Icons Fyne's theme doesn't have, as automator-ui's SF Symbols have them
// (Material icons' shapes, 24×24): themed, so they take the theme's
// foreground colour, or the one they're given.

func svgIcon(name, path string) fyne.Resource {
	return theme.NewThemedResource(svg(name, path))
}

func svg(name, path string) fyne.Resource {
	return fyne.NewStaticResource(name+".svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000000" d="`+path+`"/></svg>`))
}

var (
	// ClockIcon is a clock: Routines.
	ClockIcon = svgIcon("clock", "M11.99 2C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2zM12 20c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8zm.5-13H11v6l5.25 3.15.75-1.23-4.5-2.67z")
	// BoltIcon is a bolt: Hooks, run when something happens.
	BoltIcon = svgIcon("bolt", "M7 2v11h3v9l7-12h-4l4-8z")
	// AcknowledgeIcon is a speech bubble with a check: Acknowledgements.
	AcknowledgeIcon = svgIcon("acknowledge", "M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zm0 14H5.17L4 17.17V4h16v12zM10.5 13.5l-3-3 1.41-1.41 1.59 1.58 4.59-4.58L16.5 7.5z")
	// ChecklistIcon is a checklist: Setup.
	ChecklistIcon = svgIcon("checklist", "M22 7h-9v2h9V7zm0 8h-9v2h9v-2zM5.54 11L2 7.46l1.41-1.41 2.12 2.12 4.24-4.24 1.41 1.41L5.54 11zm0 8L2 15.46l1.41-1.41 2.12 2.12 4.24-4.24 1.41 1.41L5.54 19z")
	// DownloadCircleIcon is an arrow down in a circle: Updates.
	DownloadCircleIcon = svgIcon("download-circle", "M12 4c4.41 0 8 3.59 8 8s-3.59 8-8 8-8-3.59-8-8 3.59-8 8-8m0-2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 10V8h-2v4H8l4 4 4-4h-3z")
	// ChevronLeftIcon is Back's chevron.
	ChevronLeftIcon = svgIcon("chevron-left", "M14.71 6.71a1 1 0 0 0-1.41 0L8.71 11.3a1 1 0 0 0 0 1.41l4.59 4.59a1 1 0 1 0 1.41-1.41L10.83 12l3.88-3.88a1 1 0 0 0 0-1.41z")
	// chevronRight is a row's chevron: it opens something, or opens under it.
	chevronRight = svg("chevron-right", "M9.29 6.71a1 1 0 0 0 0 1.41L13.17 12l-3.88 3.88a1 1 0 1 0 1.41 1.41l4.59-4.59a1 1 0 0 0 0-1.41L10.7 6.7a1 1 0 0 0-1.41.01z")
	// chevronDown is an open row's chevron.
	chevronDown = svg("chevron-down", "M6.71 9.29a1 1 0 0 1 1.41 0L12 13.17l3.88-3.88a1 1 0 1 1 1.41 1.41l-4.59 4.59a1 1 0 0 1-1.41 0L6.7 10.7a1 1 0 0 1 .01-1.41z")
)

// chevron is a row's chevron, quiet: right, or down when open.
func chevron(open bool) fyne.CanvasObject {
	res := chevronRight
	if open {
		res = chevronDown
	}
	return sized(16, widget.NewIcon(theme.NewColoredResource(res, theme.ColorNamePlaceHolder)))
}
