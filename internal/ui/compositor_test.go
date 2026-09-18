package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	wmTheme "fyshos.com/tyde/theme"
)

// indexOf returns the position of obj in objs, or -1. Accessories are drawn
// inside a container held over their window, so those are searched too.
func indexOf(objs []fyne.CanvasObject, obj fyne.CanvasObject) int {
	for i, o := range objs {
		if o == obj {
			return i
		}
		if cont, ok := o.(*fyne.Container); ok {
			for _, child := range cont.Objects {
				if child == obj {
					return i
				}
			}
		}
	}
	return -1
}

// TestCompositorInterleavesAccessories verifies that a window accessory is drawn
// directly above its window (so a window stacked higher occludes it), and that
// top accessories are drawn above everything.
func TestCompositorInterleavesAccessories(t *testing.T) {
	cw := NewCompositorWidget(nil)
	lower := cw.EnsureWindow(1) // appended first => bottom
	upper := cw.EnsureWindow(2) // appended next => on top

	onLower := canvas.NewRectangle(color.White)
	onTop := canvas.NewRectangle(color.Black)
	cw.SetAccessories(map[uint32][]fyne.CanvasObject{1: {onLower}}, []fyne.CanvasObject{onTop})

	r := cw.CreateRenderer().(*compositorRenderer)
	r.Refresh()
	objs := r.cont.Objects

	iLowerWin := indexOf(objs, lower.Img)
	iUpperWin := indexOf(objs, upper.Img)
	iOnLower := indexOf(objs, onLower)
	iOnTop := indexOf(objs, onTop)

	for name, idx := range map[string]int{"lowerWin": iLowerWin, "upperWin": iUpperWin, "onLower": iOnLower, "onTop": iOnTop} {
		if idx < 0 {
			t.Fatalf("%s not present in draw order", name)
		}
	}
	// Accessory on the lower window sits above it but below the upper window.
	if !(iLowerWin < iOnLower && iOnLower < iUpperWin) {
		t.Fatalf("accessory not occluded by higher window: lowerWin=%d onLower=%d upperWin=%d", iLowerWin, iOnLower, iUpperWin)
	}
	// Top accessory is above all windows.
	if iOnTop < iUpperWin {
		t.Fatalf("top accessory should be above all windows: onTop=%d upperWin=%d", iOnTop, iUpperWin)
	}
}

// TestCompositorAccessoryFallsBackToTop verifies that an accessory anchored to a
// window that no longer exists is still drawn (above everything) rather than lost.
func TestCompositorAccessoryFallsBackToTop(t *testing.T) {
	cw := NewCompositorWidget(nil)
	win := cw.EnsureWindow(1)
	orphan := canvas.NewRectangle(color.White)
	cw.SetAccessories(map[uint32][]fyne.CanvasObject{99: {orphan}}, nil)

	r := cw.CreateRenderer().(*compositorRenderer)
	r.Refresh()
	objs := r.cont.Objects

	if indexOf(objs, orphan) <= indexOf(objs, win.Img) {
		t.Fatal("orphaned accessory should fall back above the windows")
	}
}

// TestCompositorAccessoryFollowsItsWindow verifies the accessory contract: a
// module positions its object relative to the window and we keep the two
// together, without touching the position the module set.
func TestCompositorAccessoryFollowsItsWindow(t *testing.T) {
	cw := NewCompositorWidget(nil) // no screen => canvas scale of 1
	wi := cw.EnsureWindow(1)
	wi.X, wi.Y, wi.W, wi.H = 100, 200, 400, 300

	offset := fyne.NewPos(10, -20) // a pet hanging over the window's top edge
	pet := canvas.NewRectangle(color.White)
	pet.Move(offset)
	cw.SetAccessories(map[uint32][]fyne.CanvasObject{1: {pet}}, nil)

	acc := cw.accessories[1]
	if acc.Position() != fyne.NewPos(100, 200) {
		t.Fatalf("accessories were not placed over their window, got %v", acc.Position())
	}
	if acc.Size() != fyne.NewSize(400, 300) {
		t.Fatalf("accessories were not sized to their window, got %v", acc.Size())
	}

	// Drag the window: its decorations come along, still where the module put them.
	wi.X, wi.Y = 500, 260
	cw.PlaceWindow(wi)
	if acc.Position() != fyne.NewPos(500, 260) {
		t.Fatalf("accessories did not follow their window, got %v", acc.Position())
	}
	if pet.Position() != offset {
		t.Fatalf("the module's placement was overwritten: %v, expected %v", pet.Position(), offset)
	}
}

// TestCompositorShadowBeneathManagedWindow verifies that a managed window is
// drawn over a shadow that follows its geometry and focus, and that an
// unmanaged one (a menu, say) is just its image.
func TestCompositorShadowBeneathManagedWindow(t *testing.T) {
	cw := NewCompositorWidget(nil) // no screen => canvas scale of 1
	plain := cw.EnsureWindow(1)
	win := cw.EnsureWindow(2)
	win.Shadow, win.Active = true, true
	win.X, win.Y, win.W, win.H = 100, 200, 400, 300

	r := cw.CreateRenderer().(*compositorRenderer)
	r.Refresh()
	objs := r.cont.Objects

	if len(objs) != 4 || objs[0] != plain.Img || objs[1] != win.shadow || objs[2] != win.Img {
		t.Fatal("expected the plain image, then the shadow beneath the managed window's image")
	}
	if win.shadow.Position() != fyne.NewPos(100, 200) || win.shadow.Size() != fyne.NewSize(400, 300) {
		t.Fatalf("shadow should cover the window geometry, got %v %v", win.shadow.Position(), win.shadow.Size())
	}
	if win.shadow.Shadow != wmTheme.WindowShadow(true) {
		t.Fatal("focused window should have the active shadow")
	}

	win.Active = false
	r.Refresh()
	if win.shadow.Shadow != wmTheme.WindowShadow(false) {
		t.Fatal("unfocused window should have the inactive shadow")
	}
}
