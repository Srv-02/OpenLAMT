package ui

import (
	"testing"

	"fyne.io/fyne/v2"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// reachable reports whether o lies fully inside the canvas.
func reachable(w fyne.Window, o fyne.CanvasObject) (bool, fyne.Position, fyne.Size) {
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(o)
	cs := w.Canvas().Size()
	sz := o.Size()
	ok := pos.X >= 0 && pos.Y >= 0 && pos.X+sz.Width <= cs.Width && pos.Y+sz.Height <= cs.Height
	return ok, pos, sz
}

func TestDesignDialogsStayReachableInSmallWindows(t *testing.T) {
	for _, size := range []fyne.Size{fyne.NewSize(800, 300), fyne.NewSize(500, 400), fyne.NewSize(500, 270), fyne.NewSize(1000, 700)} {
		for _, kind := range []string{"about", "settings"} {
			h := newStart(t)
			h.s.app.Settings().SetTheme(style.Theme{})
			h.s.win.Resize(size)
			if kind == "about" {
				showAboutDialog(h.s.win, h.s.app)
			} else {
				showSettingsDialog(h.s.win, &h.s.settings)
			}
			_, _, buttons := dialogTexts(h.s.win)
			for name, b := range buttons {
				ok, pos, sz := reachable(h.s.win, b)
				t.Logf("%s %v canvas %v: button %q at %v size %v reachable=%v", kind, size, h.s.win.Canvas().Size(), name, pos, sz, ok)
				if !ok {
					t.Errorf("%s in %v: button %q outside the canvas (%v, %v)", kind, size, name, pos, sz)
				}
			}
		}
	}
}
