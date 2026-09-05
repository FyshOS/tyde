package ui

import (
	"image/color"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/FyshOS/appie"

	_ "github.com/fyne-io/image/xpm" // load in unix image format

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	wmtheme "fyshos.com/tyde/theme"
)

func (w *widgetPanel) appendAppCategories(acc *widget.Accordion, dismiss func()) {
	accList := acc.Items
	cats := w.desk.IconProvider().CategorizedApps()
	var catNames []string
	hasOther := false
	for cat := range cats {
		if cat == "Other" {
			hasOther = true
			continue
		}
		catNames = append(catNames, cat)
	}
	sort.Strings(catNames)
	if hasOther {
		catNames = append(catNames, "Other")
	}

	for _, cat := range catNames {
		list := cats[cat]
		sort.Slice(list, func(i, j int) bool {
			return strings.ToLower(list[i].Name()) < strings.ToLower(list[j].Name())
		})
		var items []fyne.CanvasObject
		for _, app := range list {
			if app.Hidden() {
				continue
			}
			btn := w.newAppButton(app, dismiss)
			items = append(items, btn)
			defer w.loadIcon(app, btn)
		}

		accList = append(accList, widget.NewAccordionItem(cat,
			container.NewVBox(items...)))
	}

	fyne.Do(func() {
		acc.Items = accList
		acc.Refresh()
	})
}

func (w *widgetPanel) askLogout() {
	var combined fyne.CanvasObject
	dismiss := func() {
		w.desk.HideOverlay(combined)
	}

	logout := widget.NewButtonWithIcon("Logout", theme.LogoutIcon(), func() {
		dismiss()
		time.Sleep(time.Second / 10)
		w.desk.WindowManager().Close()
	})
	logout.Importance = widget.DangerImportance
	cancel := widget.NewButton("Cancel", func() {
		dismiss()
	})

	header := widget.NewRichTextFromMarkdown("### Log out")
	header.Truncation = fyne.TextTruncateEllipsis
	bottomPad := canvas.NewRectangle(color.Transparent)
	bottomPad.SetMinSize(fyne.NewSquareSize(10))
	inner := container.NewBorder(
		header,
		container.NewVBox(
			container.NewHBox(layout.NewSpacer(),
				container.NewGridWithColumns(2, cancel, logout),
				layout.NewSpacer()), bottomPad,
		),
		nil, nil,
		widget.NewLabel("Are you sure you want to log out?"),
	)

	r, g, b, _ := theme.Color(theme.ColorNameOverlayBackground).RGBA()
	bgCol := &color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 230}

	bg := canvas.NewRectangle(bgCol)
	icon := canvas.NewImageFromResource(theme.LogoutIcon())
	iconBox := container.NewWithoutLayout(icon)
	icon.Resize(fyne.NewSize(92, 92))
	icon.Move(fyne.NewPos(280-92-theme.Padding(), theme.Padding()))
	logoutContent := container.NewStack(
		iconBox, bg,
		container.NewPadded(inner),
	)

	primary := w.desk.Screens().Primary()
	scale := primary.CanvasScale()
	pW := float32(primary.Width) / scale
	pH := float32(primary.Height) / scale
	size := fyne.NewSize(280, 150)
	pos := fyne.NewPos((pW-size.Width)/2, (pH-size.Height)/2)
	combined = w.desk.(*desktop).ShowOverlayWithBackdrop(logoutContent, size, size, pos, fyne.Position{})
}

func (w *widgetPanel) showAccountMenu(_ fyne.CanvasObject) {
	var combined fyne.CanvasObject
	dismiss := func() {
		w.desk.HideOverlay(combined)
	}

	items1 := []fyne.CanvasObject{
		&widget.Button{Icon: theme.LogoutIcon(), Importance: widget.DangerImportance, OnTapped: func() {
			dismiss()
			w.askLogout()
		}},
	}
	items1 = append(items1, &widget.Button{Icon: wmtheme.LockIcon, Importance: widget.LowImportance, OnTapped: func() {
		dismiss()
		w.desk.TriggerScreenSaver(false)
	}})
	if os.Getenv("FYNE_DESK_RUNNER") != "" {
		items1 = append(items1, &widget.Button{Icon: theme.ViewRefreshIcon(), Importance: widget.LowImportance, OnTapped: func() {
			os.Exit(5)
		}})
	}

	items2 := []fyne.CanvasObject{
		&widget.Button{Icon: theme.QuestionIcon(), Importance: widget.LowImportance, OnTapped: func() {
			dismiss()
			w.showAbout()
		}},
		&widget.Button{Icon: theme.SettingsIcon(), Importance: widget.LowImportance, OnTapped: func() {
			dismiss()
			w.showSettings("")
		}},
	}
	items := container.NewBorder(nil, nil, container.NewHBox(items1...), container.NewHBox(items2...),
		&widget.Button{Icon: theme.SearchIcon(), Text: "Search", Importance: widget.LowImportance, OnTapped: func() {
			dismiss()
			ShowAppLauncher()
		}})

	var recent []fyne.CanvasObject
	for _, app := range w.desk.RecentApps() {
		btn := w.newAppButton(app, dismiss)
		recent = append(recent, btn)
		btn.Icon = app.Icon(w.desk.Settings().IconTheme(), int(64*w.desk.Screens().Primary().CanvasScale()))
	}

	acc := widget.NewAccordion(widget.NewAccordionItem("Recent",
		container.NewVBox(recent...)))
	acc.MultiOpen = true
	acc.Open(0)
	go w.appendAppCategories(acc, dismiss)

	r, g, b, _ := theme.Color(theme.ColorNameOverlayBackground).RGBA()
	bgCol := &color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 230}
	bg := canvas.NewRectangle(bgCol)

	inner := container.NewBorder(items, nil, nil, nil, container.NewScroll(acc))
	menuContent := container.NewStack(bg, container.NewPadded(inner))

	// Anchor to the window canvas, bottom right.
	winSize := w.desk.(*desktop).primaryWin.win.Canvas().Size()
	menuSize := fyne.NewSize(300, 360)
	pos := fyne.NewPos(winSize.Width-menuSize.Width, winSize.Height-menuSize.Height)
	combined = w.desk.(*desktop).ShowOverlayWithBackdrop(menuContent, menuSize, menuSize, pos, fyne.Position{})
}

func (w *widgetPanel) newAppButton(app appie.AppData, dismiss func()) *widget.Button {
	b := widget.NewButtonWithIcon(app.Name(), wmtheme.BrokenImageIcon, func() {
		dismiss()
		_ = w.desk.RunApp(app)
	})
	b.Alignment = widget.ButtonAlignLeading
	return b
}

func (w *widgetPanel) loadIcon(app appie.AppData, btn *widget.Button) {
	iconRes := app.Icon(w.desk.Settings().IconTheme(), int(64*w.desk.Screens().Primary().CanvasScale()))

	fyne.Do(func() {
		btn.SetIcon(iconRes)
	})
}
