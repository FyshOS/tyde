package wm

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// FindObjectAtPositionMatching looks for the deepest visible object within root
// that lies under pos, given relative to root's top-left, and that matches fn.
func FindObjectAtPositionMatching(pos fyne.Position, root fyne.CanvasObject, fn func(fyne.CanvasObject) bool) fyne.CanvasObject {
	if root == nil {
		return nil
	}

	obj, _ := findObjectAtPositionMatching(pos.Add(root.Position()), fn, root)
	return obj
}

// some internal Fyne functions that we find very useful as we have to drive the frame UI

func findObjectAtPositionMatching(mouse fyne.Position, matches func(object fyne.CanvasObject) bool,
	overlay fyne.CanvasObject, roots ...fyne.CanvasObject,
) (fyne.CanvasObject, fyne.Position) {
	var found fyne.CanvasObject
	var foundPos fyne.Position

	findFunc := func(walked fyne.CanvasObject, pos fyne.Position, clipPos fyne.Position, clipSize fyne.Size) bool {
		if !walked.Visible() {
			return false
		}

		if mouse.X < clipPos.X || mouse.Y < clipPos.Y {
			return false
		}

		if mouse.X >= clipPos.X+clipSize.Width || mouse.Y >= clipPos.Y+clipSize.Height {
			return false
		}

		if mouse.X < pos.X || mouse.Y < pos.Y {
			return false
		}

		if mouse.X >= pos.X+walked.Size().Width || mouse.Y >= pos.Y+walked.Size().Height {
			return false
		}

		if matches(walked) {
			found = walked
			foundPos = fyne.NewPos(mouse.X-pos.X, mouse.Y-pos.Y)
		}
		return false
	}

	if overlay != nil {
		walkVisibleObjectTree(overlay, findFunc, nil)
	} else {
		for _, root := range roots {
			if root == nil {
				continue
			}
			walkVisibleObjectTree(root, findFunc, nil)
			if found != nil {
				break
			}
		}
	}

	return found, foundPos
}

func walkObjectTree(
	obj fyne.CanvasObject,
	parent fyne.CanvasObject,
	offset, clipPos fyne.Position,
	clipSize fyne.Size,
	beforeChildren func(fyne.CanvasObject, fyne.Position, fyne.Position, fyne.Size) bool,
	afterChildren func(fyne.CanvasObject, fyne.CanvasObject),
	requireVisible bool,
) bool {
	if requireVisible && !obj.Visible() {
		return false
	}
	pos := obj.Position().Add(offset)

	var children []fyne.CanvasObject
	switch co := obj.(type) {
	case *fyne.Container:
		children = co.Objects
	case fyne.Widget:
		children = test.WidgetRenderer(co).Objects()

		if _, ok := obj.(fyne.Scrollable); ok {
			clipPos = pos
			clipSize = obj.Size()
		}
	}

	if beforeChildren != nil {
		if beforeChildren(obj, pos, clipPos, clipSize) {
			return true
		}
	}

	cancelled := false
	for _, child := range children {
		if walkObjectTree(child, obj, pos, clipPos, clipSize, beforeChildren, afterChildren, requireVisible) {
			cancelled = true
			break
		}
	}

	if afterChildren != nil {
		afterChildren(obj, parent)
	}
	return cancelled
}

func walkVisibleObjectTree(
	obj fyne.CanvasObject,
	beforeChildren func(fyne.CanvasObject, fyne.Position, fyne.Position, fyne.Size) bool,
	afterChildren func(fyne.CanvasObject, fyne.CanvasObject),
) bool {
	clipSize := fyne.NewSize(math.MaxInt32, math.MaxInt32)
	return walkObjectTree(obj, nil, fyne.NewPos(0, 0), fyne.NewPos(0, 0), clipSize, beforeChildren, afterChildren, true)
}
