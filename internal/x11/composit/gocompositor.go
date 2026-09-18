//go:build linux || openbsd || freebsd || netbsd
// +build linux openbsd freebsd netbsd

// Package composit provides an X11 compositor that captures window content
// and displays it as Fyne canvas images in the desktop window.
// Based on https://github.com/bvkgo/gcompositor and https://github.com/jmanc3/xcompmgr-simple/.
package composit

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"github.com/FyshOS/saver"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/composite"
	"github.com/BurntSushi/xgb/damage"
	"github.com/BurntSushi/xgb/shape"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgbutil"

	"fyshos.com/tyde"
	"fyshos.com/tyde/internal/ui"
	"fyshos.com/tyde/internal/x11"
)

type opaqueType string

type client struct {
	win          xproto.Window
	opacity      uint32
	opacitySet   bool // true if the window has an explicit _NET_WM_WINDOW_OPACITY value
	opaqueType   opaqueType
	damaged      bool
	skipped      bool // desktop root window or other skipped windows
	fullscreened bool // unredirected for fullscreen bypass
	visualMoving bool // position being managed by VisualMoveCallback (drag/animation)
	pending      bool // true = a refresh was requested, awaiting render
	priority     bool // newly mapped: capture and paint ahead of the bulk re-capture

	geom       xproto.GetGeometryReply
	attributes xproto.GetWindowAttributesReply
	damage     damage.Damage
	pixmap     xproto.Pixmap // cached NameWindowPixmap

	// Double-buffered capture: the compositor writes to bufs[writeIdx] and
	// toggles writeIdx on each successful push so that the buffer being
	// displayed by the renderer is never mutated.
	bufs     [2]*image.RGBA
	writeIdx int
}

var (
	defaultScreen int
	xu            *xgbutil.XUtil
	rootWindow    xproto.Window
	rootWidth     uint16
	rootHeight    uint16
	allDamage     bool
	clients       []*client

	opacityAtom    xproto.Atom
	decorationAtom xproto.Atom
	netWmNameAtom  xproto.Atom
	netWmStateAtom xproto.Atom
	utf8StringAtom xproto.Atom
	atomAtom       xproto.Atom
	wmNameAtom     xproto.Atom
	stringAtom     xproto.Atom

	windowTypeAtom xproto.Atom
	tooltipAtom    xproto.Atom
	popupMenuAtom  xproto.Atom
	dropdownAtom   xproto.Atom
	comboAtom      xproto.Atom
)

const (
	solid       opaqueType = ""
	transparent opaqueType = "TRANSPARENT"
	argb        opaqueType = "ARGB"

	opaque = math.MaxUint32

	// backgroundDim is the translucency applied to background windows that have no explicit opacity.
	// (see win.defaultBackgroundTransparency).
	backgroundDim = 0.1
)

type cookieReply[R any] interface {
	Reply() (R, error)
}

func initExtension[S any, T cookieReply[S]](conn *xgb.Conn, initFunc func(conn *xgb.Conn) error,
	verFunc func(*xgb.Conn, uint32, uint32) T, major, minor uint32,
) error {
	if err := initFunc(conn); err != nil {
		return err
	}
	_, err := verFunc(conn, major, minor).Reply()
	return err
}

func setup(conn *xgb.Conn) error {
	if err := initExtension[*composite.QueryVersionReply, composite.QueryVersionCookie](conn, composite.Init, composite.QueryVersion, 0, 2); err != nil {
		return err
	}
	if err := initExtension[*damage.QueryVersionReply, damage.QueryVersionCookie](conn, damage.Init, damage.QueryVersion, 1, 1); err != nil {
		return err
	}
	if err := shape.Init(conn); err != nil {
		return err
	}
	if _, err := shape.QueryVersion(conn).Reply(); err != nil {
		return err
	}

	err := setupRoot(conn)
	if err != nil {
		return err
	}
	if err = registerManager(conn, defaultScreen); err != nil {
		return err
	}

	err = composite.RedirectSubwindowsChecked(conn, rootWindow, composite.RedirectManual).Check()
	if err != nil {
		return err
	}

	mask := []uint32{
		xproto.EventMaskSubstructureNotify |
			xproto.EventMaskExposure |
			xproto.EventMaskStructureNotify |
			xproto.EventMaskPropertyChange,
	}
	if err = xproto.ChangeWindowAttributesChecked(conn, rootWindow, xproto.CwEventMask, mask).Check(); err != nil {
		return err
	}

	name := "_NET_WM_WINDOW_OPACITY"
	opacityAtomReply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return err
	}
	opacityAtom = opacityAtomReply.Atom

	decorationAtomReply, err := xproto.InternAtom(conn, false, uint16(len(x11.DecorationProperty)), x11.DecorationProperty).Reply()
	if err != nil {
		return err
	}
	decorationAtom = decorationAtomReply.Atom

	stateAtomName := "_NET_WM_STATE"
	stateAtomReply, err := xproto.InternAtom(conn, false, uint16(len(stateAtomName)), stateAtomName).Reply()
	if err != nil {
		return err
	}
	netWmStateAtom = stateAtomReply.Atom

	// Cache window type atoms for overlay detection (tooltips, popups, etc.)
	typeNames := []struct {
		name string
		dest *xproto.Atom
	}{
		{"_NET_WM_WINDOW_TYPE", &windowTypeAtom},
		{"_NET_WM_WINDOW_TYPE_TOOLTIP", &tooltipAtom},
		{"_NET_WM_WINDOW_TYPE_POPUP_MENU", &popupMenuAtom},
		{"_NET_WM_WINDOW_TYPE_DROPDOWN_MENU", &dropdownAtom},
		{"_NET_WM_WINDOW_TYPE_COMBO", &comboAtom},
	}
	for _, tn := range typeNames {
		reply, err := xproto.InternAtom(conn, false, uint16(len(tn.name)), tn.name).Reply()
		if err != nil {
			return err
		}
		*tn.dest = reply.Atom
	}

	return nil
}

// Run starts the X11 compositor event loop. It captures window content and
// renders it into per-screen widgets. The normal widget shows regular windows;
// the overlay widget shows fullscreen windows above desktop chrome.
// Run blocks until done is closed.
//
//gocyclo:ignore
func Run(done chan struct{}, screenComps []ui.ScreenCompositors) error {
	c, err := xgbutil.NewConn()
	if err != nil {
		return err
	}
	xu = c

	conn := c.Conn()
	defer conn.Close()

	ws := &widgets{}
	for _, sc := range screenComps {
		ws.screens = append(ws.screens, screenWidgets{
			screen:  sc.Screen,
			normal:  sc.Normal,
			overlay: sc.Overlay,
		})
	}

	// Receive runtime screen-set changes from the desktop. The list is stashed
	// and applied from the event loop below so ws.screens stays single-writer.
	ui.CompositorScreensChanged = ws.setPending
	defer func() { ui.CompositorScreensChanged = nil }()

	// Let the desktop synthesise the cube's rolling face for a desktop that has
	// never been on screen by reading its windows' pixmaps directly.
	ui.CompositorWindowSnapshot = func(screen *tyde.Screen, offsetY int) image.Image {
		return snapshotWindows(conn, screen, offsetY)
	}
	defer func() { ui.CompositorWindowSnapshot = nil }()

	// Let modules' window accessories (e.g. desktop pets) be re-assembled onto
	// the compositor. Invoked on the main thread by Desktop.RefreshWindowAccessories.
	ui.AccessoryRefresher = func() { rebuildAccessories(ws) }

	// Set up visual move callback for fast drag repositioning.
	// Called from the main thread (fyne.Do context) so no additional queueing needed.
	x11.VisualMoveCallback = func(winID uint32, absX, absY int16, width, height uint16) {
		win := xproto.Window(winID)

		// Mark the client as visually moving so refreshWindows skips recapture
		if c := getClientFromWindow(win); c != nil {
			c.visualMoving = true
		}

		// Update position on all screens that have a cached entry, and
		// add to new screens if the window now overlaps them.
		for i := range ws.screens {
			sw := &ws.screens[i]
			localX := absX - int16(sw.screen.X)
			localY := absY - int16(sw.screen.Y)

			// Always update position for existing cached entries so
			// windows animate smoothly even as they leave the screen.
			for _, target := range []*ui.CompositorWidget{sw.normal, sw.overlay} {
				wi := target.GetWindow(winID)
				if wi == nil {
					continue
				}
				wi.X = localX
				wi.Y = localY
				wi.W = width
				wi.H = height
				target.PlaceWindow(wi) // carries the window's accessories along
				// A cached entry can be blank: it was created before this window had been
				// captured on any screen.
				if wi.Img.Image == nil {
					copyImageFromOtherScreen(ws, winID, wi, sw)
					if wi.Img.Image != nil {
						wi.Img.Refresh()
						target.Refresh()
					}
				}
			}

			// If the window newly overlaps this screen and has no entry, create one.
			if intersectsScreen(absX, absY, width, height, sw.screen) {
				if sw.normal.GetWindow(winID) == nil && sw.overlay.GetWindow(winID) == nil {
					wi := sw.normal.EnsureWindow(winID)
					copyImageFromOtherScreen(ws, winID, wi, sw)
					wi.X = localX
					wi.Y = localY
					wi.W = width
					wi.H = height
					sw.normal.PlaceWindow(wi)
					sw.normal.Refresh()
				}
			}
		}
	}

	err = setup(conn)
	if err != nil {
		return err
	}

	// Wait for the WM to finish framing existing windows, then scan the
	// tree once in its settled state. This avoids the race between the WM's
	// async framing and the compositor's initial scan that causes flicker.
	time.Sleep(200 * time.Millisecond)

	// Drain events that accumulated during the wait
	conn.Sync()
	for {
		ev, err := conn.PollForEvent()
		if ev == nil && err == nil {
			break
		}
		_ = err
	}

	// Scan the tree in its final state — all windows are now framed
	tree, err := xproto.QueryTree(conn, rootWindow).Reply()
	if err != nil {
		return err
	}
	for _, child := range tree.Children {
		_ = addClient(conn, child)
	}

	// Populate widgets from the settled clients list
	for _, c := range clients {
		if c.skipped || c.attributes.MapState != xproto.MapStateViewable {
			continue
		}
		if isFullscreenClient(c) {
			updateFullscreen(conn, ws, c)
		} else {
			ensureWindowOnScreens(ws, c)
		}
	}
	syncOrder(ws)
	ws.refreshAll()

	// Initial capture of all visible windows
	refreshWindows(conn, ws)

	// Ensure the top window has focus after compositor settles
	if inst := tyde.Instance(); inst != nil {
		if top := inst.WindowManager().TopWindow(); top != nil {
			top.Focus()
		}
	}

	for {
		select {
		case <-done:
			return nil
		default:
		}

		// Block until at least one event arrives.
		ev, err := conn.WaitForEvent()
		var repaint bool

		if err != nil {
			var badDamageError damage.BadDamageError
			if errors.As(err, &badDamageError) {
				repaint = true
			} else {
				fyne.LogError("error waiting for event", err)
			}
		}

		// Process the first event and then drain all queued events so
		// that multiple damage notifications are coalesced into a
		// single capture pass.
		for {
			if ev != nil {
				switch e := ev.(type) {
				case xproto.CreateNotifyEvent:
					if err := addClient(conn, e.Window); err != nil {
						fyne.LogError("failed to add client", err)
						repaint = true
					}
				case xproto.ConfigureNotifyEvent:
					if err := configureClient(conn, ws, e); err != nil {
						fyne.LogError("failed to configure client", err)
						repaint = true
					}
				case xproto.DestroyNotifyEvent:
					destroyWin(conn, ws, e.Window)
				case xproto.MapNotifyEvent:
					if err := mapWin(conn, ws, e.Window); err != nil {
						fyne.LogError("failed to map window", err)
						repaint = true
					}
				case xproto.UnmapNotifyEvent:
					unmapWin(conn, ws, e.Window)
				case xproto.ReparentNotifyEvent:
					if e.Parent == rootWindow {
						if err := addClient(conn, e.Window); err != nil {
							fyne.LogError("failed to add client", err)
							repaint = true
						}
					} else {
						destroyWin(conn, ws, e.Window)
					}
				case xproto.CirculateNotifyEvent:
					circulateClient(ws, e)
				case xproto.PropertyNotifyEvent:
					if e.Atom == opacityAtom {
						if c := getClientFromWindow(e.Window); c != nil {
							updateOpacity(conn, 1, c)
							allDamage = true
						}
					}
					if e.Atom == decorationAtom { // the WM has a new frame to paint
						if c := getClientFromWindow(e.Window); c != nil {
							c.damaged = true
							allDamage = true
						}
					}
					if e.Atom == netWmStateAtom {
						if cl := getClientFromWindow(e.Window); cl != nil {
							updateFullscreen(conn, ws, cl)
						}
					}
				case damage.NotifyEvent:
					if err := damageClient(conn, &e); err != nil {
						fyne.LogError("failed to send damage notify", err)
						repaint = true
					}
				}
			}

			// Poll for more events without blocking.
			ev, err = conn.PollForEvent()
			if err != nil {
				var badDamageError damage.BadDamageError
				if errors.As(err, &badDamageError) {
					repaint = true
				}
			}
			if ev == nil && err == nil {
				break // queue drained
			}
		}

		// Pick up any screen-set change handed in from the desktop. A new
		// screen always brings X events (its root window is mapped) so the
		// loop wakes promptly; applyPending repopulates it on its own.
		if ws.applyPending(conn) {
			repaint = true
		}

		if allDamage || repaint {
			refreshTranslucency(conn, ws)
			refreshWindows(conn, ws)
			allDamage = false
		}
	}
}

func registerManager(conn *xgb.Conn, screen int) error {
	atomName := fmt.Sprintf("_NET_WM_CM_S%d", screen)
	atom, err := xproto.InternAtom(conn, false, uint16(len(atomName)), atomName).Reply()
	if err != nil {
		return err
	}
	owner, err := xproto.GetSelectionOwner(conn, atom.Atom).Reply()
	if err != nil {
		return err
	}
	if owner.Owner != 0 {
		log.Printf("Another composite manager is running")
		return fmt.Errorf("another composite manager is running: %w", os.ErrExist)
	}
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		return err
	}
	err = xproto.CreateWindowChecked(conn, xproto.WindowClassCopyFromParent, win, rootWindow,
		0, 0, 1, 1, 0, xproto.WindowClassInputOutput, 0, 0, nil).Check()
	if err != nil {
		return err
	}
	err = xproto.SetSelectionOwnerChecked(conn, win, atom.Atom, xproto.TimeCurrentTime).Check()
	if err != nil {
		return err
	}
	return nil
}

func setupRoot(conn *xgb.Conn) error {
	screen := xproto.Setup(conn).DefaultScreen(conn)
	defaultScreen = conn.DefaultScreen
	rootWindow = screen.Root
	rootWidth = screen.WidthInPixels
	rootHeight = screen.HeightInPixels
	return nil
}

// screenWidgets holds the compositor widgets for a single screen.
type screenWidgets struct {
	screen  *tyde.Screen
	normal  *ui.CompositorWidget
	overlay *ui.CompositorWidget
}

// widgets holds per-screen compositor widget pairs.
type widgets struct {
	screens []screenWidgets

	// pending holds a screen-widget list handed in from the desktop's main
	// goroutine when screens change at runtime. It is applied from the
	// compositor event loop (applyPending) so ws.screens is only ever mutated
	// by the event-loop goroutine.
	mu         sync.Mutex
	pending    []ui.ScreenCompositors
	hasPending bool
}

// setPending records a new screen-widget list to be applied by the event loop.
// Safe to call from any goroutine.
func (ws *widgets) setPending(comps []ui.ScreenCompositors) {
	ws.mu.Lock()
	ws.pending = comps
	ws.hasPending = true
	ws.mu.Unlock()
}

// applyPending reconciles ws.screens with the latest list from the desktop and
// returns true if the set changed. Existing screens reuse their widgets (so
// cached window images survive); newly connected screens are repopulated with
// every currently visible client. Must run on the event-loop goroutine.
func (ws *widgets) applyPending(conn *xgb.Conn) bool {
	ws.mu.Lock()
	if !ws.hasPending {
		ws.mu.Unlock()
		return false
	}
	comps := ws.pending
	ws.pending = nil
	ws.hasPending = false
	ws.mu.Unlock()

	known := make(map[string]bool, len(ws.screens))
	for _, sw := range ws.screens {
		known[sw.screen.Name] = true
	}

	var next []screenWidgets
	var added []*screenWidgets
	for _, sc := range comps {
		next = append(next, screenWidgets{screen: sc.Screen, normal: sc.Normal, overlay: sc.Overlay})
		if !known[sc.Screen.Name] {
			added = append(added, &next[len(next)-1])
		}
	}
	ws.screens = next

	if len(added) == 0 {
		return true
	}

	// Populate freshly connected screens with the windows already on display
	// and force a recapture so their images land in the new widgets.
	for _, c := range clients {
		if c.skipped || c.attributes.MapState != xproto.MapStateViewable {
			continue
		}
		if isFullscreenClient(c) {
			updateFullscreen(conn, ws, c)
		} else {
			ensureWindowOnScreens(ws, c)
		}
		c.damaged = true
	}
	syncOrder(ws)
	refreshWindows(conn, ws)
	ws.refreshAll()
	return true
}

// targetFor returns the appropriate widget type name for a client based on fullscreen state.
func isFullscreenClient(c *client) bool {
	return checkFullscreen(c)
}

// screensForClient returns the screen widgets whose screens overlap the client's geometry.
func (ws *widgets) screensForClient(c *client) []*screenWidgets {
	var result []*screenWidgets
	for i := range ws.screens {
		if intersectsScreen(c.geom.X, c.geom.Y,
			c.geom.Width+c.geom.BorderWidth*2, c.geom.Height+c.geom.BorderWidth*2,
			ws.screens[i].screen) {
			result = append(result, &ws.screens[i])
		}
	}
	if len(result) == 0 && len(ws.screens) > 0 {
		// Fallback: assign to nearest screen (use ScreenForGeometry)
		inst := tyde.Instance()
		if inst != nil {
			s := inst.Screens().ScreenForGeometry(int(c.geom.X), int(c.geom.Y),
				int(c.geom.Width), int(c.geom.Height))
			for i := range ws.screens {
				if ws.screens[i].screen == s {
					result = append(result, &ws.screens[i])
					break
				}
			}
		}
	}
	return result
}

// refreshAll refreshes all screen widgets via fyne.Do.
func (ws *widgets) refreshAll() {
	fyne.Do(func() {
		for i := range ws.screens {
			ws.screens[i].normal.Refresh()
			ws.screens[i].overlay.Refresh()
		}
	})
}

// rebuildAccessories pulls WindowAccessory items from the enabled modules and
// stacks them onto the primary screen's normal compositor at the z-level of the
// window each one sits on (a nil window draws above all windows). It must run on
// the main (fyne.Do) goroutine; the installed AccessoryRefresher is always
// invoked from there.
func rebuildAccessories(ws *widgets) {
	inst := tyde.Instance()
	if inst == nil {
		return
	}

	byWindow := map[uint32][]fyne.CanvasObject{}
	var top []fyne.CanvasObject
	for _, m := range inst.Modules() {
		am, ok := m.(tyde.WindowAccessoryModule)
		if !ok {
			continue
		}
		for _, acc := range am.WindowAccessories() {
			if acc.Object == nil {
				continue
			}
			if xw, ok := acc.Window.(x11.XWin); ok {
				id := uint32(xw.FrameID())
				byWindow[id] = append(byWindow[id], acc.Object)
			} else {
				top = append(top, acc.Object) // nil/unknown window => above everything
			}
		}
	}

	var primaryName string
	if p := inst.Screens().Primary(); p != nil {
		primaryName = p.Name
	}
	for i := range ws.screens {
		sw := &ws.screens[i]
		if sw.screen != nil && sw.screen.Name == primaryName {
			sw.normal.SetAccessories(byWindow, top)
			sw.normal.Refresh()
		} else {
			sw.normal.SetAccessories(nil, nil)
		}
	}
}

// intersectsScreen returns whether a rectangle overlaps a screen.
func intersectsScreen(x, y int16, w, h uint16, screen *tyde.Screen) bool {
	return int(x) < screen.X+screen.Width &&
		int(x)+int(w) > screen.X &&
		int(y) < screen.Y+screen.Height &&
		int(y)+int(h) > screen.Y
}

// copyImageFromOtherScreen copies the canvas.Image data from another screen's
// widget entry for the same window ID into the target WindowImage.
func copyImageFromOtherScreen(ws *widgets, winID uint32, target *ui.WindowImage, exclude *screenWidgets) {
	for i := range ws.screens {
		sw := &ws.screens[i]
		if sw == exclude {
			continue
		}
		for _, w := range []*ui.CompositorWidget{sw.normal, sw.overlay} {
			src := w.GetWindow(winID)
			if src == nil {
				continue
			}
			// Prefer the displayed image, but fall back to the latest captured buffer.
			img := src.Img.Image
			if img == nil {
				if back := src.Back.Load(); back != nil {
					img, _ = back.(image.Image)
				}
			}
			if img != nil {
				target.Img.Image = img
				target.Img.Translucency = src.Img.Translucency
				return
			}
		}
	}
}

// wmWindow returns the WM's Window for a compositor client, or nil.
func wmWindow(c *client) tyde.Window {
	inst := tyde.Instance()
	if inst == nil || inst.WindowManager() == nil {
		return nil
	}
	for _, w := range inst.WindowManager().Windows() {
		xw, ok := w.(x11.XWin)
		if ok && xw.FrameID() == c.win {
			return w
		}
	}
	return nil
}

// checkFullscreen returns whether the WM considers this window fullscreen.
func checkFullscreen(c *client) bool {
	w := wmWindow(c)
	return w != nil && w.Fullscreened()
}

// updateFullscreen checks whether a client's fullscreen state changed and
// unredirects/redirects the window so that fullscreen windows bypass compositing.
func updateFullscreen(conn *xgb.Conn, ws *widgets, c *client) {
	if c.skipped {
		return // root window or screensaver — don't touch
	}

	winID := uint32(c.win)
	isFS := isFullscreenClient(c)

	if isFS && !c.fullscreened {
		// Unredirect: let X11 display the window directly, bypassing compositing.
		removeWindowFromAllScreens(ws, winID)
		freeClientPixmap(conn, c)
		if c.damage != 0 {
			_ = damage.Destroy(conn, c.damage)
			c.damage = 0
		}
		_ = composite.UnredirectWindowChecked(conn, c.win, composite.RedirectManual).Check()
		c.fullscreened = true
	} else if !isFS && c.fullscreened {
		// Re-redirect: bring the window back under compositing.
		_ = composite.RedirectWindowChecked(conn, c.win, composite.RedirectManual).Check()
		c.fullscreened = false
		if c.damage == 0 {
			dmg, err := damage.NewDamageId(conn)
			if err == nil {
				if err = damage.CreateChecked(conn, dmg, xproto.Drawable(c.win), damage.ReportLevelNonEmpty).Check(); err == nil {
					c.damage = dmg
				}
			}
		}
		ensureWindowOnScreens(ws, c)
		c.damaged = true
		allDamage = true
		syncOrder(ws)
		ws.refreshAll()
	}
}

// syncOrder rebuilds the image ordering in all screen widgets to match the clients list.
func syncOrder(ws *widgets) {
	order := make([]uint32, len(clients))
	for i, c := range clients {
		order[i] = uint32(c.win)
	}
	for i := range ws.screens {
		ws.screens[i].normal.Reorder(order)
		ws.screens[i].overlay.Reorder(order)
	}
}

// ensureWindowOnScreens adds a window to all screen widgets that overlap its geometry.
func ensureWindowOnScreens(ws *widgets, c *client) {
	winID := uint32(c.win)
	isFS := isFullscreenClient(c)
	for _, sw := range ws.screensForClient(c) {
		var wi *ui.WindowImage
		if isFS {
			wi = sw.overlay.EnsureWindow(winID)
		} else {
			wi = sw.normal.EnsureWindow(winID)
		}
		// A freshly created entry has no image yet. Seed it from a screen that
		// already shows this window.
		if wi != nil && wi.Img.Image == nil {
			copyImageFromOtherScreen(ws, winID, wi, sw)
		}
	}
}

// removeWindowFromAllScreens removes a window from all screen widgets.
func removeWindowFromAllScreens(ws *widgets, winID uint32) {
	for i := range ws.screens {
		ws.screens[i].normal.RemoveWindow(winID)
		ws.screens[i].overlay.RemoveWindow(winID)
	}
}

// refreshWindows captures damaged windows and pushes frames to back buffers.
// Every damaged window is captured; the back buffer is always updated so the
// renderer sees the latest frame. A widget refresh is only requested when no
// previous refresh is pending — the pending refresh will pick up whatever is
// in the back buffer when it runs, so the last frame is never lost.
func refreshWindows(conn *xgb.Conn, ws *widgets) {
	refreshed := make(map[*ui.CompositorWidget]bool)

	// Pass 1: newly-mapped ("priority") windows first, painted immediately, so a
	// window opened right after wake appears without waiting for the bulk
	// re-capture of every previously-open window — which can take seconds when
	// everything is damaged at once, as happens on resume from sleep.
	hadPriority := false
	for _, c := range clients {
		if !c.priority {
			continue
		}
		c.priority = false
		if captureClient(conn, ws, c, refreshed) {
			hadPriority = true
		}
	}
	if hadPriority {
		flushRefresh(refreshed)
	}

	// Pass 2: everything else, batched into a single refresh at the end. Windows
	// captured in pass 1 are no longer damaged, so they are skipped here.
	for _, c := range clients {
		captureClient(conn, ws, c, refreshed)
	}
	flushRefresh(refreshed)
}

// captureClient grabs window c's current pixmap into its back buffer and marks
// the affected compositor widgets for refresh (via flushRefresh). It returns
// whether a capture happened.
func captureClient(conn *xgb.Conn, ws *widgets, c *client, refreshed map[*ui.CompositorWidget]bool) bool {
	if !c.damaged || c.skipped || c.fullscreened || c.visualMoving {
		return false
	}
	if c.attributes.MapState != xproto.MapStateViewable {
		return false
	}
	if c.geom.X+int16(c.geom.Width) < 1 || c.geom.Y+int16(c.geom.Height) < 1 ||
		c.geom.X >= int16(rootWidth) || c.geom.Y >= int16(rootHeight) {
		return false
	}

	// Ensure we have a named pixmap
	if c.pixmap == 0 {
		pixmap, err := xproto.NewPixmapId(conn)
		if err != nil {
			return false
		}
		if err = composite.NameWindowPixmapChecked(conn, c.win, pixmap).Check(); err != nil {
			return false
		}
		c.pixmap = pixmap
	}

	winID, isFullScreen, pending := checkPending(c, ws)

	totalW := c.geom.Width + c.geom.BorderWidth*2
	totalH := c.geom.Height + c.geom.BorderWidth*2
	isARGB := c.opaqueType == argb

	// When a refresh is pending the renderer may read the previous
	// write buffer via Back, so use a fresh allocation to avoid a race.
	buf := c.bufs[c.writeIdx]
	if pending {
		buf = nil
	}
	img := capturePixmap(conn, xproto.Drawable(c.pixmap), totalW, totalH, isARGB, buf)
	if img == nil {
		return false
	}
	c.damaged = false

	if !pending {
		c.bufs[c.writeIdx] = img
		c.writeIdx = 1 - c.writeIdx
	}

	w := wmWindow(c)
	if xw, ok := w.(x11.XWin); ok {
		xw.Decorate(img) // the WM paints the frame over its own border pixels
	}
	radius := float32(0)
	fyne.DoAndWait(func() {
		radius = theme.Size(theme.SizeNameInnerWindowRadius)
	})
	if w == nil || (!w.Fullscreened() && !w.Maximized()) {
		scale := float32(1)
		if len(ws.screens) > 0 {
			scale = ws.screens[0].screen.CanvasScale()
		}
		roundCorners(img, int(radius*scale))
	}

	translucency := computeTranslucency(conn, c)

	// Push to back buffer on all screen widgets for this window.
	// The renderer will swap Back→Img.Image on its next Refresh.
	for _, sw := range ws.screensForClient(c) {
		var target *ui.CompositorWidget
		if isFullScreen {
			target = sw.overlay
		} else {
			target = sw.normal
		}
		wi := target.GetWindow(winID)
		if wi == nil {
			continue
		}

		wi.Back.Store(img)
		wi.Img.Translucency = translucency

		localX := c.geom.X - int16(sw.screen.X)
		localY := c.geom.Y - int16(sw.screen.Y)

		if wi.W == 0 {
			wi.X = localX
			wi.Y = localY
			wi.W = totalW
			wi.H = totalH
			fyne.Do(func() {
				target.PlaceWindow(wi)
			})
		}

		// Only request a refresh when none is pending — the pending
		// refresh reads Back at render time so it always gets the
		// latest frame.
		if !pending {
			wi.Pending.Store(true)
			refreshed[target] = true
		}
	}
	if !pending {
		c.pending = true
	}
	return true
}

// flushRefresh issues a single widget-level refresh for each compositor marked
// in refreshed, then clears the map so it can be reused for a later pass.
func flushRefresh(refreshed map[*ui.CompositorWidget]bool) {
	if len(refreshed) == 0 {
		return
	}
	targets := make([]*ui.CompositorWidget, 0, len(refreshed))
	for target := range refreshed {
		targets = append(targets, target)
		delete(refreshed, target)
	}
	fyne.Do(func() {
		for _, target := range targets {
			target.Refresh()
		}
	})
}

// snapshotWindows composes the windows of the desktop offsetY pixels from the
// current viewport into a transparent RGBA image the size of the screen, by
// reading each mapped client's content pixmap directly. offsetY is the same
// pixel slide SetDesktop applies when switching (negative to reveal a desktop
// further down). Because every mapped window keeps a composite-redirected
// pixmap regardless of position, this works even for a desktop that has never
// been on screen — which is exactly when the cube has no live capture to roll
// in. Pinned windows live on every desktop, so they are drawn at their current
// position without the offset. The bar, widget panel and wallpaper are the
// caller's concern; this returns windows on a transparent background.
//
// Called on the Fyne main goroutine via the ui.CompositorWindowSnapshot hook.
// The clients slice is copied first so a concurrent add/remove in the event
// loop can't corrupt the iteration; reads of per-client geometry remain
// best-effort, matching the existing main-thread access in VisualMoveCallback.
func snapshotWindows(conn *xgb.Conn, screen *tyde.Screen, offsetY int) image.Image {
	if conn == nil || screen == nil || screen.Width <= 0 || screen.Height <= 0 {
		return nil
	}

	cs := make([]*client, len(clients))
	copy(cs, clients)

	out := image.NewRGBA(image.Rect(0, 0, screen.Width, screen.Height))
	scale := screen.CanvasScale()

	// clients is top-first, so draw back-to-front for correct stacking.
	for i := len(cs) - 1; i >= 0; i-- {
		c := cs[i]
		if c == nil || c.skipped || c.fullscreened {
			continue
		}
		if c.attributes.MapState != xproto.MapStateViewable {
			continue
		}

		off := offsetY
		w := wmWindow(c)
		if w != nil && w.Pinned() {
			off = 0 // pinned windows appear on every desktop
		}

		if c.pixmap == 0 {
			pixmap, err := xproto.NewPixmapId(conn)
			if err != nil {
				continue
			}
			if err = composite.NameWindowPixmapChecked(conn, c.win, pixmap).Check(); err != nil {
				continue
			}
			c.pixmap = pixmap
		}

		totalW := c.geom.Width + c.geom.BorderWidth*2
		totalH := c.geom.Height + c.geom.BorderWidth*2
		img := capturePixmap(conn, xproto.Drawable(c.pixmap), totalW, totalH, c.opaqueType == argb, nil)
		if img == nil {
			continue
		}
		if xw, ok := w.(x11.XWin); ok {
			xw.Decorate(img)
		}
		if w == nil || (!w.Fullscreened() && !w.Maximized()) {
			roundCorners(img, int(theme.Size(theme.SizeNameInnerWindowRadius)*scale))
		}

		dstX := int(c.geom.X) - screen.X
		dstY := int(c.geom.Y) - screen.Y + off
		draw.Draw(out, image.Rect(dstX, dstY, dstX+int(totalW), dstY+int(totalH)),
			img, image.Point{}, draw.Over)
	}
	return out
}

func checkPending(c *client, ws *widgets) (uint32, bool, bool) {
	winID := uint32(c.win)
	isFS := isFullscreenClient(c)
	pending := c.pending
	if pending {
		// Check if the renderer has consumed the previous frame.
		pending = false
		for _, sw := range ws.screensForClient(c) {
			var target *ui.CompositorWidget
			if isFS {
				target = sw.overlay
			} else {
				target = sw.normal
			}
			if wi := target.GetWindow(winID); wi != nil && wi.Pending.Load() {
				pending = true
				break
			}
		}
		c.pending = pending
	}
	return winID, isFS, pending
}

// refreshTranslucency updates the translucency and shadow of all visible
// windows without recapturing their content.
func refreshTranslucency(conn *xgb.Conn, ws *widgets) {
	activeWin, _ := x11.WindowActiveGet(xu)

	changed := false
	for _, c := range clients {
		if c.skipped || c.fullscreened || c.attributes.MapState != xproto.MapStateViewable {
			continue
		}
		winID := uint32(c.win)
		translucency := computeTranslucency(conn, c)
		managed, _ := wmWindow(c).(x11.XWin) // menus and the like cast no shadow
		active := managed != nil && managed.ChildID() == activeWin

		for i := range ws.screens {
			sw := &ws.screens[i]
			for _, target := range []*ui.CompositorWidget{sw.normal, sw.overlay} {
				wi := target.GetWindow(winID)
				if wi == nil {
					continue
				}
				if wi.Img.Translucency != translucency {
					wi.Img.Translucency = translucency
					changed = true
				}
				if wi.Shadow != (managed != nil) || wi.Active != active {
					wi.Shadow, wi.Active = managed != nil, active
					changed = true
				}
			}
		}
	}

	if changed {
		ws.refreshAll()
	}
}

func computeTranslucency(conn *xgb.Conn, c *client) float64 {
	// Check if this is the top visible window
	isTop := true
	idx := -1
	for i, cl := range clients {
		if cl.win == c.win {
			idx = i
			break
		}
	}
	if idx > 0 {
		for j := idx - 1; j >= 0; j-- {
			above := clients[j]
			if above.skipped {
				continue
			}
			if above.attributes.OverrideRedirect {
				continue
			}
			if ok, err := windowSkipped(conn, above.win); err == nil && ok {
				continue
			}
			if isOverlayWindow(conn, above.win) {
				continue
			}
			if above.attributes.MapState == xproto.MapStateViewable {
				isTop = false
				break
			}
		}
	}

	if c.opacitySet {
		return 1.0 - float64(c.opacity)/float64(opaque)
	}

	if !isTop {
		return backgroundDim
	}

	return 0.0
}

// isScreensaver detects screensaver windows.
func isScreensaver(title string, attr *xproto.GetWindowAttributesReply, geom *xproto.GetGeometryReply) bool {
	if title == saver.WindowTitle {
		return true
	}

	// Override-redirect windows covering a full screen are likely screensavers
	// (e.g. xscreensaver), so keep detecting those by their shape.
	if attr.OverrideRedirect {
		if inst := tyde.Instance(); inst != nil {
			for _, screen := range inst.Screens().Screens() {
				if geom.Width >= uint16(screen.Width) && geom.Height >= uint16(screen.Height) {
					return true
				}
			}
		}
	}

	return false
}

func getClientFromWindow(window xproto.Window) *client {
	for _, c := range clients {
		if c.win == window {
			return c
		}
	}
	return nil
}

func addClient(conn *xgb.Conn, window xproto.Window) error {
	if getClientFromWindow(window) != nil {
		return nil
	}

	attr, err := xproto.GetWindowAttributes(conn, window).Reply()
	if err != nil {
		return err
	}
	name, err := windowTitle(conn, window)
	if err != nil {
		return err
	}
	geom, err := xproto.GetGeometry(conn, xproto.Drawable(window)).Reply()
	if err != nil {
		return err
	}
	c := &client{
		win:        window,
		attributes: *attr,
		geom:       *geom,
		opacity:    opaque,
		damaged:    false,
	}

	// Skip the desktop root window, skip-hinted windows, and screensavers.
	if strings.Contains(name, ui.RootWindowName) || isScreensaver(name, attr, geom) {
		c.skipped = true
		_ = composite.UnredirectWindowChecked(conn, window, composite.RedirectManual).Check()
	}

	if !c.skipped && attr.Class != xproto.WindowClassInputOnly {
		c.damage, err = damage.NewDamageId(conn)
		if err != nil {
			return err
		}
		if err = damage.CreateChecked(conn, c.damage, xproto.Drawable(window), damage.ReportLevelNonEmpty).Check(); err != nil {
			return err
		}
	}

	clients = append([]*client{c}, clients...)
	if c.attributes.MapState == xproto.MapStateViewable {
		return mapWin(conn, nil, window)
	}
	return nil
}

func mapWin(conn *xgb.Conn, ws *widgets, window xproto.Window) error {
	c := getClientFromWindow(window)
	if c == nil {
		return fmt.Errorf("could not get client for window %x", window)
	}

	mask := []uint32{xproto.EventMaskPropertyChange}
	_ = xproto.ChangeWindowAttributes(conn, window, xproto.CwEventMask, mask)

	c.attributes.MapState = xproto.MapStateViewable
	c.damaged = true
	c.priority = true // a just-mapped window is what the user is waiting to see
	updateOpacity(conn, 1, c)

	if !c.skipped {
		name, _ := windowTitle(conn, c.win)
		geom := &c.geom
		if strings.Contains(name, ui.RootWindowName) || isScreensaver(name, &c.attributes, geom) {
			c.skipped = true
			_ = composite.UnredirectWindowChecked(conn, c.win, composite.RedirectManual).Check()
			if c.damage != 0 {
				_ = damage.Destroy(conn, c.damage).Check()
				c.damage = 0
			}
		}
	}

	freeClientPixmap(conn, c)

	if ws != nil && !c.skipped {
		ensureWindowOnScreens(ws, c)
		syncOrder(ws)
		ws.refreshAll()
	}

	allDamage = true
	return nil
}

func unmapWin(conn *xgb.Conn, ws *widgets, window xproto.Window) {
	c := getClientFromWindow(window)
	if c == nil {
		return
	}
	c.attributes.MapState = xproto.MapStateUnmapped
	c.damaged = false
	freeClientPixmap(conn, c)

	if ws != nil && !c.skipped {
		removeWindowFromAllScreens(ws, uint32(c.win))
		ws.refreshAll()
	}

	allDamage = true
}

func destroyWin(conn *xgb.Conn, ws *widgets, window xproto.Window) {
	var i int
	var c *client
	for i, c = range clients {
		if c.win == window {
			goto found
		}
	}
	return

found:
	freeClientPixmap(conn, c)
	if c.damage != 0 {
		_ = damage.Destroy(conn, c.damage).Check()
		c.damage = 0
	}

	if ws != nil && !c.skipped {
		removeWindowFromAllScreens(ws, uint32(c.win))
		ws.refreshAll()
	}

	clients = append(clients[:i], clients[i+1:]...)
	allDamage = true
}

func freeClientPixmap(conn *xgb.Conn, c *client) {
	if c.pixmap != 0 {
		xproto.FreePixmap(conn, c.pixmap)
		c.pixmap = 0
	}
}

func configureClient(conn *xgb.Conn, ws *widgets, e xproto.ConfigureNotifyEvent) error {
	client := getClientFromWindow(e.Window)
	if client == nil {
		if e.Window == rootWindow {
			rootWidth = e.Width
			rootHeight = e.Height
		}
		return nil
	}

	client.visualMoving = false // X11 position synced, drag/animation ended

	resized := client.geom.Width != e.Width || client.geom.Height != e.Height
	if resized {
		freeClientPixmap(conn, client)
	}

	client.geom.X = e.X
	client.geom.Y = e.Y
	client.geom.Width = e.Width
	client.geom.Height = e.Height
	client.geom.BorderWidth = e.BorderWidth
	client.attributes.OverrideRedirect = e.OverrideRedirect

	restackClientOnly(e.Window, e.AboveSibling)

	if client.skipped {
		return nil
	}

	updateFullscreen(conn, ws, client)
	if client.fullscreened {
		return nil
	}

	// Update screen membership: remove from screens the window no longer overlaps,
	// add to screens it now overlaps.
	winID := uint32(client.win)
	isFS := isFullscreenClient(client)
	overlapping := ws.screensForClient(client)
	screenChanged := false
	for i := range ws.screens {
		sw := &ws.screens[i]
		hasNormal := sw.normal.GetWindow(winID) != nil
		hasOverlay := sw.overlay.GetWindow(winID) != nil
		if !hasNormal && !hasOverlay {
			// Window appeared on a new screen — create the entry and
			// copy image data from whichever screen previously had it.
			var wi *ui.WindowImage
			if isFS {
				wi = sw.overlay.EnsureWindow(winID)
			} else {
				wi = sw.normal.EnsureWindow(winID)
			}
			copyImageFromOtherScreen(ws, winID, wi, sw)
			screenChanged = true
		}
	}

	if screenChanged {
		// Force recapture so the new screen gets a fresh image
		client.damaged = true
		allDamage = true
	}

	syncOrder(ws)
	ws.refreshAll()

	totalW := client.geom.Width + client.geom.BorderWidth*2
	totalH := client.geom.Height + client.geom.BorderWidth*2

	for _, sw := range overlapping {
		var target *ui.CompositorWidget
		if isFS {
			target = sw.overlay
		} else {
			target = sw.normal
		}
		wi := target.GetWindow(winID)
		if wi == nil {
			continue
		}

		localX := client.geom.X - int16(sw.screen.X)
		localY := client.geom.Y - int16(sw.screen.Y)

		if resized || screenChanged {
			fyne.Do(func() {
				wi.X = localX
				wi.Y = localY
				wi.W = totalW
				wi.H = totalH
				target.Refresh()
			})
			client.damaged = true
			allDamage = true
		} else {
			wi.X = localX
			wi.Y = localY
			fyne.Do(func() {
				target.PlaceWindow(wi)
			})
		}
	}

	return nil
}

func restackClientOnly(window, target xproto.Window) {
	i := -1
	for idx, c := range clients {
		if c.win == window {
			i = idx
			break
		}
	}
	if i == -1 {
		return
	}
	c := clients[i]
	clients = append(clients[:i], clients[i+1:]...)

	if target == 0 {
		clients = append(clients, c)
	} else {
		j := -1
		for idx, c := range clients {
			if c.win == target {
				j = idx
				break
			}
		}
		if j == -1 {
			clients = append(clients, c)
		} else {
			clients = append(clients[:j], append([]*client{c}, clients[j:]...)...)
		}
	}
}

func restackWin(ws *widgets, window, target xproto.Window) {
	restackClientOnly(window, target)

	if ws != nil {
		syncOrder(ws)
		ws.refreshAll()
	}
}

func circulateClient(ws *widgets, e xproto.CirculateNotifyEvent) {
	client := getClientFromWindow(e.Window)
	if client == nil {
		return
	}
	var target xproto.Window
	if e.Place == xproto.PlaceOnTop {
		target = clients[0].win
	} else if e.Place == xproto.PlaceOnBottom {
		target = 0
	}
	restackWin(ws, client.win, target)
	allDamage = true
}

func damageClient(conn *xgb.Conn, e *damage.NotifyEvent) error {
	client := getClientFromWindow(xproto.Window(e.Drawable))
	if client == nil {
		return nil
	}

	if err := damage.SubtractChecked(conn, client.damage, 0, 0).Check(); err != nil {
		return err
	}

	client.damaged = true
	allDamage = true
	return nil
}

func updateOpacity(conn *xgb.Conn, fallback float32, c *client) {
	opacity, err := getOpacity(conn, c.win)
	c.opacitySet = err == nil
	if err != nil {
		if fallback < 1.0 {
			opacity = uint32(fallback * float32(opaque))
		} else {
			opacity = opaque
		}
	}
	c.opacity = opacity

	c.opaqueType = solid
	if c.geom.Depth == 32 {
		c.opaqueType = argb
	} else if opacity != opaque {
		c.opaqueType = transparent
	}
}

func windowTitle(conn *xgb.Conn, window xproto.Window) (string, error) {
	if netWmNameAtom == 0 {
		a := "_NET_WM_NAME"
		atom, err := xproto.InternAtom(conn, false, uint16(len(a)), a).Reply()
		if err != nil {
			return "", err
		}
		netWmNameAtom = atom.Atom
	}

	if utf8StringAtom == 0 {
		b := "UTF8_STRING"
		atom, err := xproto.InternAtom(conn, false, uint16(len(b)), b).Reply()
		if err != nil {
			return "", err
		}
		utf8StringAtom = atom.Atom
	}

	prop, err := xproto.GetProperty(conn, false, window, netWmNameAtom, utf8StringAtom, 0, 1024).Reply()
	if err == nil && prop.Type == utf8StringAtom && len(prop.Value) > 0 {
		return string(prop.Value), nil
	}

	if wmNameAtom == 0 {
		c := "WM_NAME"
		atom, err := xproto.InternAtom(conn, false, uint16(len(c)), c).Reply()
		if err != nil {
			return "", err
		}
		wmNameAtom = atom.Atom
	}

	if stringAtom == 0 {
		d := "STRING"
		atom, err := xproto.InternAtom(conn, false, uint16(len(d)), d).Reply()
		if err != nil {
			return "", fmt.Errorf("failed to intern STRING atom: %v", err)
		}
		stringAtom = atom.Atom
	}

	prop, err = xproto.GetProperty(conn, false, window, wmNameAtom, stringAtom, 0, 1024).Reply()
	if err == nil && prop.Type == stringAtom && len(prop.Value) > 0 {
		return string(prop.Value), nil
	}
	return "Unnamed", nil
}

func windowSkipped(conn *xgb.Conn, window xproto.Window) (bool, error) {
	if netWmStateAtom == 0 {
		a := "_NET_WM_STATE"
		atom, err := xproto.InternAtom(conn, false, uint16(len(a)), a).Reply()
		if err != nil {
			return false, err
		}
		netWmStateAtom = atom.Atom
	}

	if atomAtom == 0 {
		b := "ATOM"
		atom, err := xproto.InternAtom(conn, false, uint16(len(b)), b).Reply()
		if err != nil {
			return false, err
		}
		atomAtom = atom.Atom
	}

	prop, err := xproto.GetProperty(conn, false, window, netWmStateAtom, atomAtom, 0, 1024).Reply()
	if err == nil && prop.Type == atomAtom && len(prop.Value) > 0 {
		aid := xproto.Atom(xgb.Get32(prop.Value))
		reply, err := xproto.GetAtomName(conn, aid).Reply()
		if err != nil {
			return false, fmt.Errorf("AtomName: Error fetching name for ATOM "+
				"id '%d': %s", aid, err)
		}
		if reply.Name == "_NET_WM_STATE_SKIP_TASKBAR" {
			return true, nil
		}

		return false, nil
	}

	return false, err
}

// isOverlayWindow returns true if the window is a tooltip, popup menu, dropdown,
// or combo — transient overlays that should not cause the window beneath to dim.
func isOverlayWindow(conn *xgb.Conn, window xproto.Window) bool {
	if windowTypeAtom == 0 {
		return false
	}
	prop, err := xproto.GetProperty(conn, false, window, windowTypeAtom, xproto.AtomAtom, 0, 32).Reply()
	if err != nil || prop.Type != xproto.AtomAtom || len(prop.Value) < 4 {
		return false
	}
	for i := 0; i+3 < len(prop.Value); i += 4 {
		a := xproto.Atom(xgb.Get32(prop.Value[i:]))
		if a == tooltipAtom || a == popupMenuAtom || a == dropdownAtom || a == comboAtom {
			return true
		}
	}
	return false
}

func getOpacity(conn *xgb.Conn, window xproto.Window) (uint32, error) {
	if opacityAtom == 0 {
		name := "_NET_WM_WINDOW_OPACITY"
		opacityAtomReply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			return opaque, err
		}
		opacityAtom = opacityAtomReply.Atom
	}

	reply, err := xproto.GetProperty(conn, false, window, opacityAtom, xproto.GetPropertyTypeAny, 0, (1<<32)-1).Reply()
	if err != nil {
		return opaque, err
	}
	if reply.Format == 0 {
		return opaque, os.ErrNotExist
	}
	if reply.Format != 32 {
		return opaque, fmt.Errorf("unexpected format %d", reply.Format)
	}
	return xgb.Get32(reply.Value), nil
}
