//go:generate fyne bundle -package theme -o bundled.go assets/

package theme // import "fyshos.com/tyde/theme"

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
)

// ColorNamePanelBackground is used in themes to look up the background color
const ColorNamePanelBackground fyne.ThemeColorName = "tydePanelBackground"

var (
	// PointerDefault is the standard pointer resource
	PointerDefault = resourcePointerPng

	// FyneLogo is the fyne tooklit icon
	FyneLogo = resourceFynePng
	// FyshOSLogo is the fyne tooklit icon
	FyshOSLogo = resourceFishLogoPng
	// LogoFade is the logo with a semi-transparent background (faded).
	LogoFade = resourceLogoFadePng
	// AppIcon is the image for this application icon
	AppIcon = resourceIconPng

	// BatteryIcon is the material design icon for battery in light and dark theme
	BatteryIcon = theme.NewThemedResource(resourceBatterySvg)
	// BrightnessIcon is the material design icon for brightness in light and dark theme
	BrightnessIcon = theme.NewThemedResource(resourceBrightnessSvg)
	// CalculateIcon is the material design icon for a calculator in light and dark theme
	CalculateIcon = theme.NewThemedResource(resourceCalculateSvg)
	// DisplayIcon is the material design icon for computer displays in light and dark theme
	DisplayIcon = theme.NewThemedResource(resourceDisplaySvg)
	// LaptopIcon is the material design icon for a portable computer
	LaptopIcon = theme.NewThemedResource(resourceLaptopSvg)
	// TabletIcon is the material design icon for a tablet computer
	TabletIcon = theme.NewThemedResource(resourceTabletSvg)
	// InternetIcon is the material design icon for the internet in light and dark theme
	InternetIcon = theme.NewThemedResource(resourceInternetSvg)
	// EthernetIcon is the material design icon for a network connection
	EthernetIcon = theme.NewThemedResource(resourceEthernetSvg)
	// WifiIcon is the material design icon for a wireless network connection
	WifiIcon = theme.NewThemedResource(resourceWifiSvg)
	// WifiOffIcon is the material design icon for a wireless device without a connection
	WifiOffIcon = theme.NewThemedResource(resourceWifioffSvg)
	// AirplaneIcon is the material design icon for a wireless device in airplane mode
	AirplaneIcon = theme.NewThemedResource(resourceAirplaneSvg)
	// PowerIcon is the material design icon for a power connection in light and dark theme
	PowerIcon = theme.NewThemedResource(resourcePowerSvg)
	// UserIcon is the material design icon for a user in light and dark theme
	UserIcon = theme.NewThemedResource(resourcePersonSvg)

	// BrokenImageIcon is the material design icon for a broken image
	BrokenImageIcon = theme.NewThemedResource(resourceBrokenimageSvg)
	// MaximizeIcon is the material design icon for maximizing a window
	MaximizeIcon = theme.NewThemedResource(resourceMaximizeSvg)
	// IconifyIcon is the material design icon for minimizing a window
	IconifyIcon = theme.NewThemedResource(resourceMinimizeSvg)
	// KeyboardIcon is the material design icon for the keyboard settings
	KeyboardIcon = theme.NewThemedResource(resourceKeyboardSvg)
	// LockIcon is the material design icon for the screen lock icon
	LockIcon = theme.NewThemedResource(resourceLockSvg)
	// ScreensIcon is the material design icon for multiple screens
	ScreensIcon = theme.NewThemedResource(resourceScreensSvg)
	// SoundHighIcon is the material design icon for sound in light and dark theme
	SoundHighIcon = theme.NewThemedResource(resourceSoundHighSvg)
	// SoundMidIcon is the material design icon for sound in light and dark theme
	SoundMidIcon = theme.NewThemedResource(resourceSoundMidSvg)
	// SoundLowIcon is the material design icon for sound in light and dark theme
	SoundLowIcon = theme.NewThemedResource(resourceSoundLowSvg)
	// MuteIcon is the material design icon for mute in light and dark theme
	MuteIcon = theme.NewThemedResource(resourceMuteSvg)
	// WallpaperIcon is the material design icon for a desktop wallpaper
	WallpaperIcon = theme.NewThemedResource(resourceWallpaperSvg)
	// ClockIcon is the material design icon for time and date settings
	ClockIcon = theme.NewThemedResource(resourceClockSvg)
	// UpdateIcon is the material design icon for available system updates
	UpdateIcon = theme.NewThemedResource(resourceUpdateSvg)

	// BorderWidth is the width of window frames
	BorderWidth = float32(4)
	// ButtonWidth is the width of window buttons
	ButtonWidth = buttonWidth
	// NarrowBarWidth is the size for the bars in narrow layout
	NarrowBarWidth = float32(36)
	// TitleHeight is the height of a frame titleBar
	TitleHeight = titleHeight
	// TitleButtonHeight is the size of the buttons drawn in a frame titleBar
	TitleButtonHeight = titleButtonHeight
	// TitleButtonIconSize is the size of the icon inside a titleBar button
	TitleButtonIconSize = titleButtonIconSize
	// WidgetPanelWidth defines how wide the large widget panel should be
	WidgetPanelWidth = float32(196)
)

// The frame metrics that SetTouchScreen chooses between. A finger needs a much
// bigger target than a pointer, so a touch screen gets a taller title bar with
// bigger buttons in it. The buttons are sized apart from the bar as they are
// centred in it, so growing the bar alone would leave them small.
const (
	buttonWidth      = float32(32)
	buttonWidthTouch = float32(44)

	titleHeight      = float32(28)
	titleHeightTouch = float32(48)

	titleButtonHeight      = float32(16)
	titleButtonHeightTouch = float32(36)

	titleButtonIconSize      = float32(14)
	titleButtonIconSizeTouch = float32(22)
)

// SetTouchScreen configures the window frame metrics for the input the user
// has. On a touch screen the title bar and the window buttons drawn in it grow
// big enough to tap; anywhere else they return to the pointer sizes.
func SetTouchScreen(touch bool) {
	if touch {
		ButtonWidth = buttonWidthTouch
		TitleHeight = titleHeightTouch
		TitleButtonHeight = titleButtonHeightTouch
		TitleButtonIconSize = titleButtonIconSizeTouch
		return
	}

	ButtonWidth = buttonWidth
	TitleHeight = titleHeight
	TitleButtonHeight = titleButtonHeight
	TitleButtonIconSize = titleButtonIconSize
}

// WindowShadow returns the shadow drawn beneath a window frame - deeper when
// the window is active - so that overlay dialogs (app switcher, launcher,
// menus) float the same way as a focused window. The values match the shadows
// of a Fyne container.InnerWindow.
func WindowShadow(active bool) canvas.Shadow {
	shadow := canvas.Shadow{Color: theme.Color(theme.ColorNameShadow)}
	if active {
		shadow.Offset = fyne.NewPos(2, 5)
		shadow.BlurRadius = 20
		shadow.Spread = 10
	} else {
		shadow.Offset = fyne.NewPos(1, 2)
		shadow.BlurRadius = 8
		shadow.Spread = 3
	}
	return shadow
}

// WidgetPanelBackground returns the semi-transparent background matching the users current theme theme
func WidgetPanelBackground() color.Color {
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	if th := fyne.CurrentApp().Settings().Theme(); th != nil {
		// Respond on known colours and legacy names too
		for _, name := range []fyne.ThemeColorName{ColorNamePanelBackground, "fynedeskPanelBackground"} {
			if col := th.Color(name, variant); col != color.Transparent { // non-transparent means found
				return col
			}
		}
	}

	if variant == theme.VariantLight {
		return color.RGBA{0xaa, 0xaa, 0xaa, 0xaa}
	}
	return color.RGBA{0x24, 0x24, 0x24, 0xcc}
}
