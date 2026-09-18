package test

import (
	"image"

	"github.com/BurntSushi/xgb/xproto"
)

// just stubs so we can use the generic test Window in X11 tests

// ChildID returns the X window ID that this window contains
func (w *Window) ChildID() xproto.Window {
	return 0
}

// Decorate paints the window frame over a capture of its frame window
func (w *Window) Decorate(*image.RGBA) {
	// no-op
}

// FrameID returns the X window ID that this frame is drawing in
func (w *Window) FrameID() xproto.Window {
	return 0
}

// Geometry returns the x, y position and width, height size of this window's outer bounds
func (w *Window) Geometry() (int, int, uint, uint) {
	return w.x, w.y, w.width, w.height
}

// NotifyBorderChange is called when the border should be shown or hidden
func (w *Window) NotifyBorderChange() {
	// no-op
}

// NotifyGeometry is called when the window is instructed to change it's geometry
func (w *Window) NotifyGeometry(int, int, uint, uint) {
	// no-op
}

// NotifyMoveResizeEnded is called to inform the window that it was moved or resized
func (w *Window) NotifyMoveResizeEnded() {
	// no-op
}

// NotifyFullscreen is called when the window is instructed to become fullscreen
func (w *Window) NotifyFullscreen() {
	// no-op
}

// NotifyIconChange is called when the window icon has been updated
func (w *Window) NotifyIconChange() {
	// no-op
}

// NotifyIconify is called when the window is instructed to become iconified
func (w *Window) NotifyIconify() {
	// no-op
}

// NotifyMaximize is called when the window is instructed to become maximized
func (w *Window) NotifyMaximize() {
	// no-op
}

// NotifyUnFullscreen is called when the window is instructed to revert from fullscreen size
func (w *Window) NotifyUnFullscreen() {
	// no-op
}

// NotifyUnIconify is called when the window is instructed to return from icon form
func (w *Window) NotifyUnIconify() {
	// no-op
}

// NotifyUnMaximize is called when the window is instructed to return from maximized size
func (w *Window) NotifyUnMaximize() {
	// no-op
}

// NotifyMouseDrag is called when the mouse was seen to drag inside the window frame
func (w *Window) NotifyMouseDrag(int16, int16) {
	// no-op
}

// NotifyMouseMotion is called when the mouse was seen to move inside the window frame
func (w *Window) NotifyMouseMotion(int16, int16) {
	// no-op
}

// NotifyMousePress is called when the mouse was seen to press inside the window frame
func (w *Window) NotifyMousePress(int16, int16, xproto.Button, uint16) {
	// no-op
}

// NotifyMouseRelease is called when the mouse was seen to release inside the window frame
func (w *Window) NotifyMouseRelease(int16, int16, xproto.Button) {
	// no-op
}

// QueueMoveResizeGeometry is called when a MoveResize event reports new geometry
func (w *Window) QueueMoveResizeGeometry(int, int, uint, uint) {
	// no-op
}

// Refresh is called when the window should update with new border state
func (w *Window) Refresh() {
	// no-op
}

// SettingsChanged is called on a window when the theme or icon set changes
func (w *Window) SettingsChanged() {
	// no-op
}

// SizeMin returns the minimum size this window allows
func (w *Window) SizeMin() (uint, uint) {
	return 0, 0
}

// SizeMax returns the maximum size this window can become (which may disable maximize)
func (w *Window) SizeMax() (int, int) {
	return -1, -1
}
