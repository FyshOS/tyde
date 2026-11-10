package ui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// deskBadgeHold is how long the badge stays fully visible before fading out.
	deskBadgeHold = 350 * time.Millisecond
	// deskBadgeScale is the badge text height as a fraction of the screen height.
	deskBadgeScale = 0.15
	// deskBadgeMaxWidth is the widest the text may be as a fraction of the screen width.
	deskBadgeMaxWidth = 0.8
)

// deskBadge briefly shows the number of the desktop just switched to, large and
// centred on its screen. It is a full-screen, visual-only layer placed above the
// cube transition so the number reads through the roll, and it implements no
// pointer interfaces so input passes to whatever is beneath it.
type deskBadge struct {
	widget.BaseWidget
	bg   *canvas.Rectangle
	text *canvas.Text

	timer *time.Timer
	fade  *fyne.Animation
}

func newDeskBadge() *deskBadge {
	b := &deskBadge{
		bg:   canvas.NewRectangle(color.Transparent),
		text: canvas.NewText("", color.Transparent),
	}
	b.text.Alignment = fyne.TextAlignCenter
	b.text.TextStyle = fyne.TextStyle{Bold: true}
	b.ExtendBaseWidget(b)
	b.Hide()
	return b
}

// show presents label, a desktop's name or number, at full opacity, holds it
// briefly, then fades it out and hides the badge. A show during a hold or fade
// starts over. A long name is scaled down to fit within the screen.
func (b *deskBadge) show(label string) {
	b.cancel()

	b.text.Text = label
	fitDeskLabel(b.bg, b.text, b.Size())
	b.setOpacity(1)
	b.Show()
	b.Refresh()

	b.timer = time.AfterFunc(deskBadgeHold, func() {
		fyne.Do(b.fadeOut)
	})
}

func (b *deskBadge) fadeOut() {
	b.timer = nil
	var a *fyne.Animation
	a = fyne.NewAnimation(canvas.DurationStandard, func(f float32) {
		if b.fade != a {
			return // superseded
		}
		b.setOpacity(1 - f)
		if f >= 1 {
			b.fade = nil
			b.Hide()
		}
	})
	b.fade = a
	a.Start()
}

// cancel stops any pending hold or fade so a fresh show starts from the beginning.
func (b *deskBadge) cancel() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if b.fade != nil {
		b.fade.Stop()
		b.fade = nil
	}
}

// setOpacity applies the theme colours at the given opacity (0..1).
func (b *deskBadge) setOpacity(alpha float32) {
	tintDeskLabel(b.bg, b.text, alpha)
	b.bg.Refresh()
	b.text.Refresh()
}

func (b *deskBadge) CreateRenderer() fyne.WidgetRenderer {
	return &deskBadgeRenderer{badge: b}
}

// deskBadgeRenderer centres a padded box holding the number within the full layer.
type deskBadgeRenderer struct {
	badge *deskBadge
}

func (r *deskBadgeRenderer) Layout(size fyne.Size) {
	placeDeskLabel(r.badge.bg, r.badge.text, size)
}

func (r *deskBadgeRenderer) MinSize() fyne.Size {
	return fyne.Size{}
}

func (r *deskBadgeRenderer) Refresh() {
	r.Layout(r.badge.Size())
	canvas.Refresh(r.badge)
}

func (r *deskBadgeRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.badge.bg, r.badge.text}
}

func (r *deskBadgeRenderer) Destroy() {
	r.badge.cancel()
}

// fitDeskLabel sizes a desktop label for an area of the given size: the text height
// is a fixed fraction of the area's height, and a long name is scaled down to fit
// its width.
func fitDeskLabel(bg *canvas.Rectangle, text *canvas.Text, size fyne.Size) {
	text.TextSize = size.Height * deskBadgeScale
	// Text width is not quite proportional to its size, so converge in a few steps.
	maxW := size.Width * deskBadgeMaxWidth
	for i := 0; i < 4 && maxW > 0 && text.MinSize().Width > maxW; i++ {
		text.TextSize *= maxW / text.MinSize().Width
	}
	bg.CornerRadius = text.TextSize / 4
}

// placeDeskLabel centres a padded box holding the label within an area of the given size.
func placeDeskLabel(bg *canvas.Rectangle, text *canvas.Text, size fyne.Size) {
	pad := text.TextSize / 12
	box := text.MinSize().Add(fyne.NewSize(pad*2, pad))
	if box.Width < box.Height {
		box.Width = box.Height // a single digit sits in a square
	}
	origin := fyne.NewPos((size.Width-box.Width)/2, (size.Height-box.Height)/2)

	bg.Resize(box)
	bg.Move(origin)
	text.Resize(fyne.NewSize(box.Width, text.MinSize().Height))
	text.Move(origin.Add(fyne.NewPos(0, (box.Height-text.MinSize().Height)/2)))
}

// tintDeskLabel applies the theme colours to a desktop label at the given opacity (0..1).
func tintDeskLabel(bg *canvas.Rectangle, text *canvas.Text, alpha float32) {
	bg.FillColor = withAlpha(theme.Color(theme.ColorNameOverlayBackground), uint8(alpha*0.85*0xff))
	text.Color = withAlpha(theme.Color(theme.ColorNameForeground), uint8(alpha*0xff))
}
