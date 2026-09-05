package ui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	deskDriver "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	wmtheme "fyshos.com/tyde/theme"
)

const (
	tileIconSize   = 36 // home-screen icon size
	tileWidth      = 100
	tileHeight     = 82
	headerIconSize = 48 // icon size once docked in the detail header

	seaHeight = 150 // height of the brand-water underlay, fading up into the background
	seaFish   = 72  // size of the mascot resting on the water
)

// settingsPanel is one settings category. It is shown as an icon on the home
// screen and, when opened, its content fills the detail view. Content is built
// lazily on first open and then cached so state (and any dbus/fprintd
// connections it opens) survives navigating away and back.
type settingsPanel struct {
	title string
	icon  fyne.Resource
	build func() fyne.CanvasObject

	content fyne.CanvasObject // cached built content
	tile    *settingsTile     // home-screen icon, drives the animation start/end

	tileCentre fyne.Position // grid-icon centre captured on open, reused to fly back
}

// settingsGroup is a titled cluster of panels on the home screen.
type settingsGroup struct {
	title  string
	panels []*settingsPanel
}

// settingsTile is a tappable vertical icon+label used on the settings home
// screen. It highlights on hover and reports a pointer cursor.
type settingsTile struct {
	widget.BaseWidget
	icon    *canvas.Image
	label   *widget.Label
	bg      *canvas.Rectangle
	onTap   func()
	hovered bool
}

func newSettingsTile(res fyne.Resource, title string, tapped func()) *settingsTile {
	t := &settingsTile{onTap: tapped}
	t.icon = canvas.NewImageFromResource(res)
	t.icon.FillMode = canvas.ImageFillContain
	t.icon.SetMinSize(fyne.NewSquareSize(tileIconSize))
	t.label = widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{})
	t.label.Truncation = fyne.TextTruncateEllipsis
	t.bg = canvas.NewRectangle(theme.Color(theme.ColorNameHover))
	t.bg.CornerRadius = theme.Size(theme.SizeNameSelectionRadius)
	t.bg.Hidden = true
	t.ExtendBaseWidget(t)
	return t
}

func (t *settingsTile) CreateRenderer() fyne.WidgetRenderer {
	content := container.NewVBox(container.NewCenter(t.icon), t.label)
	return widget.NewSimpleRenderer(container.NewStack(t.bg, content))
}

func (t *settingsTile) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *settingsTile) MouseIn(*deskDriver.MouseEvent) {
	t.hovered = true
	t.bg.Hidden = false
	t.bg.Refresh()
}

func (t *settingsTile) MouseMoved(*deskDriver.MouseEvent) {}

func (t *settingsTile) MouseOut() {
	t.hovered = false
	t.bg.Hidden = true
	t.bg.Refresh()
}

func (t *settingsTile) Cursor() deskDriver.Cursor {
	return deskDriver.PointerCursor
}

// settingsNav owns the settings window body: an icon-grid home screen and a
// detail view, with an animated transition between them.
type settingsNav struct {
	groups     []settingsGroup
	headerIcon fyne.Resource // icon shown top-left on the home screen (matches the docked slot)

	root   *fyne.Container // stack: home, detail, flyer overlay
	home   fyne.CanvasObject
	detail *fyne.Container

	detailIcon    *canvas.Image
	detailTitle   *widget.Label
	detailContent *fyne.Container
	backButton    *widget.Button

	flyer     *canvas.Image
	flyerBG   *canvas.Rectangle // opaque backing so grid icons don't show through the flyer
	anim      *fyne.Animation
	current   *settingsPanel
	animating bool

	waveAnim  *fyne.Animation // drives the brand-water footer; started/stopped with the window
	fish      *canvas.Image   // mascot floating on that water, bobbed by the wave tick
	fishLayer *fyne.Container // unlaid-out band the mascot is positioned within
}

func newSettingsNav(groups []settingsGroup, headerIcon fyne.Resource) *settingsNav {
	n := &settingsNav{groups: groups, headerIcon: headerIcon}
	n.home = n.buildHome()

	n.detailIcon = canvas.NewImageFromResource(nil)
	n.detailIcon.FillMode = canvas.ImageFillContain
	n.detailIcon.SetMinSize(fyne.NewSquareSize(headerIconSize))
	n.detailTitle = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	n.detailTitle.SizeName = theme.SizeNameHeadingText
	n.backButton = widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), n.close)
	n.backButton.Importance = widget.LowImportance
	n.detailContent = container.NewStack()

	// A fixed-height header keeps the docked icon at a predictable corner, so
	// the flight target lands where the real icon will sit.
	iconSlot := container.NewGridWrap(fyne.NewSquareSize(headerIconSize), n.detailIcon)
	header := container.NewBorder(nil, nil,
		container.NewHBox(iconSlot, n.detailTitle), container.NewCenter(n.backButton))
	n.detail = container.NewPadded(container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()), nil, nil, nil,
		n.detailContent,
	))
	n.detail.Hide()

	n.flyer = canvas.NewImageFromResource(nil)
	n.flyer.FillMode = canvas.ImageFillContain
	n.flyer.Hide()
	// A rounded backing rides behind the flyer so the grid icons it passes over
	// don't shine through as it settles into the header slot.
	// Fading in from translucent over the sea underlay.
	n.flyerBG = canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	n.flyerBG.CornerRadius = theme.Size(theme.SizeNameSelectionRadius)
	n.flyerBG.Hide()
	flyerLayer := container.NewWithoutLayout(n.flyerBG, n.flyer)

	n.root = container.NewStack(n.home, n.detail, flyerLayer)
	return n
}

func (n *settingsNav) buildHome() fyne.CanvasObject {
	groups := container.NewVBox()
	for gi := range n.groups {
		g := &n.groups[gi]
		header := widget.NewLabelWithStyle(g.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		grid := container.NewGridWrap(fyne.NewSize(tileWidth, tileHeight))
		for _, p := range g.panels {
			p := p
			p.tile = newSettingsTile(p.icon, p.title, func() { n.open(p) })
			grid.Add(p.tile)
		}
		groups.Add(container.NewVBox(header, grid))
	}

	scroll := container.NewVScroll(groups)

	// A persistent header (settings icon + title) mirrors the detail view.
	homeIcon := canvas.NewImageFromResource(n.headerIcon)
	homeIcon.FillMode = canvas.ImageFillContain
	homeIcon.SetMinSize(fyne.NewSquareSize(headerIconSize))
	iconSlot := container.NewGridWrap(fyne.NewSquareSize(headerIconSize), homeIcon)
	title := widget.NewLabelWithStyle("Settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.SizeName = theme.SizeNameHeadingText
	head := container.NewVBox(
		container.NewBorder(nil, nil, container.NewHBox(iconSlot, title), nil),
		widget.NewSeparator(),
	)
	body := container.NewPadded(container.NewBorder(head, nil, nil, nil, scroll))

	sea := container.NewBorder(nil, n.buildSea(), nil, nil)
	return container.NewStack(sea, body)
}

// buildSea builds the brand-water underlay along the bottom of the home screen.
func (n *settingsNav) buildSea() fyne.CanvasObject {
	shader := canvas.NewShader("tydeWelcomeWaves", welcomeWaveGL, welcomeWaveES)
	shader.Uniforms = map[string]float32{"reveal": 1, "fade": 1}

	// A transparent strut fixes the band height; the shader stretches to fill it.
	strut := canvas.NewRectangle(color.Transparent)
	strut.SetMinSize(fyne.NewSize(0, seaHeight))

	// The mascot rests low on the water at the right, clear of the window edge, and bobs.
	n.fish = canvas.NewImageFromResource(wmtheme.FyshOSLogo)
	n.fish.FillMode = canvas.ImageFillContain
	n.fish.Resize(fyne.NewSquareSize(seaFish))
	n.fish.Hide()
	n.fishLayer = container.NewWithoutLayout(n.fish)

	n.waveAnim = newWaveAnimation(shader, n.bob)
	return container.NewStack(shader, strut, n.fishLayer)
}

// bob floats the mascot on the settings water, rising and falling with the
// waves in the same repaint so it doesn't look pasted onto moving water.
func (n *settingsNav) bob(elapsed time.Duration) {
	band := n.fishLayer.Size()
	if band.IsZero() {
		return // not laid out yet, so there is no corner to rest in
	}

	n.fish.Move(fyne.NewPos(band.Width-seaFish-theme.Padding()*3,
		band.Height-seaFish+waveBobOffset(elapsed)))
	if n.fish.Hidden {
		n.fish.Show()
	}
}

// open animates the tapped panel's icon up into the detail header and reveals
// its content below. Both the grid and the flying icon are visible during the move.
func (n *settingsNav) open(p *settingsPanel) {
	if n.animating {
		return
	}
	n.current = p
	n.loadDetail(p)

	start := n.absCentre(p.tile.icon)
	p.tileCentre = start // remember: the grid never moves, so we fly straight back here
	n.flyer.Resource = p.icon
	n.flyer.Refresh()

	pad := theme.Padding()
	iconCenter := fyne.NewPos(pad+headerIconSize/2, pad+headerIconSize/2)
	n.animate(start, tileIconSize, headerIconSize, iconCenter, func() {
		n.home.Hide()
		n.detail.Show()
	})
}

// loadDetail populates the detail header and body from a panel, building its
// content on first use.
func (n *settingsNav) loadDetail(p *settingsPanel) {
	if p.content == nil {
		p.content = p.build()
	}
	n.detailTitle.SetText(p.title)
	n.detailIcon.Resource = p.icon
	n.detailIcon.Refresh()
	n.detailContent.Objects = []fyne.CanvasObject{p.content}
	n.detailContent.Refresh()
}

// showPanel jumps straight to the named panel and reports whether the title matched.
func (n *settingsNav) showPanel(title string) bool {
	p := n.panelByTitle(title)
	if p == nil {
		return false
	}
	if n.animating || n.current == p {
		return n.current == p
	}

	n.current = p
	n.loadDetail(p)
	n.home.Hide()
	n.detail.Show()
	return true
}

// panelByTitle finds a panel by its title across all groups.
func (n *settingsNav) panelByTitle(title string) *settingsPanel {
	for gi := range n.groups {
		for _, p := range n.groups[gi].panels {
			if p.title == title {
				return p
			}
		}
	}
	return nil
}

// close reverses the animation, flying the docked icon back down to its grid
// tile while the grid shows behind it.
func (n *settingsNav) close() {
	if n.animating || n.current == nil {
		return
	}
	p := n.current

	start := n.absCentre(n.detailIcon) // read while the detail is still shown
	n.detail.Hide()
	n.home.Show()
	n.flyer.Resource = p.icon
	n.flyer.Refresh()

	// A panel reached via showPanel never captured a tile centre.
	// Read it now that home is visible again.
	target := p.tileCentre
	if target.IsZero() {
		target = n.absCentre(p.tile.icon)
	}

	n.animate(start, headerIconSize, tileIconSize, target, func() {
		n.current = nil
	})
}

// animate flies the overlay icon from a start centre/size to an end centre/size
// (the end is resolved on the first frame) and runs onDone when it lands.
func (n *settingsNav) animate(start fyne.Position, fromSize, toSize float32, target fyne.Position, onDone func()) {
	n.animating = true
	place := func(centre fyne.Position, size float32) {
		pos := topLeft(centre, size)
		square := fyne.NewSquareSize(size)
		n.setFlyerBGAlpha(centre.Y)
		n.flyerBG.Resize(square)
		n.flyerBG.Move(pos)
		n.flyer.Resize(square)
		n.flyer.Move(pos)
	}
	place(start, fromSize)
	n.flyerBG.Show()
	n.flyer.Show()

	end := fyne.Position{} // resolved on the first frame, after layout settles
	haveEnd := false

	var a *fyne.Animation
	a = fyne.NewAnimation(canvas.DurationStandard, func(f float32) {
		if n.anim != a {
			return // superseded
		}
		if !haveEnd {
			end = target
			haveEnd = true
		}
		centre := fyne.NewPos(start.X+(end.X-start.X)*f, start.Y+(end.Y-start.Y)*f)
		place(centre, fromSize+(toSize-fromSize)*f)

		if f >= 1 {
			n.flyer.Hide()
			n.flyerBG.Hide()
			n.animating = false
			n.anim = nil
			onDone()
		}
	})
	a.Curve = fyne.AnimationEaseInOut
	n.anim = a
	a.Start()
}

// absCentre returns the centre point (in canvas coordinates) of the given
// object, from its absolute position and current size.
func (n *settingsNav) absCentre(obj fyne.CanvasObject) fyne.Position {
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(obj)
	s := obj.Size()
	return fyne.NewPos(pos.X+s.Width/2, pos.Y+s.Height/2)
}

// topLeft converts a centre point and square size to a top-left position.
func topLeft(centre fyne.Position, size float32) fyne.Position {
	return fyne.NewPos(centre.X-size/2, centre.Y-size/2)
}

// setFlyerBGAlpha fades the flyer's opaque backing in as it rises: transparent
// down over the brand-water and opaque higher up.
func (n *settingsNav) setFlyerBGAlpha(centreY float32) {
	seaTop := n.root.Size().Height - seaHeight
	const fadeSpan = seaHeight // distance above the water over which it turns opaque
	a := (seaTop - centreY) / fadeSpan
	if a < 0 {
		a = 0
	} else if a > 1 {
		a = 1
	}

	bg := color.NRGBAModel.Convert(theme.Color(theme.ColorNameBackground)).(color.NRGBA)
	bg.A = uint8(a * 255)
	n.flyerBG.FillColor = bg
	n.flyerBG.Refresh()
}

// sectionHeading titles a block of settings with a bold sub-heading label (and an
// optional caption beneath).
func sectionHeading(title, subtitle string) fyne.CanvasObject {
	if subtitle == "" {
		return widget.NewRichTextFromMarkdown("## " + title)
	}

	return widget.NewRichTextFromMarkdown("## " + title + "\n\n" + subtitle)
}
