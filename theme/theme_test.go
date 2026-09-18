package theme

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	_ "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIconResources(t *testing.T) {
	assert.NotNil(t, BatteryIcon.Name())
	assert.NotNil(t, BrightnessIcon.Name())
	assert.NotNil(t, SoundHighIcon.Name())
	assert.NotNil(t, MuteIcon.Name())
}

func TestIconTheme(t *testing.T) {
	th := &testTheme{fg: color.White}
	fyne.CurrentApp().Settings().SetTheme(th)
	battDark := BatteryIcon.Content()

	th.fg = color.Black
	assert.NotEqual(t, battDark, BatteryIcon.Content())
}

func TestIconTheme_BrokenImage(t *testing.T) {
	assert.NotNil(t, BrokenImageIcon) // must not be nil as we fall back
}

type testTheme struct {
	fyne.Theme

	fg color.Color
}

func (t *testTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameForeground {
		return t.fg
	}

	return t.Theme.Color(n, v)
}

func TestSetTouchScreen(t *testing.T) {
	defer SetTouchScreen(false)

	SetTouchScreen(true)
	// big enough to tap - the buttons grow with the bar they sit in
	assert.Greater(t, TitleHeight, titleHeight)
	assert.Greater(t, TitleButtonHeight, titleButtonHeight)
	assert.Greater(t, TitleButtonIconSize, titleButtonIconSize)
	assert.Greater(t, ButtonWidth, buttonWidth)
	assert.Less(t, TitleButtonHeight, TitleHeight) // still fits in the bar

	SetTouchScreen(false)
	assert.Equal(t, titleHeight, TitleHeight)
	assert.Equal(t, titleButtonHeight, TitleButtonHeight)
	assert.Equal(t, titleButtonIconSize, TitleButtonIconSize)
	assert.Equal(t, buttonWidth, ButtonWidth)
}

// Verify theme color names - both current and legacy for backward compatibility checks.
func TestWidgetPanelBackground_ThemeColorNames(t *testing.T) {
	panel := color.NRGBA{R: 0x0e, G: 0x26, B: 0x34, A: 0xb3}
	defer fyne.CurrentApp().Settings().SetTheme(theme.DefaultTheme())

	for name, json := range map[string]string{
		"current": `{"Colors":{"tydePanelBackground":"#0e2634b3"}}`,
		"legacy":  `{"Colors":{"fynedeskPanelBackground":"#0e2634b3"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			th, err := theme.FromJSON(json)
			require.NoError(t, err)
			fyne.CurrentApp().Settings().SetTheme(th)

			assert.Equal(t, panel, WidgetPanelBackground())
		})
	}
}

// A theme that says nothing about the panel still has to give a usable colour,
// and one you can see the desktop through.
func TestWidgetPanelBackground_Unthemed(t *testing.T) {
	defer fyne.CurrentApp().Settings().SetTheme(theme.DefaultTheme())

	th, err := theme.FromJSON(`{"Colors":{"primary":"#ff0000"}}`)
	require.NoError(t, err)
	fyne.CurrentApp().Settings().SetTheme(th)

	col := WidgetPanelBackground()
	assert.NotEqual(t, color.Transparent, col)

	_, _, _, a := col.RGBA()
	assert.NotZero(t, a)
	assert.Less(t, a, uint32(0xffff), "the panel is meant to be see-through")
}
