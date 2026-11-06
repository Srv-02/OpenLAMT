package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/Srv-02/openlamt/internal/session"
)

// TestPrimaryButtonsHaveDesignMeasures (#61): "Connect" and "Connect KVM" are 36 high with
// 18 px side padding, a 16 px symbol and an 8 px gap; the text is 13.5 semibold.
func TestPrimaryButtonsHaveDesignMeasures(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	check := func(name, text string, w float32, h float32) {
		t.Helper()
		tw := fyne.MeasureText(text, 13.5, fyne.TextStyle{Bold: true}).Width
		if want := 18 + tw + 8 + 16 + 18; w != want || h != 36 {
			t.Errorf("%s: %v x %v, want %v x 36", name, w, h, want)
		}
	}
	sz := h.s.connectBtn.MinSize()
	t.Logf("Connect %v", sz)
	check("Connect", "Connect", sz.Width, sz.Height)

	d := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	sz = d.d.emptyBtn.MinSize()
	check("Connect KVM", "Connect KVM", sz.Width, sz.Height)
}
