package wm

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/stretchr/testify/assert"
)

func TestFindObjectAtPositionMatching(t *testing.T) {
	l := widget.NewRichTextFromMarkdown("* Test")
	e := widget.NewEntry()
	w := test.NewWindow(
		container.NewGridWithColumns(1, l, e),
	)
	root := w.Canvas().Content()

	assert.Equal(t, l, FindObjectAtPositionMatching(fyne.NewPos(2, 2), root, func(fyne.CanvasObject) bool {
		return true
	}))
	assert.Equal(t, e, FindObjectAtPositionMatching(fyne.NewPos(4, 48), root, func(o fyne.CanvasObject) bool {
		_, ok := o.(*widget.Entry)
		return ok
	}))
	assert.Nil(t, FindObjectAtPositionMatching(fyne.NewPos(64, 64), root, func(fyne.CanvasObject) bool {
		return true
	}))
	assert.Nil(t, FindObjectAtPositionMatching(fyne.NewPos(2, 2), root, func(fyne.CanvasObject) bool {
		return false
	}))
}
