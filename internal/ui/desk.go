package ui

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"os/exec"
	"strconv"

	"fyne.io/fyne/v2/theme"
	"github.com/FyshOS/appie"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	deskDriver "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	"fyshos.com/tyde/internal/notify"
	wmtheme "fyshos.com/tyde/theme"
	"fyshos.com/tyde/wm"
)

const (
	// RootWindowName is the base string that all root windows will have in their title and is used to identify root windows.
	RootWindowName = "Tyde Desktop"
)

// screenWindow holds the Fyne window and per-screen widgets for a single monitor.
type screenWindow struct {
	screen            *tyde.Screen
	win               fyne.Window
	compositor        *CompositorWidget
	compositorOverlay *CompositorWidget
	bg                *background
	overlay           *fyne.Container

	// deskShaderBG is a black rectangle drawn directly behind deskShader so the
	// transparent area around the cube reads as empty rather than letting the
	// live desktop show through. Shown and hidden together with deskShader.
	deskShaderBG *canvas.Rectangle
	// deskShader is a full-window overlay that plays the 3D cube transition
	// between virtual desktops. It is hidden except while a switch is animating.
	deskShader *canvas.Shader
	// deskSnapshots caches the last seen frame of each virtual desktop, keyed by
	// desktop id, so a switch can show the target desktop on the rolling face.
	// Only genuine captures are stored here, never derived images, so an entry
	// always faithfully represents that desktop.
	deskSnapshots map[int]image.Image

	// overviewShader is a full-window overlay that plays the "reveal all" desktop
	// overview zoom. Hidden except while the overview is opening, shown or closing.
	overviewShader *canvas.Shader
}

// ScreenCompositors groups the compositor widgets for a single screen,
// passed to the platform compositor so it can route windows per-monitor.
type ScreenCompositors struct {
	Screen  *tyde.Screen
	Normal  *CompositorWidget
	Overlay *CompositorWidget
}

// CompositorScreensChanged is registered by the running compositor so the
// desktop can hand it an updated per-screen widget list when screens are
// added, removed or resized at runtime. It is called on the Fyne main
// goroutine and is nil when no compositor is running.
var CompositorScreensChanged func([]ScreenCompositors)

// CompositorWindowSnapshot is registered by the running compositor so the
// desktop can build the cube's rolling face for a desktop it has never captured
// live. It renders that desktop's windows — the one offsetY pixels from the
// current viewport, matching SetDesktop's slide — into a transparent RGBA image
// for the given screen. It is called on the Fyne main goroutine and is nil when
// no compositor is running.
var CompositorWindowSnapshot func(screen *tyde.Screen, offsetY int) image.Image

type desktop struct {
	wm.ShortcutHandler
	app      fyne.App
	wm       tyde.WindowManager
	icons    appie.Provider
	recent   []appie.AppData
	screens  tyde.ScreenList
	settings tyde.DeskSettings

	run         func()
	showMenu    func(*fyne.Menu, fyne.Position)
	moduleCache []tyde.Module

	bar             *bar
	widgets         *widgetPanel
	mouse           fyne.CanvasObject
	overlayLayer    *overlayLayer   // above-windows layer for OverlayAreaModule widgets (primary screen)
	accessoryLayer  *fyne.Container // embedded-mode home for WindowAccessoryModule items (no compositor to host them)
	screenWindows   []*screenWindow
	primaryWin      *screenWindow
	running         bool // true once the run loop has started and windows can be shown directly
	desk            int
	deskAnim        *fyne.Animation
	deskAnimTargets map[tyde.Window]fyne.Position // where the in-flight animation is heading
	deskCubeAnim    *fyne.Animation               // drives the 3D cube transition overlay
	overview        *deskOverview                 // the "reveal all" overview, when on screen
	compositorDone  chan struct{}

	// overlayShapes maps each shown overlay to the screen-pixel rectangle it occupies,
	// so frame input shapes can be made transparent only under the overlay content.
	overlayShapes map[fyne.CanvasObject]image.Rectangle
	// canvasOverlay records whether the root window canvas currently has a Fyne
	// overlay (a dialog or pop-up) shown on it - see canvasOverlaysChanged.
	canvasOverlay bool
	// root is the input-aware wrapper handed out by Root().
	root *rootWindow

	// welcomeDone guards the first-run welcome splash so it is only ever triggered
	// once per session, from the first primary-window layout with a real size.
	welcomeDone bool

	// activityLayer, in embedded mode only, watches for mouse movement to defer the
	// screen saver.
	activityLayer fyne.CanvasObject
}

func (l *desktop) Desktop() int {
	return l.desk
}

func (l *desktop) SetDesktop(id int) {
	l.setDesktop(id, true)
}

// setDesktop switches to desktop id, sliding the windows into place. When cube is
// true the 3D cube transition is rolled over the top to mask the slide; the desktop
// overview passes false because its own zoom-in already masks the slide.
func (l *desktop) setDesktop(id int, cube bool) {
	old := l.desk
	if id != old && cube {
		// Roll the 3D cube over the top while the windows below slide into place.
		l.startDeskCube(old, id)
	}

	diff := id - l.desk
	prevTargets := l.deskAnimTargets

	// Stop any in-flight animation; the new one takes over.
	if l.deskAnim != nil {
		l.deskAnim.Stop()
		l.deskAnim = nil
	}

	l.desk = id

	_, height := l.RootSizePixels()
	offPix := float32(diff * -int(height))
	wins := l.wm.Windows()

	starts := make([]fyne.Position, len(wins))
	targets := make(map[tyde.Window]fyne.Position, len(wins))
	for i, win := range wins {
		// If the previous animation was heading somewhere, start from
		// that target rather than the current (mid-flight) position.
		if prev, ok := prevTargets[win]; ok {
			starts[i] = prev
		} else {
			starts[i] = win.Position()
		}

		display := l.Screens().ScreenForWindow(win)
		off := offPix / display.CanvasScale()
		targets[win] = fyne.NewPos(starts[i].X, starts[i].Y+off)
	}

	type visualMover interface {
		MoveVisual(fyne.Position)
	}

	l.deskAnimTargets = targets
	var a *fyne.Animation
	a = fyne.NewAnimation(canvas.DurationStandard, func(f float32) {
		if l.deskAnim != a {
			return // superseded by a newer animation
		}
		for i, item := range wins {
			if item.Pinned() {
				continue
			}

			target := targets[item]
			newX := starts[i].X + (target.X-starts[i].X)*f
			newY := starts[i].Y + (target.Y-starts[i].Y)*f
			pos := fyne.NewPos(newX, newY)

			if f >= 1.0 {
				item.Move(pos)
			} else if vm, ok := item.(visualMover); ok {
				vm.MoveVisual(pos)
			} else {
				item.Move(pos)
			}
		}

		// Completion runs once the slide finishes, independent of the window loop:
		// it must fire even with no windows, or when the last window is pinned and
		// skipped above, so modules (e.g. the pager) always learn of the switch.
		if f >= 1.0 {
			l.deskAnim = nil
			l.deskAnimTargets = nil
			for _, m := range l.Modules() {
				if desk, ok := m.(notify.DesktopNotify); ok {
					desk.DesktopChangeNotify(id)
				}
			}
			l.raiseTopWindow(id)
		}
	})
	l.deskAnim = a
	a.Start()
}

// raiseTopWindow raises and focuses the topmost window that is visible on the
// given desktop. Pinned windows count as visible on every desktop.
func (l *desktop) raiseTopWindow(id int) {
	for _, win := range l.wm.Windows() {
		if win.Iconic() {
			continue
		}
		if win.Desktop() != id && !win.Pinned() {
			continue
		}

		win.RaiseToTop()
		win.Focus()
		return
	}
}

// newDeskShader builds the hidden full-window cube transition overlay. All
// screens share the same shader source (and therefore the compiled program),
// each driving its own captured desktop faces.
func newDeskShader() *canvas.Shader {
	s := canvas.NewShader("tydeDeskCube", cubeShaderGL, cubeShaderES)
	s.Uniforms = map[string]float32{"progress": 0}
	s.Hide()
	return s
}

// newDeskShaderBG builds the hidden black backdrop drawn behind the cube overlay.
func newDeskShaderBG() *canvas.Rectangle {
	r := canvas.NewRectangle(color.Black)
	r.Hide()
	return r
}

// captureOpaque grabs the canvas content as a fully opaque RGBA image.
//
// Canvas().Capture() reads the GL framebuffer with glReadPixels. The default
// framebuffer's alpha channel is not meaningful and commonly reads back as zero,
// so the raw capture has correct colour but zero alpha. Uploaded as a shader
// texture that samples straight through, the face renders transparent (it looks
// "missing"). Copying into a fresh RGBA and forcing every alpha byte to 255
// yields a guaranteed-opaque snapshot, and the concrete *image.RGBA type also
// takes the painter's fast texture-upload path.
func captureOpaque(c fyne.Canvas) image.Image {
	src := c.Capture()
	if src == nil {
		return nil
	}

	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	for i := 3; i < len(out.Pix); i += 4 {
		out.Pix[i] = 0xff
	}
	return out
}

// refreshShaderParent repaints sw's window content so a child that was just
// switched from hidden to visible (a transition shader) is rendered and
// registered against the canvas.
func refreshShaderParent(sw *screenWindow) {
	if sw == nil || sw.win == nil {
		return
	}
	if content := sw.win.Content(); content != nil {
		content.Refresh()
	}
}

// startDeskCube begins the 3D cube transition from desktop old to id. For each
// screen it captures the current desktop as the front face and uses the cached
// snapshot of the target desktop (or the current frame, first time) as the face
// rolling in, then animates the shader's progress uniform. The overlay covers
// the screen, so the window slide driven by SetDesktop happens unseen beneath
// it and the live target desktop is revealed when the roll completes.
func (l *desktop) startDeskCube(old, id int) {
	if l.deskCubeAnim != nil {
		l.deskCubeAnim.Stop()
		l.deskCubeAnim = nil
	}

	var shaders []*canvas.Shader
	var bgs []*canvas.Rectangle
	for _, sw := range l.screenWindows {
		if sw.deskShader == nil || sw.win == nil {
			continue
		}
		if sw.deskSnapshots == nil {
			sw.deskSnapshots = make(map[int]image.Image)
		}

		from := captureOpaque(sw.win.Canvas())
		if from == nil {
			continue
		}
		// A blank capture means GL front-buffer readback isn't working in this
		// environment (e.g. tyde nested in Xephyr via "make embed"). Don't poison the
		// shared snapshot cache - which the desktop overview also reads - with a black
		// frame; synthesise the leaving face instead so the cube isn't black either.
		if captureIsBlank(from) {
			if face := l.synthesizeDeskFace(sw, old, old); face != nil {
				from = face
			}
		} else {
			sw.deskSnapshots[old] = from
		}

		to := sw.deskSnapshots[id]
		if to == nil {
			// Never captured live: synthesise the face from the compositor's
			// window pixmaps over the wallpaper. Falls back to the current frame
			// only if that isn't available.
			if face := l.synthesizeDeskFace(sw, old, id); face != nil {
				to = face
			} else {
				to = from
			}
		}

		// desk0 is the lower-numbered (upper) desktop, desk1 the higher one, so
		// the roll direction matches the vertical window slide.
		if id > old {
			sw.deskShader.Textures = map[string]image.Image{"desk0": from, "desk1": to}
		} else {
			sw.deskShader.Textures = map[string]image.Image{"desk0": to, "desk1": from}
		}
		sw.deskShaderBG.Show()
		sw.deskShader.Show()
		// Force the first paint so the just-shown shader renders
		refreshShaderParent(sw)
		shaders = append(shaders, sw.deskShader)
		bgs = append(bgs, sw.deskShaderBG)
	}

	if len(shaders) == 0 {
		return
	}

	// Going to a higher desktop rolls forward (0->1); going back rolls in reverse.
	start, end := float32(0), float32(1)
	if id < old {
		start, end = 1, 0
	}

	var a *fyne.Animation
	a = fyne.NewAnimation(canvas.DurationStandard, func(f float32) {
		if l.deskCubeAnim != a {
			return // superseded by a newer transition
		}

		p := start + (end-start)*f
		for _, s := range shaders {
			s.Uniforms["progress"] = p
			s.Refresh()
		}

		if f >= 1.0 {
			l.deskCubeAnim = nil
			for _, s := range shaders {
				s.Hide()
			}
			for _, b := range bgs {
				if b != nil {
					b.Hide()
				}
			}
		}
	})
	a.Curve = fyne.AnimationLinear
	l.deskCubeAnim = a
	a.Start()
}

// synthesizeDeskFace builds a best-effort image of desktop id for the cube's
// rolling face when no live capture of it exists yet: that desktop's windows,
// read straight from the compositor, drawn over the shared wallpaper. The bar
// and widget panel are omitted; the real desktop, with full chrome, is revealed
// when the roll completes (and cached for next time, so this is only ever the
// first roll onto a given desktop). Returns nil if the compositor or wallpaper
// pieces aren't available, leaving the caller to fall back to the current frame.
func (l *desktop) synthesizeDeskFace(sw *screenWindow, old, id int) image.Image {
	snap := CompositorWindowSnapshot
	if snap == nil || sw.screen == nil {
		return nil
	}

	_, height := l.RootSizePixels()
	wins := snap(sw.screen, (id-old)*-int(height))
	if wins == nil {
		return nil
	}

	b := wins.Bounds()
	face := renderWallpaper(b.Dx(), b.Dy())
	if face == nil {
		face = image.NewRGBA(b)
	}
	draw.Draw(face, face.Bounds(), wins, b.Min, draw.Over)
	return face
}

func (l *desktop) ShowSettings(panel string) {
	l.widgets.showSettings(panel)
}

func (l *desktop) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if esp, ok := l.screens.(*embeddedScreensProvider); ok {
		esp.UpdatePrimarySize(int(size.Width), int(size.Height))
	}

	// Each window covers exactly one screen, so origin is always 0,0.
	pW := size.Width
	pH := size.Height

	// objects order: background, [compositor], bar, widgets, [compositorOverlay], overlay, mouse
	// Size all full-window layers (everything except bar and widgets) to fill.
	for _, o := range objects {
		if o == l.bar || o == l.widgets || o == l.mouse {
			continue
		}
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}

	l.bar.Resize(fyne.NewSize(wmtheme.NarrowBarWidth, pH))
	l.bar.Move(fyne.NewPos(0, 0))
	l.bar.Refresh()

	widgetsWidth := l.widgets.MinSize().Width
	l.widgets.Resize(fyne.NewSize(widgetsWidth, pH))
	l.widgets.Move(fyne.NewPos(pW-widgetsWidth, 0))
	l.widgets.Refresh()

	// On the very first boot, once the primary window has a real (full-screen)
	// size, present the welcome splash.
	if !l.welcomeDone && shouldShowWelcome() && l.primaryWin != nil &&
		size.Width >= welcomeWidth && size.Height >= welcomeHeight {
		l.welcomeDone = true
		fyne.Do(l.ShowWelcome)
	}
}

func (l *desktop) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(640, 480) // tiny - window manager will scale up to screen size
}

// Root returns the primary desktop window, wrapped so that any Fyne overlay shown
// on it (dialogs and pop-ups) is registered with the desktop - see rootWindow.
func (l *desktop) Root() fyne.Window {
	if l.primaryWin == nil || l.primaryWin.win == nil {
		return nil
	}

	// The primary window changes when the screen layout does, so re-wrap if needed.
	if l.root == nil || l.root.Window != l.primaryWin.win {
		l.root = newRootWindow(l.primaryWin.win, l)
	}
	return l.root
}

// canvasOverlaysChanged reacts to a Fyne overlay being added to or removed from the
// root window's canvas.
func (l *desktop) canvasOverlaysChanged(added bool) {
	if l.primaryWin == nil || l.primaryWin.win == nil {
		return
	}

	c := l.primaryWin.win.Canvas()
	l.canvasOverlay = len(c.Overlays().List()) > 0
	l.applyOverlayShapes()

	if added && l.canvasOverlay && c.Focused() == nil {
		c.FocusNext()
	}
}

func (l *desktop) ShowMenuAt(menu *fyne.Menu, pos fyne.Position) {
	l.showMenu(menu, pos)
}

// inputShaper is implemented by window managers that can update X11 input shapes
// on the root and frame windows to control which areas receive mouse events.
type inputShaper interface {
	SetOverlayActive(active bool, regions []image.Rectangle)
}

// backdrop is a full-screen widget that dismisses an overlay on tap or mouse-in
// (mouse-in on the backdrop means the cursor left the overlay content).
// Mouse-out dismiss only activates after the mouse has been inside the content at least once.
type backdrop struct {
	widget.BaseWidget
	onDismiss func()
	armed     bool // true after mouse has entered the content area
}

func (b *backdrop) CreateRenderer() fyne.WidgetRenderer {
	rad := theme.Size(theme.SizeNameModalBlurRadius)
	return widget.NewSimpleRenderer(canvas.NewBlur(rad))
}

func (b *backdrop) Tapped(*fyne.PointEvent) {
	if b.onDismiss != nil {
		b.onDismiss()
	}
}

func (b *backdrop) MouseIn(*deskDriver.MouseEvent) {
	// Only dismiss if the mouse was previously inside the content
	if b.armed && b.onDismiss != nil {
		b.onDismiss()
	}
}

func (b *backdrop) MouseMoved(*deskDriver.MouseEvent) {}
func (b *backdrop) MouseOut()                         {}

func newBackdrop(onDismiss func()) *backdrop {
	b := &backdrop{onDismiss: onDismiss}
	b.ExtendBaseWidget(b)
	return b
}

// hoverCatch is a wrapper that absorbs hover events, preventing them from
// falling through to the backdrop when the cursor is between child widgets.
// It also arms the backdrop for mouse-out dismiss once the mouse enters.
type hoverCatch struct {
	widget.BaseWidget
	content  fyne.CanvasObject
	backdrop *backdrop
}

func (h *hoverCatch) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewWithoutLayout(h.content))
}

func (h *hoverCatch) MouseIn(*deskDriver.MouseEvent) {
	if h.backdrop != nil {
		h.backdrop.armed = true
	}
}

func (h *hoverCatch) MouseMoved(*deskDriver.MouseEvent) {}
func (h *hoverCatch) MouseOut()                         {}

func newHoverCatch(content fyne.CanvasObject, bg *backdrop) *hoverCatch {
	h := &hoverCatch{content: content, backdrop: bg}
	h.ExtendBaseWidget(h)
	return h
}

// ShowOverlayWithBackdrop shows an overlay with click-outside and mouse-out dismiss.
// catchSize defines the hover-sensitive area (use content size + room for submenus).
// Returns the combined object (backdrop + content) for use with HideOverlay.
func (l *desktop) ShowOverlayWithBackdrop(content fyne.CanvasObject, size fyne.Size, catchSize fyne.Size, pos fyne.Position, contentOffset fyne.Position) fyne.CanvasObject {
	return l.showOverlayWithBackdrop(content, size, catchSize, pos, nil, contentOffset)
}

func (l *desktop) showOverlayWithBackdrop(content fyne.CanvasObject, size fyne.Size, catchSize fyne.Size, pos fyne.Position, focus fyne.Focusable, contentOffset fyne.Position) fyne.CanvasObject {
	var combined fyne.CanvasObject
	dismiss := func() {
		l.HideOverlay(combined)
	}

	bg := newBackdrop(dismiss)
	catch := newHoverCatch(content, bg)
	catch.Resize(catchSize)
	catch.Move(pos)
	content.Move(contentOffset)
	content.Resize(size)
	combined = container.NewStack(bg, container.NewWithoutLayout(catch))

	// Size the combined to fill the full window
	winSize := l.primaryWin.win.Canvas().Size()
	l.showOverlay(combined, winSize, fyne.NewPos(0, 0), focus)
	return combined
}

// ShowModal centres content above a blurred, full-screen backdrop. Unlike the
// menu overlays it does not dismiss on background tap or mouse-out (the backdrop
// has no dismiss action), so the modal stays until the caller invokes the
// returned hide function — typically from a button inside the content.
func (l *desktop) ShowModal(content fyne.CanvasObject, size fyne.Size) func() {
	winSize := l.primaryWin.win.Canvas().Size()
	pos := fyne.NewPos((winSize.Width-size.Width)/2, (winSize.Height-size.Height)/2)

	bg := newBackdrop(nil) // nil dismiss => modal: no tap or mouse-out dismissal
	content.Move(pos)
	content.Resize(size)
	combined := container.NewStack(bg, container.NewWithoutLayout(content))

	l.showOverlay(combined, winSize, fyne.NewPos(0, 0), nil)
	return func() { l.HideOverlay(combined) }
}

// ShowOverlay adds content to the desktop overlay layer, above all chrome and windows.
// The root window's input shape is expanded and frame input shapes are cleared
// so that Fyne receives mouse events for the overlay.
func (l *desktop) ShowOverlay(content fyne.CanvasObject, size fyne.Size, pos fyne.Position) {
	l.showOverlay(content, size, pos, nil)
}

func (l *desktop) showOverlay(content fyne.CanvasObject, size fyne.Size, pos fyne.Position, focus fyne.Focusable) {
	overlay := l.primaryWin.overlay
	win := l.primaryWin.win
	fyne.Do(func() {
		content.Resize(size)
		content.Move(pos)
		overlay.Add(content)
		overlay.Refresh()

		if l.overlayShapes == nil {
			l.overlayShapes = map[fyne.CanvasObject]image.Rectangle{}
		}
		l.overlayShapes[content] = l.overlayRegion(pos, size)
		l.applyOverlayShapes()

		if focus != nil {
			win.Canvas().Focus(focus)
		}
	})
}

// overlayRegion converts an overlay's canvas position and size into the screen-pixel
// rectangle it covers on the primary screen. Callers pass the overlay's resting
// position, so an overlay that animates into place still reports its final area.
func (l *desktop) overlayRegion(pos fyne.Position, size fyne.Size) image.Rectangle {
	screen := l.screens.Primary()
	if screen == nil {
		return image.Rectangle{}
	}

	scale := screen.CanvasScale()
	return image.Rect(
		screen.X+int(pos.X*scale),
		screen.Y+int(pos.Y*scale),
		screen.X+int((pos.X+size.Width)*scale),
		screen.Y+int((pos.Y+size.Height)*scale),
	)
}

// applyOverlayShapes pushes the union of the current overlay rectangles to the window
// manager, or clears the overlay state when no overlays remain.
func (l *desktop) applyOverlayShapes() {
	is, ok := l.wm.(inputShaper)
	if !ok {
		return
	}

	regions := make([]image.Rectangle, 0, len(l.overlayShapes)+1)
	for _, r := range l.overlayShapes {
		regions = append(regions, r)
	}

	// A Fyne canvas overlay (dialog or pop-up) is modal over the whole canvas, so
	// rather than measuring its content it claims the entire primary screen.
	if l.canvasOverlay {
		if screen := l.screens.Primary(); screen != nil {
			regions = append(regions, image.Rect(screen.X, screen.Y,
				screen.X+screen.Width, screen.Y+screen.Height))
		}
	}

	if len(regions) == 0 {
		is.SetOverlayActive(false, nil)
		return
	}
	is.SetOverlayActive(true, regions)
}

// HideOverlay removes content from the desktop overlay layer.
// When no overlays remain, input shapes are restored to normal.
func (l *desktop) HideOverlay(content fyne.CanvasObject) {
	overlay := l.primaryWin.overlay
	fyne.Do(func() {
		overlay.Remove(content)
		overlay.Refresh()

		delete(l.overlayShapes, content)
		l.applyOverlayShapes()
	})
}

func (l *desktop) updateBackgrounds(path string) {
	for _, sw := range l.screenWindows {
		if sw.bg != nil {
			sw.bg.updateBackground(path)
		}
	}
}

func (l *desktop) createPrimaryContent(sw *screenWindow) fyne.CanvasObject {
	l.bar = newBar(l)
	l.widgets = newWidgetPanel(l)
	l.mouse = newMouse()
	l.mouse.Hide()

	sw.bg = newBackground()

	// Order: background -> compositor -> overlay modules -> bar -> widgets -> compositor overlay -> UI overlay -> mouse
	objects := []fyne.CanvasObject{sw.bg}

	// Embedded mode's screen-saver activity monitor sits just above the background.
	if l.activityLayer != nil {
		objects = append(objects, l.activityLayer)
	}

	// Normal compositor for regular windows below desktop chrome
	if sw.compositor != nil {
		objects = append(objects, sw.compositor)
	} else if l.accessoryLayer != nil {
		// Embedded mode has no compositor to host window accessories, so add here.
		objects = append(objects, l.accessoryLayer)
	}

	// Overlay-area modules (e.g. desktop pets) draw above regular windows but
	// below the bar, widget panel, fullscreen windows, menus and the cursor.
	l.overlayLayer = newOverlayLayer(l)
	objects = append(objects, l.overlayLayer)

	objects = append(objects, l.bar, l.widgets)

	// Compositor overlay for fullscreen windows above desktop chrome
	if sw.compositorOverlay != nil {
		objects = append(objects, sw.compositorOverlay)
	}

	// Desktop overview zoom, below the UI overlay so the interactive selection
	// layer (added to sw.overlay) sits above it, but above the bar and widgets so
	// it hides the chrome while playing.
	sw.overviewShader = newOverviewShader()
	objects = append(objects, sw.overviewShader)

	// UI overlay for menus, dialogs, switcher, notifications
	sw.overlay = container.NewWithoutLayout()
	objects = append(objects, sw.overlay, l.mouse)

	// Desktop transition cube, topmost so it covers the whole screen while playing.
	sw.deskShaderBG = newDeskShaderBG()
	sw.deskShader = newDeskShader()
	objects = append(objects, sw.deskShaderBG, sw.deskShader)
	return container.New(l, objects...)
}

// overlayLayer is the full-screen, visual-only layer that renders the widgets
// of any OverlayAreaModule above application windows. It can be rebuilt at
// runtime (rebuild) so that enabling or disabling such a module takes effect
// immediately. Wired to the primary screen only.
type overlayLayer struct {
	widget.BaseWidget
	desk    *desktop
	content *fyne.Container
}

func newOverlayLayer(d *desktop) *overlayLayer {
	o := &overlayLayer{desk: d, content: container.NewStack()}
	o.ExtendBaseWidget(o)
	o.rebuild()
	return o
}

func (o *overlayLayer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(o.content)
}

// rebuild repopulates the layer from the currently enabled overlay-area
// modules. Calling OverlayAreaWidget on a freshly created module instance is
// what starts it (e.g. a desktop pet's animation loop); previous instances
// were already torn down via Module.Destroy in clearModuleCache.
func (o *overlayLayer) rebuild() {
	var objs []fyne.CanvasObject
	for _, m := range o.desk.Modules() {
		if om, ok := m.(tyde.OverlayAreaModule); ok {
			if w := om.OverlayAreaWidget(); w != nil {
				objs = append(objs, w)
			}
		}
	}
	o.content.Objects = objs
	o.content.Refresh()
}

// secondaryLayout is a simple layout for non-primary screen windows (no bar/widgets).
type secondaryLayout struct{}

func (s *secondaryLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (s *secondaryLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(640, 480)
}

func (l *desktop) createSecondaryContent(sw *screenWindow) fyne.CanvasObject {
	sw.bg = newBackground()

	objects := []fyne.CanvasObject{sw.bg}

	// Normal compositor for regular windows
	if sw.compositor != nil {
		objects = append(objects, sw.compositor)
	}

	// Compositor overlay for fullscreen windows
	if sw.compositorOverlay != nil {
		objects = append(objects, sw.compositorOverlay)
	}

	sw.overviewShader = newOverviewShader()
	objects = append(objects, sw.overviewShader)

	sw.overlay = container.NewWithoutLayout()
	objects = append(objects, sw.overlay)

	sw.deskShaderBG = newDeskShaderBG()
	sw.deskShader = newDeskShader()
	objects = append(objects, sw.deskShaderBG, sw.deskShader)
	return container.New(&secondaryLayout{}, objects...)
}

func (l *desktop) setupRoot() {
	primary := l.screens.Primary()

	// Build or update screenWindow for each screen
	existingByName := make(map[string]*screenWindow, len(l.screenWindows))
	for _, sw := range l.screenWindows {
		existingByName[sw.screen.Name] = sw
	}

	var newWindows, createdWindows []*screenWindow
	for _, screen := range l.screens.Screens() {
		sw := existingByName[screen.Name]
		if sw != nil {
			// Update screen pointer (geometry may have changed)
			sw.screen = screen
			if sw.compositor != nil {
				sw.compositor.Screen = screen
			}
			if sw.compositorOverlay != nil {
				sw.compositorOverlay.Screen = screen
			}
			delete(existingByName, screen.Name)
		} else {
			// Create new screenWindow
			sw = &screenWindow{screen: screen}
			if l.compositorDone != nil {
				sw.compositor = NewCompositorWidget(screen)
				sw.compositorOverlay = NewCompositorWidget(screen)
			}
			win := l.app.NewWindow(RootWindowName + screen.Name)
			win.SetPadded(false)
			sw.win = win

			if screen == primary {
				win.SetMaster()
				win.SetOnClosed(func() {
					if l.compositorDone != nil {
						close(l.compositorDone)
					}
					l.wm.Close()
				})
				win.SetContent(l.createPrimaryContent(sw))
			} else {
				win.SetContent(l.createSecondaryContent(sw))
			}
			createdWindows = append(createdWindows, sw)
		}

		newWindows = append(newWindows, sw)
		if screen == primary {
			l.primaryWin = sw
		}
	}

	// Close windows for disconnected screens
	for _, sw := range existingByName {
		sw.win.Close()
	}

	l.screenWindows = newWindows

	// Resize each window to cover its screen
	for _, sw := range l.screenWindows {
		scale := sw.screen.CanvasScale()
		sw.win.Resize(fyne.NewSize(float32(sw.screen.Width)/scale, float32(sw.screen.Height)/scale))
	}

	// At startup runFull/runEmbed shows the windows once the run loop starts.
	// For screens hot-plugged at runtime the loop is already running, so the
	// newly created windows must be shown here or they never get mapped.
	if l.running {
		for _, sw := range createdWindows {
			if sw != l.primaryWin {
				sw.win.Show()
			}
		}
	}

	// Hand the running compositor the current per-screen widgets so windows
	// drawn on a newly connected screen get composited (and geometry stays in
	// sync). The initial list is passed to the compositor at startup instead.
	if CompositorScreensChanged != nil {
		CompositorScreensChanged(l.screenCompositors())
	}
}

func (l *desktop) RecentApps() []appie.AppData {
	return l.recent
}

func (l *desktop) Run() {
	go l.wm.Run()
	go l.watchScreenActivity()
	go l.watchSleep()
	l.run() // use the configured run method
}

func (l *desktop) RunApp(app appie.AppData) error {
	return l.runExec(app, app.Run)
}

func (l *desktop) RunAppAction(app appie.AppData, id int) error {
	if app.Actions() == nil || len(app.Actions())-1 < id {
		return nil
	}

	return l.runExec(app, app.Actions()[id].Run)
}

func (l *desktop) runExec(app appie.AppData, runner func(env []string) error) error {
	vars := l.scaleVars(l.Screens().Active().CanvasScale())
	err := runner(vars)

	if err == nil {
		l.recent = append([]appie.AppData{app}, l.recent...)
		// remove if it was already on the list
		for i := 1; i < len(l.recent); i++ {
			if l.recent[i] == app {
				if i == len(l.recent)-1 {
					l.recent = l.recent[:i]
				} else {
					l.recent = append(l.recent[:i], l.recent[i+1:]...)
				}
				break
			}
		}
		// limit to 5 items
		if len(l.recent) > 5 {
			l.recent = l.recent[:5]
		}
		l.settings.(*deskSettings).saveRecents()
	}
	return err
}

func (l *desktop) Settings() tyde.DeskSettings {
	return l.settings
}

func (l *desktop) ContentBoundsPixels(screen *tyde.Screen) (x, y, w, h uint32) {
	screenW := uint32(screen.Width)
	screenH := uint32(screen.Height)
	pad := wmtheme.WidgetPanelWidth
	if l.Settings().NarrowWidgetPanel() {
		pad = wmtheme.NarrowBarWidth
	}
	if l.screens.Primary() == screen {
		bar := uint32(wmtheme.NarrowBarWidth * screen.CanvasScale())
		wid := uint32(pad * screen.CanvasScale())
		return bar, 0, screenW - bar - wid, screenH
	}
	return 0, 0, screenW, screenH
}

func (l *desktop) RootSizePixels() (w, h uint32) {
	for _, screen := range l.Screens().Screens() {
		right := uint32(screen.X + screen.Width)
		bottom := uint32(screen.Y + screen.Height)

		if right > w {
			w = right
		}
		if bottom > h {
			h = bottom
		}
	}

	return w, h
}

func (l *desktop) IconProvider() appie.Provider {
	return l.icons
}

func (l *desktop) WindowManager() tyde.WindowManager {
	return l.wm
}

func (l *desktop) clearModuleCache() {
	for _, mod := range l.moduleCache {
		mod.Destroy()
	}

	l.moduleCache = nil
}

func (l *desktop) Modules() []tyde.Module {
	if l.moduleCache != nil {
		return l.moduleCache
	}

	var mods []tyde.Module
	for _, meta := range tyde.AvailableModules() {
		if !isModuleEnabled(meta.Name, l.settings) {
			continue
		}

		instance := meta.NewInstance()
		mods = append(mods, instance)

		if bind, ok := instance.(tyde.KeyBindModule); ok {
			for sh, f := range bind.Shortcuts() {
				l.AddShortcut(sh, f)
			}
		}
	}

	l.moduleCache = mods
	return mods
}

func (l *desktop) qtScreenScales() string {
	screenScales := ""
	for i, screen := range l.Screens().Screens() {
		if i > 0 {
			screenScales += ";"
		}
		// Qt toolkit cannot handle scale < 1
		positiveScale := math.Max(1.0, float64(screen.CanvasScale()))
		screenScales += screen.Name + "=" + strconv.FormatFloat(positiveScale, 'f', 1, 32)
	}
	return screenScales
}

func (l *desktop) scaleVars(scale float32) []string {
	intScale := int(math.Round(float64(scale)))

	return []string{
		"QT_SCREEN_SCALE_FACTORS=" + l.qtScreenScales(),
		"GDK_SCALE=" + strconv.Itoa(intScale),
		"ELM_SCALE=" + strconv.FormatFloat(float64(scale), 'f', 1, 32),
	}
}

// AccessoryRefresher is installed by the platform compositor so that
// RefreshWindowAccessories can ask it to re-assemble WindowAccessoryModule
// items. It is nil before the compositor starts (and in embedded mode).
var AccessoryRefresher func()

// RefreshWindowAccessories asks the compositor to re-pull and re-stack the
// WindowAccessoryModule items (see modules implementing that interface).
func (l *desktop) RefreshWindowAccessories() {
	if AccessoryRefresher != nil {
		AccessoryRefresher()
	}
}

func (l *desktop) fireSettingsChangeListener(s tyde.DeskSettings) {
	l.clearModuleCache()
	l.updateBackgrounds(s.Background())
	l.widgets.reloadModules(l.Modules())
	if l.overlayLayer != nil {
		l.overlayLayer.rebuild()
	}
	l.RefreshWindowAccessories() // pick up enabling/disabling of accessory modules

	l.bar.updateIcons()
	l.bar.updateIconOrder()
	l.bar.updateTaskbar()
}

func (l *desktop) addSettingsChangeListener() {
	l.Settings().AddChangeListener(l.fireSettingsChangeListener)

	l.app.Settings().AddListener(func(_ fyne.Settings) {
		l.updateBackgrounds(l.Settings().Background())
	})
}

func (l *desktop) registerShortcuts() {
	l.AddShortcut(tyde.NewShortcut("Show Launcher", fyne.KeySpace, tyde.UserModifier),
		ShowAppLauncher)
	l.AddShortcut(tyde.NewShortcut("Switch App Next", fyne.KeyTab, tyde.UserModifier),
		func() {
			// dummy - the wm handles app switcher
		})
	l.AddShortcut(tyde.NewShortcut("Switch App Previous", fyne.KeyTab, tyde.UserModifier|fyne.KeyModifierShift),
		func() {
			// dummy - the wm handles app switcher
		})
	l.AddShortcut(tyde.NewShortcut("Iconify Window", fyne.KeyF9, tyde.UserModifier),
		l.iconifyCurrentWindow)
	l.AddShortcut(tyde.NewShortcut("Maximize Window", fyne.KeyF10, tyde.UserModifier),
		l.maximizeCurrentWindow)
	l.AddShortcut(tyde.NewShortcut("FullScreen Window", fyne.KeyF11, tyde.UserModifier),
		l.fullscreenCurrentWindow)
	l.AddShortcut(tyde.NewShortcut("Print Window", deskDriver.KeyPrintScreen, fyne.KeyModifierShift),
		l.screenshotWindow)
	l.AddShortcut(tyde.NewShortcut("Print Screen", deskDriver.KeyPrintScreen, 0),
		l.screenshot)
	l.AddShortcut(tyde.NewShortcut("Calculator", tyde.KeyCalculator, 0),
		l.calculator)
	l.AddShortcut(tyde.NewShortcut("Lock screen", fyne.KeyL, tyde.UserModifier),
		func() {
			l.TriggerScreenSaver(false)
		})
}

// Screens returns the screens provider of the current desktop environment for access to screen functionality.
func (l *desktop) Screens() tyde.ScreenList {
	return l.screens
}

// CompositorRunFunc is a function that runs a platform compositor using the
// provided per-screen widgets. It blocks until done is closed.
type CompositorRunFunc func(done chan struct{}, screens []ScreenCompositors) error

// NewDesktop creates the full desktop environment with window management.
// If compositorRun is non-nil, the compositor is started in a background goroutine.
func NewDesktop(app fyne.App, mgr tyde.WindowManager, icons appie.Provider, screenProvider tyde.ScreenList, compositorRun CompositorRunFunc) tyde.Desktop {
	desk := newDesktop(app, mgr, icons)
	desk.run = desk.runFull
	if compositorRun != nil {
		desk.compositorDone = make(chan struct{})
	}
	// Screen changes arrive on the X11 event goroutine. Once the run loop is
	// up, the window operations in setupRoot must happen on the Fyne main
	// goroutine, so marshal them with fyne.Do. Before the loop starts (the
	// initial call below) we run it directly, as fyne.Do requires a running loop.
	screenProvider.AddChangeListener(func() {
		if desk.running {
			fyne.Do(desk.setupRoot)
		} else {
			desk.setupRoot()
		}
	})
	desk.screens = screenProvider

	desk.setupRoot()

	if compositorRun != nil {
		go func() {
			screens := desk.screenCompositors()
			if err := compositorRun(desk.compositorDone, screens); err != nil {
				fyne.LogError("Compositor failed", err)
			}
		}()
	}

	wm.StartAuthAgent()
	if desk.Settings().ScreenSaverType() == "XScreensaver" {
		go desk.startXscreensaver()
	}
	return desk
}

// screenCompositors returns the per-screen compositor widget pairs.
func (l *desktop) screenCompositors() []ScreenCompositors {
	var out []ScreenCompositors
	for _, sw := range l.screenWindows {
		if sw.compositor != nil {
			out = append(out, ScreenCompositors{
				Screen:  sw.screen,
				Normal:  sw.compositor,
				Overlay: sw.compositorOverlay,
			})
		}
	}
	return out
}

// NewEmbeddedDesktop creates a new windowed desktop for test purposes.
// An ApplicationProvider is used to lookup application icons from the operating system.
// If run during CI for testing it will return an in-memory window using the
// fyne/test package.
func NewEmbeddedDesktop(app fyne.App, icons appie.Provider) tyde.Desktop {
	wm := &embededWM{}
	desk := newDesktop(app, wm, icons)
	desk.run = desk.runEmbed
	desk.showMenu = desk.showMenuEmbed

	win := desk.newDesktopWindowEmbed()
	sw := &screenWindow{
		screen: desk.screens.Primary(),
		win:    win,
	}
	desk.screenWindows = []*screenWindow{sw}
	desk.primaryWin = sw

	// Embedded mode runs without the platform compositor that normally hosts
	// window accessories, so install a refresher that renders them above desktop.
	desk.accessoryLayer = container.NewWithoutLayout()
	AccessoryRefresher = func() { rebuildEmbeddedAccessories(desk.accessoryLayer) }

	// The saver monitor watches mouse movement to defer the screen saver.
	desk.activityLayer = wm.setWindow(win)
	win.SetContent(desk.createPrimaryContent(sw))
	return desk
}

// rebuildEmbeddedAccessories collects the WindowAccessory items from the enabled
// modules and renders them flat into layer. Embedded mode has no compositor to
// interleave them with windows at the right z-levels, so they all draw together
// in this single layer; each window's decorations go in a container held over
// that window. Runs on the main goroutine (via RefreshWindowAccessories).
func rebuildEmbeddedAccessories(layer *fyne.Container) {
	inst := tyde.Instance()
	if inst == nil || layer == nil {
		return
	}

	byWindow := map[tyde.Window]*fyne.Container{}
	var objs []fyne.CanvasObject
	for _, m := range inst.Modules() {
		am, ok := m.(tyde.WindowAccessoryModule)
		if !ok {
			continue
		}
		for _, acc := range am.WindowAccessories() {
			if acc.Object == nil {
				continue
			}
			if acc.Window == nil {
				objs = append(objs, acc.Object) // positioned on the screen
				continue
			}
			cont, ok := byWindow[acc.Window]
			if !ok {
				cont = container.NewWithoutLayout()
				cont.Move(acc.Window.Position())
				cont.Resize(acc.Window.Size())
				byWindow[acc.Window] = cont
				objs = append(objs, cont)
			}
			cont.Objects = append(cont.Objects, acc.Object)
		}
	}

	layer.Objects = objs
	layer.Refresh()
}

func newDesktop(app fyne.App, wm tyde.WindowManager, icons appie.Provider) *desktop {
	desk := &desktop{app: app, wm: wm, icons: icons, screens: newEmbeddedScreensProvider()}
	desk.showMenu = desk.showMenuFull

	tyde.SetInstance(desk)
	desk.settings = newDeskSettings()
	desk.addSettingsChangeListener()

	desk.registerShortcuts()
	return desk
}

func (l *desktop) calculator() {
	err := exec.Command("calculator").Start()
	if err != nil {
		fyne.LogError("Failed to open calculator", err)
	}
}

func (l *desktop) fullscreenCurrentWindow() {
	if len(l.WindowManager().Windows()) == 0 {
		return
	}

	w := l.WindowManager().Windows()[0]
	if w.Fullscreened() {
		w.Unfullscreen()
	} else {
		w.Fullscreen()
	}
}

func (l *desktop) iconifyCurrentWindow() {
	if len(l.WindowManager().Windows()) == 0 {
		return
	}

	w := l.WindowManager().Windows()[0]
	w.Iconify()
}

func (l *desktop) maximizeCurrentWindow() {
	if len(l.WindowManager().Windows()) == 0 {
		return
	}

	w := l.WindowManager().Windows()[0]
	if w.Maximized() {
		w.Unmaximize()
	} else {
		w.Maximize()
	}
}

// captureIsBlank reports whether a framebuffer capture came back empty - uniformly
// black - when GL front-buffer readback is unsupported (e.g. Xephyr with "make embed")
func captureIsBlank(img image.Image) bool {
	b := img.Bounds()
	if b.Empty() {
		return true
	}

	const steps = 5
	for i := 0; i <= steps; i++ {
		for j := 0; j <= steps; j++ {
			x := b.Min.X + (b.Dx()-1)*i/steps
			y := b.Min.Y + (b.Dy()-1)*j/steps
			if r, g, bl, _ := img.At(x, y).RGBA(); r != 0 || g != 0 || bl != 0 {
				return false
			}
		}
	}
	return true
}
