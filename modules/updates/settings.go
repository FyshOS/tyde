package updates

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// SettingsContent builds the "System Updates" settings panel: what is pending,
// a manual re-check, and a button that applies everything via pkexec. It is
// backed by the shared Checker, so it reflects whatever the status area
// indicator is already showing.
func SettingsContent() fyne.CanvasObject {
	c := Shared()
	if c.Backend() == nil {
		msg := widget.NewLabel("System updates are unavailable.\n\n" +
			"No supported package manager was found on this system. " +
			"Tyde can manage updates on Debian-based systems (apt) and Arch Linux (pacman).")
		msg.Wrapping = fyne.TextWrapWord
		return container.NewCenter(msg)
	}

	p := &updatesPanel{checker: c}
	return p.build()
}

type updatesPanel struct {
	checker *Checker

	status   *widget.Label
	detail   *widget.Label
	list     *widget.List
	progress *widget.ProgressBarInfinite
	check    *widget.Button
	install  *widget.Button

	logText   *widget.Label
	logScroll *container.Scroll

	// items and installing are only touched on the render thread: the checker
	// delivers its callbacks there and every install transition goes via doOnMain.
	items      []Update
	installing bool
}

func (p *updatesPanel) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")
	p.status.TextStyle = fyne.TextStyle{Bold: true}
	p.detail = widget.NewLabel("")
	p.detail.Wrapping = fyne.TextWrapWord

	p.progress = widget.NewProgressBarInfinite()
	p.progress.Hide()

	p.list = widget.NewList(
		func() int { return len(p.items) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil,
				widget.NewLabel("package"), widget.NewLabel("version"))
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(p.items) {
				return
			}
			row := obj.(*fyne.Container)
			up := p.items[id]
			row.Objects[0].(*widget.Label).SetText(up.Name)
			row.Objects[1].(*widget.Label).SetText(versionChange(up))
		},
	)

	p.check = widget.NewButtonWithIcon("Check Now", theme.ViewRefreshIcon(), p.runCheck)
	p.install = widget.NewButtonWithIcon("Install Updates", theme.DownloadIcon(), p.runInstall)
	p.install.Importance = widget.HighImportance

	p.logText = widget.NewLabel("")
	p.logText.TextStyle = fyne.TextStyle{Monospace: true}
	p.logScroll = container.NewScroll(p.logText)
	p.logScroll.SetMinSize(fyne.NewSize(0, 160))
	p.logScroll.Hide()

	head := container.NewVBox(
		p.status,
		p.detail,
		p.progress,
		container.NewHBox(p.check, p.install),
		widget.NewSeparator(),
	)

	p.checker.SetListener("settings", p.refresh)
	p.refresh()

	return container.NewBorder(head, nil, nil, nil, container.NewStack(p.list, p.logScroll))
}

// refresh redraws the panel from checker state. Runs on the render thread.
func (p *updatesPanel) refresh() {
	res, checking, checked, err := p.checker.State()
	p.items = res.Updates
	p.list.Refresh()

	// An install in flight owns the status text; leave its progress alone.
	if p.installing {
		return
	}

	switch {
	case checking:
		p.status.SetText("Checking for updates…")
		p.detail.SetText("Refreshing package information from your configured repositories.")
		p.progress.Show()
		p.check.Disable()
		p.install.Disable()
		return
	case err != nil:
		p.status.SetText("Could not check for updates")
		// Show the package manager's own words: "no mirrors configured" and
		// "network unreachable" need very different responses from the user.
		p.detail.SetText(err.Error())
	case len(res.Updates) == 0 && res.Stale:
		// Never claim the system is up to date off a list we could not refresh.
		p.status.SetText("No known updates")
		p.detail.SetText(res.StaleReason)
	case len(res.Updates) == 0:
		p.status.SetText("Your system is up to date")
		p.detail.SetText(lastCheckedText(checked))
	default:
		p.status.SetText(updateCount(len(res.Updates)) + " available")
		if res.Stale {
			p.detail.SetText(res.StaleReason)
		} else {
			p.detail.SetText(lastCheckedText(checked))
		}
	}

	p.progress.Hide()
	p.check.Enable()
	if len(res.Updates) > 0 {
		p.install.Enable()
	} else {
		p.install.Disable()
	}
}

func (p *updatesPanel) runCheck() {
	go p.checker.Check() // Check notifies listeners, which drives refresh
}

// runInstall applies every pending update through pkexec, streaming the package
// manager's output into the panel so a long upgrade visibly progresses.
func (p *updatesPanel) runInstall() {
	if p.installing {
		return
	}
	p.installing = true

	p.status.SetText("Installing updates…")
	p.detail.SetText("Authenticating. Do not power off the machine while updates are being applied.")
	p.progress.Show()
	p.check.Disable()
	p.install.Disable()
	p.list.Hide()
	p.logText.SetText("")
	p.logScroll.Show()

	args := p.checker.Backend().UpgradeArgs()
	go func() {
		err := p.stream(append([]string{"pkexec"}, args...))

		doOnMain(func() {
			p.installing = false
			p.progress.Hide()

			if err != nil {
				p.status.SetText("Update failed")
				p.detail.SetText(err.Error())
				p.check.Enable()
				p.install.Enable()
				return
			}

			p.status.SetText("Updates installed")
			p.detail.SetText("Some updates only take effect after restarting.")
			p.check.Enable()
			p.install.Disable()
			p.logScroll.Hide()
			p.list.Show()
		})

		// Re-check regardless of outcome: a partial failure leaves a different
		// set pending, and reporting the pre-install list would be wrong.
		p.checker.MarkStale()
		p.checker.Check()
	}()
}

// stream runs argv, appending its combined output to the log as it arrives.
func (p *updatesPanel) stream(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return &backendError{what: "could not start update", detail: err.Error()}
	}
	cmd.Stderr = cmd.Stdout // interleave, so errors appear in context

	if err := cmd.Start(); err != nil {
		return &backendError{what: "could not start update", detail: err.Error()}
	}

	tail := p.consume(out)
	if err := cmd.Wait(); err != nil {
		return installError(err, tail())
	}
	return nil
}

// consume pumps r into the log view and returns an accessor for the last lines
// seen, used to explain a failure.
func (p *updatesPanel) consume(r io.Reader) func() string {
	var (
		mu    sync.Mutex
		lines []string
		done  = make(chan struct{})
	)

	go func() {
		defer close(done)
		scan := bufio.NewScanner(r)
		// Package managers emit long dependency lines; the default 64K token
		// limit would abort the scan partway through an upgrade.
		scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scan.Scan() {
			line := scan.Text()
			mu.Lock()
			lines = append(lines, line)
			// Keep the view bounded: a full dist-upgrade emits thousands of
			// lines and re-laying out all of them stutters the UI.
			if len(lines) > 200 {
				lines = lines[len(lines)-200:]
			}
			text := strings.Join(lines, "\n")
			mu.Unlock()

			doOnMain(func() {
				p.logText.SetText(text)
				p.logScroll.ScrollToBottom()
			})
		}
	}()

	return func() string {
		<-done
		mu.Lock()
		defer mu.Unlock()
		if len(lines) > 6 {
			return strings.Join(lines[len(lines)-6:], "\n")
		}
		return strings.Join(lines, "\n")
	}
}

// installError turns a pkexec failure into something actionable. pkexec's own
// exit codes are distinct from the wrapped command's, so a cancelled password
// prompt is not reported as a broken upgrade.
func installError(err error, tail string) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 126:
			return errors.New("authentication was cancelled, so no updates were installed")
		case 127:
			return errors.New("authentication failed, so no updates were installed. " +
				"Your account must be an administrator to install system updates")
		}
	}

	if tail = strings.TrimSpace(tail); tail != "" {
		return &backendError{what: "the package manager reported a problem", detail: tail}
	}
	return &backendError{what: "the update did not complete", detail: err.Error()}
}

// versionChange renders the version transition for a pending update.
func versionChange(up Update) string {
	if up.OldVersion == "" {
		return up.NewVersion + " (new)"
	}
	return fmt.Sprintf("%s -> %s", up.OldVersion, up.NewVersion)
}

func lastCheckedText(t time.Time) string {
	if t.IsZero() {
		return "Not checked yet."
	}
	return "Last checked " + humanSince(time.Since(t)) + "."
}

func humanSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < time.Hour*24:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
