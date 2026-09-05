package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"fyshos.com/tyde"
	"fyshos.com/tyde/modules/keyboard"
	"fyshos.com/tyde/modules/status"
	wmTheme "fyshos.com/tyde/theme"
	"github.com/FyshOS/appie"

	"fyne.io/fyne/v2"
)

type deskSettings struct {
	background             string
	backgroundFill         string
	backgroundColor        string
	iconTheme              string
	launcherIcons          []string
	launcherIconSize       float32
	launcherDisableTaskbar bool
	borderButtonPosition   string
	clockFormatting        string
	computerType           string

	modifier    fyne.KeyModifier
	moduleNames []string

	narrowPanel                   bool
	screenSaverClock              bool
	screenSaver, screenSaverLabel string

	listenerLock    sync.Mutex
	changeListeners []func(tyde.DeskSettings)
}

func (d *deskSettings) Background() string {
	return d.background
}

func (d *deskSettings) BackgroundFill() string {
	return d.backgroundFill
}

func (d *deskSettings) BackgroundColor() string {
	return d.backgroundColor
}

func (d *deskSettings) IconTheme() string {
	return d.iconTheme
}

func (d *deskSettings) LauncherIcons() []string {
	return d.launcherIcons
}

func (d *deskSettings) LauncherDisableTaskbar() bool {
	return d.launcherDisableTaskbar
}

func (d *deskSettings) KeyboardModifier() fyne.KeyModifier {
	return d.modifier
}

func (d *deskSettings) ModuleNames() []string {
	return d.moduleNames
}

func (d *deskSettings) NarrowWidgetPanel() bool {
	return d.narrowPanel
}

func (d *deskSettings) ScreenSaverClock() bool {
	return d.screenSaverClock
}

func (d *deskSettings) ScreenSaverType() string {
	return d.screenSaver
}

func (d *deskSettings) ScreenSaverLabel() string {
	return d.screenSaverLabel
}

func (d *deskSettings) BorderButtonPosition() string {
	return d.borderButtonPosition
}

func (d *deskSettings) ClockFormatting() string {
	return d.clockFormatting
}

func (d *deskSettings) ComputerType() string {
	return d.computerType
}

func (d *deskSettings) AddChangeListener(listener func(tyde.DeskSettings)) {
	d.listenerLock.Lock()
	defer d.listenerLock.Unlock()
	d.changeListeners = append(d.changeListeners, listener)
}

func (d *deskSettings) apply() {
	d.listenerLock.Lock()
	listeners := d.changeListeners
	defer d.listenerLock.Unlock()

	fyne.Do(func() {
		for _, listener := range listeners {
			listener(d)
		}
	})
}

func isModuleEnabled(name string, settings tyde.DeskSettings) bool {
	for _, mod := range settings.ModuleNames() {
		if mod == name {
			return true
		}
	}

	return false
}

func (d *deskSettings) setBackground(name string) {
	d.background = name
	fyne.CurrentApp().Preferences().SetString("background", d.background)
	d.apply()
}

func (d *deskSettings) setBackgroundFill(fill string) {
	d.backgroundFill = fill
	fyne.CurrentApp().Preferences().SetString("backgroundfill", d.backgroundFill)
	d.apply()
}

func (d *deskSettings) setBackgroundColor(hex string) {
	d.backgroundColor = hex
	fyne.CurrentApp().Preferences().SetString("backgroundcolor", d.backgroundColor)
	d.apply()
}

// TODO re-add the icon setting
//func (d *deskSettings) setIconTheme(name string) {
//	d.iconTheme = name
//	fyne.CurrentApp().Preferences().SetString("icontheme", d.iconTheme)
//	d.apply()
//}

func (d *deskSettings) setLauncherIcons(defaultApps []string) {
	newLauncherIcons := strings.Join(defaultApps, "|")
	d.launcherIcons = defaultApps
	fyne.CurrentApp().Preferences().SetString("launchericons", newLauncherIcons)
	d.apply()
}

func (d *deskSettings) setLauncherDisableTaskbar(taskbar bool) {
	d.launcherDisableTaskbar = taskbar
	fyne.CurrentApp().Preferences().SetBool("launcherdisabletaskbar", d.launcherDisableTaskbar)
	d.apply()
}

func (d *deskSettings) setKeyboardModifier(mod fyne.KeyModifier) {
	d.modifier = mod
	fyne.CurrentApp().Preferences().SetInt("keyboardmodifier", int(d.modifier))
	d.apply()
}

func (d *deskSettings) setModuleNames(names []string) {
	newModuleNames := strings.Join(names, "|")
	d.moduleNames = names
	fyne.CurrentApp().Preferences().SetString("modulenames", newModuleNames)
	d.apply()
}

func (d *deskSettings) setNarrowWidgetPanel(narrow bool) {
	d.narrowPanel = narrow
	fyne.CurrentApp().Preferences().SetBool("narrowpanel", narrow)
	d.apply()
}

func (d *deskSettings) setScreenSaver(saver string) {
	oldSaver := d.screenSaver
	d.screenSaver = saver

	fyne.CurrentApp().Preferences().SetString("savertype", saver)

	if oldSaver == "XScreensaver" && saver != "XScreensaver" {
		cmd := exec.Command("xscreensaver-command", "-exit")
		_ = cmd.Start()
	} else if oldSaver != "XScreensaver" && saver == "XScreensaver" {
		cmd := exec.Command("xscreensaver", "--no-splash")
		_ = cmd.Start()
	}
}

func (d *deskSettings) setScreenSaverClock(show bool) {
	d.screenSaverClock = show
	fyne.CurrentApp().Preferences().SetBool("saverclock", show)
}

func (d *deskSettings) setScreenSaverLabel(text string) {
	d.screenSaverLabel = text
	fyne.CurrentApp().Preferences().SetString("saverlabel", text)
}

func (d *deskSettings) setBorderButtonPosition(pos string) {
	d.borderButtonPosition = pos
	fyne.CurrentApp().Preferences().SetString("borderbuttonposition", d.borderButtonPosition)
	d.apply()
}

// setComputerType records the hardware Tyde should configure for.
func (d *deskSettings) setComputerType(kind string) {
	d.computerType = kind
	fyne.CurrentApp().Preferences().SetString("computertype", kind)
	wmTheme.SetTouchScreen(kind == tyde.ComputerTablet)

	d.setModuleNames(modulesForComputer(kind, d.moduleNames)) // applies the change
}

// modulesForComputer returns the enabled module list adjusted for the type of computer.
func modulesForComputer(kind string, names []string) []string {
	out := make([]string, 0, len(names)+3)
	for _, name := range names {
		if name != status.BatteryModuleName && name != status.BrightnessModuleName && name != keyboard.ModuleName {
			out = append(out, name)
		}
	}

	if kind != tyde.ComputerDesktop {
		out = append(out, status.BatteryModuleName, status.BrightnessModuleName)
	}
	if kind == tyde.ComputerTablet {
		out = append(out, keyboard.ModuleName)
	}

	return out
}

func (d *deskSettings) setClockFormatting(format string) {
	d.clockFormatting = format
	fyne.CurrentApp().Preferences().SetString("clockformatting", d.clockFormatting)
	d.apply()
}

func (d *deskSettings) load() {
	env := os.Getenv("FYNEDESK_BACKGROUND")
	if env != "" {
		d.background = env
	} else {
		d.background = fyne.CurrentApp().Preferences().String("background")
	}

	d.backgroundFill = fyne.CurrentApp().Preferences().StringWithFallback("backgroundfill", "Stretch")
	d.backgroundColor = fyne.CurrentApp().Preferences().StringWithFallback("backgroundcolor", "#000000")

	env = os.Getenv("FYNEDESK_ICONTHEME")
	if env != "" {
		d.iconTheme = env
	} else {
		d.iconTheme = fyne.CurrentApp().Preferences().String("icontheme")
	}
	if d.iconTheme == "" {
		d.iconTheme = "hicolor"
	}

	launcherIcons := fyne.CurrentApp().Preferences().String("launchericons")
	if launcherIcons != "" {
		d.launcherIcons = strings.Split(launcherIcons, "|")
	}
	if len(d.launcherIcons) == 0 {
		defaultApps := tyde.Instance().IconProvider().DefaultApps()
		for _, appData := range defaultApps {
			d.launcherIcons = append(d.launcherIcons, appData.Name())
		}
	}

	d.launcherIconSize = float32(fyne.CurrentApp().Preferences().Int("launchericonsize"))
	if d.launcherIconSize == 0 {
		d.launcherIconSize = 48
	}

	d.launcherDisableTaskbar = fyne.CurrentApp().Preferences().Bool("launcherdisabletaskbar")

	defaultModules := "Sound|Emoji Picker|Launcher: Calculate|Launcher: Convert units|Launcher: Large Type|Launcher: Open URLs|Launcher: QR Codes|Launcher: Web Search|Network|Virtual Desktops|SystemTray|Terminal Overlay|Desktop Files|Remote Control|Updates"
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" { // testing
		defaultModules = "Sound|Launcher: Calculate|Launcher: Convert units|Launcher: Large Type|Launcher: Open URLs|Launcher: QR Codes|Launcher: Web Search|Network|Virtual Desktops|Terminal Overlay|Desktop Files|Remote Control"
	}
	moduleNames := fyne.CurrentApp().Preferences().StringWithFallback("modulenames", defaultModules)
	if moduleNames != "" {
		d.moduleNames = strings.Split(moduleNames, "|")
	}
	d.modifier = fyne.KeyModifier(fyne.CurrentApp().Preferences().IntWithFallback("keyboardmodifier", int(fyne.KeyModifierSuper)))
	d.narrowPanel = fyne.CurrentApp().Preferences().BoolWithFallback("narrowpanel", true)

	d.borderButtonPosition = fyne.CurrentApp().Preferences().StringWithFallback("borderbuttonposition", "Right")
	d.screenSaver = fyne.CurrentApp().Preferences().StringWithFallback("savertype", "FyshOS")
	d.screenSaverClock = fyne.CurrentApp().Preferences().BoolWithFallback("saverclock", true)
	d.screenSaverLabel = fyne.CurrentApp().Preferences().StringWithFallback("saverlabel", "Tyde")

	d.clockFormatting = fyne.CurrentApp().Preferences().StringWithFallback("clockformatting", "12h")

	d.computerType = fyne.CurrentApp().Preferences().StringWithFallback("computertype", tyde.ComputerLaptop)
	wmTheme.SetTouchScreen(d.computerType == tyde.ComputerTablet)

	d.loadRecents()
}

func (d *deskSettings) loadRecents() {
	str := fyne.CurrentApp().Preferences().String("recentapps")
	desk := tyde.Instance().(*desktop)

	var apps []appie.AppData
	list := strings.Split(str, ",")

	for _, s := range list {
		app := desk.icons.FindAppFromName(s)
		if app == nil {
			continue
		}
		apps = append(apps, app)
	}

	desk.recent = apps
}

func (d *deskSettings) saveRecents() {
	var list []string

	for _, a := range tyde.Instance().(*desktop).recent {
		list = append(list, a.Name())
	}

	fyne.CurrentApp().Preferences().SetString("recentapps", strings.Join(list, ","))
}

// newDeskSettings loads the user's preferences from environment or config
func newDeskSettings() *deskSettings {
	settings := &deskSettings{}
	settings.load()

	return settings
}
