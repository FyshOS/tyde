package ui

import (
	"image/color"
	"strings"

	"github.com/FyshOS/appie"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	"fyshos.com/tyde/modules/ai"
	wmTheme "fyshos.com/tyde/theme"
)

const (
	launcherWidth      = 350
	launcherMaxResults = 5
)

var appExec *picker

// runAsync runs f on a new goroutine in production. Tests override it to run inline.
var runAsync = func(f func()) { go f() }

type appEntry struct {
	widget.Entry

	pick *picker
}

func (e *appEntry) TypedKey(ev *fyne.KeyEvent) {
	switch ev.Name {
	case fyne.KeyEscape:
		e.pick.close()
	case fyne.KeyReturn:
		e.pick.pickSelected()
	case fyne.KeyUp:
		e.pick.setActiveIndex(e.pick.activeIndex - 1)
	case fyne.KeyDown:
		e.pick.setActiveIndex(e.pick.activeIndex + 1)
	default:
		e.Entry.TypedKey(ev)
	}
}

type picker struct {
	desk     tyde.Desktop
	callback func(data appie.AppData, actionID int)
	showMods bool

	entry       *appEntry
	appList     *fyne.Container
	appScroll   *container.Scroll
	activeIndex int

	bg       *canvas.Rectangle
	fullSize fyne.Size

	overlay  fyne.CanvasObject
	onClosed func()
}

func (l *picker) close() {
	if l.overlay != nil {
		l.desk.HideOverlay(l.overlay)
	}
	if l.onClosed != nil {
		l.onClosed()
	}
}

func (l *picker) pickSelected() {
	if len(l.appList.Objects) == 0 {
		return
	}

	l.appList.Objects[l.activeIndex].(*widget.Button).OnTapped()
}

func (l *picker) setActiveIndex(index int) {
	if index < 0 || index >= len(l.appList.Objects) {
		return
	}

	oldActive := l.appList.Objects[l.activeIndex].(*widget.Button)
	oldActive.Importance = widget.MediumImportance
	oldActive.Refresh()
	active := l.appList.Objects[index].(*widget.Button)
	active.Importance = widget.HighImportance
	active.Refresh()

	l.activeIndex = index
	l.appScroll.Offset = fyne.NewPos(0,
		active.Position().Y+active.Size().Height/2-l.appScroll.Size().Height/2)
	l.appScroll.Refresh()
}

func (l *picker) updateAppListMatching(input string) {
	l.activeIndex = 0
	l.appScroll.ScrollToTop()
	l.appList.Objects = l.appButtonListMatching(input)
	l.appList.Refresh()
	l.updateBgSize()
}

func (l *picker) updateBgSize() {
	if l.bg == nil {
		return
	}

	pad := l.entry.Theme().Size(theme.SizeNamePadding)
	entryHeight := l.entry.MinSize().Height
	h := entryHeight + pad*2

	if len(l.appList.Objects) > 0 {
		btnHeight := l.appList.Objects[0].(*widget.Button).MinSize().Height
		count := len(l.appList.Objects)
		if count > launcherMaxResults {
			count = launcherMaxResults
		}
		h += float32(count) * (btnHeight + pad)
	}
	if h > l.fullSize.Height {
		h = l.fullSize.Height
	}

	l.bg.Resize(fyne.NewSize(l.fullSize.Width, h))
	l.bg.Refresh()
}

func (l *picker) appButtonListMatching(input string) []fyne.CanvasObject {
	var appList []fyne.CanvasObject
	iconList := []appie.AppData{}

	dataRange := l.desk.IconProvider().FindAppsMatching(input)
	for _, data := range dataRange {
		if data == nil || data.Hidden() {
			continue
		}
		appData := data // capture for goroutine below
		app := widget.NewButtonWithIcon(appData.Name(), wmTheme.BrokenImageIcon, func() {
			l.callback(appData, -1)
			l.close()
		})
		app.Alignment = widget.ButtonAlignLeading

		appList = append(appList, app)
		iconList = append(iconList, data)

		for id, action := range appData.Actions() {
			actionID := id // capture for goroutine below
			app := widget.NewButtonWithIcon(data.Name()+" : "+action.Name(), wmTheme.BrokenImageIcon, func() {
				l.callback(appData, actionID)
				l.close()
			})
			app.Alignment = widget.ButtonAlignLeading

			appList = append(appList, app)
			iconList = append(iconList, data)
		}
	}
	runAsync(func() { l.loadIcons(iconList, appList) })

	appList = append(appList, l.loadSuggestionsMatching(input)...)
	if len(appList) > 0 {
		appList[0].(*widget.Button).Importance = widget.HighImportance
	}

	return appList
}

func (l *picker) loadIcons(dataRange []appie.AppData, appList []fyne.CanvasObject) {
	iconTheme := l.desk.Settings().IconTheme()

	for i, data := range dataRange {
		app := appList[i].(*widget.Button)
		icon := data.Icon(iconTheme, 32)
		fyne.Do(func() {
			app.SetIcon(icon)
		})
	}
}

func (l *picker) loadSuggestionsMatching(input string) []fyne.CanvasObject {
	var suggestList, searchList, aiList []fyne.CanvasObject

	for _, m := range l.desk.Modules() {
		suggest, ok := m.(tyde.LaunchSuggestionModule)
		if !ok {
			continue
		}

		name := m.Metadata().Name
		for _, item := range suggest.LaunchSuggestions(input) {
			launchData := item // capture for goroutine below
			button := widget.NewButtonWithIcon(item.Title(), item.Icon(), func() {
				l.close()
				launchData.Launch()
			})

			switch {
			case name == ai.ModuleName:
				aiList = append(aiList, button) // AI assistant sits after web search
			case strings.Contains(strings.ToLower(name), "search"):
				searchList = append(searchList, button)
			default:
				suggestList = append(suggestList, button)
			}
		}
	}

	if len(suggestList) == 0 && len(searchList) == 0 && len(aiList) == 0 {
		return nil
	}
	return append(append(suggestList, searchList...), aiList...)
}

func newAppPicker(callback func(appie.AppData, int)) *picker {
	appList := container.NewVBox()
	appScroller := container.NewScroll(appList)
	l := &picker{desk: tyde.Instance(), appList: appList, appScroll: appScroller, callback: callback}

	entry := &appEntry{pick: l}
	entry.ExtendBaseWidget(entry)
	entry.SetPlaceHolder("Application")
	entry.OnChanged = func(input string) {
		appList.Objects = nil
		if input == "" {
			l.appList.Refresh()
			l.updateBgSize()
			return
		}

		l.updateAppListMatching(input)
	}
	l.entry = entry

	return l
}

func (l *picker) show() {
	r, g, b, _ := theme.Color(theme.ColorNameOverlayBackground).RGBA()
	bgCol := &color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 230}
	l.bg = canvas.NewRectangle(bgCol)
	th := l.entry.Theme()
	pad := theme.Padding()
	l.bg.CornerRadius = th.Size(theme.SizeNameInputRadius) + pad
	l.bg.Shadow = wmTheme.WindowShadow(true)

	inner := container.NewBorder(l.entry, nil, nil, nil, l.appScroll)
	content := container.NewStack(container.NewWithoutLayout(l.bg), container.NewPadded(inner))

	d := l.desk.(*desktop)
	primary := d.Screens().Primary()
	scale := primary.CanvasScale()
	midX := float32(primary.Width/2) / scale
	midY := float32(primary.Height/2) / scale

	entryHeight := l.entry.MinSize().Height
	l.fullSize = fyne.NewSize(launcherWidth,
		entryHeight*float32(launcherMaxResults+1)+pad*float32(launcherMaxResults+4)-3)
	pos := fyne.NewPos(midX-l.fullSize.Width/2, midY-l.fullSize.Height/2)

	// Start bg at entry-only height
	l.bg.Resize(fyne.NewSize(l.fullSize.Width, entryHeight+pad*2))

	l.overlay = d.showOverlayWithBackdrop(content, l.fullSize, l.fullSize, pos, l.entry, fyne.Position{})
}

// ShowAppLauncher opens a new application launcher, closing an old one if it existed.
func ShowAppLauncher() {
	if appExec != nil {
		appExec.close()
		return
	}

	appExec = newAppPicker(func(app appie.AppData, actionID int) {
		var err error
		if actionID == -1 {
			err = tyde.Instance().RunApp(app)
		} else {
			err = tyde.Instance().RunAppAction(app, actionID)
		}
		if err != nil {
			fyne.LogError("Failed to start app", err)
			return
		}
	})
	appExec.showMods = true
	appExec.onClosed = func() {
		appExec = nil
	}
	appExec.show()
}
