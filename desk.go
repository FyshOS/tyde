package tyde // import "fyshos.com/tyde"

import (
	"fyne.io/fyne/v2"

	"github.com/FyshOS/appie"
)

// Desktop defines an embedded or full desktop environment that we can run.
type Desktop interface {
	Run()
	RunApp(appie.AppData) error
	RunAppAction(data appie.AppData, id int) error
	RecentApps() []appie.AppData
	Settings() DeskSettings
	ContentBoundsPixels(*Screen) (x, y, w, h uint32)
	RootSizePixels() (w, h uint32)
	Screens() ScreenList

	IconProvider() appie.Provider
	WindowManager() WindowManager
	Modules() []Module

	AddShortcut(shortcut *Shortcut, handler func())
	ShowMenuAt(menu *fyne.Menu, pos fyne.Position)
	ShowOverlay(content fyne.CanvasObject, size fyne.Size, pos fyne.Position)
	ShowModal(content fyne.CanvasObject, size fyne.Size) func()
	HideOverlay(content fyne.CanvasObject)
	RefreshWindowAccessories() // ask the compositor to re-assemble WindowAccessoryModule items
	Root() fyne.Window

	Desktop() int
	SetDesktop(int)
	ShowDesktopOverview(count int)
	// ShowSettings opens the settings window. A non-empty panel title opens that
	// panel directly; an empty string shows whatever the window was last on.
	ShowSettings(panel string)

	DelayScreenSaver()
	TriggerScreenSaver(bool)
}

var instance Desktop

// Instance returns the current desktop environment and provides access to injected functionality.
func Instance() Desktop {
	return instance
}

// SetInstance is an internal call :( TODO
func SetInstance(desk Desktop) {
	instance = desk
}
