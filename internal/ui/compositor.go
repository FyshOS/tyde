package ui

import (
	"image"
	"image/color"
	"sync"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	wmTheme "fyshos.com/tyde/theme"
)

// WindowImage holds the Fyne image and pixel-space geometry for a single
// composited window. The platform compositor populates these fields.
//
// Images are double-buffered: the compositor writes to Back (from any
// goroutine) and the renderer swaps Back→Img.Image during its Refresh.
// Pending is set by the compositor after a push; the renderer clears it
// after consuming the frame. The compositor should skip capture while
// Pending is true to avoid wasteful work.
type WindowImage struct {
	ID   uint32
	Img  *canvas.Image
	X, Y int16
	W, H uint16

	// Shadow is set for a managed window, which is drawn over a drop shadow;
	// Active deepens that shadow for the window that has focus.
	Shadow, Active bool

	Back    atomic.Value // image.Image — latest frame from compositor
	Pending atomic.Bool  // true = refresh requested, not yet rendered

	shadow *canvas.Rectangle // drawn beneath Img when Shadow is set
}

// CompositorWidget is a Fyne widget that displays composited window images.
// It is platform-agnostic; the platform-specific compositor (e.g. X11) is
// responsible for capturing window content and calling the methods below.
type CompositorWidget struct {
	widget.BaseWidget

	Screen *tyde.Screen // the screen this widget renders on
	mu     sync.RWMutex
	images []*WindowImage // Fyne draw order: first = bottom, last = top

	// accessories holds the decorative objects of the window with each frame id,
	// wrapped in a container that we keep over that window.
	accessories    map[uint32]*fyne.Container
	accessoriesTop *fyne.Container
}

// NewCompositorWidget creates a new compositor widget for the given screen.
func NewCompositorWidget(screen *tyde.Screen) *CompositorWidget {
	w := &CompositorWidget{Screen: screen, accessoriesTop: container.NewWithoutLayout()}
	w.ExtendBaseWidget(w)
	return w
}

func (cw *CompositorWidget) CreateRenderer() fyne.WidgetRenderer {
	cont := container.NewWithoutLayout()
	return &compositorRenderer{
		widget:  cw,
		cont:    cont,
		objects: []fyne.CanvasObject{cont},
	}
}

// EnsureWindow ensures a window image entry exists for the given ID. Returns it.
func (cw *CompositorWidget) EnsureWindow(id uint32) *WindowImage {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	for _, wi := range cw.images {
		if wi.ID == id {
			return wi
		}
	}

	img := canvas.NewImageFromImage(nil)
	img.ScaleMode = canvas.ImageScaleFastest
	img.FillMode = canvas.ImageFillStretch

	wi := &WindowImage{ID: id, Img: img}
	cw.images = append(cw.images, wi) // append = on top
	return wi
}

// RemoveWindow removes a window image entry.
func (cw *CompositorWidget) RemoveWindow(id uint32) {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	for i, wi := range cw.images {
		if wi.ID == id {
			cw.images = append(cw.images[:i], cw.images[i+1:]...)
			return
		}
	}
}

// SetAccessories sets the decorative objects to interleave among the windows:
// byWindow[id] is drawn just above the window with that frame id, positioned
// relative to it, and top is drawn above all windows in screen coordinates. It
// does not by itself repaint - call Refresh after.
func (cw *CompositorWidget) SetAccessories(byWindow map[uint32][]fyne.CanvasObject, top []fyne.CanvasObject) {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	previous := cw.accessories
	cw.accessories = make(map[uint32]*fyne.Container, len(byWindow))
	for id, objs := range byWindow {
		cont, ok := previous[id]
		if !ok {
			cont = container.NewWithoutLayout()
		}
		cont.Objects = objs
		cw.accessories[id] = cont
	}
	cw.accessoriesTop.Objects = top

	for _, wi := range cw.images {
		cw.placeWindow(wi)
	}
}

// PlaceWindow lays a window image out from the geometry stored in wi, bringing
// any accessories hanging on that window with it.
func (cw *CompositorWidget) PlaceWindow(wi *WindowImage) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	cw.placeWindow(wi)
}

// placeWindow positions and sizes a window image, its shadow and its accessory
// container.
func (cw *CompositorWidget) placeWindow(wi *WindowImage) {
	scale := float32(1)
	if cw.Screen != nil {
		scale = cw.Screen.CanvasScale()
	}
	pos := fyne.NewPos(float32(wi.X)/scale, float32(wi.Y)/scale)
	size := fyne.NewSize(float32(wi.W)/scale, float32(wi.H)/scale)

	wi.Img.Move(pos)
	wi.Img.Resize(size)
	if wi.Shadow {
		if wi.shadow == nil {
			wi.shadow = canvas.NewRectangle(color.Transparent)
		}
		wi.shadow.Move(pos)
		wi.shadow.Resize(size)
		wi.shadow.CornerRadius = theme.Size(theme.SizeNameInnerWindowRadius)
		if shadow := wmTheme.WindowShadow(wi.Active); wi.shadow.Shadow != shadow {
			wi.shadow.Shadow = shadow
			wi.shadow.Refresh()
		}
	}
	if acc, ok := cw.accessories[wi.ID]; ok {
		// Unlike a canvas object, moving a container always marks the canvas
		// dirty - so only do it when the window really has moved, or every
		// refresh would schedule the next one.
		if acc.Position() != pos {
			acc.Move(pos)
		}
		acc.Resize(size) // no-op when unchanged
	}
}

// TopImage returns the image of the topmost window, or nil if empty.
func (cw *CompositorWidget) TopImage() image.Image {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	if len(cw.images) == 0 {
		return nil
	}
	return cw.images[len(cw.images)-1].Img.Image
}

// GetWindow returns the window image for a given ID.
func (cw *CompositorWidget) GetWindow(id uint32) *WindowImage {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	for _, wi := range cw.images {
		if wi.ID == id {
			return wi
		}
	}
	return nil
}

// Reorder reorders images to match the given ID list (top-first order).
// Only windows present in this widget are kept.
func (cw *CompositorWidget) Reorder(topFirst []uint32) {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	byID := make(map[uint32]*WindowImage, len(cw.images))
	for _, wi := range cw.images {
		byID[wi.ID] = wi
	}

	// Rebuild in Fyne draw order (bottom first = reverse of top-first)
	newImages := make([]*WindowImage, 0, len(cw.images))
	for i := len(topFirst) - 1; i >= 0; i-- {
		if wi, ok := byID[topFirst[i]]; ok {
			newImages = append(newImages, wi)
		}
	}
	cw.images = newImages
}

type compositorRenderer struct {
	widget  *CompositorWidget
	cont    *fyne.Container
	objects []fyne.CanvasObject // stable: always [cont]
}

func (r *compositorRenderer) Destroy() {}

func (r *compositorRenderer) Layout(size fyne.Size) {
	r.cont.Resize(size)
}

func (r *compositorRenderer) MinSize() fyne.Size {
	return fyne.NewSize(0, 0)
}

func (r *compositorRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *compositorRenderer) Refresh() {
	r.widget.mu.RLock()
	defer r.widget.mu.RUnlock()

	seen := make(map[uint32]bool, len(r.widget.images))
	objs := make([]fyne.CanvasObject, 0, len(r.widget.images))
	for _, wi := range r.widget.images {
		// Swap back→front: pick up the latest frame from the compositor.
		swapped := wi.Pending.Load()
		if swapped {
			if back := wi.Back.Load(); back != nil {
				wi.Img.Image = back.(image.Image)
			}
			wi.Pending.Store(false)
		}

		before := wi.Img.Size()
		r.widget.placeWindow(wi)
		// Only an image with new content or a new size needs its texture
		// rebuilt; refreshing every child on each capture is what makes a
		// resize crawl.
		if swapped || wi.Img.Size() != before {
			wi.Img.Refresh()
		}
		if wi.Shadow {
			objs = append(objs, wi.shadow)
		}
		objs = append(objs, wi.Img)

		// Decorations sitting on this window are drawn directly above it (and so
		// below any window stacked higher).
		seen[wi.ID] = true
		if acc, ok := r.widget.accessories[wi.ID]; ok {
			objs = append(objs, acc)
		}
	}

	// Accessories anchored to a window no longer present fall back to the top so
	// they are not lost mid-frame, followed by the always-on-top accessories.
	for id, acc := range r.widget.accessories {
		if !seen[id] {
			objs = append(objs, acc)
		}
	}
	objs = append(objs, r.widget.accessoriesTop)

	// Update the stable container's objects in place — never replace the
	// container itself — and repaint without refreshing every child.
	r.cont.Objects = objs
	canvas.Refresh(r.cont)
}
