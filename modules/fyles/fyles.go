package fyles

import (
	"image/color"
	"log"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"

	lib "github.com/FyshOS/fyles/pkg/fyles"
	"golang.org/x/sys/execabs"

	"fyshos.com/tyde"
	wmtheme "fyshos.com/tyde/theme"
)

var fylesMeta = tyde.ModuleMetadata{
	Name:        "Desktop Files",
	NewInstance: newFyles,
}

type fyles struct {
	icons *lib.Panel
}

func (f *fyles) Destroy() {
}

func (f *fyles) ScreenAreaWidget() fyne.CanvasObject {
	holder := container.NewPadded()
	win := tyde.Instance().Root()
	go func() {
		for win == nil {
			time.Sleep(10 * time.Millisecond)
			win = tyde.Instance().Root()
		}
		icons := lib.NewFylesPanel(f.tapped, win)
		icons.HideParent = true
		icons.Filter = filterHidden()
		list := desktopListing()
		f.icons = icons

		fyne.Do(func() {
			if list != nil {
				icons.SetListing(list)
			}

			holder.Objects = []fyne.CanvasObject{icons}
			holder.Refresh()
		})
	}()

	desk := tyde.Instance()
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(wmtheme.NarrowBarWidth, 1))
	barPad := r

	rightIndent := wmtheme.WidgetPanelWidth
	if desk.Settings().NarrowWidgetPanel() {
		rightIndent = wmtheme.NarrowBarWidth
	}
	widgetPad := canvas.NewRectangle(color.Transparent)
	widgetPad.SetMinSize(fyne.NewSize(rightIndent, 1))

	return container.NewBorder(nil, nil, barPad, widgetPad, holder)
}

func (f *fyles) Metadata() tyde.ModuleMetadata {
	return fylesMeta
}

// desktopListing reads the desktop directory and returns the items to show,
// the shortcuts we always offer first. Call this from a goroutine as it can be slow.
// Returns nil if the directory could not be read.
func desktopListing() []fyne.URI {
	home, _ := os.UserHomeDir()
	u := storage.NewFileURI(filepath.Join(home, "Desktop"))
	homeDir := newCustomURI("file://"+home, "Home", theme.FolderIcon())
	settings := newCustomURI("settings://", "Settings", theme.SettingsIcon())
	trash := newCustomURI("file://"+filepath.Join(home, ".local", "share", "Trash", "files"), "Trash", theme.DeleteIcon())

	list, err := storage.List(u)
	if err != nil {
		fyne.LogError("Could not read Desktop dir", err)
		return nil
	}

	return append([]fyne.URI{homeDir, trash, settings}, list...)
}

func (f *fyles) tapped(u fyne.URI) {
	go func() {
		time.Sleep(canvas.DurationShort)
		fyne.Do(f.icons.ClearSelection)
	}()

	if u.Scheme() == "settings" {
		tyde.Instance().ShowSettings("")
		return
	}
	p, err := execabs.LookPath("fyles")
	if p != "" && err == nil {
		if ok, _ := storage.CanList(u); ok {
			err := execabs.Command(p, u.Path()).Start()
			if err != nil {
				log.Println("Error opening Fyles", err)
			}
			return
		}
	} else {
		log.Println("Error opening folder in Fyles app", err)
		return
	}

	err = lib.Open(u)
	if err != nil {
		log.Println("Error opening file", err)
	}
}

// newFyles creates a new module that will manage desktop file icons.
func newFyles() tyde.Module {
	return &fyles{}
}

type filter struct{}

func (f *filter) Matches(u fyne.URI) bool {
	return u.Name()[0] != '.'
}

func filterHidden() storage.FileFilter {
	return &filter{}
}

type trashURI struct {
	fyne.URI

	name string
	icon fyne.Resource
}

func newCustomURI(str, name string, icon fyne.Resource) fyne.URI {
	u, _ := storage.ParseURI(str)
	return &trashURI{URI: u, name: name, icon: icon}
}

func (t *trashURI) Name() string {
	return t.name
}

func (t *trashURI) Icon() fyne.Resource {
	return t.icon
}
