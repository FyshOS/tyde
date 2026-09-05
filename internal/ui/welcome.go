package ui

import (
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	deskDriver "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde/modules/ai"
	wmtheme "fyshos.com/tyde/theme"
	"github.com/FyshOS/networks/pkg/netman"
	"github.com/godbus/dbus/v5"
)

const (
	welcomePrefKey = "welcome.done"

	welcomeWidth      = 560
	welcomeHeight     = 510
	welcomeMargin     = 60
	welcomeCardRadius = 14
	welcomeFish       = 116
	welcomeCardAlpha  = .7

	// waveFrameRate caps how often the resting animation repaints.
	// The water drifts and the mascot bobs slowly enough that 20fps is fine.
	waveFrameRate = 20

	// waveBobPeriod is how long one rise-and-fall of a resting mascot takes.
	waveBobPeriod = time.Millisecond * 2400
	waveBobHeight = 4
)

// shouldShowWelcome reports whether the first-run welcome splash is yet to be shown.
func shouldShowWelcome() bool {
	return !fyne.CurrentApp().Preferences().Bool(welcomePrefKey)
}

// welcome is the first-run setup splash: a centred panel whose background is the
// animated brand water (a shader), with the FyshOS mascot swimming in and a card
// of quick setup options drawn over the top. It is shown as a desktop modal so
// it captures input and dims the desktop behind it.
type welcome struct {
	desk *desktop
	sui  *settingsUI

	shader     *canvas.Shader
	waveAnim   *fyne.Animation // continuous gentle motion (drives "time" and the bob)
	revealAnim *fyne.Animation // 0->1 wash-in (drives "reveal")
	fish       *canvas.Image
	fishAnim   *fyne.Animation // the swim-in
	fishRest   fyne.Position   // where the mascot settles once it has swum in
	bobbing    bool            // set once at rest, so the wave tick bobs the mascot

	cardBg       *canvas.Rectangle // card surface, faded in from transparent over the waves
	cardColor    color.NRGBA       // its resting (opaque) colour
	cardFadeAnim *fyne.Animation

	body    *fyne.Container // swappable card contents (home <-> a setup screen)
	rebuild func()          // re-runs whichever screen the card is showing
	hide    func()          // tears down the modal overlay
	gone    bool            // set once dismissed, so late callbacks stand down

	conn *dbus.Conn       // system bus for Wi-Fi setup, opened lazily and reused
	net  *netman.Networks // the Wi-Fi widget, built once on first use
}

// ShowWelcome builds and presents the first-run welcome splash over the desktop.
func (l *desktop) ShowWelcome() {
	if l.primaryWin == nil || l.primaryWin.win == nil {
		return
	}

	ds := l.settings.(*deskSettings)
	w := &welcome{
		desk: l,
		sui:  &settingsUI{settings: ds, launcherIcons: ds.LauncherIcons(), win: l.Root()},
	}

	// Animated brand water filling the whole panel.
	w.shader = canvas.NewShader("tydeWelcomeWaves", welcomeWaveGL, welcomeWaveES)
	w.shader.Uniforms = map[string]float32{"reveal": 0, "fade": 0} // fade off: full-panel rounded card

	// The mascot. It faces right, so it rests in the bottom-right corner and
	// swims in rightward (forward). Hidden until the water has washed in.
	w.fish = canvas.NewImageFromResource(wmtheme.FyshOSLogo)
	w.fish.FillMode = canvas.ImageFillContain
	w.fish.Resize(fyne.NewSquareSize(welcomeFish))
	restY := float32(welcomeHeight - welcomeFish - 22)
	restX := float32(welcomeWidth - welcomeFish - 30)
	w.fish.Move(fyne.NewPos(restX, restY))
	w.fish.Hide()
	fishLayer := container.NewWithoutLayout(w.fish)

	// The setup card, inset so the waves read as a frame around it. The card
	// surface starts fully transparent so the shader shows through it, then fades
	// in and the content surfaces once the water has washed across.
	w.cardColor = color.NRGBAModel.Convert(theme.Color(theme.ColorNameBackground)).(color.NRGBA)
	w.cardBg = canvas.NewRectangle(color.NRGBA{R: w.cardColor.R, G: w.cardColor.G, B: w.cardColor.B, A: 0})
	w.cardBg.CornerRadius = welcomeCardRadius
	w.body = container.NewStack()
	w.showHome()
	w.body.Hide()
	card := container.NewStack(w.cardBg, container.NewPadded(w.body))
	framed := container.New(layout.NewCustomPaddedLayout(welcomeMargin, welcomeMargin, welcomeMargin, welcomeMargin), card)

	fyne.CurrentApp().Settings().AddListener(func(_ fyne.Settings) {
		if w.gone {
			return
		}

		w.refreshCardColor()
		if w.rebuild != nil {
			w.rebuild()
		}
	})

	panel := container.NewStack(w.shader, framed, fishLayer)
	w.hide = l.ShowModal(panel, fyne.NewSize(welcomeWidth, welcomeHeight))

	w.startWaves()
	w.startReveal(func() {
		w.fish.Show()
		w.startFishSwim(restX, restY)
		w.startCardFade()
	})
}

// startWaves drives the resting animation.
func (w *welcome) startWaves() {
	w.waveAnim = newWaveAnimation(w.shader, w.bob)
	w.waveAnim.Start()
}

// newWaveAnimation drives a brand-water shader's "time" uniform, repainting at
// welcomeFrameRate rather than on every tick the driver offers - the water
// drifts slowly enough that this looks identical for a fraction of the CPU.
// onFrame, when set, is called with the elapsed time of each frame drawn so
// callers can move anything riding on the water in the same repaint.
func newWaveAnimation(shader *canvas.Shader, onFrame func(elapsed time.Duration)) *fyne.Animation {
	interval := time.Second / waveFrameRate
	var elapsed time.Duration
	var last, drawn time.Time

	return &fyne.Animation{
		Duration:    time.Second,
		Curve:       fyne.AnimationLinear,
		RepeatCount: fyne.AnimationRepeatForever,
		Tick: func(float32) {
			now := time.Now()
			if !last.IsZero() {
				elapsed += now.Sub(last)
			}
			last = now

			if now.Sub(drawn) < interval {
				return
			}
			drawn = now

			shader.Uniforms["time"] = float32(elapsed.Seconds())
			shader.Refresh()
			if onFrame != nil {
				onFrame(elapsed)
			}
		},
	}
}

// waveBobOffset is the vertical offset of a mascot floating on the brand water
// at the given point in the animation - one gentle rise and fall per period.
func waveBobOffset(elapsed time.Duration) float32 {
	phase := float64(elapsed%waveBobPeriod) / float64(waveBobPeriod)
	return float32(math.Sin(phase*math.Pi*2)) * waveBobHeight
}

// bob keeps the resting mascot gently rising and falling so it doesn't look
// frozen on the moving water.
func (w *welcome) bob(elapsed time.Duration) {
	if !w.bobbing {
		return
	}

	w.fish.Move(fyne.NewPos(w.fishRest.X, w.fishRest.Y+waveBobOffset(elapsed)))
}

// startReveal animates the shader's water washing up from the bottom edge, then
// invokes done so the mascot and card can follow once the water is in.
func (w *welcome) startReveal(done func()) {
	fired := false
	a := fyne.NewAnimation(time.Millisecond*1000, func(f float32) {
		w.shader.Uniforms["reveal"] = f
		w.shader.Refresh()
		if f >= 1 && !fired {
			fired = true
			if done != nil {
				done()
			}
		}
	})
	a.Curve = fyne.AnimationEaseOut
	w.revealAnim = a
	a.Start()
}

// startFishSwim darts the mascot forward (rightward, the way it faces) into its
// resting corner with a slight rise, then hands off to a gentle idle bob.
func (w *welcome) startFishSwim(restX, restY float32) {
	w.fishRest = fyne.NewPos(restX, restY)
	startX := restX - 420
	fired := false
	a := fyne.NewAnimation(time.Millisecond*1800, func(f float32) {
		x := startX + (restX-startX)*f
		dip := float32(math.Sin(float64(f)*math.Pi)) * -8 // rises slightly mid-swim
		w.fish.Move(fyne.NewPos(x, restY+dip))
		if f >= 1 && !fired {
			fired = true
			w.bobbing = true // the wave tick takes the mascot from here
		}
	})
	//	a.Curve = fyne.AnimationEaseOut
	w.fishAnim = a
	a.Start()
}

// startCardFade fades the card surface in from transparent - so the waves show
// through and are seen washing across the card area - then surfaces the content
// once the surface is most of the way in.
func (w *welcome) startCardFade() {
	shown := false
	a := fyne.NewAnimation(time.Millisecond*800, func(f float32) {
		c := w.cardColor
		c.A = uint8(float32(w.cardColor.A) * f * welcomeCardAlpha)
		w.cardBg.FillColor = c
		w.cardBg.Refresh()
		if f >= 0.7 && !shown {
			shown = true
			w.body.Show()
		}
	})
	a.Curve = fyne.AnimationEaseOut
	w.cardFadeAnim = a
	a.Start()
}

// refreshCardColor re-samples the card surface from the current theme, for when
// the desktop switches between light and dark while the welcome is up.
func (w *welcome) refreshCardColor() {
	w.cardColor = color.NRGBAModel.Convert(theme.Color(theme.ColorNameBackground)).(color.NRGBA)

	c := w.cardColor
	c.A = uint8(float32(c.A) * welcomeCardAlpha)
	w.cardBg.FillColor = c
	w.cardBg.Refresh()
}

// showHome populates the card with the welcome message and the list of setup
// options, matching the first-run mock-up.
func (w *welcome) showHome() {
	w.rebuild = w.showHome
	fg := theme.Color(theme.ColorNameForeground)
	hello := canvas.NewText("Welcome to ", fg)
	hello.TextSize = 22
	brand := canvas.NewText("FyshOS", fg)
	brand.TextSize = 22
	brand.TextStyle = fyne.TextStyle{Bold: true}
	titleRow := container.NewHBox(layout.NewSpacer(), hello, brand, layout.NewSpacer())

	subtitle := canvas.NewText("Let's get you set up!", theme.Color(theme.ColorNamePlaceHolder))
	subtitle.TextSize = theme.Size(theme.SizeNameSubHeadingText)
	header := container.NewVBox(titleRow, container.NewCenter(subtitle))

	rows := container.NewVBox(
		newWelcomeRow(theme.ColorPaletteIcon(), "Customize Appearance",
			"Choose your theme and colors", w.openAppearance),
		newWelcomeRow(wmtheme.WifiIcon, "Connect to Wi-Fi",
			"Setup a Wi-Fi network", w.openWifi),
		newWelcomeRow(ai.Icon, "Set up AI",
			"Turn on the AI assistant", w.openAI),
		newWelcomeRow(theme.SettingsIcon(), "Additional Settings",
			"Change system preferences", w.openFullSettings),
	)

	getStarted := &widget.Button{Text: "Get Started", Importance: widget.HighImportance, OnTapped: func() {
		w.dismiss(true)
	}}
	skip := &widget.Button{Text: "Skip for now", Importance: widget.LowImportance, OnTapped: func() {
		w.dismiss(false)
	}}
	footer := container.NewCenter(container.NewPadded(container.NewHBox(skip, getStarted)))

	w.setBody(container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()), footer, nil, nil, rows,
	))
}

// showScreen swaps the card to a single setup screen with a Back button.
func (w *welcome) showScreen(title string, content fyne.CanvasObject) {
	w.rebuild = func() { w.showScreen(title, content) }
	back := &widget.Button{
		Text: "Back", Icon: theme.NavigateBackIcon(),
		Importance: widget.LowImportance, OnTapped: w.showHome,
	}
	head := container.NewBorder(nil, nil, back, nil,
		widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}))
	w.setBody(container.NewBorder(container.NewVBox(head, widget.NewSeparator()), nil, nil, nil, content))
}

func (w *welcome) setBody(o fyne.CanvasObject) {
	w.body.Objects = []fyne.CanvasObject{o}
	w.body.Refresh()
}

func (w *welcome) openAppearance() {
	w.showScreen("Customize Appearance", w.sui.loadAppearanceScreen())
}

// openAI shows the AI assistant setup screen, letting the user enable the
// assistant and enter a provider token (or leave it turned off).
func (w *welcome) openAI() {
	w.showScreen("Set up AI", w.sui.loadAIScreen())
}

// openWifi shows Wi-Fi setup screen allowing user to pick a network from those found.
func (w *welcome) openWifi() {
	win := w.sui.win

	// Build the network browser once and reuse it (and its bus connection).
	if w.net == nil {
		nm, conn, err := newWifiNetworks(win)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		w.conn, w.net = conn, nm
	}

	w.showScreen("Connect to Wi-Fi", w.net)
}

// newWifiNetworks loads the network browser from our networks repo.
// The caller owns the returned connection and must Close it once the widget is no longer needed.
func newWifiNetworks(win fyne.Window) (*netman.Networks, *dbus.Conn, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, nil, err
	}

	// handlePass prompts for a network passphrase, blocking until the user submits
	// or cancels. It is called from netman's iwd agent callback (off the UI thread),
	// so the blocking read is safe; Cancel returns "" to abort the connection.
	handlePass := func(name string) string {
		result := make(chan string, 1)
		entry := widget.NewPasswordEntry()
		d := dialog.NewForm("Connect to "+name, "Connect", "Cancel",
			[]*widget.FormItem{widget.NewFormItem("Password", entry)},
			func(ok bool) {
				if ok {
					result <- entry.Text
				} else {
					result <- ""
				}
			}, win)
		d.Resize(fyne.NewSize(320, d.MinSize().Height))
		d.Show()

		return <-result
	}

	nm, err := netman.New(conn, handlePass, func(err error) {
		dialog.ShowError(err, win)
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return nm, conn, nil
}

// openFullSettings closes the welcome and launches the full settings window,
// which opens as its own window beneath where this overlay was.
func (w *welcome) openFullSettings() {
	w.dismiss(true)
	w.desk.ShowSettings("")
}

// dismiss stops the animations, records that the welcome has been seen and tears
// down the overlay. Safe to call more than once.
func (w *welcome) dismiss(done bool) {
	w.gone = true
	if w.waveAnim != nil {
		w.waveAnim.Stop()
	}
	if w.revealAnim != nil {
		w.revealAnim.Stop()
	}
	if w.fishAnim != nil {
		w.fishAnim.Stop()
	}
	w.bobbing = false
	if w.cardFadeAnim != nil {
		w.cardFadeAnim.Stop()
	}

	if w.conn != nil {
		_ = w.conn.Close()
		w.conn, w.net = nil, nil
	}

	if done {
		fyne.CurrentApp().Preferences().SetBool(welcomePrefKey, true)
	}
	if w.hide != nil {
		w.hide()
	}
}

// welcomeRow is one tappable setup option in the welcome card: an icon, a bold
// title and a dimmer description, with a chevron and a hover highlight.
type welcomeRow struct {
	widget.BaseWidget
	icon  fyne.Resource
	title string
	desc  string
	onTap func()

	bg *canvas.Rectangle
}

func newWelcomeRow(icon fyne.Resource, title, desc string, onTap func()) *welcomeRow {
	r := &welcomeRow{icon: icon, title: title, desc: desc, onTap: onTap}
	r.ExtendBaseWidget(r)
	return r
}

func (r *welcomeRow) CreateRenderer() fyne.WidgetRenderer {
	r.bg = canvas.NewRectangle(color.Transparent)
	r.bg.CornerRadius = theme.Size(theme.SizeNameInputRadius)

	icon := widget.NewIcon(r.icon)
	text := widget.NewRichTextFromMarkdown("## " + r.title + "\n" + r.desc)
	chevron := widget.NewIcon(theme.NavigateNextIcon())

	row := container.NewBorder(nil, nil, container.NewPadded(icon), container.NewPadded(chevron), text)
	return widget.NewSimpleRenderer(container.NewStack(r.bg, row))
}

func (r *welcomeRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *welcomeRow) MouseIn(*deskDriver.MouseEvent) {
	r.bg.FillColor = theme.Color(theme.ColorNameHover)
	r.bg.Refresh()
}

func (r *welcomeRow) MouseMoved(*deskDriver.MouseEvent) {}

func (r *welcomeRow) MouseOut() {
	r.bg.FillColor = color.Transparent
	r.bg.Refresh()
}
