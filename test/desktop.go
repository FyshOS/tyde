package test

import (
	"github.com/FyshOS/appie"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
)

// Desktop is an in-memory implementation for test purposes
type Desktop struct {
	settings tyde.DeskSettings
	icons    appie.Provider
	screens  tyde.ScreenList
	wm       tyde.WindowManager
	modules  []tyde.Module
}

// NewDesktop returns a new in-memory desktop instance
func NewDesktop() *Desktop {
	screen := &tyde.Screen{Name: "Screen0", X: 0, Y: 0, Width: 2000, Height: 1000, Scale: 1.0}
	screens := &testScreensProvider{screens: []*tyde.Screen{screen}, active: screen, primary: screen}
	return &Desktop{settings: &Settings{}, icons: &testAppProvider{}, screens: screens}
}

// NewDesktopWithWM returns a new in-memory desktop instance using the specified window manager
func NewDesktopWithWM(wm tyde.WindowManager) *Desktop {
	desk := NewDesktop()
	desk.wm = wm
	tyde.SetInstance(desk)
	return desk
}

// AddShortcut is called from modules that wish to register keyboard handlers
func (*Desktop) AddShortcut(shortcut *tyde.Shortcut, handler func()) {
	// TODO
}

// ContentBoundsPixels returns a default value for how much space maximised apps should use
func (*Desktop) ContentBoundsPixels(_ *tyde.Screen) (x, y, w, h uint32) {
	return 0, 0, 320, 240
}

// RootSizePixels returns the total number of pixels required to fit all the screens
func (*Desktop) RootSizePixels() (w, h uint32) {
	return 320, 240
}

// Desktop returns the index of the current desktop (in test this is always 0)
func (*Desktop) Desktop() int {
	return 0
}

// SetDesktop sets the desired desktop index, a no-op in test code
func (*Desktop) SetDesktop(int) {
}

// ShowDesktopOverview reveals all desktops, a no-op in test code
func (*Desktop) ShowDesktopOverview(int) {}

// ShowSettings does nothing for the test package
func (*Desktop) ShowSettings(string) {}

// IconProvider returns the icon provider, by default it uses a simple in-memory implementation
func (td *Desktop) IconProvider() appie.Provider {
	return td.icons
}

// SetIconProvider allows tests to set the icon provider used in this desktop
func (td *Desktop) SetIconProvider(icons appie.Provider) {
	td.icons = icons
}

// RecentApps returns applications that have recently been run. This test instance returns nil.
func (td *Desktop) RecentApps() []appie.AppData {
	return nil
}

// Modules returns the list of modules currently loaded (by default no modules for this implementation)
func (td *Desktop) Modules() []tyde.Module {
	return td.modules
}

// SetModules allows tests to override the modules returned by Modules().
func (td *Desktop) SetModules(mods []tyde.Module) {
	td.modules = mods
}

// Root returns the root window, this is an in-memory test Fyne window
func (*Desktop) Root() fyne.Window {
	return test.NewWindow(nil)
}

// Run will run the desktop mainloop - no-op for testing
func (*Desktop) Run() {
}

// RunApp launches the passed application with appropriate environment setup
func (*Desktop) RunApp(app appie.AppData) error {
	return app.Run([]string{}) // no added env
}

// RunAppAction launches the passed application's action with appropriate environment setup
func (*Desktop) RunAppAction(app appie.AppData, id int) error {
	if app.Actions() == nil || len(app.Actions())-1 < id {
		return nil
	}

	return app.Actions()[id].Run([]string{}) // no added env
}

// Screens returns the list of screens this desktop runs on, by default a simple 2000x1000 value
func (td *Desktop) Screens() tyde.ScreenList {
	return td.screens
}

// Settings returns an in-memory test settings implementation
func (td *Desktop) Settings() tyde.DeskSettings {
	return td.settings
}

// ShowMenuAt is used to show a menu overlay above the desktop
func (td *Desktop) ShowMenuAt(menu *fyne.Menu, pos fyne.Position) {
	wid := widget.NewPopUpMenu(menu, test.Canvas())
	wid.Resize(wid.MinSize())
	wid.ShowAtPosition(pos)
}

// ShowOverlay adds content to the desktop overlay
func (td *Desktop) ShowOverlay(content fyne.CanvasObject, size fyne.Size, pos fyne.Position) {
	content.Resize(size)
	content.Move(pos)
}

// ShowModal centres content above a backdrop and returns a no-op hide function
func (td *Desktop) ShowModal(content fyne.CanvasObject, size fyne.Size) func() {
	content.Resize(size)
	return func() {}
}

// HideOverlay removes content from the desktop overlay
func (td *Desktop) HideOverlay(content fyne.CanvasObject) {
}

// RefreshWindowAccessories is a no-op in the test desktop (no compositor).
func (td *Desktop) RefreshWindowAccessories() {
}

// WindowManager returns the window manager for this desktop, an in-memory test instance unless
// configured through the constructor
func (td *Desktop) WindowManager() tyde.WindowManager {
	return td.wm
}

// DelayScreenSaver is called each time the user interacts with the system.
func (td *Desktop) DelayScreenSaver() {}

// TriggerScreenSaver can be called to immediately) show the screensaver (or with a delay).
func (td *Desktop) TriggerScreenSaver(bool) {}
