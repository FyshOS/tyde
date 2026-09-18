package ui

import (
	"image/color"
	"log"
	"runtime/debug"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	wmTheme "fyshos.com/tyde/theme"
)

func (l *desktop) runFull() {
	debug.SetPanicOnFault(true)

	defer func() {
		if r := recover(); r != nil {
			log.Println("Tyde panic cause", r)
			debug.PrintStack()
			l.wm.Close() // attempt to close cleanly to leave X server running
		}
	}()

	// Show secondary windows before starting the event loop on primary
	l.running = true
	for _, sw := range l.screenWindows {
		if sw != l.primaryWin {
			sw.win.Show()
		}
	}

	l.primaryWin.win.ShowAndRun()
}

func (l *desktop) showMenuFull(menu *fyne.Menu, pos fyne.Position) {
	menuSize := widget.NewMenu(menu).MinSize()
	size := menuSize

	// Measure child menus to calculate the total hover-catch area.
	// The submenu appears to the right by default but Fyne flips it
	// to the left when there isn't enough room on the right.
	catchSize := size
	childWidth := float32(0)
	itemY := float32(0)
	for _, item := range menu.Items {
		itemH := widget.NewLabel(item.Label).MinSize().Height
		if item.ChildMenu != nil {
			childSize := widget.NewMenu(item.ChildMenu).MinSize()
			childWidth = childSize.Width
			catchSize.Width = size.Width + childSize.Width
			totalH := itemY + childSize.Height
			if totalH > catchSize.Height {
				catchSize.Height = totalH
			}
		}
		if item.IsSeparator {
			itemY += theme.SeparatorThicknessSize()
		} else {
			itemY += itemH
		}
	}

	// Check if submenus would overflow the right edge of the canvas — if so,
	// the catch area extends to the left and the menu content is offset within it.
	// This mirrors Fyne's own submenu flip logic which checks against the full canvas width.
	canvasWidth := l.primaryWin.win.Canvas().Size().Width
	contentOffset := fyne.NewPos(0, 0)
	if childWidth > 0 && pos.X+size.Width+childWidth > canvasWidth {
		pos.X -= childWidth
		contentOffset = fyne.NewPos(childWidth, 0)
	}

	// Flip the menu to the left of the anchor when it would overflow the right edge.
	if pos.X+size.Width > canvasWidth {
		pos.X -= size.Width
	}
	if pos.X < 0 {
		pos.X = 0
	}

	var combined fyne.CanvasObject

	// Wrap each menu item action (and child menu item actions) to dismiss the overlay
	var wrapItems func([]*fyne.MenuItem)
	wrapItems = func(items []*fyne.MenuItem) {
		for _, item := range items {
			if item.Action != nil {
				origAction := item.Action
				item.Action = func() {
					l.HideOverlay(combined)
					origAction()
				}
			}
			if item.ChildMenu != nil {
				wrapItems(item.ChildMenu.Items)
			}
		}
	}
	wrapItems(menu.Items)

	r, g, b, _ := theme.Color(theme.ColorNameOverlayBackground).RGBA()
	bgCol := &color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 230}
	bg := canvas.NewRectangle(bgCol)
	bg.Shadow = wmTheme.WindowShadow(true)

	menuWidget := widget.NewMenu(menu)
	content := container.NewStack(bg, menuWidget)

	combined = l.ShowOverlayWithBackdrop(content, size, catchSize, pos, contentOffset)
}
