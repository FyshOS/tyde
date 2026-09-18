//go:build linux || openbsd || freebsd || netbsd
// +build linux openbsd freebsd netbsd

package x11

import (
	"image"

	"github.com/BurntSushi/xgb/xproto"

	"fyshos.com/tyde"
)

// DecorationProperty is the name of a property on a frame window whose value
// the window manager changes each time it has new decorations to paint, so
// that the compositor captures the frame again.
const DecorationProperty = "_TYDE_DECORATION"

// XWin describes the additional functions that X windows need to expose to be managed
type XWin interface {
	tyde.Window

	FrameID() xproto.Window
	ChildID() xproto.Window

	SizeMin() (uint, uint)
	SizeMax() (int, int)
	Geometry() (int, int, uint, uint)

	// Decorate paints the window's frame decoration over a capture of its
	// frame window. Safe to call from any goroutine.
	Decorate(*image.RGBA)
	MarkDestroyed()
	Reframe()
	Refresh()
	SettingsChanged()

	NotifyBorderChange()
	NotifyIconChange()
	NotifyGeometry(int, int, uint, uint)
	NotifyMoveResizeEnded()

	NotifyMaximize()
	NotifyUnMaximize()
	NotifyFullscreen()
	NotifyUnFullscreen()
	NotifyIconify()
	NotifyUnIconify()

	NotifyMouseDrag(int16, int16)
	NotifyMouseMotion(int16, int16)
	NotifyMousePress(int16, int16, xproto.Button, uint16)
	NotifyMouseRelease(int16, int16, xproto.Button)

	QueueMoveResizeGeometry(int, int, uint, uint)
}

// VisualMoveCallback is set by the compositor to allow visual-only position
// updates during window drag without expensive X11 ConfigureWindow calls.
var VisualMoveCallback func(winID uint32, x, y int16, w, h uint16)
