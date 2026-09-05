package updates

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	wmtheme "fyshos.com/tyde/theme"
)

const ModuleName = "Updates"

var updatesMeta = tyde.ModuleMetadata{
	Name:        ModuleName,
	NewInstance: newUpdates,
}

// autoStart gates the background schedule so tests can build the widget without
// a goroutine racing the test driver.
var autoStart = true

type updates struct {
	checker *Checker

	root  *fyne.Container
	icon  *widget.Icon
	label *widget.Label
}

func newUpdates() tyde.Module {
	return &updates{checker: Shared()}
}

func (u *updates) Metadata() tyde.ModuleMetadata {
	return updatesMeta
}

func (u *updates) Destroy() {
	u.checker.Stop()
}

// StatusAreaWidget builds the indicator. It returns nil on a system whose
// package manager we cannot drive, so nothing is shown rather than an item that
// could never report anything.
func (u *updates) StatusAreaWidget() fyne.CanvasObject {
	if u.checker.Backend() == nil {
		return nil
	}

	u.icon = widget.NewIcon(wmtheme.UpdateIcon)
	u.label = widget.NewLabel("")
	u.root = container.New(&narrowRow{}, u.icon, u.label)

	row := widget.NewButtonWithIcon("", wmtheme.UpdateIcon, func() {
		if d := tyde.Instance(); d != nil {
			d.ShowSettings("Updates")
		}
	})
	row.ExtendBaseWidget(row)

	// The status area is for things needing attention, so stay out of the way
	// until there is actually something to report.
	row.Hide()
	u.checker.SetListener("status", func() { u.refresh(row) })
	u.refresh(row)

	if autoStart {
		u.checker.Start()
	}
	return row
}

// refresh syncs the indicator with the checker state. It must run on the render
// thread; the checker guarantees that for listener callbacks.
func (u *updates) refresh(row fyne.CanvasObject) {
	res, checking, _, err := u.checker.State()

	switch {
	case len(res.Updates) > 0:
		u.icon.SetResource(wmtheme.UpdateIcon)
		u.label.SetText(updateCount(len(res.Updates)))
		row.Show()
	case err != nil && !checking:
		// A failed check is worth surfacing: silently showing nothing would be
		// indistinguishable from "up to date", which is exactly the wrong thing
		// to imply when we do not actually know.
		u.icon.SetResource(theme.NewErrorThemedResource(wmtheme.UpdateIcon))
		u.label.SetText("Check failed")
		row.Show()
	default:
		row.Hide()
	}
}

// updateCount renders the pending count for the narrow status area.
func updateCount(n int) string {
	if n == 1 {
		return "1 update"
	}
	return fmt.Sprintf("%d updates", n)
}

// narrowRow lays an icon beside its label, dropping the label when the widget
// panel is in narrow mode. It mirrors the layout the other status modules use
// so the update row lines up with battery, sound and network.
type narrowRow struct{}

func (h *narrowRow) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(fyne.NewSize(size.Height, size.Height))
	objects[1].Resize(fyne.NewSize(size.Width-size.Height-theme.Padding(), size.Height))
	objects[1].Move(fyne.NewPos(size.Height+theme.Padding(), 0))

	if tyde.Instance() != nil && tyde.Instance().Settings().NarrowWidgetPanel() {
		objects[1].Hide()
	} else {
		objects[1].Show()
	}
}

func (h *narrowRow) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(36, 36)
}
