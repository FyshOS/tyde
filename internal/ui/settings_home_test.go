package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/assert"
)

// newTestNav builds a nav with two trivial panels, enough to exercise navigation
// without pulling in dbus-backed settings screens.
func newTestNav() *settingsNav {
	build := func(name string) func() fyne.CanvasObject {
		return func() fyne.CanvasObject { return widget.NewLabel(name) }
	}

	return newSettingsNav([]settingsGroup{
		{title: "System", panels: []*settingsPanel{
			{title: "Network", icon: theme.ComputerIcon(), build: build("network")},
			{title: "Updates", icon: theme.DownloadIcon(), build: build("updates")},
		}},
	}, theme.SettingsIcon())
}

// TestSettingsNavShowPanel covers the status area update indicator's promise:
// asking for a named panel must land on that panel, not merely open the window.
func TestSettingsNavShowPanel(t *testing.T) {
	n := newTestNav()

	assert.True(t, n.showPanel("Updates"))
	assert.Equal(t, "Updates", n.current.title)
	assert.Equal(t, "Updates", n.detailTitle.Text)
	assert.False(t, n.home.Visible(), "home should be hidden once a panel is open")
	assert.False(t, n.detail.Hidden, "detail should be shown")
}

// TestSettingsNavShowPanelSwitches covers re-targeting a window that is already
// sitting on a different panel - the case that made the indicator feel broken.
func TestSettingsNavShowPanelSwitches(t *testing.T) {
	n := newTestNav()

	assert.True(t, n.showPanel("Network"))
	assert.Equal(t, "Network", n.current.title)

	assert.True(t, n.showPanel("Updates"))
	assert.Equal(t, "Updates", n.current.title)
	assert.Equal(t, "Updates", n.detailTitle.Text)
}

func TestSettingsNavShowPanelUnknown(t *testing.T) {
	n := newTestNav()

	assert.False(t, n.showPanel("Nonexistent"))
	assert.Nil(t, n.current, "an unknown title should leave the view alone")
}

// TestSettingsNavShowPanelIsIdempotent guards against a second request for the
// panel already on screen rebuilding or disturbing it.
func TestSettingsNavShowPanelIsIdempotent(t *testing.T) {
	n := newTestNav()

	assert.True(t, n.showPanel("Updates"))
	content := n.current.content

	assert.True(t, n.showPanel("Updates"))
	assert.Same(t, content, n.current.content, "content should not be rebuilt")
}

// TestSettingsNavCloseAfterShowPanel covers the deep-linked panel closing
// cleanly, even though it never recorded a tile centre to fly back to.
func TestSettingsNavCloseAfterShowPanel(t *testing.T) {
	n := newTestNav()
	n.showPanel("Updates")

	n.close()
	// The flight is asynchronous, so assert on what close commits to up front.
	assert.True(t, n.home.Visible(), "home should be shown again")
	assert.True(t, n.detail.Hidden, "detail should be hidden")
}
