package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// edTheme is the Elite Dangerous-styled Fyne theme: deep charcoal
// background, the iconic ED orange as accent, off-white text. Dark only —
// we ignore the user's system light/dark preference because the source
// game has a dark cockpit aesthetic and a light theme would feel wrong.
type edTheme struct{}

// ED palette constants.
var (
	edOrange     = color.NRGBA{0xff, 0x80, 0x00, 0xff} // primary action / accent
	edOrangeDim  = color.NRGBA{0xc7, 0x6a, 0x14, 0xff} // hover/pressed
	edBg         = color.NRGBA{0x14, 0x16, 0x1a, 0xff} // base
	edBgRaised   = color.NRGBA{0x1c, 0x1f, 0x25, 0xff} // panels, cards
	edBgInput    = color.NRGBA{0x23, 0x27, 0x2e, 0xff} // text inputs
	edFg         = color.NRGBA{0xe6, 0xe6, 0xe8, 0xff} // primary text
	edFgMuted    = color.NRGBA{0x99, 0x9f, 0xa8, 0xff} // captions, labels
	edFgDim      = color.NRGBA{0x60, 0x66, 0x70, 0xff} // placeholder, separators
	edBorder     = color.NRGBA{0x2d, 0x32, 0x3b, 0xff}
	edHover      = color.NRGBA{0x2a, 0x2f, 0x38, 0xff}
	edDisabled   = color.NRGBA{0x44, 0x48, 0x50, 0xff}

	edStatusError = color.NRGBA{0xff, 0x6b, 0x6b, 0xff}
	edStatusWarn  = color.NRGBA{0xff, 0xb8, 0x4d, 0xff}
	edStatusOK    = color.NRGBA{0x6b, 0xd5, 0x87, 0xff}
	edStatusInfo  = color.NRGBA{0x6f, 0xa9, 0xff, 0xff}
)

func (edTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return edBg
	case theme.ColorNameForeground:
		return edFg
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{0x14, 0x16, 0x1a, 0xff} // dark text on orange button
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return edOrange
	case theme.ColorNameHover:
		return edHover
	case theme.ColorNamePressed:
		return edOrangeDim
	case theme.ColorNameButton:
		return edBgRaised
	case theme.ColorNameInputBackground, theme.ColorNameMenuBackground:
		return edBgInput
	case theme.ColorNameInputBorder, theme.ColorNameSeparator, theme.ColorNameShadow:
		return edBorder
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return edFgDim
	case theme.ColorNameDisabledButton:
		return edDisabled
	case theme.ColorNameOverlayBackground, theme.ColorNameHeaderBackground:
		return edBgRaised
	case theme.ColorNameScrollBar:
		return color.NRGBA{0x40, 0x46, 0x50, 0x80}
	case theme.ColorNameError:
		return edStatusError
	case theme.ColorNameSuccess:
		return edStatusOK
	case theme.ColorNameWarning:
		return edStatusWarn
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (edTheme) Font(s fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(s)
}

func (edTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (edTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameText:
		return 13
	case theme.SizeNameHeadingText:
		return 18
	case theme.SizeNameSubHeadingText:
		return 14
	}
	return theme.DefaultTheme().Size(n)
}
