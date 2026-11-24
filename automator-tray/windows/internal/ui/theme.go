package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Theme is the window's look: Fyne's theme with automator-ui's palette
// (macOS's system colours, near-black text, quiet greys) and its smaller,
// rounder controls. Unchecked boxes and radio buttons get a border that
// shows on a card.
func Theme() fyne.Theme { return appTheme{theme.DefaultTheme()} }

type appTheme struct{ fyne.Theme }

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

func rgba(hex uint32, a uint8) color.NRGBA {
	c := rgb(hex)
	c.A = a
	return c
}

// palettes are the colours changed, by variant.
var palettes = map[fyne.ThemeVariant]map[fyne.ThemeColorName]color.Color{
	theme.VariantLight: {
		theme.ColorNameBackground:          rgb(0xF5F5F7),
		theme.ColorNameForeground:          rgb(0x1D1D1F),
		theme.ColorNamePlaceHolder:         rgb(0x6E6E73),
		theme.ColorNameDisabled:            rgb(0x9A9AA0),
		theme.ColorNameButton:              rgb(0xFFFFFF),
		theme.ColorNameDisabledButton:      rgba(0x000000, 0x0d),
		theme.ColorNameInputBackground:     rgb(0xFFFFFF),
		theme.ColorNameInputBorder:         rgb(0xBDBDC2),
		theme.ColorNameSeparator:           rgba(0x000000, 0x1a),
		theme.ColorNameHover:               rgba(0x000000, 0x0b),
		theme.ColorNamePressed:             rgba(0x000000, 0x16),
		theme.ColorNamePrimary:             rgb(0x007AFF),
		theme.ColorNameForegroundOnPrimary: rgb(0xFFFFFF),
		theme.ColorNameSuccess:             rgb(0x1F9D45),
		theme.ColorNameError:               rgb(0xE0352B),
		theme.ColorNameWarning:             rgb(0xC96A00),
		theme.ColorNameHeaderBackground:    rgb(0xFFFFFF),
		theme.ColorNameMenuBackground:      rgb(0xFFFFFF),
		theme.ColorNameOverlayBackground:   rgb(0xFFFFFF),
		theme.ColorNameScrollBar:           rgba(0x000000, 0x50),
		theme.ColorNameFocus:               rgba(0x007AFF, 0x30),
		theme.ColorNameSelection:           rgba(0x007AFF, 0x38),
	},
	theme.VariantDark: {
		theme.ColorNameBackground:          rgb(0x1F1F21),
		theme.ColorNameForeground:          rgb(0xECECF0),
		theme.ColorNamePlaceHolder:         rgb(0xA1A1A6),
		theme.ColorNameDisabled:            rgb(0x76767C),
		theme.ColorNameButton:              rgb(0x3A3A3D),
		theme.ColorNameDisabledButton:      rgba(0xFFFFFF, 0x0d),
		theme.ColorNameInputBackground:     rgb(0x18181A),
		theme.ColorNameInputBorder:         rgb(0x5C5C62),
		theme.ColorNameSeparator:           rgba(0xFFFFFF, 0x1c),
		theme.ColorNameHover:               rgba(0xFFFFFF, 0x10),
		theme.ColorNamePressed:             rgba(0xFFFFFF, 0x20),
		theme.ColorNamePrimary:             rgb(0x0A84FF),
		theme.ColorNameForegroundOnPrimary: rgb(0xFFFFFF),
		theme.ColorNameSuccess:             rgb(0x30D158),
		theme.ColorNameError:               rgb(0xFF453A),
		theme.ColorNameWarning:             rgb(0xFF9F0A),
		theme.ColorNameHeaderBackground:    rgb(0x2A2A2D),
		theme.ColorNameMenuBackground:      rgb(0x2C2C2F),
		theme.ColorNameOverlayBackground:   rgb(0x2C2C2F),
		theme.ColorNameScrollBar:           rgba(0xFFFFFF, 0x50),
		theme.ColorNameFocus:               rgba(0x0A84FF, 0x40),
		theme.ColorNameSelection:           rgba(0x0A84FF, 0x48),
	},
}

func (t appTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if c, ok := palettes[v][n]; ok {
		return c
	}
	return t.Theme.Color(n, v)
}

func (t appTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameInputRadius, theme.SizeNameButtonRadius:
		return 6
	case theme.SizeNameSelectionRadius:
		return 4
	case theme.SizeNameScrollBar:
		return 8
	}
	return t.Theme.Size(n)
}
