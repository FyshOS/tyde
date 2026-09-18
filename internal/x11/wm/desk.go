//go:build linux || openbsd || freebsd || netbsd
// +build linux openbsd freebsd netbsd

package wm // import "fyshos.com/tyde/internal/x11/wm"

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/randr"
	"github.com/BurntSushi/xgb/screensaver"
	"github.com/BurntSushi/xgb/shape"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgbutil"
	"github.com/BurntSushi/xgbutil/ewmh"
	"github.com/BurntSushi/xgbutil/icccm"
	"github.com/BurntSushi/xgbutil/keybind"
	"github.com/BurntSushi/xgbutil/xevent"
	"github.com/BurntSushi/xgbutil/xgraphics"
	"github.com/BurntSushi/xgbutil/xprop"
	"github.com/FyshOS/backgrounds"
	"github.com/nfnt/resize"

	"fyne.io/fyne/v2"
	deskDriver "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	"fyshos.com/tyde/internal/ui"
	"fyshos.com/tyde/internal/x11"
	xwin "fyshos.com/tyde/internal/x11/win"
	"fyshos.com/tyde/wm"
)

type x11WM struct {
	stack
	x                       *xgbutil.XUtil
	framedExisting          bool
	moveResizing            bool
	moveResizingX           int
	moveResizingY           int
	moveResizingStartX      int16
	moveResizingStartY      int16
	moveResizingLastX       int16
	moveResizingLastY       int16
	moveResizingStartWidth  uint
	moveResizingStartHeight uint
	moveResizingType        moveResizeType
	screenChangeTimestamp   xproto.Timestamp

	currentBindings []*tyde.Shortcut

	died           bool
	rootIDs        map[string]xproto.Window
	overlayActive  bool
	overlayRegions []image.Rectangle
	menuSize       fyne.Size
	menuPos        fyne.Position
	transientMap   map[xproto.Window][]xproto.Window
	saverScreens   map[xproto.Window]string
	oldRoot        *xgraphics.Image
}

type moveResizeType uint32

const (
	moveResizeTopLeft      moveResizeType = 0
	moveResizeTop          moveResizeType = 1
	moveResizeTopRight     moveResizeType = 2
	moveResizeRight        moveResizeType = 3
	moveResizeBottomRight  moveResizeType = 4
	moveResizeBottom       moveResizeType = 5
	moveResizeBottomLeft   moveResizeType = 6
	moveResizeLeft         moveResizeType = 7
	moveResizeMove         moveResizeType = 8
	moveResizeKeyboard     moveResizeType = 9
	moveResizeMoveKeyboard moveResizeType = 10
	moveResizeCancel       moveResizeType = 11

	keyCodeEscape      = 9
	keyCodeTab         = 23
	keyCodeReturn      = 36
	keyCodeBacktick    = 49
	keyCodeAlt         = 64
	keyCodeSpace       = 65
	keyCodePrintScreen = 107
	keyCodeSuper       = 133
	keyCodeCalculator  = 148
	keyCodeAssistant   = 201 // F23, sent by the assistant ("Copilot") key

	keyCodeEnter = 108
	keyCodeLeft  = 113
	keyCodeRight = 114
	keyCodeUp    = 111
	keyCodeDown  = 116

	keyCodeBrightLess = 232
	keyCodeBrightMore = 233

	keyCodeVolumeMute = 121
	keyCodeVolumeLess = 122
	keyCodeVolumeMore = 123

	windowNameMenu = "Tyde Menu"
)

// NewX11WindowManager sets up a new X11 Window Manager to control a desktop in X11.
func NewX11WindowManager(a fyne.App) (tyde.WindowManager, error) {
	conn, err := xgbutil.NewConn()
	if err != nil {
		fyne.LogError("Failed to connect to the XServer", err)
		return nil, err
	}

	mgr := &x11WM{x: conn}
	root := conn.RootWin()
	mgr.takeSelectionOwnership()
	mgr.rootIDs = make(map[string]xproto.Window)
	mgr.transientMap = make(map[xproto.Window][]xproto.Window)

	eventMask := xproto.EventMaskPropertyChange |
		xproto.EventMaskFocusChange |
		xproto.EventMaskButtonPress |
		xproto.EventMaskButtonRelease |
		xproto.EventMaskKeyPress |
		xproto.EventMaskStructureNotify |
		xproto.EventMaskSubstructureRedirect |
		screensaver.EventNotifyMask | screensaver.EventCycleMask
	if err := xproto.ChangeWindowAttributesChecked(conn.Conn(), root, xproto.CwEventMask,
		[]uint32{uint32(eventMask)}).Check(); err != nil {
		conn.Conn().Close()

		return nil, errors.New("window manager detected, running embedded")
	}

	err = ewmh.SupportedSet(mgr.x, x11.SupportedHints)
	if err != nil {
		fyne.LogError("", err)
	}
	err = ewmh.SupportingWmCheckSet(mgr.x, mgr.x.RootWin(), mgr.x.Dummy())
	if err != nil {
		fyne.LogError("", err)
	}
	err = ewmh.SupportingWmCheckSet(mgr.x, mgr.x.Dummy(), mgr.x.Dummy())
	if err != nil {
		fyne.LogError("", err)
	}
	err = ewmh.WmNameSet(mgr.x, mgr.x.Dummy(), "Tyde")
	if err != nil {
		fyne.LogError("", err)
	}
	err = ewmh.DesktopViewportSet(mgr.x, []ewmh.DesktopViewport{{X: 0, Y: 0}}) // Will always be 0, 0 until virtual desktops are supported
	if err != nil {
		fyne.LogError("", err)
	}
	err = ewmh.NumberOfDesktopsSet(mgr.x, 1) // Will always be 1 until virtual desktops are supported
	if err != nil {
		fyne.LogError("", err)
	}
	err = ewmh.CurrentDesktopSet(mgr.x, 0) // Will always be 0 until virtual desktops are supported
	if err != nil {
		fyne.LogError("", err)
	}

	x11.LoadCursors(conn)
	_ = shape.Init(conn.Conn())
	mgr.initScreensaver()

	a.Settings().AddListener(func(_ fyne.Settings) {
		mgr.updateBackgrounds()
		mgr.refreshBorders()
		mgr.configureRoots()
	})
	a.Preferences().AddChangeListener(mgr.refreshBorders)

	return mgr, nil
}

func (x *x11WM) AddStackListener(l tyde.StackListener) {
	x.stack.listeners = append(x.stack.listeners, l)
}

func (x *x11WM) RemoveStackListener(l tyde.StackListener) {
	for i, cur := range x.stack.listeners {
		if cur != l {
			continue
		}

		x.stack.listeners = append(x.stack.listeners[:i], x.stack.listeners[i+1:]...)
		return
	}
}

func (x *x11WM) Blank() {
	go func() {
		time.Sleep(time.Second / 3)
		err := exec.Command("xset", "-display", os.Getenv("DISPLAY"), "dpms", "force", "off").Start()
		if err != nil {
			fyne.LogError("", err)
		}
	}()
}

func (x *x11WM) Close() {
	for _, child := range x.clients {
		child.Close()
	}
	if x.died {
		// x server died, no point attempting to shut it cleanly
		return
	}

	cancel := false
	exit := make(chan interface{})
	go func() {
		for !cancel && len(x.clients) > 0 {
			time.Sleep(time.Millisecond * 100)
		}

		close(exit)
	}()

	go func() {
		select {
		case <-exit:
			x.x.Conn().Close()
			os.Exit(0)
		case <-time.NewTimer(time.Second * 10).C:
			notify := wm.NewNotification("Log Out", "Log Out was cancelled by an open application", "")
			wm.SendNotification(notify)
			cancel = true
		}
	}()
}

func (x *x11WM) Conn() *xgb.Conn {
	return x.x.Conn()
}

func (x *x11WM) Run() {
	x.setupBindings()
	go x.runLoop()
}

// SetOverlayActive updates X11 input shapes on the root and all frame windows.
// regions are the screen-pixel rectangles actually covered by Fyne overlay content.
// When active is true, frames are made input-transparent only within those regions, so
// clicks there fall through to the Fyne overlay while the rest of each window stays
// interactive. A full-screen overlay (e.g. a menu backdrop) passes a region covering the
// whole primary screen, blanking primary frames entirely as before.
// When active is false, frames resume their normal input shapes (excluding panels).
//
// regions can change on every call (e.g. a second overlay opening), so shapes are always
// updated. Focus and the desktop button grab only change on the inactive<->active edge:
// the first overlay claims keyboard focus, and a passive button grab on the desktop
// windows is installed so later clicks on the desktop/panels/overlay re-focus it (see
// handleButtonPress). When the last overlay closes the focus that was taken is handed
// back to the top window.
func (x *x11WM) SetOverlayActive(active bool, regions []image.Rectangle) {
	// Remember the regions so a frame that re-shapes does not wipe out overlays.
	x.overlayRegions = regions
	x.updateRootInputShape(active)
	x.updateFrameInputShapes(active, regions)

	if active == x.overlayActive {
		return
	}
	x.overlayActive = active

	x.grabRootButtons(active)
	if active {
		// Focus the primary root window so the overlay receives keyboard events.
		if primary := x.RootID(); primary != 0 {
			xproto.SetInputFocus(x.x.Conn(), xproto.InputFocusPointerRoot, primary, xproto.TimeCurrentTime)
		}
		return
	}

	x.focusTopWindow()
}

// focusTopWindow returns keyboard focus to the topmost window that can take it.
// If no window qualifies focus is left where it is, on the desktop itself.
func (x *x11WM) focusTopWindow() {
	win := x.topFocusable()
	if win == nil {
		return
	}

	win.Focus()
	// Focus() sends an async client message that may be overridden, so also
	// set the X input focus directly (as setupWindow does for new windows).
	if xwin, ok := win.(x11.XWin); ok {
		xproto.SetInputFocus(x.x.Conn(), xproto.InputFocusPointerRoot,
			xwin.ChildID(), xproto.TimeCurrentTime)
	}
}

// topFocusable returns the topmost window that should be given keyboard focus,
// skipping iconified windows and any that are not on the current desktop.
func (x *x11WM) topFocusable() tyde.Window {
	current := tyde.Instance().Desktop()
	for i := len(x.clients) - 1; i >= 0; i-- {
		win := x.clients[i]
		if win.Iconic() || (win.Desktop() != current && !win.Pinned()) {
			continue
		}

		return win
	}

	return nil
}

// grabRootButtons installs (or removes) a passive sync grab of the primary mouse button
// on the primary desktop window (the one that hosts the panels and overlays). While an
// overlay is active this lets the window manager see clicks that land on the desktop,
// panels, or overlay content — which otherwise go straight to Fyne — so it can move
// keyboard focus to the desktop window before replaying the click (handleButtonPress).
// It mirrors the click-to-focus grab used on client frames.
func (x *x11WM) grabRootButtons(grab bool) {
	primary := x.RootID()
	if primary == 0 {
		return
	}

	if grab {
		xproto.GrabButton(x.x.Conn(), false, primary,
			xproto.EventMaskButtonPress, xproto.GrabModeSync, xproto.GrabModeAsync,
			x.x.RootWin(), xproto.CursorNone, xproto.ButtonIndex1, xproto.ModMaskAny)
	} else {
		xproto.UngrabButton(x.x.Conn(), xproto.ButtonIndex1, primary, xproto.ModMaskAny)
	}
}

// isRoot reports whether win is one of the desktop (root) windows.
func (x *x11WM) isRoot(win xproto.Window) bool {
	for _, rootID := range x.rootIDs {
		if rootID == win {
			return true
		}
	}
	return false
}

func (x *x11WM) updateFrameInputShapes(overlayActive bool, regions []image.Rectangle) {
	inst := tyde.Instance()
	if inst == nil {
		return
	}

	emptyRect := []xproto.Rectangle{{X: 0, Y: 0, Width: 0, Height: 0}}
	for _, win := range x.clients {
		xwin := win.(x11.XWin)
		frameID := xwin.FrameID()
		fx, fy, fw, fh := xwin.Geometry()

		// Non-primary screens have no panels: accept input across the whole frame.
		// Overlay regions live on the primary screen, so these frames stay interactive.
		screen := inst.Screens().ScreenForWindow(win)
		if screen == nil || screen != inst.Screens().Primary() {
			shape.Mask(x.x.Conn(), shape.SoSet, shape.SkInput, frameID, 0, 0, xproto.PixmapNone)
			continue
		}

		// Base shape: full frame clipped to the desktop content bounds (excludes panels).
		cbX, cbY, cbW, cbH := inst.ContentBoundsPixels(screen)
		cx1 := int(cbX) + screen.X
		cy1 := int(cbY) + screen.Y
		cx2 := cx1 + int(cbW)
		cy2 := cy1 + int(cbH)

		ix1 := max(fx, cx1)
		iy1 := max(fy, cy1)
		ix2 := min(fx+int(fw), cx2)
		iy2 := min(fy+int(fh), cy2)

		var rects []xproto.Rectangle
		if ix1 < ix2 && iy1 < iy2 {
			rects = append(rects, xproto.Rectangle{
				X:      int16(ix1 - fx),
				Y:      int16(iy1 - fy),
				Width:  uint16(ix2 - ix1),
				Height: uint16(iy2 - iy1),
			})
		}
		if len(rects) == 0 {
			rects = emptyRect
		}

		shape.Rectangles(x.x.Conn(), shape.SoSet, shape.SkInput,
			0, frameID, 0, 0, rects)

		if !overlayActive {
			continue
		}

		x.subtractOverlayRegions(frameID, fx, fy, int(fw), int(fh), regions)
	}
}

// subtractOverlayRegions makes the parts of a frame that lie under overlay content
// input-transparent. fx/fy/fw/fh are the frame's screen geometry.
func (x *x11WM) subtractOverlayRegions(frameID xproto.Window, fx, fy, fw, fh int, regions []image.Rectangle) {
	for _, r := range regions {
		sx1 := max(fx, r.Min.X)
		sy1 := max(fy, r.Min.Y)
		sx2 := min(fx+fw, r.Max.X)
		sy2 := min(fy+fh, r.Max.Y)
		if sx1 >= sx2 || sy1 >= sy2 {
			continue
		}

		shape.Rectangles(x.x.Conn(), shape.SoSubtract, shape.SkInput,
			0, frameID, 0, 0, []xproto.Rectangle{{
				X:      int16(sx1 - fx),
				Y:      int16(sy1 - fy),
				Width:  uint16(sx2 - sx1),
				Height: uint16(sy2 - sy1),
			}})
	}
}

// RefreshOverlayShape re-applies the active overlay's input-transparent regions to a
// single frame. This ensures that our overlays keep input over real windows.
func (x *x11WM) RefreshOverlayShape(frameID xproto.Window, fx, fy, fw, fh int) {
	if !x.overlayActive {
		return
	}
	x.subtractOverlayRegions(frameID, fx, fy, fw, fh, x.overlayRegions)
}

// updateRootInputShape sets the input shape on all root windows.
// The primary root accepts input everywhere (for panels and desktop interaction).
// Secondary roots accept no input — they are purely visual; all mouse events
// on secondary screens should go to the X11 frame windows above them.
func (x *x11WM) updateRootInputShape(_ bool) {
	inst := tyde.Instance()
	var primaryName string
	if inst != nil && inst.Screens().Primary() != nil {
		primaryName = inst.Screens().Primary().Name
	}

	emptyRect := []xproto.Rectangle{{X: 0, Y: 0, Width: 0, Height: 0}}
	for name, rootID := range x.rootIDs {
		if name == primaryName {
			// Primary root: accept input everywhere (for bar, widgets, desktop)
			shape.Mask(x.x.Conn(), shape.SoSet, shape.SkInput, rootID, 0, 0, xproto.PixmapNone)
		} else {
			// Secondary roots: no input — frames handle all events
			shape.Rectangles(x.x.Conn(), shape.SoSet, shape.SkInput,
				0, rootID, 0, 0, emptyRect)
		}
	}
}

func (x *x11WM) ShowOverlay(w fyne.Window, s fyne.Size, p fyne.Position) {
	w.SetTitle(windowNameMenu)
	w.SetFixedSize(true)
	w.Resize(s)

	x.menuSize = s
	x.menuPos = p
	w.Show()
}

func (x *x11WM) ShowMenuOverlay(m *fyne.Menu, s fyne.Size, p fyne.Position) {
	win := fyne.CurrentApp().Driver().(deskDriver.Driver).CreateSplashWindow()
	for _, item := range m.Items {
		action := item.Action
		item.Action = func() {
			action()
			win.Close()
		}
	}

	pop := widget.NewPopUpMenu(m, win.Canvas())
	pop.OnDismiss = win.Close
	pop.Show()
	pop.Resize(s)
	x.ShowOverlay(win, s, p)
}

func (x *x11WM) X() *xgbutil.XUtil {
	return x.x
}

func (x *x11WM) bindShortcut(short *tyde.Shortcut, win xproto.Window) {
	mask := x.modifierToKeyMask(short.Modifier)
	code := x.keyNameToCode(short.KeyName)
	if code == 0 {
		return
	}

	xproto.GrabKey(x.x.Conn(), true, win, mask, code, xproto.GrabModeAsync, xproto.GrabModeAsync)
	if mask == xproto.ModMaskAny {
		return // no need for the extra binds
	}
	xproto.GrabKey(x.x.Conn(), true, win, mask|xproto.ModMaskLock, code, xproto.GrabModeAsync, xproto.GrabModeAsync)
	xproto.GrabKey(x.x.Conn(), true, win, mask|xproto.ModMask2, code, xproto.GrabModeAsync, xproto.GrabModeAsync)
	xproto.GrabKey(x.x.Conn(), true, win, mask|xproto.ModMask3, code, xproto.GrabModeAsync, xproto.GrabModeAsync)
}

func (x *x11WM) bindShortcuts(win xproto.Window) {
	if _, ok := tyde.Instance().(wm.ShortcutManager); !ok {
		return
	}

	shortcutList := tyde.Instance().(wm.ShortcutManager).Shortcuts()
	for _, shortcut := range shortcutList {
		x.bindShortcut(shortcut, win)
	}

	if x.currentBindings == nil {
		x.currentBindings = shortcutList
	}
}

func (x *x11WM) keyNameToCode(n fyne.KeyName) xproto.Keycode {
	keybind.Initialize(x.x)
	switch n {
	case fyne.KeySpace:
		return keyCodeSpace
	case fyne.KeyLeft:
		return keyCodeLeft
	case fyne.KeyRight:
		return keyCodeRight
	case fyne.KeyUp:
		return keyCodeUp
	case fyne.KeyDown:
		return keyCodeDown
	case fyne.KeyTab:
		return keyCodeTab
	case fyne.KeyBackTick:
		return keyCodeBacktick
	case deskDriver.KeyPrintScreen:
		return keyCodePrintScreen
	case tyde.KeyBrightnessDown:
		return keyCodeBrightLess
	case tyde.KeyBrightnessUp:
		return keyCodeBrightMore
	case tyde.KeyCalculator:
		return keyCodeCalculator
	case tyde.KeyAssistant:
		// Most keyboard maps have no keysym for this key, fall back to F23.
		if code := x.codeForKeysym("F23"); code != 0 {
			return code
		}
		return keyCodeAssistant
	case tyde.KeyVolumeMute:
		return keyCodeVolumeMute
	case tyde.KeyVolumeDown:
		return keyCodeVolumeLess
	case tyde.KeyVolumeUp:
		return keyCodeVolumeMore
	}

	// Anything else - letters, digits, function keys, punctuation - is looked up by name.
	name := string(n)
	if keysym, ok := keysymNames[n]; ok {
		name = keysym
	}
	return x.codeForKeysym(name)
}

// keysymNames maps the Fyne keys whose names are the character itself onto the
// X keysym names they are known by, which is what the keysym table can look up.
var keysymNames = map[fyne.KeyName]string{
	fyne.KeyApostrophe:   "apostrophe",
	fyne.KeyAsterisk:     "asterisk",
	fyne.KeyBackslash:    "backslash",
	fyne.KeyComma:        "comma",
	fyne.KeyEqual:        "equal",
	fyne.KeyLeftBracket:  "bracketleft",
	fyne.KeyMinus:        "minus",
	fyne.KeyPeriod:       "period",
	fyne.KeyPlus:         "plus",
	fyne.KeyRightBracket: "bracketright",
	fyne.KeySemicolon:    "semicolon",
	fyne.KeySlash:        "slash",
}

// codeForKeysym resolves a keysym name to the keycode carrying it, or 0 when
// this keyboard has no such key.
func (x *x11WM) codeForKeysym(name string) xproto.Keycode {
	codes := keybind.StrToKeycodes(x.x, name)
	if len(codes) == 0 {
		return 0
	}
	return codes[0]
}

func (x *x11WM) modifierToKeyMask(m fyne.KeyModifier) uint16 {
	mask := uint16(0)
	if m&tyde.UserModifier != 0 {
		if tyde.Instance().Settings().KeyboardModifier() == fyne.KeyModifierAlt {
			m |= fyne.KeyModifierAlt
		} else {
			m |= fyne.KeyModifierSuper
		}
	}

	if m&fyne.KeyModifierAlt != 0 {
		mask |= xproto.ModMask1
	}
	if m&fyne.KeyModifierControl != 0 {
		mask |= xproto.ModMaskControl
	}
	if m&fyne.KeyModifierShift != 0 {
		mask |= xproto.ModMaskShift
	}
	if m&fyne.KeyModifierSuper != 0 {
		mask |= xproto.ModMask4
	}

	if mask == 0 {
		return xproto.ModMaskAny
	}
	return mask
}

func (x *x11WM) runLoop() {
	conn := x.x.Conn()

	for {
		ev, err := conn.WaitForEvent()
		if err != nil {
			fyne.LogError("X11 Error:", err)
			continue
		}
		if ev == nil { // disconnected if both are nil
			x.died = true
			break
		}
		switch ev := ev.(type) {
		case xproto.ButtonPressEvent:
			x.handleButtonPress(ev)
		case xproto.ButtonReleaseEvent:
			x.handleButtonRelease(ev)
		case xproto.ClientMessageEvent:
			x.handleClientMessage(ev)
		case xproto.ConfigureNotifyEvent:
			x.notifyConfigure(ev)
		case xproto.ConfigureRequestEvent:
			x.configureWindow(ev.Window, ev)
		case xproto.CreateNotifyEvent:
			x.setInitialWindowAttributes(ev.Window)
		case xproto.DestroyNotifyEvent:
			x.destroyWindow(ev.Window)
		case xproto.EnterNotifyEvent:
			x.handleMouseEnter(ev)
		case xproto.FocusInEvent:
			x.handleFocus(ev.Event)
		case xproto.FocusOutEvent:
			x.handleFocus(ev.Event)
		case xproto.KeyPressEvent:
			x.handleKeyPress(ev)
		case xproto.KeyReleaseEvent:
			x.handleKeyRelease(ev)
		case xproto.LeaveNotifyEvent:
			x.handleMouseLeave(ev)
		case xproto.MapRequestEvent:
			x.showWindow(ev.Window, ev.Parent)
		case xproto.MotionNotifyEvent:
			x.handleMouseMotion(ev)
		case xproto.PropertyNotifyEvent:
			x.handlePropertyChange(ev)
		case randr.ScreenChangeNotifyEvent:
			x.handleScreenChange(ev.Timestamp)
		case xproto.UnmapNotifyEvent:
			x.hideWindow(ev.Window)
		case xproto.VisibilityNotifyEvent:
			x.handleVisibilityChange(ev)
		case screensaver.NotifyEvent:
			// screensaver activate, except we manage it with an internal timer
		}
	}

	fyne.LogError("X11 connection terminated!", nil)
}

func (x *x11WM) configureRoots() {
	if tyde.Instance() == nil {
		return
	}

	x.setupX11DPIHints()
	minX, minY, maxX, maxY := math.MaxInt16, math.MaxInt16, 0, 0
	for _, screen := range tyde.Instance().Screens().Screens() {
		minX = min(minX, screen.X)
		minY = min(minY, screen.Y)
		maxX = max(maxX, screen.X+screen.Width)
		maxY = max(maxY, screen.Y+screen.Height)

		rootID := x.rootIDs[screen.Name]
		if rootID == 0 {
			continue
		}

		// Check if this root is already at the right geometry
		priX, priY, priW, priH := 0, 0, 0, 0
		geom, err := xproto.GetGeometry(x.x.Conn(), xproto.Drawable(rootID)).Reply()
		if err == nil {
			priX, priY = int(geom.X), int(geom.Y)
			priW, priH = int(geom.Width), int(geom.Height)
		}
		if screen.X == priX && screen.Y == priY && screen.Width == priW && screen.Height == priH {
			continue
		}

		notifyEv := xproto.ConfigureNotifyEvent{
			Event: rootID, Window: rootID, AboveSibling: 0,
			X: int16(screen.X), Y: int16(screen.Y), Width: uint16(screen.Width), Height: uint16(screen.Height),
			BorderWidth: 0, OverrideRedirect: false,
		}
		xproto.SendEvent(x.x.Conn(), false, rootID, xproto.EventMaskStructureNotify, string(notifyEv.Bytes()))

		// Trigger a move so that the correct scale is picked up
		xproto.ConfigureWindow(x.x.Conn(), rootID, xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			[]uint32{uint32(screen.X + 1), uint32(screen.Y + 1), uint32(screen.Width - 2), uint32(screen.Height - 2)})

		// Then set the correct location
		xproto.ConfigureWindow(x.x.Conn(), rootID, xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			[]uint32{uint32(screen.X), uint32(screen.Y), uint32(screen.Width), uint32(screen.Height)})
	}

	// Always ensure root windows stay at the bottom of the X11 stack.
	// Fyne/GLFW may re-raise them during window creation or configuration.
	for _, rootID := range x.rootIDs {
		xproto.ConfigureWindow(x.x.Conn(), rootID,
			xproto.ConfigWindowStackMode, []uint32{uint32(xproto.StackModeBelow)})
	}

	rootWidth := maxX - minX
	rootHeight := maxY - minY

	x.updateRootInputShape(false) // reapply input shape after resize

	err := ewmh.DesktopGeometrySet(x.x, &ewmh.DesktopGeometry{Width: rootWidth, Height: rootHeight})
	if err != nil {
		fyne.LogError("", err)
	}

	err = ewmh.WorkareaSet(x.x, []ewmh.Workarea{{X: 0, Y: 0, Width: uint(rootWidth), Height: uint(rootHeight)}})
	if err != nil {
		fyne.LogError("", err)
	}
	go x.updateBackgrounds()
}

func (x *x11WM) notifyConfigure(ev xproto.ConfigureNotifyEvent) {
	if ev.Window == x.x.RootWin() {
		x.configureRoots()
	}
}

func (x *x11WM) configureWindow(win xproto.Window, ev xproto.ConfigureRequestEvent) {
	// Check if this is a root window first — Fyne/GLFW may send configure
	// requests (including restacking) that we must intercept to keep roots below.
	for _, rootID := range x.rootIDs {
		if rootID == win {
			x.configureRoots()
			return
		}
	}

	c := x.clientForWin(win)
	xcoord := ev.X
	ycoord := ev.Y
	width := ev.Width
	height := ev.Height

	if c != nil {
		if c.ChildID() == win { // ignore requests from our frame as we must have caused it
			x, y, w, h := c.Geometry()
			// Per EWMH, fullscreen and maximized windows must keep their
			// WM-imposed geometry — clients are not allowed to resize themselves
			// out of those states via ConfigureRequest. Re-issue the current
			// geometry so the client receives a ConfigureNotify telling it the
			// requested size was not honoured (ICCCM 4.1.5).
			if c.Fullscreened() || c.Maximized() {
				c.NotifyGeometry(x, y, w, h)
				return
			}

			borderWidth := x11.BorderWidth(c)
			titleHeight := x11.TitleHeight(c)

			if c.Properties().Decorated() {
				c.NotifyGeometry(x, y, uint(ev.Width+(borderWidth*2)), uint(ev.Height+borderWidth+titleHeight))
			} else {
				if ev.X == 0 && ev.Y == 0 {
					ev.X = int16(x)
					ev.Y = int16(y)
				}
				c.NotifyGeometry(int(ev.X), int(ev.Y), uint(ev.Width), uint(ev.Height))
			}
		}
		return
	}

	name := x11.WindowName(x.x, win)
	if x.isRootTitle(name) {
		screenName := screenNameFromRootTitle(name)
		x.rootIDs[screenName] = win

		x.configureRoots() // we added a root window, so reconfigure
		return
	}
	if isScreensaverName(name) {
		x.configureSaver(win, &ev) // the saver covers a screen, it does not get to size itself
		return
	}
	xproto.ConfigureWindow(x.x.Conn(), win, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(xcoord), uint32(ycoord), uint32(width), uint32(height)})
}

func (x *x11WM) destroyWindow(win xproto.Window) {
	for name, rootID := range x.rootIDs {
		if rootID == win {
			delete(x.rootIDs, name)
			return
		}
	}

	c := x.clientForWin(win)
	if c == nil {
		// check if it was recently closed
		for i, w := range x.stack.deleted {
			if w.(x11.XWin).FrameID() == win || w.(x11.XWin).ChildID() == win {
				c = w.(x11.XWin)

				x.stack.deleted = append(x.stack.deleted[:i], x.stack.deleted[i+1:]...)
				break
			}
		}
	}
	if c == nil || win == c.FrameID() {
		return
	}
	transient := x11.WindowTransientForGet(x.x, win)
	if transient > 0 && transient != win {
		x.transientChildRemove(transient, win)
	} else if transient > 0 && transient == win {
		x.transientLeaderRemove(transient)
	}
	windowClientListUpdate(x)
	windowClientListStackingUpdate(x)

	c.MarkDestroyed()
	_ = xproto.DestroyWindowChecked(x.x.Conn(), c.FrameID()).Check()
	_ = xproto.DestroyWindowChecked(x.x.Conn(), c.ChildID()).Check()
}

func (x *x11WM) frameExisting() {
	tree, err := xproto.QueryTree(x.x.Conn(), x.x.RootWin()).Reply()
	if err != nil {
		fyne.LogError("Query Tree Error", err)
		return
	}

	for _, child := range tree.Children {
		name := x11.WindowName(x.x, child)
		if x.isRootTitle(name) {
			continue
		}
		// Also skip by window ID — the title may not be set yet
		isRoot := false
		for _, rootID := range x.rootIDs {
			if rootID == child {
				isRoot = true
				break
			}
		}
		if isRoot {
			continue
		}
		attrs, err := xproto.GetWindowAttributes(x.x.Conn(), child).Reply()
		if err != nil {
			fyne.LogError("Get Window Attributes Error", err)
			continue
		}
		if attrs.MapState == xproto.MapStateUnmapped || attrs.OverrideRedirect {
			continue
		}
		x.setupWindow(child)
	}
}

func (x *x11WM) RootID() xproto.Window {
	if tyde.Instance() == nil {
		return 0
	}
	primary := tyde.Instance().Screens().Primary()
	if primary == nil {
		return 0
	}
	return x.rootIDs[primary.Name]
}

func (x *x11WM) RootIDForScreen(screenName string) xproto.Window {
	return x.rootIDs[screenName]
}

func (x *x11WM) NotifyWindowMoved(win tyde.Window) {
	for _, l := range x.listeners {
		fyne.Do(func() {
			l.WindowMoved(win)
		})
	}
}

func (x *x11WM) hideWindow(win xproto.Window) {
	c := x.clientForWin(win)
	if c == nil || win == c.FrameID() {
		return
	}
	xproto.UnmapWindow(x.x.Conn(), c.FrameID())
	c.MarkDestroyed()
	if !c.Iconic() {
		fyne.Do(func() {
			x.RemoveWindow(c)
		})
	}
}

func (x *x11WM) isRootTitle(title string) bool {
	return strings.Index(title, ui.RootWindowName) == 0
}

func (x *x11WM) refreshBorders() {
	for _, c := range x.clients {
		c.(x11.XWin).SettingsChanged()
	}
}

func screenNameFromRootTitle(title string) string {
	if len(title) <= len(ui.RootWindowName) {
		return ""
	}
	return title[len(ui.RootWindowName):]
}

func (x *x11WM) setActiveScreenFromWindow(win tyde.Window) {
	if win == nil || tyde.Instance() == nil {
		return
	}

	windowScreen := tyde.Instance().Screens().ScreenForWindow(win)
	if windowScreen != nil {
		tyde.Instance().Screens().SetActive(windowScreen)
	}
}

func (x *x11WM) setInitialWindowAttributes(win xproto.Window) {
	xproto.ChangeWindowAttributes(x.x.Conn(), win, xproto.CwCursor,
		[]uint32{uint32(x11.DefaultCursor)})
}

func (x *x11WM) setupBindings() {
	tyde.Instance().Settings().AddChangeListener(func(_ tyde.DeskSettings) {
		// this uses the state from the previous bind call
		for _, rootID := range x.rootIDs {
			x.unbindShortcuts(rootID)
		}
		for _, c := range x.clients {
			x.unbindShortcuts(c.(x11.XWin).ChildID())
		}
		x.currentBindings = nil

		// this call sets up the new cache of shortcuts
		for _, rootID := range x.rootIDs {
			x.bindShortcuts(rootID)
		}
		for _, c := range x.clients {
			x.bindShortcuts(c.(x11.XWin).ChildID())
		}

		go x.updateBackgrounds()
	})
}

func (x *x11WM) setupWindow(win xproto.Window) {
	c := x.clientForWin(win)
	if c != nil {
		return
	}
	c = xwin.NewClient(win, x)
	if c == nil {
		return // a previous reported problem occurred framing the window
	}

	x.bindShortcuts(win)
	if x11.WindowName(x.x, win) == "" {
		x11.WindowExtendedHintsAdd(x.x, win, "_NET_WM_STATE_SKIP_TASKBAR")
		x11.WindowExtendedHintsAdd(x.x, win, "_NET_WM_STATE_SKIP_PAGER")
	}
	x.AddWindow(c) // synchronous on the WM goroutine; listener notifications are deferred internally
	c.RaiseToTop()
	// Ensure the frame is above all root windows and has focus.
	// Focus() sends an async client message that GLFW may override, so also
	// raise the frame explicitly here.
	xproto.ConfigureWindow(x.x.Conn(), c.FrameID(),
		xproto.ConfigWindowStackMode, []uint32{uint32(xproto.StackModeAbove)})
	c.Focus()
	xproto.SetInputFocus(x.x.Conn(), xproto.InputFocusPointerRoot,
		c.ChildID(), xproto.TimeCurrentTime)
	windowClientListUpdate(x)
	windowClientListStackingUpdate(x)
}

func (x *x11WM) setupX11DPIHints() {
	// TODO move from global once xrandr --dpi <dpi>/<output> is better supported
	canvasScale := tyde.Instance().Screens().Primary().CanvasScale()
	dpi := int(float32(baselineDPI) * canvasScale)
	cmd := exec.Command("xrandr", "--dpi", strconv.Itoa(dpi))
	_ = cmd.Start() // if it fails that's a shame but it's just info
}

func (x *x11WM) showWindow(win xproto.Window, parent xproto.Window) {
	// If the parent is a frame (not the X root), the window is already
	// reparented — just map it. This avoids re-framing a window when
	// frame.show() maps the client inside a frame with SubstructureRedirect.
	if parent != x.x.RootWin() {
		// If this is a previously-hidden (soft-closed) window being re-shown,
		// the frame was unmapped and the client removed from the live stack
		// in hideWindow. Re-map the frame and restore the stack entry so it
		// becomes visible again and behaves like a normal managed window.
		if c := x.deletedClientForWin(win); c != nil {
			c.Reframe() // rebuilds the frame from scratch — same path NotifyUnIconify uses
			x.restoreWindow(c)
			c.RaiseToTop()
			c.Focus()
			windowClientListUpdate(x)
			windowClientListStackingUpdate(x)
			return
		}
		xproto.MapWindow(x.x.Conn(), win)
		return
	}

	name := x11.WindowName(x.x, win)
	if x.isRootTitle(name) {
		screenName := screenNameFromRootTitle(name)
		x.rootIDs[screenName] = win

		err := xproto.MapWindowChecked(x.x.Conn(), win).Check()
		if err != nil {
			fyne.LogError("Show Window Error", err)
		}
		xproto.ConfigureWindow(x.x.Conn(), win, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeBelow})
		x.configureRoots() // position all roots at their screen geometry immediately
		_ = ewmh.WmWindowTypeSet(x.x, win, []string{windowTypeDesktop})
		x.bindShortcuts(win)

		// Only frame existing windows once ALL root windows have been shown.
		// If we frame too early, secondary root windows may not have their
		// title set yet and would be accidentally framed as client windows.
		expectedRoots := 1
		if inst := tyde.Instance(); inst != nil {
			expectedRoots = len(inst.Screens().Screens())
		}
		if !x.framedExisting && len(x.rootIDs) >= expectedRoots {
			x.framedExisting = true
			go x.frameExisting()
		}
		return
	}
	if name == windowNameMenu {
		x11.WindowExtendedHintsAdd(x.x, win, "_NET_WM_STATE_SKIP_TASKBAR")
		x11.WindowExtendedHintsAdd(x.x, win, "_NET_WM_STATE_SKIP_PAGER")
		xproto.ChangeWindowAttributes(x.Conn(), win, xproto.CwEventMask, []uint32{xproto.EventMaskLeaveWindow})

		screen := tyde.Instance().Screens().Primary()
		w, h := x.menuSize.Width*screen.CanvasScale(), x.menuSize.Height*screen.CanvasScale()
		mx, my := screen.X+int(x.menuPos.X*screen.CanvasScale()), screen.Y+int(x.menuPos.Y*screen.CanvasScale())
		xproto.ConfigureWindow(x.Conn(), win, xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{
			uint32(mx), uint32(my),
			uint32(w), uint32(h),
		})

		x.bindShortcuts(win)
		xproto.MapWindow(x.Conn(), win)
		return
	}

	// A screensaver must cover the whole screen, panels included. Detect it here
	// — by the name the saver sets before mapping.
	if isScreensaverName(name) {
		x.configureSaver(win, nil) // full size before it is mapped, so it never appears small
		xproto.MapWindow(x.x.Conn(), win)
		xproto.ConfigureWindow(x.x.Conn(), win, xproto.ConfigWindowStackMode,
			[]uint32{uint32(xproto.StackModeAbove)})
		xproto.SetInputFocus(x.x.Conn(), xproto.InputFocusPointerRoot, win, xproto.TimeCurrentTime)
		return
	}

	hints, err := icccm.WmHintsGet(x.x, win)
	if err == nil {
		if hints.Flags&icccm.HintState > 0 && hints.InitialState == icccm.StateWithdrawn { // We don't want to manage windows that are not mapped
			return
		}
	}

	override := windowOverrideGet(x.x, win) // We don't want to manage windows that have an override on the WM
	if override {
		return
	}

	winType := windowTypeGet(x.x, win)
	switch winType[len(winType)-1] { // KDE etc put their window types first
	case windowTypeUtility, windowTypeDialog, windowTypeNormal:
		break
	default:
		return
	}

	transient := x11.WindowTransientForGet(x.x, win)
	if transient > 0 && transient != win {
		x.transientChildAdd(transient, win)
	}

	x.setupWindow(win)
}

func (x *x11WM) takeSelectionOwnership() {
	name := "WM_S" + strconv.Itoa(x.x.Conn().DefaultScreen)
	selAtom, err := xprop.Atm(x.x, name)
	if err != nil {
		fyne.LogError("Error getting selection atom", err)
		return
	}
	err = xproto.SetSelectionOwnerChecked(x.x.Conn(), x.x.Dummy(), selAtom, xproto.TimeCurrentTime).Check()
	if err != nil {
		fyne.LogError("Error setting selection owner", err)
		return
	}
	reply, err := xproto.GetSelectionOwner(x.x.Conn(), selAtom).Reply()
	if err != nil {
		fyne.LogError("Error getting selection owner", err)
		return
	}
	if reply.Owner != x.x.Dummy() {
		fyne.LogError("Could not obtain ownership - Another WM is likely running", err)
	}
	manAtom, err := xprop.Atm(x.x, "MANAGER")
	if err != nil {
		fyne.LogError("Error getting manager atom", err)
		return
	}
	cm, err := xevent.NewClientMessage(32, x.x.RootWin(), manAtom,
		xproto.TimeCurrentTime, int(selAtom), int(x.x.Dummy()))
	if err != nil {
		fyne.LogError("Error creating client message", err)
		return
	}
	xproto.SendEvent(x.x.Conn(), false, x.x.RootWin(), xproto.EventMaskStructureNotify,
		string(cm.Bytes()))
}

func (x *x11WM) unbindShortcut(short *tyde.Shortcut, win xproto.Window) {
	mask := x.modifierToKeyMask(short.Modifier)
	code := x.keyNameToCode(short.KeyName)
	if code == 0 {
		return
	}

	xproto.UngrabKey(x.x.Conn(), code, win, mask)
	xproto.UngrabKey(x.x.Conn(), code, win, mask|xproto.ModMaskLock)
	xproto.UngrabKey(x.x.Conn(), code, win, mask|xproto.ModMask2)
	xproto.UngrabKey(x.x.Conn(), code, win, mask|xproto.ModMask3)
}

func (x *x11WM) unbindShortcuts(win xproto.Window) {
	if _, ok := tyde.Instance().(wm.ShortcutManager); !ok {
		return
	}

	for _, shortcut := range x.currentBindings {
		x.unbindShortcut(shortcut, win)
	}
}

func (x *x11WM) updatedBackgroundImage(w, h int) image.Image {
	settings := tyde.Instance().Settings()
	path := settings.Background()
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			fyne.LogError("Failed to open background image", err)
		} else {
			img, _, err := image.Decode(file)
			if err != nil {
				fyne.LogError("Failed to read background image", err)
			} else {
				_ = file.Close()
				return fitBackgroundImage(img, w, h, settings.BackgroundFill(),
					ui.ParseHexColor(settings.BackgroundColor()))
			}
		}
	}

	set := fyne.CurrentApp().Settings()
	b := backgrounds.Default()
	c := software.NewCanvas()
	c.SetContent(b.Load(set.Theme(), set.ThemeVariant()))
	c.SetScale(1.0)
	c.Resize(fyne.NewSize(float32(w), float32(h)))
	return c.Capture()
}

// fitBackgroundImage scales src into a w*h image according to the chosen fill
// mode, painting any uncovered area with the given background colour.
//   - "Fit" preserves aspect ratio and fits the whole image inside the screen.
//   - "Fill" preserves aspect ratio and covers the screen, cropping overflow.
//   - "Stretch" (default) scales to the exact screen size, ignoring aspect.
func fitBackgroundImage(src image.Image, w, h int, fill string, bg color.NRGBA) image.Image {
	if fill != "Fit" && fill != "Fill" {
		return resize.Resize(uint(w), uint(h), src, resize.Lanczos3)
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)

	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return dst
	}

	// Choose the scale that fits inside (min) or covers (max) the screen.
	scaleX := float64(w) / float64(sw)
	scaleY := float64(h) / float64(sh)
	scale := math.Min(scaleX, scaleY)
	if fill == "Fill" {
		scale = math.Max(scaleX, scaleY)
	}

	dw := uint(math.Round(float64(sw) * scale))
	dh := uint(math.Round(float64(sh) * scale))
	scaled := resize.Resize(dw, dh, src, resize.Lanczos3)

	// Centre the scaled image; for "Fill" the overflow is cropped by the draw.
	offset := image.Pt((w-int(dw))/2, (h-int(dh))/2)
	draw.Draw(dst, scaled.Bounds().Add(offset), scaled, scaled.Bounds().Min, draw.Over)
	return dst
}

func (x *x11WM) updateBackgrounds() {
	geom, err := xproto.GetGeometry(x.x.Conn(), xproto.Drawable(x.x.RootWin())).Reply()
	if err != nil {
		fyne.LogError("Unable to look up root geometry", err)
		return
	}
	root := xgraphics.New(x.x, image.Rect(0, 0, int(geom.Width), int(geom.Height)))

	for _, screen := range tyde.Instance().Screens().Screens() {
		scaled := x.updatedBackgroundImage(screen.Width, screen.Height)
		for y := screen.Y; y < screen.Y+screen.Height; y++ {
			for x := screen.X; x < screen.X+screen.Width; x++ {
				root.Set(x, y, scaled.At(x-screen.X, y-screen.Y))
			}
		}
	}

	err = root.XSurfaceSet(x.x.RootWin())
	if err != nil {
		fyne.LogError("", err)
	}
	root.XDraw()
	root.XPaint(x.x.RootWin())

	if x.oldRoot != nil {
		x.oldRoot.Destroy()
		x.oldRoot = nil
	}

	err = xprop.ChangeProp32(x.x, x.x.RootWin(), "_XROOTPMAP_ID", "PIXMAP", uint(root.Pixmap))
	if err != nil {
		fyne.LogError("rootprop", err)
	}
	err = xprop.ChangeProp32(x.x, x.x.RootWin(), "ESETROOT_PMAP_ID", "PIXMAP", uint(root.Pixmap))
	if err != nil {
		fyne.LogError("esetrootprop", err)
	}

	// save root so we can free it later if not needed
	x.oldRoot = root
}

func max(a, b int) int {
	if a > b {
		return a
	}

	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}

	return b
}
