package ui

import (
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/amt"
)

// The red warning text of the "Certificate changed" dialog uses the readable text red in dark
// (#FF6961), not the red of the button fill (#D70015, 2.59:1 on the dialog background).
func TestMismatchWarningTextColor(t *testing.T) {
	for _, c := range []struct {
		name string
		v    fyne.ThemeVariant
		want color.NRGBA
	}{
		{"dark", theme.VariantDark, color.NRGBA{R: 0xFF, G: 0x69, B: 0x61, A: 0xFF}},
		{"light", theme.VariantLight, color.NRGBA{R: 0xD7, G: 0x00, B: 0x15, A: 0xFF}},
	} {
		t.Run(c.name, func(t *testing.T) {
			restoreDarkVariant(t)
			a := test.NewTempApp(t)
			reportVariant(t, a, c.v)
			w := a.NewWindow("x")
			w.Resize(fyne.NewSize(900, 600))
			t.Cleanup(w.Close)
			showMismatchDialog(w, &amt.FingerprintMismatchError{Host: "h.example.com", Expected: "aa", Actual: "bb"})
			var got []color.Color
			walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
				if txt, ok := o.(*canvas.Text); ok && (strings.Contains(txt.Text, "WARNING") || strings.Contains(txt.Text, "man-in-the-middle")) {
					got = append(got, txt.Color)
				}
			})
			if len(got) == 0 {
				t.Fatal("warning text not found")
			}
			for _, g := range got {
				if rgba(g) != rgba(c.want) {
					t.Errorf("warning text %s, want %s", rgba(g), rgba(c.want))
				}
			}
		})
	}
}
