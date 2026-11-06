package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// walkAbs calls fn for o and everything below it with the position of o relative to the
// position of the root's parent (the root is at its own Position).
func walkAbs(o fyne.CanvasObject, origin fyne.Position, fn func(fyne.CanvasObject, fyne.Position)) {
	pos := origin.Add(o.Position())
	fn(o, pos)
	switch v := o.(type) {
	case *fyne.Container:
		for _, c := range v.Objects {
			walkAbs(c, pos, fn)
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			walkAbs(c, pos, fn)
		}
	}
}

// assertBordersInsideScroll checks that every stroked rectangle in the scroll area of the
// top dialog lies fully inside the clip: the 1 px stroke is centered on the edge, so half a
// pixel on each side would otherwise be cut off.
func assertBordersInsideScroll(t *testing.T, h *startHarness) {
	t.Helper()
	var scroll *container.Scroll
	walk(h.s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if s, ok := o.(*container.Scroll); ok && scroll == nil {
			scroll = s
		}
	})
	if scroll == nil {
		t.Fatal("no scroll area in the dialog")
	}
	clip := scroll.Size().Width
	n := 0
	for _, c := range test.WidgetRenderer(scroll).Objects() {
		walkAbs(c, fyne.NewPos(0, 0), func(o fyne.CanvasObject, p fyne.Position) {
			r, ok := o.(*canvas.Rectangle)
			if !ok || r.StrokeWidth <= 0 {
				return
			}
			n++
			if left, right := p.X-r.StrokeWidth/2, p.X+r.Size().Width+r.StrokeWidth/2; left < 0 || right > clip {
				t.Errorf("border spans %.1f..%.1f, clip is 0..%.1f", left, right, clip)
			}
		})
	}
	if n == 0 {
		t.Fatal("no bordered rectangle found, the test measures nothing")
	}
}

func TestAboutBordersLieInsideTheScrollClip(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	h.s.win.Resize(fyne.NewSize(1000, 700))
	showAboutDialog(h.s.win, h.s.app)
	assertBordersInsideScroll(t, h)
}

func TestCoffeeBordersLieInsideTheScrollClip(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	h.s.win.Resize(fyne.NewSize(1000, 700))
	showCoffeeDialog(h.s.win, h.s.app)
	assertBordersInsideScroll(t, h)
}
