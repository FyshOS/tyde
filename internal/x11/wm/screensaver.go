//go:build linux || openbsd || freebsd || netbsd
// +build linux openbsd freebsd netbsd

package wm

import (
	"log"
	"os/exec"
	"time"

	"fyshos.com/tyde"
	"github.com/BurntSushi/xgb/screensaver"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/FyshOS/saver"

	"fyne.io/fyne/v2"
)

func (x *x11WM) initScreensaver() {
	err := screensaver.Init(x.x.Conn())
	if err != nil {
		log.Println("Failed to init screensaver extension")
		return
	}

	// screensaver.SelectInput(conn.Conn(), xproto.Drawable(conn.Screen().Root),
	//	screensaver.EventNotifyMask)
	go x.watchScreensaver()
}

func (x *x11WM) watchScreensaver() {
	// A ticker drops missed ticks during computer sleeps. We still measure the
	// wall-clock gap between ticks to notice a sleep happened and lock at once.
	t := time.NewTicker(time.Second)
	defer t.Stop()
	previous := time.Now()

	for range t.C {
		info, err := screensaver.QueryInfo(x.x.Conn(), xproto.Drawable(x.x.Screen().Root)).Reply()
		if err != nil {
			fyne.LogError("Failed to query screensaver info", err)
			continue
		}

		now := time.Now()
		slept := now.Sub(previous).Seconds() > 2 // skipped a tick
		previous = now
		if slept {
			fyne.Do(func() {
				tyde.Instance().TriggerScreenSaver(false) // no delay on lock prompt after sleep
			})
		} else if info.MsSinceUserInput <= 1500 {
			tyde.Instance().DelayScreenSaver()
		}
	}
}

// isScreensaverName reports whether a window carrying the given _NET_WM_NAME is
// one of the screensaver's windows.
func isScreensaverName(name string) bool {
	return name == saver.WindowTitle
}

// unclaimedScreen returns the first screen that no screensaver window covers yet.
func unclaimedScreen(screens []*tyde.Screen, claimed map[xproto.Window]string) *tyde.Screen {
	for _, screen := range screens {
		taken := false
		for _, name := range claimed {
			if name == screen.Name {
				taken = true
				break
			}
		}
		if !taken {
			return screen
		}
	}

	return nil
}

// saverScreen returns the screen a screensaver window covers, claiming the next
// free one the first time we see this window.
func (x *x11WM) saverScreen(win xproto.Window) *tyde.Screen {
	desk := tyde.Instance()
	if desk == nil {
		return nil
	}

	screens := desk.Screens().Screens()
	if name, ok := x.saverScreens[win]; ok {
		for _, screen := range screens {
			if screen.Name == name {
				return screen
			}
		}
	}

	x.pruneSaverScreens()
	screen := unclaimedScreen(screens, x.saverScreens)
	if screen == nil { // more saver windows than screens, don't claim a second time
		return desk.Screens().Primary()
	}

	if x.saverScreens == nil {
		x.saverScreens = make(map[xproto.Window]string)
	}
	x.saverScreens[win] = screen.Name
	return screen
}

// configureSaver sizes a screensaver window to exactly cover the screen it was assigned.
func (x *x11WM) configureSaver(win xproto.Window, req *xproto.ConfigureRequestEvent) {
	screen := x.saverScreen(win)
	if screen == nil {
		return
	}

	geom, err := xproto.GetGeometry(x.x.Conn(), xproto.Drawable(win)).Reply()
	covering := err == nil && int(geom.X) == screen.X && int(geom.Y) == screen.Y &&
		int(geom.Width) == screen.Width && int(geom.Height) == screen.Height
	if !covering {
		x.moveSaver(win, screen.Width, screen.Height)
		return
	}

	if !requestedSize(req) || (int(req.Width) == screen.Width && int(req.Height) == screen.Height) {
		return // it is where we want it and it asked for nothing else
	}

	// The window covers the screen but its toolkit still lays out for the size it
	// asked for. Repeating our geometry would not change anything — resize by a pixel.
	x.moveSaver(win, screen.Width-1, screen.Height-1)
	x.moveSaver(win, screen.Width, screen.Height)
}

// pruneSaverScreens forgets the windows of a saver that has gone away.
func (x *x11WM) pruneSaverScreens() {
	for win := range x.saverScreens {
		_, err := xproto.GetGeometry(x.x.Conn(), xproto.Drawable(win)).Reply()
		if err != nil {
			delete(x.saverScreens, win)
		}
	}
}

// requestedSize reports whether a configure request asked for a size at all,
// rather than only moving or restacking the window.
func requestedSize(req *xproto.ConfigureRequestEvent) bool {
	if req == nil {
		return false
	}

	return req.ValueMask&(xproto.ConfigWindowWidth|xproto.ConfigWindowHeight) != 0
}

// moveSaver puts a screensaver window at the top left of the screen it covers,
// at the given size.
func (x *x11WM) moveSaver(win xproto.Window, width, height int) {
	screen := x.saverScreen(win)
	if screen == nil {
		return
	}

	xproto.ConfigureWindow(x.x.Conn(), win, xproto.ConfigWindowX|xproto.ConfigWindowY|
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(screen.X), uint32(screen.Y), uint32(width), uint32(height)})
}

// configureSavers re-applies the screen geometry to all screensaver windows,
// for when the screen layout changed while the saver is showing.
func (x *x11WM) configureSavers() {
	x.pruneSaverScreens()
	for win := range x.saverScreens {
		x.configureSaver(win, nil)
	}
}

var screenSaverActive bool

func (x *x11WM) ShowScreensaver(s *saver.ScreenSaver) {
	if tyde.Instance().Settings().ScreenSaverType() == "XScreensaver" {
		task := "-activate"
		if s.Lock {
			task = "-lock"
		}
		cmd := exec.Command("xscreensaver-command", task)
		cmd.Start()
		return
	}

	if screenSaverActive {
		return
	}

	screenSaverActive = true
	s.OnUnlocked = func() {
		screenSaverActive = false
	}

	if path, err := exec.LookPath("fyshsaver"); err == nil {
		var params []string
		if s.Lock {
			params = append(params, "-lock")
			if !s.LockImmediately {
				params = append(params, "-lock-delay")
			}
		}
		if s.Label != "" {
			params = append(params, "-label", s.Label)
		}
		if s.Suspending {
			// It cannot see the sleep signal that brought us here, it is only
			// starting up as that is sent, so tell it on the way in.
			params = append(params, "-suspending")
		}

		go func() {
			time.Sleep(time.Millisecond * 100)

			err = exec.Command(path, params...).Run()
			if err != nil {
				fyne.LogError("Failed to activate fyne screensaver", err)
			}
			s.OnUnlocked()
		}()
		return
	}

	go func() {
		time.Sleep(time.Millisecond * 100)
		fyne.Do(s.ShowWindows)
	}()
}
