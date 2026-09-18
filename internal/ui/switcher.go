package ui

import (
	"image/color"
	"time"

	"fyshos.com/tyde/internal/icon"
	wmTheme "fyshos.com/tyde/theme"
	"github.com/FyshOS/appie"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
)

const (
	switcherIconSize = 64
	switcherTextSize = 24
)

type switchIcon struct {
	widget.BaseWidget
	current bool

	parent *Switcher
	win    tyde.Window
}

func (s *switchIcon) CreateRenderer() fyne.WidgetRenderer {
	var res fyne.Resource
	title := s.win.Properties().Title()
	app := icon.FindAppFromWinInfo(s.win, s.parent.provider)
	if app != nil {
		res = app.Icon(tyde.Instance().Settings().IconTheme(), switcherIconSize*2)
		title = app.Name()
	} else {
		res = s.win.Properties().Icon()
	}
	if res == nil {
		res = wmTheme.BrokenImageIcon
	}

	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = theme.InputRadiusSize()
	img := canvas.NewImageFromResource(res)
	if s.win.Iconic() {
		img.Translucency = 0.67
	}
	text := widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{})
	text.Truncation = fyne.TextTruncateEllipsis
	return &switchIconRenderer{
		icon: s, bg: bg,
		img: img, text: text, objects: []fyne.CanvasObject{bg, img, text},
	}
}

// FocusGained is called when this icon gets focus - it becomes the candidate for window raising
func (s *switchIcon) FocusGained() {
	s.current = true
	s.Refresh()
}

// FocusLost is called when a different item is focused
func (s *switchIcon) FocusLost() {
	s.current = false
	s.Refresh()
}

// Focused returns whether or not this icon has focus
func (s *switchIcon) Focused() bool {
	return s.current
}

// TypedRune notifies when a rune is typed, we don't care
func (s *switchIcon) TypedRune(rune) {
}

// TypedKey is called when a key is typed. Currently this is handled by the window manager
func (s *switchIcon) TypedKey(*fyne.KeyEvent) {
}

func newSwitchIcon(p *Switcher, win tyde.Window) *switchIcon {
	ret := &switchIcon{
		parent: p,
		win:    win,
	}
	ret.ExtendBaseWidget(ret)
	return ret
}

type switchIconRenderer struct {
	icon *switchIcon

	bg      *canvas.Rectangle
	img     *canvas.Image
	text    *widget.Label
	objects []fyne.CanvasObject
}

func (s switchIconRenderer) Layout(size fyne.Size) {
	s.bg.Move(fyne.NewPos(-theme.Padding()/2, -theme.Padding()/2))
	s.bg.Resize(size.Add(fyne.NewSize(theme.Padding(), theme.Padding())))
	s.img.Resize(fyne.NewSize(switcherIconSize, switcherIconSize))
	s.text.Resize(fyne.NewSize(switcherIconSize+theme.Padding()*2, switcherTextSize))
	s.text.Move(fyne.NewPos(-theme.Padding(), switcherIconSize-theme.Padding()/2))
}

func (s switchIconRenderer) MinSize() fyne.Size {
	return fyne.NewSize(switcherIconSize, switcherIconSize+switcherTextSize+theme.Padding())
}

func (s switchIconRenderer) Refresh() {
	if s.icon.current {
		s.bg.FillColor = theme.Color(theme.ColorNamePrimary)
	} else {
		s.bg.FillColor = color.Transparent
	}
	canvas.Refresh(s.icon)
}

func (s switchIconRenderer) Objects() []fyne.CanvasObject {
	return s.objects
}

func (s switchIconRenderer) Destroy() {
}

// Switcher is an instance of a visible app switcher that can request a change in window stacking order
type Switcher struct {
	content  fyne.CanvasObject
	icons    []fyne.CanvasObject
	provider appie.Provider
	selected tyde.Window
}

func (s *Switcher) currentIndex() int {
	for i, item := range s.icons {
		if item.(*switchIcon).current {
			return i
		}
	}

	return 0
}

func (s *Switcher) setCurrent(i int) {
	// Unfocus current
	for _, ico := range s.icons {
		ico.(*switchIcon).FocusLost()
	}
	icon := s.icons[i].(*switchIcon)
	icon.FocusGained()
	s.selected = icon.win
}

// Next selects the next logical lower window in the stack.
// If the bottom most window is selected then it will wrap to the topmost.
// This will be raised if the change is applied by calling HideApply.
func (s *Switcher) Next() {
	if len(s.icons) == 0 {
		return
	}

	i := s.currentIndex()
	i++
	if i >= len(s.icons) {
		i = 0
	}
	s.setCurrent(i)
}

// Previous selects the next logical higher window in the stack.
// If the top most window was selected it wraps to the lowest.
// This will be raised if the change is applied by calling HideApply.
func (s *Switcher) Previous() {
	if len(s.icons) == 0 {
		return
	}

	i := s.currentIndex()
	i--
	if i < 0 {
		i = len(s.icons) - 1
	}
	s.setCurrent(i)
}

func (s *Switcher) raise(win tyde.Window) {
	if win.Iconic() {
		win.Uniconify()
	}
	win.RaiseToTop()
}

func (s *Switcher) loadUI() {
	r, g, b, _ := theme.Color(theme.ColorNameOverlayBackground).RGBA()
	bgCol := &color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 230}
	bg := canvas.NewRectangle(bgCol)
	bg.CornerRadius = theme.Size(theme.SizeNameInputRadius) + theme.Padding()
	bg.Shadow = wmTheme.WindowShadow(true)

	inner := container.NewPadded(container.NewHBox(s.icons...))
	s.content = container.NewStack(bg, inner)
}

func (s *Switcher) loadIcons(list []tyde.Window) []fyne.CanvasObject {
	var ret []fyne.CanvasObject

	for _, item := range list {
		if item.Desktop() != tyde.Instance().Desktop() && !item.Pinned() {
			continue
		}
		ret = append(ret, newSwitchIcon(s, item))
	}

	return ret
}

// HideApply dismisses the application Switcher and raises
// whichever window was selected.
func (s *Switcher) HideApply() {
	s.HideCancel()

	if s.selected != nil {
		s.raise(s.selected)
	}
}

// HideCancel dismisses the application Switcher without changing window order.
func (s *Switcher) HideCancel() {
	go func() {
		time.Sleep(time.Millisecond * 100)

		tyde.Instance().HideOverlay(s.content)
	}()
}

// Show the app switcher, it would then be hidden with HideApply or HideCancel.
func (s *Switcher) Show() {
	inst := tyde.Instance()
	size := s.content.MinSize()

	primary := inst.Screens().Primary()
	scale := primary.CanvasScale()
	pW := float32(primary.Width) / scale
	pH := float32(primary.Height) / scale
	pos := fyne.NewPos((pW-size.Width)/2, (pH-size.Height)/2)

	inst.ShowOverlay(s.content, size, pos)
}

func newAppSwitcherAt(off int, wins []tyde.Window, prov appie.Provider) *Switcher {
	s := &Switcher{provider: prov}
	s.icons = s.loadIcons(wins)
	if len(s.icons) <= 1 { // don't actually show if only 1 is visible
		return nil
	}

	s.loadUI()
	if off < 0 {
		off = len(s.icons) + off // plus a negative is minus
	}
	fyne.Do(func() {
		s.setCurrent(off)
	})
	return s
}

// NewAppSwitcher creates the application Switcher to change windows.
// The most recently used not-top window will be selected by default.
// If the Switcher was already visible then it will select the next window in order.
func NewAppSwitcher(wins []tyde.Window, prov appie.Provider) *Switcher {
	return newAppSwitcherAt(1, wins, prov)
}

// NewAppSwitcherReverse creates the application Switcher to change windows.
// The least recently used window will be selected by default.
// If the Switcher was already visible then it will select the last window in order.
func NewAppSwitcherReverse(wins []tyde.Window, prov appie.Provider) *Switcher {
	return newAppSwitcherAt(-1, wins, prov)
}
