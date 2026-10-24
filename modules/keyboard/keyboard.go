// Package keyboard adds an on-screen keyboard to Tyde, for touchscreen machines
// where there may be no hardware keyboard within reach. It is toggled from a
// keyboard icon sits in the status area.
//
// Keys are typed into the focused application as real key events through the
// XTEST extension.
package keyboard

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	wmTheme "fyshos.com/tyde/theme"
)

const ModuleName = "TouchScreen Keyboard"

var meta = tyde.ModuleMetadata{
	Name:        ModuleName,
	NewInstance: newKeyboard,
}

// module is the module instance. It owns the single keyboard overlay.
type module struct {
	panel *panel
}

func newKeyboard() tyde.Module {
	return &module{panel: &panel{}}
}

func (m *module) Metadata() tyde.ModuleMetadata {
	return meta
}

func (m *module) Destroy() {
	if m.panel != nil {
		m.panel.destroy()
	}
}

// StatusAreaWidget puts the keyboard icon in the status area, next to the system
// tray icons - the one place a touchscreen user can reach without a keyboard.
func (m *module) StatusAreaWidget() fyne.CanvasObject {
	return &widget.Button{
		Icon: wmTheme.KeyboardIcon, Importance: widget.LowImportance,
		OnTapped: func() { m.panel.toggle() },
	}
}
