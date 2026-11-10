package ui

import (
	"image"
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/assert"

	wmTest "fyshos.com/tyde/test"
)

// A failed GL readback comes back as a solid-black opaque image, which must be
// treated as blank so the preview falls back; any real content is not blank.
func TestCaptureIsBlank(t *testing.T) {
	black := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := 3; i < len(black.Pix); i += 4 {
		black.Pix[i] = 0xff // opaque, RGB left at zero
	}
	assert.True(t, captureIsBlank(black))
	assert.True(t, captureIsBlank(image.NewRGBA(image.Rect(0, 0, 0, 0))))

	withContent := image.NewRGBA(image.Rect(0, 0, 64, 48))
	wallpaper := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			withContent.Set(x, y, wallpaper)
		}
	}
	assert.False(t, captureIsBlank(withContent))
}

// The overview grid is as square as the count allows, growing down before it grows
// across, and the column choice always agrees with the row count the shader derives.
func TestOverviewGridCols(t *testing.T) {
	for count, cols := range map[int]int{1: 1, 2: 1, 3: 2, 4: 2, 5: 2, 6: 2, 7: 3, 8: 3, 9: 3, 10: 3, 12: 3, 16: 4} {
		assert.Equal(t, cols, overviewGridCols(count), "count %d", count)
		rows := int(math.Ceil(float64(count) / float64(cols)))
		assert.GreaterOrEqual(t, rows, cols, "count %d is not taller than wide", count)
		assert.Less(t, (rows-1)*cols, count, "count %d leaves an empty row", count)
	}
}

// Four desktops lay out as a centred 2x2 grid: equal cells, no overlaps, a visible
// gap between rows and columns, each cell keeping the view's aspect ratio.
func TestOverviewPanelGeometryGrid(t *testing.T) {
	size := fyne.NewSize(800, 600)
	const count = 4

	assert.Equal(t, 2, overviewGridCols(count))

	pos := make([]fyne.Position, count)
	sz := make([]fyne.Size, count)
	for i := 0; i < count; i++ {
		pos[i], sz[i] = overviewPanelGeometry(count, i, size)
	}

	// All cells are the same size and keep the screen's aspect ratio.
	for i := 1; i < count; i++ {
		assert.InDelta(t, sz[0].Width, sz[i].Width, 0.01)
		assert.InDelta(t, sz[0].Height, sz[i].Height, 0.01)
	}
	assert.InDelta(t, size.Width/size.Height, sz[0].Width/sz[0].Height, 0.001)

	// Grid positions: 0,1 on the top row; 2,3 on the bottom row.
	assert.InDelta(t, pos[0].Y, pos[1].Y, 0.01) // same row
	assert.InDelta(t, pos[2].Y, pos[3].Y, 0.01) // same row
	assert.InDelta(t, pos[0].X, pos[2].X, 0.01) // same column
	assert.InDelta(t, pos[1].X, pos[3].X, 0.01) // same column
	assert.Greater(t, pos[2].Y, pos[0].Y)       // bottom row below top
	assert.Greater(t, pos[1].X, pos[0].X)       // right column right of left

	// There is a real gap between the columns and the rows (not touching).
	colGap := pos[1].X - (pos[0].X + sz[0].Width)
	rowGap := pos[2].Y - (pos[0].Y + sz[0].Height)
	assert.Greater(t, colGap, float32(1))
	assert.Greater(t, rowGap, float32(1))

	// The grid is centred and fits within the view with a margin to spare.
	assert.Greater(t, pos[0].X, float32(0))
	assert.Greater(t, pos[0].Y, float32(0))
	assert.Less(t, pos[1].X+sz[1].Width, size.Width)
	assert.Less(t, pos[2].Y+sz[2].Height, size.Height)
	// Centred: left margin equals right margin, top equals bottom.
	assert.InDelta(t, pos[0].X, size.Width-(pos[1].X+sz[1].Width), 0.1)
	assert.InDelta(t, pos[0].Y, size.Height-(pos[2].Y+sz[2].Height), 0.1)
}

// Zoomed right in, the focused desktop's panel is the whole view, so its label is
// laid out exactly as the switch badge that takes over from it.
func TestOverviewPanelGeometryZoomedIn(t *testing.T) {
	size := fyne.NewSize(800, 600)
	pos, sz := overviewPanelGeometryAt(4, 3, 3, 0, size)
	assert.InDelta(t, 0, pos.X, 0.01)
	assert.InDelta(t, 0, pos.Y, 0.01)
	assert.InDelta(t, size.Width, sz.Width, 0.01)
	assert.InDelta(t, size.Height, sz.Height, 0.01)

	test.NewTempApp(t)
	c := newOverviewContent(4, 0, wmTest.NewSettings(), size, nil, nil)
	c.setCamera(3, 0, 1)
	badge := newDeskBadge()
	badge.Resize(size)
	badge.show(c.panels[3].text.Text)
	badge.CreateRenderer().Layout(size)
	badge.cancel()

	label := c.panels[3]
	assert.InDelta(t, badge.text.TextSize, label.text.TextSize, 0.01)
	assert.InDelta(t, badge.bg.Size().Width, label.bg.Size().Width, 0.1)
	assert.InDelta(t, badge.bg.Position().X, label.Position().X+label.bg.Position().X, 0.1)
	assert.InDelta(t, badge.bg.Position().Y, label.Position().Y+label.bg.Position().Y, 0.1)
	assert.Equal(t, badge.bg.FillColor, label.bg.FillColor)
}
