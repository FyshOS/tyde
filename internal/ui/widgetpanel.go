package ui

import (
	"image/color"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/disintegration/imaging"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	wmtheme "fyshos.com/tyde/theme"
)

// Go date package does not follow changing timezones, so we will.
// startedOffset is the minutes from UTC in our starting timezone.
var startedOffset int

// currentOffset is the latest minutes-from-UTC offset, refreshed off the render
// thread by the clock ticker (getOffset shells out to `date`, so it must not run
// on the render thread). adjustedNow reads this cached value.
var currentOffset atomic.Int64

type widgetRenderer struct {
	panel *widgetPanel
	bg    *canvas.Rectangle

	layout  fyne.Layout
	objects []fyne.CanvasObject
}

func (w *widgetRenderer) MinSize() fyne.Size {
	return w.layout.MinSize(w.objects)
}

func (w *widgetRenderer) Layout(size fyne.Size) {
	w.bg.Resize(size)
	w.layout.Layout(w.objects[1:], size)
}

func (w *widgetRenderer) Refresh() {
	w.bg.FillColor = wmtheme.WidgetPanelBackground()
	w.bg.Refresh()

	w.panel.account.SetText(w.panel.accountLabel())
	if w.panel.desk.Settings().NarrowWidgetPanel() {
		w.panel.clocks.Objects[0].Hide()
		w.panel.clocks.Objects[1].Show()
	} else {
		w.panel.clocks.Objects[0].Show()
		w.panel.clocks.Objects[1].Hide()
	}
	fg := theme.Color(theme.ColorNameForeground)
	w.panel.clock.Color = fg
	w.panel.vClock.Color = fg
	canvas.Refresh(w.panel.clock)
}

func (w *widgetRenderer) Objects() []fyne.CanvasObject {
	return w.objects
}

func (w *widgetRenderer) Destroy() {
}

type widgetPanel struct {
	widget.BaseWidget

	desk            tyde.Desktop
	about, settings fyne.Window
	settingsNav     *settingsNav // retained so a re-show can jump to a named panel

	account         *widget.Button
	clock, vClock   *canvas.Text
	date            *widget.Label
	rotated         *canvas.Image
	modules, clocks *fyne.Container
	notifications   fyne.CanvasObject
}

// startClock drives the once-a-second clock refresh. It is a package var so
// tests can disable the background ticker, which would otherwise race the test
// goroutine.
var startClock = func(w *widgetPanel) {
	go func() {
		t := time.NewTicker(time.Second)
		for range t.C {
			refreshOffset() // re-read the timezone offset off the render thread
			fyne.Do(w.clockRefresh)
		}
	}()
}

func (w *widgetPanel) clockRefresh() {
	if w.rotated == nil {
		return // not yet been drawn so don't worry
	}

	w.clock.Text = w.formattedTime()
	w.vClock.Text = w.formattedTime()
	canvas.Refresh(w.clock)
	if w.desk.Settings().NarrowWidgetPanel() {
		w.rotate(w.vClock)
	}

	w.date.SetText(w.formattedDate())
	w.date.Refresh()
}

func (w *widgetPanel) formattedTime() string {
	if w.desk.Settings().ClockFormatting() == "12h" {
		return adjustedNow().Format("3:04pm")
	}
	return adjustedNow().Format("15:04")
}

func (w *widgetPanel) formattedDate() string {
	format := "2 Jan"
	if w.desk.Settings().NarrowWidgetPanel() {
		format = "2\nJan"
	}

	return adjustedNow().Format(format)
}

func (w *widgetPanel) createClock() {
	var style fyne.TextStyle
	style.Monospace = true
	startedOffset = getOffset()
	currentOffset.Store(int64(startedOffset))

	fg := theme.Color(theme.ColorNameForeground)
	w.clock = &canvas.Text{
		Color:     fg,
		Text:      w.formattedTime(),
		Alignment: fyne.TextAlignCenter,
		TextStyle: style,
		TextSize:  3 * theme.TextSize(),
	}
	w.vClock = &canvas.Text{
		Color:     fg,
		Text:      w.formattedTime(),
		Alignment: fyne.TextAlignCenter,
		TextStyle: style,
		TextSize:  wmtheme.NarrowBarWidth * 1.5,
	}
	w.date = &widget.Label{
		Text:      w.formattedDate(),
		Alignment: fyne.TextAlignCenter,
		TextStyle: style,
	}

	startClock(w)
}

func (w *widgetPanel) rotate(time *canvas.Text) {
	c := software.NewTransparentCanvas()
	c.SetPadded(false)
	c.SetContent(time)

	img := c.Capture()
	out := imaging.Rotate270(img)

	w.rotated.Image = out
	ratio := time.MinSize().Width / time.MinSize().Height
	space := wmtheme.NarrowBarWidth - theme.Padding()*2
	fyne.Do(func() {
		w.rotated.SetMinSize(fyne.NewSize(space, space*ratio))
		w.rotated.Refresh()
	})
}

func (w *widgetPanel) CreateRenderer() fyne.WidgetRenderer {
	narrow := w.desk.Settings().NarrowWidgetPanel()
	accountLabel := w.accountLabel()
	var account *widget.Button
	w.account = widget.NewButtonWithIcon(accountLabel, accountIcon(), func() {
		w.showAccountMenu(account)
	})

	w.rotated = &canvas.Image{}
	w.clocks = container.NewStack(w.clock, container.New(&vClockPad{}, w.rotated))
	if narrow {
		w.clock.Hide()
	} else {
		w.clocks.Objects[1].Hide()
	}
	w.clockRefresh()

	bg := canvas.NewRectangle(wmtheme.WidgetPanelBackground())
	objects := []fyne.CanvasObject{
		bg,
		canvas.NewRectangle(color.Transparent), // clear top edge for clocks
		w.clocks,
		w.date,
		w.notifications,
	}

	w.modules = container.NewVBox()
	objects = append(objects, layout.NewSpacer(), w.modules, w.account)
	w.loadModules(w.desk.Modules())

	return &widgetRenderer{
		panel:   w,
		bg:      bg,
		layout:  layout.NewVBoxLayout(),
		objects: objects,
	}
}

func (w *widgetPanel) MinSize() fyne.Size {
	if w.desk.Settings().NarrowWidgetPanel() {
		return fyne.NewSize(wmtheme.NarrowBarWidth, 200)
	}
	return fyne.NewSize(wmtheme.WidgetPanelWidth, 200)
}

func (w *widgetPanel) accountLabel() string {
	if w.desk.Settings().NarrowWidgetPanel() {
		return ""
	}
	currentUser, err := user.Current()
	if err != nil {
		fyne.LogError("Unable to look up user", err)
		return "Account"
	}
	displayName := currentUser.Username
	return displayName
}

// accountIcon returns the user's avatar for the account button: their ~/.face
// image if they have set one (see the Account settings tab), otherwise the
// generic user icon. The file is read once here, not on every panel refresh.
func accountIcon() fyne.Resource {
	p := facePath()
	if p == "" {
		return wmtheme.UserIcon
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return wmtheme.UserIcon
	}
	return fyne.NewStaticResource("face", data)
}

// refreshAccountIcon updates the account button to the current ~/.face image,
// used when the user changes their picture in settings.
func (w *widgetPanel) refreshAccountIcon() {
	if w.account != nil {
		w.account.SetIcon(accountIcon())
	}
}

func (w *widgetPanel) reloadModules(mods []tyde.Module) {
	w.modules.Objects = nil
	w.loadModules(mods)
	w.modules.Refresh()
}

func (w *widgetPanel) loadModules(mods []tyde.Module) {
	for _, m := range mods {
		if statusMod, ok := m.(tyde.StatusAreaModule); ok {
			wid := statusMod.StatusAreaWidget()
			if wid == nil {
				continue
			}

			w.modules.Objects = append(w.modules.Objects, wid)
		}
	}
}

func newWidgetPanel(rootDesk tyde.Desktop) *widgetPanel {
	w := &widgetPanel{desk: rootDesk}
	w.ExtendBaseWidget(w)
	w.notifications = startNotifications()
	w.createClock()

	return w
}

type vClockPad struct {
	minCache fyne.Size
}

func (u *vClockPad) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	objects[0].Resize(objects[0].MinSize())
	objects[0].Move(fyne.NewPos(5, 0))
}

func (u *vClockPad) MinSize(objects []fyne.CanvasObject) fyne.Size {
	clockMin := objects[0].MinSize()
	u.minCache = u.minCache.Max(clockMin)
	return u.minCache.Subtract(fyne.NewSize(0, theme.Padding()))
}

func adjustedNow() time.Time {
	newOffset := int(currentOffset.Load())
	return time.Now().Add(time.Minute * time.Duration(newOffset-startedOffset))
}

// refreshOffset re-reads the timezone offset (getOffset shells out to `date`, so
// this must run off the Fyne render thread) and caches it for adjustedNow.
func refreshOffset() {
	currentOffset.Store(int64(getOffset()))
}

func getOffset() int {
	ret, err := exec.Command("date", "+%z").Output()
	if err != nil {
		fyne.LogError("Failed to load date offset", err)
		return 0
	}

	if len(ret) <= 2 {
		fyne.LogError("Invalid offset format "+string(ret), err)
	}

	hourStr := string(ret[0 : len(ret)-3])
	minStr := string(ret[len(ret)-3:])

	hours, _ := strconv.ParseInt(hourStr, 10, 64)
	mins, _ := strconv.ParseInt(minStr, 10, 0)
	return int(hours)*60 + int(mins)
}
