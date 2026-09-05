package main

import (
	_ "fyshos.com/tyde/modules/ai"
	_ "fyshos.com/tyde/modules/christmas"
	_ "fyshos.com/tyde/modules/desktops"
	_ "fyshos.com/tyde/modules/emoji"
	_ "fyshos.com/tyde/modules/esheep"
	_ "fyshos.com/tyde/modules/flames"
	_ "fyshos.com/tyde/modules/fyles"
	_ "fyshos.com/tyde/modules/keyboard"
	_ "fyshos.com/tyde/modules/launcher"
	_ "fyshos.com/tyde/modules/quaketerm"
	_ "fyshos.com/tyde/modules/rpc"
	_ "fyshos.com/tyde/modules/sloth"
	_ "fyshos.com/tyde/modules/status"
	_ "fyshos.com/tyde/modules/systray"
	_ "fyshos.com/tyde/modules/updates"
	wmtheme "fyshos.com/tyde/theme"

	"fyne.io/fyne/v2/app"
)

func main() {
	a := app.NewWithID("com.fyshos.tyde")
	a.SetIcon(wmtheme.AppIcon)
	desk := setupDesktop(a)

	desk.Run()
}
