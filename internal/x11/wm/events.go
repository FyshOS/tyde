//go:build linux || openbsd || freebsd || netbsd
// +build linux openbsd freebsd netbsd

package wm

import (
	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgbutil/icccm"
	"github.com/BurntSushi/xgbutil/xevent"
	"github.com/BurntSushi/xgbutil/xprop"

	"fyne.io/fyne/v2"

	"fyshos.com/tyde"
	"fyshos.com/tyde/internal/notify"
	"fyshos.com/tyde/internal/x11"
	"fyshos.com/tyde/wm"
)

func (x *x11WM) handleActiveWin(ev xproto.ClientMessageEvent) {
	canFocus := true
	notifyFocus := false
	hints, err := icccm.WmHintsGet(x.x, ev.Window)
	if err == nil {
		if hints.Flags&icccm.HintInput > 0 {
			canFocus = hints.Input > 0
		}
	}
	protocols, err := icccm.WmProtocolsGet(x.x, ev.Window)
	if err == nil {
		for _, protocol := range protocols {
			if protocol == "WM_TAKE_FOCUS" {
				notifyFocus = true
				break
			}
		}
	}
	if !canFocus && !notifyFocus {
		return
	}
	previous, _ := x11.WindowActiveGet(x.x)
	windowActiveSet(x.x, ev.Window)
	// Redraw both frames for the new active state here rather than waiting
	// for X focus events: those can arrive before the property changes (the
	// app switcher holds a keyboard grab) or only once a WM_TAKE_FOCUS client
	// gets round to taking focus.
	if previous != ev.Window {
		if c := x.clientForWin(previous); c != nil {
			fyne.Do(c.Refresh)
		}
		if c := x.clientForWin(ev.Window); c != nil {
			fyne.Do(c.Refresh)
		}
	}
	if canFocus {
		if c := x.clientForWin(ev.Window); c != nil && c.Iconic() {
			return // don't try to focus iconic windows
		}

		// ask for focus, when it is lost return to root window
		xproto.SetInputFocus(x.x.Conn(), 1, ev.Window, xproto.TimeCurrentTime).Check()
	}
	if notifyFocus {
		protocolAtm, err := xprop.Atm(x.x, "WM_PROTOCOLS")
		if err != nil {
			fyne.LogError("Could not get protocol atom", err)
			return
		}
		focusAtm, err := xprop.Atm(x.x, "WM_TAKE_FOCUS")
		if err != nil {
			fyne.LogError("Could not get focus atom", err)
			return
		}
		cm, err := xevent.NewClientMessage(32, ev.Window, protocolAtm, int(focusAtm), xproto.TimeCurrentTime)
		if err != nil {
			fyne.LogError("Could not create client focus message", err)
			return
		}
		xproto.SendEvent(x.x.Conn(), false, ev.Window, 0, string(cm.Bytes()))
	}
}

func (x *x11WM) handleButtonPress(ev xproto.ButtonPressEvent) {
	for _, c := range x.clients {
		if c.(x11.XWin).FrameID() == ev.Event {
			c.(x11.XWin).NotifyMousePress(ev.RootX, ev.RootY, ev.Detail, ev.State)
		}
	}
	if x.isRoot(ev.Event) {
		// A click landed on the desktop, a panel, or overlay content (e.g. the terminal):
		// give the desktop window keyboard focus so the overlay receives input, then let
		// the click reach Fyne via the replay below.
		xproto.SetInputFocus(x.x.Conn(), xproto.InputFocusPointerRoot, ev.Event, xproto.TimeCurrentTime)
	}
	xevent.ReplayPointer(x.x)
}

func (x *x11WM) handleButtonRelease(ev xproto.ButtonReleaseEvent) {
	for _, c := range x.clients {
		if c.(x11.XWin).FrameID() == ev.Event {
			if !x.moveResizing {
				c.(x11.XWin).NotifyMouseRelease(ev.RootX, ev.RootY, ev.Detail)
			}
			x.moveResizeEnd(c.(x11.XWin))
			x.NotifyWindowMoved(c)
		}
	}
}

func (x *x11WM) handleClientMessage(ev xproto.ClientMessageEvent) {
	c := x.clientForWin(ev.Window)
	msgAtom, err := xprop.AtomName(x.x, ev.Type)
	if err != nil {
		fyne.LogError("Error getting event", err)
		return
	}
	switch msgAtom {
	case "WM_CHANGE_STATE":
		if c == nil {
			return
		}
		switch ev.Data.Bytes()[0] {
		case icccm.StateIconic:
			c.NotifyIconify()
			fyne.Do(func() {
				x.publishWindowChange(c)
			})
		case icccm.StateNormal:
			c.NotifyUnIconify()
			fyne.Do(func() {
				x.publishWindowChange(c)
			})
		}
	case "_NET_ACTIVE_WINDOW":
		x.handleActiveWin(ev)
		x.setActiveScreenFromWindow(c)
	case "_NET_WM_FULLSCREEN_MONITORS":
		if c == nil {
			return
		}
		x.HandleFullScreenMonitors(ev, c)
	case "_NET_WM_MOVERESIZE":
		if c == nil {
			return
		}
		if c.Maximized() || c.Fullscreened() {
			return
		}
		x.handleMoveResize(ev, c)
	case "_NET_WM_STATE":
		subMsgAtom, err := xprop.AtomName(x.x, xproto.Atom(ev.Data.Data32[1]))
		if err != nil {
			fyne.LogError("Error getting event", err)
			return
		}
		if c == nil {
			x.handleInitialHints(ev, subMsgAtom)
			return
		}
		switch subMsgAtom {
		case "_NET_WM_STATE_FULLSCREEN":
			x.handleStateActionRequest(ev, c.NotifyUnFullscreen, c.NotifyFullscreen, c.Fullscreened())
		case "_NET_WM_STATE_HIDDEN":
			// Extended Window Manager Hints says to ignore the HIDDEN state
		case "_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ":
			extraMsgAtom, err := xprop.AtomName(x.x, xproto.Atom(ev.Data.Data32[2]))
			if err != nil {
				fyne.LogError("Error getting event", err)
				return
			}
			if extraMsgAtom == subMsgAtom {
				return
			}
			if extraMsgAtom == "_NET_WM_STATE_MAXIMIZED_VERT" || extraMsgAtom == "_NET_WM_STATE_MAXIMIZED_HORZ" {
				x.handleStateActionRequest(ev, c.NotifyUnMaximize, c.NotifyMaximize, c.Maximized())
			}
		}
	}
}

func (x *x11WM) handleFocus(win xproto.Window) {
	c := x.clientForWin(win)
	if c == nil {
		return
	}
	fyne.Do(c.Refresh)
}

func (x *x11WM) HandleFullScreenMonitors(ev xproto.ClientMessageEvent, c x11.XWin) {
	screens := tyde.Instance().Screens().Screens()
	topIdx := int(ev.Data.Data32[0])
	bottomIdx := int(ev.Data.Data32[1])
	leftIdx := int(ev.Data.Data32[2])
	rightIdx := int(ev.Data.Data32[3])
	if topIdx >= len(screens) || bottomIdx >= len(screens) ||
		leftIdx >= len(screens) || rightIdx >= len(screens) {
		return
	}
	top := screens[topIdx]
	bottom := screens[bottomIdx]
	left := screens[leftIdx]
	right := screens[rightIdx]
	targetX := left.X
	targetY := top.Y
	targetW := right.X + right.Width - left.X
	targetH := bottom.Y + bottom.Height - top.Y
	if c.Fullscreened() {
		c.NotifyGeometry(targetX, targetY, uint(targetW), uint(targetH))
	} else {
		// Move window onto the target screen so fullscreen will land there.
		_, _, w, h := c.Geometry()
		c.NotifyGeometry(targetX, targetY, w, h)
	}
}

func (x *x11WM) handleInitialHints(ev xproto.ClientMessageEvent, hint string) {
	switch x11.WindowStateAction(ev.Data.Data32[0]) {
	case x11.WindowStateActionRemove:
		x11.WindowExtendedHintsRemove(x.x, ev.Window, hint)
	case x11.WindowStateActionAdd:
		x11.WindowExtendedHintsAdd(x.x, ev.Window, hint)
	}
}

func (x *x11WM) handleKeyPress(ev xproto.KeyPressEvent) {
	if screenSaverActive {
		return
	}

	userMod := ev.State&xproto.ModMask4 != 0
	if tyde.Instance().Settings().KeyboardModifier() == fyne.KeyModifierAlt {
		userMod = ev.State&xproto.ModMask1 != 0
	}
	ctrl := ev.State&xproto.ModMaskControl != 0
	shift := ev.State&xproto.ModMaskShift != 0

	if userMod && !ctrl {
		// These methods are about app switcher, we don't want them overridden!
		// Apart from Tab they will only be called once the keyboard grab is in effect.
		if ev.Detail == keyCodeTab {
			x.showOrSelectAppSwitcher(shift)
			return
		}

		if switcherInstance != nil {
			switch ev.Detail {
			case keyCodeEscape:
				x.cancelAppSwitcher()
				return
			case keyCodeReturn, keyCodeEnter:
				x.applyAppSwitcher()
				return
			case keyCodeLeft:
				x.previousAppSwitcher()
				return
			case keyCodeRight:
				x.nextAppSwitcher()
				return
			}
		}
	}
	numlock := ev.State & xproto.ModMask2
	if desk, ok := tyde.Instance().(wm.ShortcutManager); ok {
		for _, shortcut := range desk.Shortcuts() {
			mask := x.modifierToKeyMask(shortcut.Modifier)
			code := x.keyNameToCode(shortcut.KeyName)
			if code == ev.Detail && (mask == ev.State-numlock || mask == xproto.ModMaskAny) {
				fyne.Do(func() {
					desk.TypedShortcut(shortcut)
				})
				return
			}
		}
	}
}

func (x *x11WM) handleKeyRelease(ev xproto.KeyReleaseEvent) {
	if screenSaverActive {
		return
	}

	userMod := keyCodeSuper
	if tyde.Instance().Settings().KeyboardModifier() == fyne.KeyModifierAlt {
		userMod = keyCodeAlt
	}
	if ev.Detail == xproto.Keycode(userMod) {
		x.applyAppSwitcher()
	}
}

func (x *x11WM) handleMouseEnter(ev xproto.EnterNotifyEvent) {
	xproto.ChangeWindowAttributes(x.x.Conn(), ev.Event, xproto.CwCursor,
		[]uint32{uint32(x11.DefaultCursor)})
	if mouseNotify, ok := tyde.Instance().(notify.MouseNotify); ok {
		mouseNotify.MouseOutNotify()
	}
}

func (x *x11WM) handleMouseLeave(ev xproto.LeaveNotifyEvent) {
	win := x.clientForWin(ev.Event)
	if win == nil {
		// dismiss overlay menus on mouse out
		if x11.WindowName(x.X(), ev.Event) == windowNameMenu {
			xproto.UnmapWindow(x.x.Conn(), ev.Event)
		}
	}

	for _, c := range x.clients {
		if c.(x11.XWin).FrameID() == ev.Event {
			if ev.State&xproto.ButtonMask1 == 0 {
				c.(x11.XWin).NotifyMouseMotion(-1, -1)
			}
			break
		}
	}

	if mouseNotify, ok := tyde.Instance().(notify.MouseNotify); ok {
		screen := tyde.Instance().Screens().ScreenForGeometry(int(ev.RootX), int(ev.RootY), 0, 0)
		mouseNotify.MouseInNotify(fyne.NewPos(float32(ev.RootX)/screen.CanvasScale(),
			float32(ev.RootY)/screen.CanvasScale()))
	}
}

func (x *x11WM) handleMouseMotion(ev xproto.MotionNotifyEvent) {
	for _, c := range x.clients {
		if c.(x11.XWin).FrameID() == ev.Event {
			if x.moveResizing {
				x.moveResize(ev.RootX, ev.RootY, c.(x11.XWin))
				break
			}
			if ev.State&xproto.ButtonMask1 != 0 {
				c.(x11.XWin).NotifyMouseDrag(ev.RootX, ev.RootY)
				x.NotifyWindowMoved(c)
			} else {
				c.(x11.XWin).NotifyMouseMotion(ev.RootX, ev.RootY)
			}
			break
		}
	}
}

func (x *x11WM) handleMoveResize(ev xproto.ClientMessageEvent, c x11.XWin) {
	x.moveResizing = true
	x.moveResizingLastX = int16(ev.Data.Data32[0])
	x.moveResizingLastY = int16(ev.Data.Data32[1])
	x.moveResizingStartX = x.moveResizingLastX
	x.moveResizingStartY = x.moveResizingLastY
	x.moveResizingX, x.moveResizingY, x.moveResizingStartWidth, x.moveResizingStartHeight = c.Geometry()
	x.moveResizingType = moveResizeType(ev.Data.Data32[2])
	xproto.GrabPointer(x.x.Conn(), false, c.FrameID(),
		xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease|xproto.EventMaskPointerMotion,
		xproto.GrabModeAsync, xproto.GrabModeAsync, x.x.RootWin(), xproto.CursorNone, xproto.TimeCurrentTime)
	xproto.GrabKeyboard(x.x.Conn(), false, c.FrameID(), xproto.TimeCurrentTime, xproto.GrabModeAsync, xproto.GrabModeAsync)
}

func (x *x11WM) handlePropertyChange(ev xproto.PropertyNotifyEvent) {
	c := x.clientForWin(ev.Window)
	if c == nil {
		return
	}
	propAtom, err := xprop.AtomName(x.x, ev.Atom)
	if err != nil {
		fyne.LogError("Error getting event", err)
		return
	}
	switch propAtom {
	case "_NET_WM_NAME", "WM_NAME":
		fyne.Do(c.Refresh)
	case "WM_NORMAL_HINTS":
		// Force a reconfigure to make sure the client is constrained to the new size hints
		x, y, w, h := c.Geometry()
		c.NotifyGeometry(x, y, w, h)
	case "_MOTIF_WM_HINTS":
		c.NotifyBorderChange()
	case "_NET_WM_ICON", "WM_HINTS":
		c.NotifyIconChange()
	}
}

func (x *x11WM) handleScreenChange(timestamp xproto.Timestamp) {
	if x.screenChangeTimestamp == timestamp {
		return
	}
	x.screenChangeTimestamp = timestamp
	desk := tyde.Instance()
	if desk == nil {
		return
	}
	desk.Screens().RefreshScreens()
	x.configureRoots()
	x.configureSavers()
}

func (x *x11WM) handleStateActionRequest(ev xproto.ClientMessageEvent, removeState func(), addState func(), toggleCheck bool) {
	switch x11.WindowStateAction(ev.Data.Data32[0]) {
	case x11.WindowStateActionRemove:
		removeState()
	case x11.WindowStateActionAdd:
		addState()
	case x11.WindowStateActionToggle:
		if toggleCheck {
			removeState()
		} else {
			addState()
		}
	}
	for _, c := range x.clients {
		if c.(x11.XWin).ChildID() != ev.Window {
			continue
		}

		// Maximizing or fullscreening a window both moves it and changes its
		// state - listeners such as the window bar and the desktop pets need to
		// hear about the latter.
		x.NotifyWindowMoved(c)
		fyne.Do(func() {
			x.publishWindowChange(c)
		})
		break
	}
}

func (x *x11WM) handleVisibilityChange(ev xproto.VisibilityNotifyEvent) {
	attrs, err := xproto.GetWindowAttributes(x.x.Conn(), ev.Window).Reply()
	if err == nil && attrs.Colormap != 0 {
		if ev.State != xproto.VisibilityFullyObscured {
			xproto.InstallColormap(x.x.Conn(), attrs.Colormap)
		} else {
			xproto.UninstallColormap(x.x.Conn(), attrs.Colormap)
		}
	}
	colormaps, err := icccm.WmColormapWindowsGet(x.x, ev.Window)
	if err == nil {
		for _, child := range colormaps {
			chAttrs, err := xproto.GetWindowAttributes(x.x.Conn(), child).Reply()
			if err == nil && chAttrs.Colormap != 0 {
				if ev.State != xproto.VisibilityFullyObscured {
					xproto.InstallColormap(x.x.Conn(), chAttrs.Colormap)
				} else {
					xproto.UninstallColormap(x.x.Conn(), chAttrs.Colormap)
				}
			}
		}
	}
}

func (x *x11WM) moveResizeEnd(c x11.XWin) {
	x.moveResizing = false
	xproto.UngrabPointer(x.x.Conn(), xproto.TimeCurrentTime)
	xproto.UngrabKeyboard(x.x.Conn(), xproto.TimeCurrentTime)

	// ensure menus etc update
	c.NotifyMoveResizeEnded()
}

func (x *x11WM) moveResize(moveX, moveY int16, c x11.XWin) {
	_, _, width, height := c.Geometry()
	w := int16(width)
	h := int16(height)
	deltaW := moveX - x.moveResizingLastX
	deltaH := moveY - x.moveResizingLastY
	deltaX := moveX - x.moveResizingStartX
	deltaY := moveY - x.moveResizingStartY

	switch x.moveResizingType {
	case moveResizeTopLeft:
		// Move both X,Y coords and resize both W,H
		x.moveResizingX += int(deltaW)
		x.moveResizingY += int(deltaH)

		w = int16(x.moveResizingStartWidth) - deltaX
		h = int16(x.moveResizingStartHeight) - deltaY
	case moveResizeTop:
		// Move Y coord and resize H
		x.moveResizingY += int(deltaH)
		h = int16(x.moveResizingStartHeight) - deltaY
	case moveResizeTopRight:
		// Move Y coord and resize both W,H
		x.moveResizingY += int(deltaH)
		w = int16(x.moveResizingStartWidth) + deltaX
		h = int16(x.moveResizingStartHeight) - deltaY
	case moveResizeRight:
		// Keep X coord and resize W
		w = int16(x.moveResizingStartWidth) + deltaX
	case moveResizeBottomRight, moveResizeKeyboard:
		// Keep both X,Y coords and resize both W,H
		w = int16(x.moveResizingStartWidth) + deltaX
		h = int16(x.moveResizingStartHeight) + deltaY
	case moveResizeBottom:
		// Keep Y coord and resize H
		h = int16(x.moveResizingStartHeight) + deltaY
	case moveResizeBottomLeft:
		// Move X coord and resize both W,H
		x.moveResizingX += int(deltaW)
		w = int16(x.moveResizingStartWidth) - deltaX
		h = int16(x.moveResizingStartHeight) + deltaY
	case moveResizeLeft:
		// Move X coord and resize W
		x.moveResizingX += int(deltaW)
		w = int16(x.moveResizingStartWidth) - deltaX
	case moveResizeMove, moveResizeMoveKeyboard:
		// Move both X,Y coords and no resize
		x.moveResizingX += int(deltaW)
		x.moveResizingY += int(deltaH)
	case moveResizeCancel:
		x.moveResizeEnd(c)
		return
	}
	x.moveResizingLastX = moveX
	x.moveResizingLastY = moveY
	c.QueueMoveResizeGeometry(x.moveResizingX, x.moveResizingY, uint(w), uint(h))

	x.NotifyWindowMoved(c)
}
