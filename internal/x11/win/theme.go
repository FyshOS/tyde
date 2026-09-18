//go:build linux || openbsd || freebsd || netbsd

package win

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	wmTheme "fyshos.com/tyde/theme"
)

// transparentTheme themes the window frame widgets for the window: the border
// colour follows focus and the frame draws no shadow of its own (the
// compositor draws one beneath the whole window).
type transparentTheme struct {
	fyne.Theme

	frame *frame
}

func (t *transparentTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameShadow:
		return color.Transparent
	case theme.ColorNameBackground, theme.ColorNameOverlayBackground,
		theme.ColorNameInnerWindowBorder, theme.ColorNameInnerWindowBorderInactive:
		if t.frame != nil && t.frame.active {
			n = theme.ColorNameOverlayBackground
		} else {
			n = theme.ColorNameDisabledButton
		}
	}

	return t.Theme.Color(n, v)
}

// Size reports our own frame metrics, used to customise the window borders.
func (t *transparentTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameInnerWindowRadius:
		if t.frame != nil && t.frame.client.maximized {
			return 0 // a maximized window meets the screen edges squarely
		}
	case theme.SizeNameWindowTitleBarHeight:
		return wmTheme.TitleHeight
	case theme.SizeNameWindowButtonHeight:
		return wmTheme.TitleButtonHeight
	case theme.SizeNameWindowButtonIcon:
		return wmTheme.TitleButtonIconSize
	}

	return t.Theme.Size(n)
}
