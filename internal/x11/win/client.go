//go:build linux || openbsd || freebsd || netbsd
// +build linux openbsd freebsd netbsd

package win

import (
	"image"

	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgbutil/ewmh"
	"github.com/BurntSushi/xgbutil/icccm"
	"github.com/BurntSushi/xgbutil/xevent"
	"github.com/BurntSushi/xgbutil/xprop"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"fyshos.com/tyde"
	"fyshos.com/tyde/internal/x11"
	"fyshos.com/tyde/wm"
)

type client struct {
	id, win xproto.Window

	full      bool
	iconic    bool
	maximized bool
	pinned    bool
	props     *clientProperties

	restoreX, restoreY          int16
	restoreWidth, restoreHeight uint16

	frame *frame
	wm    x11.XWM
	desk  int
}

// NewClient creates a new X11 client for the specified window ID and X window manager
func NewClient(win xproto.Window, wm x11.XWM) x11.XWin {
	c := &client{win: win, wm: wm, desk: tyde.Instance().Desktop()}
	xproto.ChangeWindowAttributes(wm.Conn(), win, xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange | xproto.EventMaskEnterWindow | xproto.EventMaskLeaveWindow |
			xproto.EventMaskVisibilityChange})
	windowAllowedActionsSet(wm.X(), win, x11.AllowedActions)

	initialHints := x11.WindowExtendedHintsGet(wm.X(), c.win)
	for _, hint := range initialHints {
		switch hint {
		case "_NET_WM_STATE_FULLSCREEN":
			c.full = true
		case "_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ":
			c.maximized = true
			// TODO Handle more of these possible hints
		}
	}
	if windowStateGet(wm.X(), win) == icccm.StateIconic {
		c.iconic = true
		xproto.UnmapWindow(wm.Conn(), win)
	} else {
		c.positionNewWindow()
		c.newFrame() // could have returned nil, set on c.frame
		if c.frame == nil {
			return nil
		}
	}

	return c
}

func (c *client) ChildID() xproto.Window {
	return c.win
}

func (c *client) MarkDestroyed() {
	if c.frame != nil {
		c.frame.closed.Store(true)
	}
}

// Reframe replaces the current frame with a freshly-built one. This is the
// same path NotifyUnIconify uses to bring a minimised window back: a brand-new
// frame X11 window is created, the inner client is reparented into it, and
// it's shown + themed + geometry-notified. The previous frame is orphaned
// (not destroyed) to avoid Destroy/Reparent event churn racing with the
// in-flight MapRequest that triggered the restore.
func (c *client) Reframe() {
	c.newFrame()
}

func (c *client) Close() {
	winProtos, err := icccm.WmProtocolsGet(c.wm.X(), c.win)
	if err != nil {
		fyne.LogError("Get Protocols Error", err)
	}

	askNicely := false
	for _, proto := range winProtos {
		if proto == "WM_DELETE_WINDOW" {
			askNicely = true
		}
	}

	if !askNicely {
		xproto.DestroyWindow(c.wm.Conn(), c.win)

		return
	}

	protocols, err := xprop.Atm(c.wm.X(), "WM_PROTOCOLS")
	if err != nil {
		fyne.LogError("Get Protocols Error", err)
		return
	}

	delWin, err := xprop.Atm(c.wm.X(), "WM_DELETE_WINDOW")
	if err != nil {
		fyne.LogError("Get Delete Window Error", err)
		return
	}

	cm, err := xevent.NewClientMessage(32, c.win, protocols, int(delWin))
	if err != nil {
		fyne.LogError("Get ClientMessage Error", err)
		return
	}

	xproto.SendEvent(c.wm.Conn(), false, c.win, 0, string(cm.Bytes()))
}

func (c *client) Desktop() int {
	return c.desk
}

func (c *client) SetDesktop(id int) {
	if c.desk == id {
		return
	}

	d := tyde.Instance()
	diff := id - c.desk
	c.desk = id

	if c.pinned {
		return
	}

	_, height := d.RootSizePixels()
	offPix := float32(diff * -int(height))
	display := d.Screens().ScreenForWindow(c)
	off := offPix / display.CanvasScale()

	type moveNotifier interface {
		NotifyWindowMoved(tyde.Window)
	}

	start := c.Position()
	fyne.NewAnimation(canvas.DurationStandard, func(f float32) {
		newY := start.Y - off*f
		pos := fyne.NewPos(start.X, newY)
		if f >= 1.0 {
			c.Move(pos) // final frame: sync X11
		} else {
			// Update frame position so Position() returns the
			// intermediate value for pager preview updates.
			screen := d.Screens().ScreenForWindow(c)
			c.frame.x = int16(pos.X * screen.CanvasScale())
			c.frame.y = int16(pos.Y * screen.CanvasScale())
			c.MoveVisual(pos)
		}
		if mn, ok := c.wm.(moveNotifier); ok {
			mn.NotifyWindowMoved(c)
		}
	}).Start()
}

// Decorate paints this window's frame over a capture of its frame window.
func (c *client) Decorate(img *image.RGBA) {
	if c.frame == nil || c.full || !c.Properties().Decorated() {
		return
	}

	c.frame.decorate(img)
}

func (c *client) Focus() {
	windowActiveReq(c.wm.X(), c.win)
}

func (c *client) Focused() bool {
	active, err := x11.WindowActiveGet(c.wm.X())
	if err != nil {
		return false
	}
	return active == c.win
}

func (c *client) FrameID() xproto.Window {
	return c.id
}

func (c *client) Fullscreen() {
	c.fullscreenMessage(x11.WindowStateActionAdd)
}

func (c *client) Fullscreened() bool {
	return c.full
}

func (c *client) Iconify() {
	if c.iconic {
		return
	}

	c.stateMessage(icccm.StateIconic)
	windowStateSet(c.wm.X(), c.win, icccm.StateIconic)
}

func (c *client) Iconic() bool {
	return c.iconic
}

func (c *client) Geometry() (int, int, uint, uint) {
	if c.frame == nil {
		return 0, 0, 0, 0
	}
	return int(c.frame.x), int(c.frame.y), uint(c.frame.width), uint(c.frame.height)
}

func (c *client) Maximize() {
	c.maximizeMessage(x11.WindowStateActionAdd)
}

func (c *client) Maximized() bool {
	return c.maximized
}

func (c *client) Move(pos fyne.Position) {
	if c.frame == nil {
		return
	}
	screen := tyde.Instance().Screens().ScreenForWindow(c)

	targetX := int16(pos.X * screen.CanvasScale())
	targetY := int16(pos.Y * screen.CanvasScale())
	c.frame.updateGeometry(targetX, targetY, c.frame.width, c.frame.height, false)
}

// MoveVisual updates only the compositor visual without X11 ConfigureWindow calls.
// Used for smooth animations. Call Move() after to sync the actual X11 position.
func (c *client) MoveVisual(pos fyne.Position) {
	if c.frame == nil {
		return
	}
	if x11.VisualMoveCallback == nil {
		c.Move(pos)
		return
	}
	screen := tyde.Instance().Screens().ScreenForWindow(c)
	targetX := int16(pos.X * screen.CanvasScale())
	targetY := int16(pos.Y * screen.CanvasScale())
	x11.VisualMoveCallback(uint32(c.id), targetX, targetY, c.frame.width, c.frame.height)
}

func (c *client) NotifyBorderChange() {
	c.props.refreshCache()
	if c.Properties().Decorated() {
		c.frame.addBorder()
	} else {
		c.frame.removeBorder()
	}
}

// NotifyIconChange invalidates the cached window icon and redraws the border so
// that an icon set or updated by the client (e.g. after load) is reflected.
func (c *client) NotifyIconChange() {
	if c.props == nil {
		return
	}
	c.props.refreshIconCache()
	c.Refresh()
}

func (c *client) NotifyGeometry(x int, y int, width uint, height uint) {
	c.frame.updateGeometry(int16(x), int16(y), uint16(width), uint16(height), true)
}

func (c *client) NotifyFullscreen() {
	c.full = true
	c.frame.maximizeApply()
	x11.WindowExtendedHintsAdd(c.wm.X(), c.win, "_NET_WM_STATE_FULLSCREEN")
}

func (c *client) NotifyIconify() {
	c.iconic = true
	c.frame.hide()
	x11.WindowExtendedHintsAdd(c.wm.X(), c.win, "_NET_WM_STATE_HIDDEN")
}

func (c *client) NotifyMaximize() {
	c.maximized = true
	c.frame.maximizeApply()
	x11.WindowExtendedHintsAdd(c.wm.X(), c.win, "_NET_WM_STATE_MAXIMIZED_VERT")
	x11.WindowExtendedHintsAdd(c.wm.X(), c.win, "_NET_WM_STATE_MAXIMIZED_HORZ")
}

func (c *client) NotifyMouseDrag(x, y int16) {
	fyne.Do(func() {
		c.frame.mouseDrag(x, y)
	})
}

func (c *client) NotifyMouseMotion(x, y int16) {
	fyne.Do(func() {
		c.frame.mouseMotion(x, y)
	})
}

func (c *client) NotifyMousePress(x, y int16, b xproto.Button, mods uint16) {
	fyne.Do(func() {
		c.frame.mousePress(x, y, b, mods)
	})
}

func (c *client) NotifyMouseRelease(x, y int16, b xproto.Button) {
	fyne.Do(func() {
		c.frame.mouseRelease(x, y, b)
	})
}

func (c *client) NotifyMoveResizeEnded() {
	c.frame.endConfigureLoop()
	c.frame.notifyInnerGeometry()
}

func (c *client) NotifyUnFullscreen() {
	// Clear fullscreen before re-applying geometry so the border offsets and
	// decorations are restored; force bypasses the size-hint guards.
	c.full = false
	c.frame.unmaximizeApply(true)
	x11.WindowExtendedHintsRemove(c.wm.X(), c.win, "_NET_WM_STATE_FULLSCREEN")
}

func (c *client) NotifyUnIconify() {
	c.newFrame()
	if c.frame == nil {
		return
	}

	c.iconic = false
	c.frame.show()
	x11.WindowExtendedHintsRemove(c.wm.X(), c.win, "_NET_WM_STATE_HIDDEN")
}

func (c *client) NotifyUnMaximize() {
	c.maximized = false
	c.frame.unmaximizeApply(false)
	x11.WindowExtendedHintsRemove(c.wm.X(), c.win, "_NET_WM_STATE_MAXIMIZED_VERT")
	x11.WindowExtendedHintsRemove(c.wm.X(), c.win, "_NET_WM_STATE_MAXIMIZED_HORZ")
}

func (c *client) Parent() tyde.Window {
	id := x11.WindowTransientForGet(c.wm.X(), c.win)
	if id == 0 {
		return nil
	}

	for _, win := range c.wm.Windows() {
		if win.(x11.XWin).ChildID() == id {
			return win
		}
	}
	return nil
}

func (c *client) Pin() {
	c.pinned = true
	d := tyde.Instance()
	c.SetDesktop(d.Desktop())
}

func (c *client) Pinned() bool {
	return c.pinned
}

func (c *client) Position() fyne.Position {
	if c.frame == nil {
		return fyne.Position{}
	}
	screen := tyde.Instance().Screens().ScreenForWindow(c)

	return fyne.NewPos(
		float32(c.frame.x)/screen.CanvasScale(),
		float32(c.frame.y)/screen.CanvasScale(),
	)
}

func (c *client) Resize(s fyne.Size) {
	if c.frame == nil {
		return
	}
	screen := tyde.Instance().Screens().ScreenForWindow(c)

	c.frame.updateGeometry(c.frame.x, c.frame.y, uint16(s.Width*screen.Scale), uint16(s.Height*screen.Scale), false)
}

func (c *client) Size() fyne.Size {
	if c.frame == nil {
		return fyne.Size{}
	}
	screen := tyde.Instance().Screens().ScreenForWindow(c)

	return fyne.NewSize(
		float32(c.frame.width)/screen.CanvasScale(),
		float32(c.frame.height)/screen.CanvasScale(),
	)
}

func (c *client) QueueMoveResizeGeometry(x int, y int, width uint, height uint) {
	c.frame.queueGeometry(int16(x), int16(y), uint16(width), uint16(height), true)
}

func (c *client) RaiseAbove(win tyde.Window) {
	c.Focus()

	if win == nil {
		// No sibling specified — raise to the top of the stack.
		// With per-screen root windows, sibling-based stacking relative
		// to a single root can place the frame behind another screen's root.
		xproto.ConfigureWindow(c.wm.Conn(), c.id, xproto.ConfigWindowStackMode,
			[]uint32{uint32(xproto.StackModeAbove)})
		return
	}

	topID := win.(*client).id
	if c.id == topID {
		return
	}

	xproto.ConfigureWindow(c.wm.Conn(), c.id, xproto.ConfigWindowSibling|xproto.ConfigWindowStackMode,
		[]uint32{uint32(topID), uint32(xproto.StackModeAbove)})
}

func (c *client) RaiseToTop() {
	c.wm.RaiseToTop(c)
}

func (c *client) Refresh() {
	if c.frame == nil || !c.props.Decorated() {
		return
	}

	c.frame.applyTheme()
}

func (c *client) SettingsChanged() {
	if c.frame == nil {
		return
	}

	fyne.Do(func() {
		c.frame.canvas = nil // force a full re-build of the border widgets
	})
	c.frame.updateScale()
}

func (c *client) SizeMax() (int, int) {
	return windowSizeMax(c.wm.X(), c.ChildID())
}

func (c *client) SizeMin() (uint, uint) {
	return windowSizeMin(c.wm.X(), c.ChildID())
}

func (c *client) TopWindow() bool {
	return c.wm.TopWindow() == c
}

func (c *client) Unfullscreen() {
	c.fullscreenMessage(x11.WindowStateActionRemove)
}

func (c *client) Uniconify() {
	if !c.iconic {
		return
	}

	c.stateMessage(icccm.StateNormal)
	windowStateSet(c.wm.X(), c.win, icccm.StateNormal)
}

func (c *client) Unmaximize() {
	c.maximizeMessage(x11.WindowStateActionRemove)
}

func (c *client) Unpin() {
	c.pinned = false
	d := tyde.Instance()
	id := d.Desktop()
	c.desk = id

	c.SetDesktop(id)
}

func (c *client) fullscreenMessage(action x11.WindowStateAction) {
	err := ewmh.WmStateReq(c.wm.X(), c.win, int(action), "_NET_WM_STATE_FULLSCREEN")
	if err != nil {
		fyne.LogError("", err)
	}
}

func (c *client) maximizeMessage(action x11.WindowStateAction) {
	err := ewmh.WmStateReqExtra(c.wm.X(), c.win, int(action), "_NET_WM_STATE_MAXIMIZED_VERT",
		"_NET_WM_STATE_MAXIMIZED_HORZ", 1)
	if err != nil {
		fyne.LogError("", err)
	}
}

func (c *client) newFrame() {
	c.frame = newFrame(c)
}

func (c *client) positionIsValid(x, y int) bool {
	for _, screen := range tyde.Instance().Screens().Screens() {
		if screen.X <= x && screen.X+screen.Width > x &&
			screen.Y <= y && screen.Y+screen.Height > y {
			return true
		}
	}

	return false
}

func (c *client) positionNewWindow() {
	attrs, err := xproto.GetGeometry(c.wm.Conn(), xproto.Drawable(c.win)).Reply()
	if err != nil {
		fyne.LogError("Get Geometry Error", err)
		return
	}

	requestPosition := false
	hints, err := icccm.WmNormalHintsGet(c.wm.X(), c.win)
	if err == nil {
		if (hints.Flags&icccm.SizeHintPPosition != 0 || hints.Flags&icccm.SizeHintUSPosition != 0) && c.Parent() == nil {
			requestPosition = true
		}
	}

	x, y, w, h := int(attrs.X), int(attrs.Y), uint(attrs.Width), uint(attrs.Height)
	hasPosition := x != 0 || y != 0
	if !requestPosition && !hasPosition || !c.positionIsValid(x, y) {
		decorated := !windowBorderless(c.wm.X(), c.win)
		x, y, w, h = wm.PositionForNewWindow(c, int(attrs.X), int(attrs.Y), uint(attrs.Width), uint(attrs.Height),
			decorated, tyde.Instance().Screens())
	}

	xproto.ConfigureWindow(c.wm.Conn(), c.win, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{
		uint32(x), uint32(y),
		uint32(w), uint32(h),
	})
}

func (c *client) stateMessage(state int) {
	stateChangeAtom, err := xprop.Atm(c.wm.X(), "WM_CHANGE_STATE")
	if err != nil {
		fyne.LogError("Error getting X Atom", err)
		return
	}
	cm, err := xevent.NewClientMessage(32, c.win, stateChangeAtom, state)
	if err != nil {
		fyne.LogError("Error creating client message", err)
		return
	}
	err = xevent.SendRootEvent(c.wm.X(), cm, xproto.EventMaskSubstructureNotify|xproto.EventMaskSubstructureRedirect)
	if err != nil {
		fyne.LogError("Error sending root event", err)
	}
}
