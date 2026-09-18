//go:build linux || openbsd || freebsd || netbsd
// +build linux openbsd freebsd netbsd

package win

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/shape"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgbutil/ewmh"
	"github.com/BurntSushi/xgbutil/icccm"
	"github.com/BurntSushi/xgbutil/xprop"
	"github.com/BurntSushi/xgbutil/xwindow"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	"fyshos.com/tyde/internal/x11"
	wmTheme "fyshos.com/tyde/theme"
	"fyshos.com/tyde/wm"
)

const unmaximizeThreshold = 84

// defaultBackgroundTransparency is the default background opacity.
const defaultBackgroundTransparency = 20

// gripSize is the size of the resize grip an InnerWindow draws in its corner.
const gripSize = 16

type frame struct {
	x, y                                int16
	width, height                       uint16
	childWidth, childHeight             uint16
	resizeStartWidth, resizeStartHeight uint16
	mouseX, mouseY                      int16
	moveX, moveY                        int16
	resizeStartX, resizeStartY          int16
	resizeBottom, resizeTop             bool
	resizeLeft, resizeRight             bool
	moveOnly, ignoreDrag                bool

	hovered    desktop.Hoverable
	clickCount int
	cancelFunc context.CancelFunc

	pendingGeometry chan *configureGeometry
	pendingMu       sync.Mutex
	transparency    int
	transparencySet bool
	closed          atomic.Bool

	// canvas holds the frame widgets, which are rendered into strip and hit
	// tested by the mouse handlers. Main goroutine only.
	canvas software.WindowlessCanvas
	active bool // whether the window was focused when last rendered

	// The rendered decoration, painted over captures of the frame by
	// decorate: the title bar strip, whose last stripRight pixels are kept at
	// the frame's right edge, the resize grip for the bottom corner and the
	// plain border colour. Guarded by decorMu as decorate runs on the
	// compositor goroutine.
	decorMu     sync.Mutex
	strip, grip *image.RGBA
	stripRight  int
	bg          color.RGBA
	decorSerial uint32

	client *client
}

type configureGeometry struct {
	x, y          int16
	width, height uint16
	force         bool
}

func newFrame(c *client) *frame {
	attrs, err := xproto.GetGeometry(c.wm.Conn(), xproto.Drawable(c.win)).Reply()
	if err != nil {
		fyne.LogError("Get Geometry Error", err)
		return nil
	}

	f, err := xwindow.Generate(c.wm.X())
	if err != nil {
		fyne.LogError("Generate Window Error", err)
		return nil
	}
	x, y, w, h := attrs.X, attrs.Y, attrs.Width, attrs.Height
	full := c.Fullscreened()
	decorated := c.Properties().Decorated()
	maximized := c.Maximized()
	screen := tyde.Instance().Screens().ScreenForGeometry(int(x), int(y), int(w), int(h))
	borderWidth := uint16(wm.ScaleToPixels(wmTheme.BorderWidth, screen))
	titleHeight := uint16(wm.ScaleToPixels(wmTheme.TitleHeight, screen))
	if full || maximized {
		// Remember the original geometry so a later unfullscreen/unmaximize
		// has somewhere to restore to (NotifyFullscreen/NotifyMaximize is not
		// called for windows that come up already in this state).
		c.restoreX = attrs.X
		c.restoreY = attrs.Y
		c.restoreWidth = attrs.Width
		c.restoreHeight = attrs.Height

		activeHead := tyde.Instance().Screens().ScreenForGeometry(int(attrs.X), int(attrs.Y), int(attrs.Width), int(attrs.Height))
		x = int16(activeHead.X)
		y = int16(activeHead.Y)
		if full {
			w = uint16(activeHead.Width)
			h = uint16(activeHead.Height)
		} else {
			maxX, maxY, maxWidth, maxHeight := tyde.Instance().ContentBoundsPixels(activeHead)
			x += int16(maxX)
			y += int16(maxY)
			w = uint16(maxWidth)
			h = uint16(maxHeight)
		}
	} else if decorated {
		x -= int16(borderWidth)
		y -= int16(titleHeight)
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if !maximized {
			w = attrs.Width + borderWidth*2
			h = attrs.Height + borderWidth + titleHeight
		}
	}
	framed := &frame{client: c}
	framed.x = x
	framed.y = y
	values := []uint32{xproto.EventMaskStructureNotify | xproto.EventMaskSubstructureNotify |
		xproto.EventMaskSubstructureRedirect |
		xproto.EventMaskButtonPress | xproto.EventMaskButtonRelease | xproto.EventMaskButtonMotion |
		xproto.EventMaskKeyPress | xproto.EventMaskPointerMotion | xproto.EventMaskFocusChange |
		xproto.EventMaskPropertyChange | xproto.EventMaskLeaveWindow}
	err = xproto.CreateWindowChecked(c.wm.Conn(), c.wm.X().Screen().RootDepth, f.Id, c.wm.X().RootWin(),
		x, y, w, h, 0, xproto.WindowClassInputOutput, c.wm.X().Screen().RootVisual,
		xproto.CwEventMask, values).Check()
	if err != nil {
		fyne.LogError("Create Window Error", err)
		return nil
	}
	c.id = f.Id

	framed.width = w
	framed.height = h
	if full || !decorated {
		framed.childWidth = w
		framed.childHeight = h
	} else {
		framed.childWidth = w - borderWidth*2
		framed.childHeight = h - borderWidth - titleHeight
	}

	_ = ewmh.WmNameSet(c.wm.X(), f.Id, "Tyde Border")
	var offsetX, offsetY int16 = 0, 0
	if !full && decorated {
		offsetX = int16(borderWidth)
		offsetY = int16(titleHeight)
		xproto.ReparentWindow(c.wm.Conn(), c.win, c.id, int16(borderWidth), int16(titleHeight))
		err = ewmh.FrameExtentsSet(c.wm.X(), c.win, &ewmh.FrameExtents{
			Left:  int(borderWidth),
			Right: int(borderWidth),
			Top:   int(titleHeight), Bottom: int(borderWidth),
		})
		if err != nil {
			fyne.LogError("", err)
		}
	} else {
		xproto.ReparentWindow(c.wm.Conn(), c.win, c.id, attrs.X, attrs.Y)
		err = ewmh.FrameExtentsSet(c.wm.X(), c.win, &ewmh.FrameExtents{Left: 0, Right: 0, Top: 0, Bottom: 0})
		if err != nil {
			fyne.LogError("", err)
		}
	}

	if full || maximized {
		xproto.ConfigureWindow(c.wm.Conn(), c.win, xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			[]uint32{uint32(offsetX), uint32(offsetY), uint32(framed.childWidth), uint32(framed.childHeight)})
	}

	windowStateSet(c.wm.X(), c.win, icccm.StateNormal)
	c.frame = framed // set early so ScreenForWindow can resolve the correct screen
	framed.show()
	framed.applyTheme()
	framed.notifyInnerGeometry()

	return framed
}

func (f *frame) addBorder() {
	borderWidth := x11.BorderWidth(x11.XWin(f.client))
	titleHeight := x11.TitleHeight(x11.XWin(f.client))
	x := int16(borderWidth)
	y := int16(titleHeight)
	w := f.width
	h := f.height
	if !f.client.maximized {
		w := f.childWidth + borderWidth*2
		h := f.childHeight + borderWidth + titleHeight
		f.x -= x
		f.y -= y
		if f.x < 0 {
			f.x = 0
		}
		if f.y < 0 {
			f.y = 0
		}
		f.width = w
		f.height = h
	}
	f.applyTheme()

	xproto.ConfigureWindow(f.client.wm.Conn(), f.client.win, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(x), uint32(y), uint32(f.childWidth), uint32(f.childHeight)})
	xproto.ConfigureWindow(f.client.wm.Conn(), f.client.id, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(f.x), uint32(f.y), uint32(w), uint32(h)})

	err := ewmh.FrameExtentsSet(f.client.wm.X(), f.client.win, &ewmh.FrameExtents{Left: int(borderWidth), Right: int(borderWidth), Top: int(titleHeight), Bottom: int(borderWidth)})
	if err != nil {
		fyne.LogError("", err)
	}
	f.notifyInnerGeometry()
}

func (f *frame) applyBorderlessTheme() {
	xproto.ConfigureWindow(f.client.wm.Conn(), f.client.win, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(0), uint32(0), uint32(f.width), uint32(f.height)})
}

// applyTheme lays the client out for the current decoration state and brings
// the frame widgets up to date with the window.
func (f *frame) applyTheme() {
	if f.client.Fullscreened() || !f.client.Properties().Decorated() {
		f.applyBorderlessTheme()
		return
	}

	f.checkScale()
	fyne.Do(f.renderDecoration)
}

func (f *frame) checkScale() {
	titleHeight := x11.TitleHeight(x11.XWin(f.client))
	borderWidth := x11.BorderWidth(x11.XWin(f.client))
	if !f.client.props.Decorated() {
		titleHeight = 0
		borderWidth = 0
	}

	if f.height-titleHeight-borderWidth != f.childHeight {
		f.updateGeometry(f.x, f.y, f.width, f.height, true)
		f.notifyInnerGeometry()
	}
}

func (f *frame) configureLoop() {
	f.pendingMu.Lock()
	ch := f.pendingGeometry
	f.pendingMu.Unlock()
	if ch == nil {
		return
	}

	var lastGeometry *configureGeometry
	change := false

	blanks := 0
	for {
		select {
		case g, ok := <-ch:
			if g == nil || !ok {
				return
			}
			lastGeometry = g
			change = true
			blanks = 0
		default:
			if change && lastGeometry != nil {
				f.updateGeometry(lastGeometry.x, lastGeometry.y, lastGeometry.width, lastGeometry.height, lastGeometry.force)
				change = false
			} else {
				blanks++
				if blanks > 1000 { // if 1000 ticks pass no resize pending
					f.endConfigureLoop()
					return
				}
			}
		}
	}
}

func (f *frame) endConfigureLoop() {
	f.pendingMu.Lock()
	if f.pendingGeometry != nil {
		close(f.pendingGeometry)
		f.pendingGeometry = nil
	}
	f.pendingMu.Unlock()

	// Sync the actual X11 window position to match the visual after drag
	f.updateGeometry(f.x, f.y, f.width, f.height, true)
	fyne.Do(f.renderDecoration) // lay the title bar out for the final size
}

// renderDecoration paints the title bar and resize grip into images that
// decorate lays over captures of the frame, then asks the compositor to
// capture again. The strip is at least as wide as the widgets need and
// decorate keeps its right-hand part at the frame's edge, so a window can be
// resized without re-rendering. Main goroutine only: font rendering is not
// goroutine safe.
func (f *frame) renderDecoration() {
	if f.closed.Load() || f.client.Fullscreened() || !f.client.Properties().Decorated() {
		return
	}

	screen := tyde.Instance().Screens().ScreenForWindow(f.client)
	scale := screen.CanvasScale()
	f.active = f.client.Focused()

	if f.canvas == nil {
		canMaximize := !windowSizeFixed(f.client.wm.X(), f.client.win) &&
			windowSizeCanMaximize(f.client.wm.X(), f.client)
		b := wm.NewBorder(f.client, f.client.Properties().Icon(), canMaximize)
		b.CloseIntercept = f.client.Close

		cnv := software.NewTransparentCanvas() // keeps the rounded corners clear
		cnv.SetPadded(false)
		cnv.SetContent(container.NewThemeOverride(b, &transparentTheme{Theme: theme.DefaultTheme(), frame: f}))
		f.canvas = cnv
	}
	b := f.canvas.Content().(*container.ThemeOverride).Content.(*wm.Border)
	b.Title = f.client.Properties().Title()
	b.Icon = f.client.Properties().Icon()
	b.Alignment = widget.ButtonAlignLeading
	if tyde.Instance().Settings().BorderButtonPosition() == "Right" {
		b.Alignment = widget.ButtonAlignTrailing
	}
	b.SetMaximized(f.client.maximized)
	b.SetActive(f.active)
	f.canvas.SetScale(scale)

	right := f.topRightPixelWidth()
	drawWidth := fyne.Max(f.canvas.Content().MinSize().Width, float32(f.width)/scale)
	f.canvas.Resize(fyne.NewSize(drawWidth, wmTheme.TitleHeight+gripSize))
	img := f.canvas.Capture()

	strip := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), int(x11.TitleHeight(f.client))))
	draw.Draw(strip, strip.Bounds(), img, image.Point{}, draw.Src)
	gripPix := int(gripSize * scale)
	grip := image.NewRGBA(image.Rect(0, 0, gripPix, gripPix))
	draw.Draw(grip, grip.Bounds(), img, img.Bounds().Max.Sub(image.Pt(gripPix, gripPix)), draw.Src)
	r, g, bl, _ := f.canvas.Content().(*container.ThemeOverride).Theme.Color(theme.ColorNameBackground, theme.VariantDark).RGBA()
	bg := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: 0xff}

	f.decorMu.Lock()
	f.strip, f.grip, f.stripRight, f.bg = strip, grip, int(right), bg
	f.decorMu.Unlock()

	f.notifyDecorated()
}

// notifyDecorated changes a property on the frame window so that the
// compositor, which watches for property changes, captures the frame again
// and picks up the new decoration.
func (f *frame) notifyDecorated() {
	atom, err := xprop.Atm(f.client.wm.X(), x11.DecorationProperty)
	if err != nil {
		fyne.LogError("Could not get decoration atom", err)
		return
	}

	f.decorSerial++
	data := make([]byte, 4)
	xgb.Put32(data, f.decorSerial)
	xproto.ChangeProperty(f.client.wm.Conn(), xproto.PropModeReplace, f.client.id, atom,
		xproto.AtomCardinal, 32, 1, data)
}

// decorate paints the frame over a capture of the frame window: the title
// strip along the top, plain borders down the sides and bottom and the resize
// grip where they meet. Safe to call from any goroutine.
func (f *frame) decorate(img *image.RGBA) {
	f.decorMu.Lock()
	defer f.decorMu.Unlock()
	if f.strip == nil {
		return
	}

	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	border := int(x11.BorderWidth(f.client))
	title := int(x11.TitleHeight(f.client))
	bg := image.NewUniform(f.bg)
	draw.Draw(img, image.Rect(0, 0, w, title), bg, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, title, border, h), bg, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(w-border, title, w, h), bg, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, h-border, w, h), bg, image.Point{}, draw.Src)

	stripW := f.strip.Bounds().Dx()
	left := min(w-f.stripRight, stripW-f.stripRight)
	draw.Draw(img, image.Rect(0, 0, left, title), f.strip, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(w-f.stripRight, 0, w, title), f.strip, image.Pt(stripW-f.stripRight, 0), draw.Src)

	gripRect := image.Rectangle{Max: image.Pt(w, h)}
	gripRect.Min = gripRect.Max.Sub(f.grip.Bounds().Size())
	for _, band := range []image.Rectangle{image.Rect(0, h-border, w, h), image.Rect(w-border, title, w, h)} {
		r := gripRect.Intersect(band)
		draw.Draw(img, r, f.grip, r.Min.Sub(gripRect.Min), draw.Src)
	}
}

// decorationObjectAt returns the deepest visible frame widget at the given
// frame-relative pixel position that matches fn, or nil. Positions in the
// right-hand part of the title bar are mapped back to where decorate drew
// them from. Main goroutine only.
func (f *frame) decorationObjectAt(relX, relY int16, fn func(fyne.CanvasObject) bool) fyne.CanvasObject {
	if f.canvas == nil || f.client.Fullscreened() || !f.client.Properties().Decorated() {
		return nil
	}

	scale := f.canvas.Scale()
	if right := int16(f.stripRight); relX > int16(f.width)-right {
		stripW := int16(f.canvas.Content().Size().Width * scale)
		relX = stripW - (int16(f.width) - relX)
	}
	pos := fyne.NewPos(float32(relX)/scale, float32(relY)/scale)
	return wm.FindObjectAtPositionMatching(pos, f.canvas.Content(), fn)
}

func (f *frame) topRightPixelWidth() uint16 {
	screen := tyde.Instance().Screens().ScreenForWindow(f.client)
	scale := screen.CanvasScale()

	iconPix := uint16(0)
	if f.client.Properties().Icon() != nil {
		iconPix = x11.ButtonWidth(x11.XWin(f.client))
	}
	iconAndBorderPix := iconPix + x11.BorderWidth(x11.XWin(f.client))*2 + uint16(theme.Padding()*scale)
	if tyde.Instance().Settings().BorderButtonPosition() == "Right" {
		iconAndBorderPix = 3*iconAndBorderPix - uint16(theme.Padding()*scale)
	}

	return iconAndBorderPix - uint16(theme.Padding()*scale)
}

func (f *frame) getInnerWindowCoordinates(w uint16, h uint16) (uint32, uint32, uint32, uint32) {
	if f.client.Fullscreened() || !f.client.Properties().Decorated() {
		constrainW, constrainH := w, h
		if !f.client.Properties().Decorated() {
			adjustedW, adjustedH := windowSizeWithIncrement(f.client.wm.X(), f.client.win, w, h)
			constrainW, constrainH = windowSizeConstrain(f.client.wm.X(), f.client.win,
				adjustedW, adjustedH)
		}
		f.width = constrainW
		f.height = constrainH
		f.height = constrainH
		return 0, 0, uint32(constrainW), uint32(constrainH)
	}

	borderWidth := x11.BorderWidth(x11.XWin(f.client))
	titleHeight := x11.TitleHeight(x11.XWin(f.client))

	extraWidth := 2 * borderWidth
	extraHeight := borderWidth + titleHeight
	// uint underflow
	if w > extraWidth {
		w -= extraWidth
	} else {
		w = 0
	}
	if h > extraHeight {
		h -= extraHeight
	} else {
		h = 0
	}

	adjustedW, adjustedH := windowSizeWithIncrement(f.client.wm.X(), f.client.win, w, h)
	constrainW, constrainH := windowSizeConstrain(f.client.wm.X(), f.client.win,
		adjustedW, adjustedH)
	f.width = constrainW + extraWidth
	f.height = constrainH + extraHeight

	return uint32(borderWidth), uint32(titleHeight), uint32(constrainW), uint32(constrainH)
}

func (f *frame) hide() {
	stack := f.client.wm.Windows()
	for i := 0; i < len(stack); i++ {
		if stack[i] == (interface{})(f.client).(tyde.Window) {
			continue
		}

		if stack[i].Iconic() {
			continue
		}

		stack[i].Focus()
		break
	}

	borderWidth := x11.BorderWidth(x11.XWin(f.client))
	titleHeight := x11.TitleHeight(x11.XWin(f.client))
	x, y := f.x+int16(borderWidth), f.y+int16(titleHeight)
	xproto.ReparentWindow(f.client.wm.Conn(), f.client.win, f.client.wm.X().RootWin(), x, y)
	xproto.UnmapWindow(f.client.wm.Conn(), f.client.win)
}

func (f *frame) maximizeApply() {
	// Per EWMH, _NET_WM_STATE_FULLSCREEN overrides WM_NORMAL_HINTS size limits,
	// so only honour the size-hint guards when the request is a maximize.
	if !f.client.Fullscreened() {
		if windowSizeFixed(f.client.wm.X(), f.client.win) ||
			!windowSizeCanMaximize(f.client.wm.X(), f.client) {
			return
		}
	}
	f.client.restoreWidth = f.width
	f.client.restoreHeight = f.height
	f.client.restoreX = f.x
	f.client.restoreY = f.y

	head := tyde.Instance().Screens().ScreenForWindow(f.client)
	maxX, maxY, maxWidth, maxHeight := tyde.Instance().ContentBoundsPixels(head)
	if f.client.Fullscreened() {
		maxX, maxY = 0, 0
		maxWidth = uint32(head.Width)
		maxHeight = uint32(head.Height)
	}
	f.updateGeometry(int16(head.X+int(maxX)), int16(head.Y+int(maxY)), uint16(maxWidth), uint16(maxHeight), true)
	f.notifyInnerGeometry()
	f.applyTheme()
}

func (f *frame) mouseDrag(x, y int16) {
	if f.client.Fullscreened() || f.ignoreDrag {
		return
	}
	moveDeltaX := x - f.mouseX
	moveDeltaY := y - f.mouseY
	if moveDeltaX == 0 && moveDeltaY == 0 {
		return
	}

	if f.client.Maximized() {
		screen := tyde.Instance().Screens().ScreenForWindow(f.client)
		scale := screen.CanvasScale()
		outsideBar := y > f.y+int16(x11.TitleHeight(x11.XWin(f.client))) || y < f.y

		if outsideBar && uint16(math.Abs(float64(moveDeltaY))) >
			uint16(math.Ceil(float64(unmaximizeThreshold)*float64(scale))) {
			diff := f.mouseX - f.x
			scale := float64(f.client.restoreWidth) / float64(f.width)

			f.client.restoreX = f.mouseX - int16(math.Ceil(float64(diff)*scale))
			f.client.restoreY = f.mouseY

			f.moveX = f.client.restoreX
			f.moveY = f.client.restoreY
			f.client.Unmaximize()
		}
		return
	}

	f.mouseX = x
	f.mouseY = y

	if f.moveOnly {
		f.moveX += moveDeltaX
		f.moveY += moveDeltaY
		// Fast path: update compositor visual directly, skip X11 and queueGeometry.
		// mouseDrag runs on the main thread (via fyne.Do) so this is safe.
		if x11.VisualMoveCallback != nil {
			f.x = f.moveX
			f.y = f.moveY
			x11.VisualMoveCallback(uint32(f.client.id), f.moveX, f.moveY, f.width, f.height)
		} else {
			f.queueGeometry(f.moveX, f.moveY, f.width, f.height, false)
		}
	}
	if f.resizeTop || f.resizeBottom || f.resizeLeft || f.resizeRight && !windowSizeFixed(f.client.wm.X(), f.client.win) {
		deltaX := x - f.resizeStartX
		deltaY := y - f.resizeStartY
		width := int16(f.resizeStartWidth)
		height := int16(f.resizeStartHeight)
		if f.resizeTop {
			f.moveY += moveDeltaY
			height -= deltaY
		} else if f.resizeBottom {
			height += deltaY
		}
		if f.resizeLeft {
			f.moveX += moveDeltaX
			width -= deltaX
		} else if f.resizeRight {
			width += deltaX
		}

		// avoid uint underflow
		if width < 1 {
			width = 1
		}
		if height < 1 {
			height = 1
		}
		f.queueGeometry(f.moveX, f.moveY, uint16(width), uint16(height), false)
	}
}

func (f *frame) mouseMotion(x, y int16) {
	relX := x - f.x
	relY := y - f.y

	obj := f.decorationObjectAt(relX, relY, func(obj fyne.CanvasObject) bool {
		if _, ok := obj.(desktop.Cursorable); ok {
			return true
		}

		_, ok := obj.(desktop.Hoverable)
		return ok
	})

	// Buttons show the pointer; anything else (including the frame's own
	// resize grip) gets the resize cursor for the edge under the mouse.
	cursor := x11.DefaultCursor
	hov, hoverable := obj.(desktop.Hoverable)
	if cur, ok := obj.(desktop.Cursorable); ok && cur.Cursor() == desktop.PointerCursor {
		cursor = x11.CloseCursor
	} else if !hoverable && !f.client.Maximized() && !f.client.Fullscreened() &&
		!windowSizeFixed(f.client.wm.X(), f.client.win) {
		cursor = f.lookupResizeCursor(relX, relY)
	}

	refresh := false
	if hoverable {
		if f.hovered == nil {
			hov.MouseIn(&desktop.MouseEvent{})
			f.hovered = hov
			refresh = true
		} else if hov != f.hovered {
			f.hovered.MouseOut()
			hov.MouseIn(&desktop.MouseEvent{})
			f.hovered = hov
			refresh = true
		} else {
			hov.MouseMoved(&desktop.MouseEvent{})
		}
	} else if f.hovered != nil {
		f.hovered.MouseOut()
		f.hovered = nil
		refresh = true
	}
	if refresh {
		f.renderDecoration()
	}
	xproto.ChangeWindowAttributes(f.client.wm.Conn(), f.client.id, xproto.CwCursor,
		[]uint32{uint32(cursor)})
}

func (f *frame) lookupResizeCursor(x, y int16) xproto.Cursor {
	cornerSize := x11.ButtonWidth(x11.XWin(f.client))
	edgeSize := x11.BorderWidth(x11.XWin(f.client))

	if y < int16(x11.TitleHeight(x11.XWin(f.client))) { // top left or right
		if x < int16(edgeSize) {
			return x11.ResizeTopLeftCursor
		} else if x >= int16(f.width-edgeSize) {
			return x11.ResizeTopRightCursor
		}
	} else if y >= int16(f.height-cornerSize) { // bottom
		if x < int16(cornerSize) {
			return x11.ResizeBottomLeftCursor
		} else if x >= int16(f.width-cornerSize) {
			return x11.ResizeBottomRightCursor
		} else {
			return x11.ResizeBottomCursor
		}
	} else { // center (sides)
		if x < int16(cornerSize) {
			return x11.ResizeLeftCursor
		} else if x >= int16(f.width-cornerSize) {
			return x11.ResizeRightCursor
		}
	}

	return x11.DefaultCursor
}

func (f *frame) mousePress(x, y int16, b xproto.Button, mods uint16) {
	if b >= xproto.ButtonIndex4 && mods > 0 {
		if !f.transparencySet {
			f.transparencySet = true
			if !f.client.Focused() {
				f.transparency = defaultBackgroundTransparency
			}
		}

		f.transparency -= 5
		if b == xproto.ButtonIndex5 {
			f.transparency += 10
		}

		if f.transparency < 0 {
			f.transparency = 0
		} else if f.transparency >= 90 {
			f.transparency = 90
		}

		_ = ewmh.WmWindowOpacitySet(f.client.wm.X(), f.client.id, float64(100-f.transparency)/100.0)
		return
	}

	if b != xproto.ButtonIndex1 {
		return
	}
	if !f.client.Focused() {
		f.client.RaiseToTop()
		f.client.Focus()
		return
	}
	if f.client.Fullscreened() {
		return
	}

	relX := x - f.x
	relY := y - f.y

	obj := f.decorationObjectAt(relX, relY, func(obj fyne.CanvasObject) bool {
		_, ok := obj.(fyne.Tappable)
		return ok
	})
	if _, ok := obj.(desktop.Cursorable); ok { // a button
		f.ignoreDrag = true
		return
	}

	buttonWidth := x11.ButtonWidth(x11.XWin(f.client))
	borderWidth := x11.BorderWidth(x11.XWin(f.client))
	titleHeight := x11.TitleHeight(x11.XWin(f.client))
	f.mouseX = x
	f.mouseY = y
	f.resizeStartX = x
	f.resizeStartY = y
	f.moveX = f.x
	f.moveY = f.y
	f.resizeStartWidth = f.width
	f.resizeStartHeight = f.height
	f.resizeBottom = false
	f.resizeLeft = false
	f.resizeRight = false
	f.resizeTop = false
	f.moveOnly = false

	if relY < int16(titleHeight) && relX >= int16(borderWidth) && relX < int16(f.width-borderWidth) {
		f.moveOnly = true
	} else if !windowSizeFixed(f.client.wm.X(), f.client.win) && !f.client.Maximized() {
		if relY < int16(titleHeight) {
			if relX < int16(borderWidth) {
				f.resizeLeft = true
				f.resizeTop = true
			} else if relX >= int16(f.width-borderWidth) {
				f.resizeRight = true
				f.resizeTop = true
			}
		} else {
			if relY >= int16(f.height-buttonWidth) {
				f.resizeBottom = true
			}
			if relX < int16(buttonWidth) {
				f.resizeLeft = true
			} else if relX >= int16(f.width-buttonWidth) {
				f.resizeRight = true
			}
		}
	}

	f.client.wm.RaiseToTop(f.client)
}

func (f *frame) mouseRelease(x, y int16, b xproto.Button) {
	f.ignoreDrag = false
	if b != xproto.ButtonIndex1 {
		return
	}
	titleHeight := x11.TitleHeight(x11.XWin(f.client))

	relX := x - f.x
	relY := y - f.y
	barYMax := int16(titleHeight)
	if relY > barYMax {
		return
	}
	f.clickCount++

	if f.cancelFunc != nil {
		f.cancelFunc()
		return
	}

	go f.mouseReleaseWaitForDoubleClick(relX, relY)
}

func (f *frame) mouseReleaseWaitForDoubleClick(relX, relY int16) {
	var ctx context.Context
	ctx, f.cancelFunc = context.WithDeadline(context.TODO(), time.Now().Add(time.Millisecond*300))
	defer f.cancelFunc()

	<-ctx.Done()
	clickCount := f.clickCount
	f.clickCount = 0
	f.cancelFunc = nil

	fyne.Do(func() {
		if clickCount == 2 {
			obj := f.decorationObjectAt(relX, relY, func(obj fyne.CanvasObject) bool {
				_, ok := obj.(fyne.DoubleTappable)
				return ok
			})
			if obj != nil {
				obj.(fyne.DoubleTappable).DoubleTapped(&fyne.PointEvent{})
			}
		} else {
			obj := f.decorationObjectAt(relX, relY, func(obj fyne.CanvasObject) bool {
				_, ok := obj.(fyne.Tappable)
				return ok
			})
			if obj != nil {
				obj.(fyne.Tappable).Tapped(&fyne.PointEvent{})
			}
		}
	})
}

// Notify the child window that it's geometry has changed to update menu positions etc.
// This should be used sparingly as it can impact performance on the child window.
func (f *frame) notifyInnerGeometry() {
	innerX, innerY, innerW, innerH := f.getInnerWindowCoordinates(f.width, f.height)
	ev := xproto.ConfigureNotifyEvent{
		Event: f.client.win, Window: f.client.win, AboveSibling: 0,
		X: f.x + int16(innerX), Y: f.y + int16(innerY), Width: uint16(innerW), Height: uint16(innerH),
		BorderWidth: x11.BorderWidth(x11.XWin(f.client)), OverrideRedirect: false,
	}
	xproto.SendEvent(f.client.wm.Conn(), false, f.client.win, xproto.EventMaskStructureNotify, string(ev.Bytes()))
}

func (f *frame) removeBorder() {
	borderWidth := x11.BorderWidth(x11.XWin(f.client))
	titleHeight := x11.TitleHeight(x11.XWin(f.client))

	if !f.client.maximized {
		f.x += int16(borderWidth)
		f.y += int16(titleHeight)
		f.width = f.childWidth
		f.height = f.childHeight
	}
	f.applyTheme()

	xproto.ConfigureWindow(f.client.wm.Conn(), f.client.id, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(f.x), uint32(f.y), uint32(f.width), uint32(f.height)})
	xproto.ConfigureWindow(f.client.wm.Conn(), f.client.win, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{0, 0, uint32(f.childWidth), uint32(f.childHeight)})

	err := ewmh.FrameExtentsSet(f.client.wm.X(), f.client.win, &ewmh.FrameExtents{Left: 0, Right: 0, Top: 0, Bottom: 0})
	if err != nil {
		fyne.LogError("", err)
	}
	f.notifyInnerGeometry()
}

func (f *frame) show() {
	c := f.client
	xproto.MapWindow(c.wm.Conn(), c.id)

	xproto.ChangeSaveSet(c.wm.Conn(), xproto.SetModeInsert, c.win)
	xproto.MapWindow(c.wm.Conn(), c.win)
	xproto.GrabButton(f.client.wm.Conn(), true, f.client.id,
		xproto.EventMaskButtonPress, xproto.GrabModeSync, xproto.GrabModeSync,
		f.client.wm.X().RootWin(), xproto.CursorNone, xproto.ButtonIndex1, xproto.ModMaskAny)
	f.updateInputShape()

	userMod := uint16(xproto.ModMask4)
	if tyde.Instance().Settings().KeyboardModifier() == fyne.KeyModifierAlt {
		userMod = xproto.ModMask1
	}

	xproto.GrabButton(f.client.wm.Conn(), false, f.client.id,
		xproto.EventMaskButtonPress, xproto.GrabModeSync, xproto.GrabModeSync,
		0, xproto.CursorNone, xproto.ButtonIndex4, userMod)
	xproto.GrabButton(f.client.wm.Conn(), false, f.client.id,
		xproto.EventMaskButtonPress, xproto.GrabModeSync, xproto.GrabModeSync,
		0, xproto.CursorNone, xproto.ButtonIndex5, userMod)

	c.RaiseToTop()
	c.Focus()
}

func (f *frame) unmaximizeApply(force bool) {
	// When leaving fullscreen, force is set so we bypass the maximize guards and
	// always restore the previous geometry, even for fixed-size windows.
	if !force {
		if windowSizeFixed(f.client.wm.X(), f.client.win) ||
			!windowSizeCanMaximize(f.client.wm.X(), f.client) {
			return
		}
	}
	if f.client.restoreWidth == 0 && f.client.restoreHeight == 0 {
		screen := tyde.Instance().Screens().ScreenForWindow(f.client)
		f.client.restoreWidth = uint16(screen.Width / 2)
		f.client.restoreHeight = uint16(screen.Height / 2)
	}
	f.updateGeometry(f.client.restoreX, f.client.restoreY, f.client.restoreWidth, f.client.restoreHeight, true)
	f.notifyInnerGeometry()
	f.applyTheme()
}

func (f *frame) queueGeometry(x int16, y int16, width uint16, height uint16, force bool) {
	f.pendingMu.Lock()
	if f.pendingGeometry == nil {
		f.pendingGeometry = make(chan *configureGeometry, 50)
		go f.configureLoop()
	}
	ch := f.pendingGeometry
	f.pendingMu.Unlock()
	ch <- &configureGeometry{x, y, width, height, force}
}

func (f *frame) updateGeometry(x, y int16, w, h uint16, force bool) {
	var move, resize bool
	if !force {
		resize = w != f.width || h != f.height
		move = x != f.x || y != f.y
		if !move && !resize {
			return
		}
	}

	currentScreen := tyde.Instance().Screens().ScreenForWindow(f.client)
	widened := w != f.width && f.pendingGeometry == nil // a drag re-renders when it ends

	f.x = x
	f.y = y

	innerX, innerY, innerW, innerH := f.getInnerWindowCoordinates(w, h)

	f.childWidth = uint16(innerW)
	f.childHeight = uint16(innerH)

	// During drag (pendingGeometry active) and move-only (no resize),
	// skip expensive X11 ConfigureWindow and only update the compositor visual.
	// The actual X11 position is synced when the drag ends.
	if f.pendingGeometry != nil && move && !resize && x11.VisualMoveCallback != nil {
		x11.VisualMoveCallback(uint32(f.client.id), f.x, f.y, f.width, f.height)
		return
	}

	xproto.ConfigureWindow(f.client.wm.Conn(), f.client.id, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(f.x), uint32(f.y), uint32(f.width), uint32(f.height)})
	xproto.ConfigureWindow(f.client.wm.Conn(), f.client.win, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{innerX, innerY, uint32(f.childWidth), uint32(f.childHeight)})

	newScreen := tyde.Instance().Screens().ScreenForWindow(f.client)
	if newScreen != currentScreen {
		f.updateScale()
		tyde.Instance().Screens().SetActive(newScreen)
	} else if widened {
		fyne.Do(f.renderDecoration)
	}

	f.updateInputShape()
}

func (f *frame) updateScale() {
	// update border offset for current scale and redraw borders
	f.updateGeometry(f.x, f.y, f.width, f.height, true)
	f.applyTheme()
}

// updateInputShape sets the frame's X11 input shape to exclude desktop panel areas.
// This allows mouse events in panel regions to pass through to the Fyne root window.
func (f *frame) updateInputShape() {
	inst := tyde.Instance()
	if inst == nil {
		return
	}

	screen := inst.Screens().ScreenForWindow(f.client)
	if screen == nil || screen != inst.Screens().Primary() {
		// Only the primary screen has panels; other screens need no clipping
		return
	}

	cbX, cbY, cbW, cbH := inst.ContentBoundsPixels(screen)

	// Build rectangles that represent the frame area INSIDE the content bounds.
	// Any part of the frame outside the content bounds (i.e. under a panel) is excluded.
	var rects []xproto.Rectangle

	// Clip frame bounds to content bounds
	fx, fy := int32(f.x), int32(f.y)
	fw, fh := int32(f.width), int32(f.height)

	cx1, cy1 := int32(cbX)+int32(screen.X), int32(cbY)+int32(screen.Y)
	cx2, cy2 := cx1+int32(cbW), cy1+int32(cbH)

	// Intersection of frame and content area
	ix1 := max32(fx, cx1)
	iy1 := max32(fy, cy1)
	ix2 := min32(fx+fw, cx2)
	iy2 := min32(fy+fh, cy2)

	if ix1 < ix2 && iy1 < iy2 {
		// There is an intersection — set input shape to just that area (in frame-local coords)
		rects = append(rects, xproto.Rectangle{
			X:      int16(ix1 - fx),
			Y:      int16(iy1 - fy),
			Width:  uint16(ix2 - ix1),
			Height: uint16(iy2 - iy1),
		})
	}

	if len(rects) == 0 {
		// Frame is entirely under panels — make it fully input-transparent
		rects = append(rects, xproto.Rectangle{X: 0, Y: 0, Width: 0, Height: 0})
	}

	_ = shape.RectanglesChecked(f.client.wm.Conn(), shape.SoSet, shape.SkInput,
		0, f.client.id, 0, 0, rects).Check()

	// Re-assert any overlays which take priority.
	if os, ok := f.client.wm.(overlayShaper); ok {
		os.RefreshOverlayShape(f.client.id, int(f.x), int(f.y), int(f.width), int(f.height))
	}
}

// overlayShaper is implemented by the window manager to re-apply the active overlay's
// input-transparent regions to a single frame after it resets its own input shape.
type overlayShaper interface {
	RefreshOverlayShape(frameID xproto.Window, fx, fy, fw, fh int)
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
