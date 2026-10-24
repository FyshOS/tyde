package desktops

import (
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
)

// ModuleName is the name the virtual desktops module is registered and enabled under.
const ModuleName = "Spaces"

var desksMeta = tyde.ModuleMetadata{
	Name:        ModuleName,
	NewInstance: newDesktops,
}

// deskCount returns the configured number of virtual desktops.
func deskCount() int {
	return tyde.Instance().Settings().DesktopCount()
}

type desktops struct {
	gui *pager
}

func (d *desktops) DesktopChangeNotify(_ int) {
	d.gui.refresh()
}

func (d *desktops) Destroy() {
	tyde.Instance().WindowManager().RemoveStackListener(d.gui) // the pager is rebuilt with the module
}

func (d *desktops) Metadata() tyde.ModuleMetadata {
	return desksMeta
}

func (d *desktops) Shortcuts() map[*tyde.Shortcut]func() {
	count := deskCount()
	mapping := make(map[*tyde.Shortcut]func(), count+4)
	for i := 0; i < count && i < 9; i++ { // one digit key per desktop
		id := strconv.Itoa(i + 1)
		deskID := i
		// The count can shrink after these are bound, so check it when pressed.
		mapping[&tyde.Shortcut{Name: "Switch to Desktop " + id, KeyName: fyne.KeyName(id), Modifier: tyde.UserModifier}] = func() {
			if deskID >= deskCount() {
				return
			}
			d.setDesktop(deskID)
		}
		mapping[&tyde.Shortcut{Name: "Move Window to Desktop " + id, KeyName: fyne.KeyName(id), Modifier: tyde.UserModifier | fyne.KeyModifierShift}] = func() {
			wins := tyde.Instance().WindowManager().Windows()
			if deskID >= deskCount() || len(wins) == 0 {
				return
			}
			wins[0].SetDesktop(deskID)
		}
	}

	mapping[&tyde.Shortcut{Name: "Switch to Previous Desktop", KeyName: fyne.KeyUp, Modifier: tyde.UserModifier}] = func() {
		current := tyde.Instance().Desktop()
		if current == 0 {
			return
		}
		d.setDesktop(current - 1)
	}
	mapping[&tyde.Shortcut{Name: "Switch to Next Desktop", KeyName: fyne.KeyDown, Modifier: tyde.UserModifier}] = func() {
		current := tyde.Instance().Desktop()
		if current >= deskCount()-1 {
			return
		}
		d.setDesktop(current + 1)
	}
	mapping[&tyde.Shortcut{Name: "Move Window to Previous Desktop", KeyName: fyne.KeyUp, Modifier: tyde.UserModifier | fyne.KeyModifierShift}] = func() {
		current := tyde.Instance().Desktop()
		if current == 0 {
			return
		}

		if len(tyde.Instance().WindowManager().Windows()) == 0 {
			return
		}

		w := tyde.Instance().WindowManager().Windows()[0]
		w.SetDesktop(current - 1)
	}
	mapping[&tyde.Shortcut{Name: "Move Window to Next Desktop", KeyName: fyne.KeyDown, Modifier: tyde.UserModifier | fyne.KeyModifierShift}] = func() {
		current := tyde.Instance().Desktop()
		if current >= deskCount()-1 {
			return
		}

		if len(tyde.Instance().WindowManager().Windows()) == 0 {
			return
		}

		w := tyde.Instance().WindowManager().Windows()[0]
		w.SetDesktop(current + 1)
	}
	return mapping
}

func (d *desktops) StatusAreaWidget() fyne.CanvasObject {
	reveal := widget.NewButtonWithIcon("", theme.GridIcon(), func() {
		tyde.Instance().ShowDesktopOverview(deskCount())
	})
	reveal.Importance = widget.LowImportance

	pager := container.NewStack(d.gui.buttons, d.gui.wins, d.gui.labels)
	return container.NewVBox(reveal, pager)
}

func (d *desktops) setDesktop(id int) {
	tyde.Instance().SetDesktop(id)
	d.gui.refreshButtons()
}

// newDesktops creates a new module that will manage virtual desktops and display a pager widget.
func newDesktops() tyde.Module {
	d := &desktops{}
	d.gui = newPager(d)
	return d
}
