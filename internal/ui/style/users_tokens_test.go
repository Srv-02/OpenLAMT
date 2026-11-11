package style

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// TestSignedInTokens pins the light values of the Edit button of the signed-in account
// (Users.dc.html); the dark ones are derived and only have to exist and differ.
func TestSignedInTokens(t *testing.T) {
	for name, w := range map[fyne.ThemeColorName]string{
		ColorNameSignedInSurface: "#FFF5F6FF",
		ColorNameSignedInBorder:  "#E7B3BAFF",
	} {
		if got := hex(ColorFor(name, theme.VariantLight)); got != w {
			t.Errorf("%s light = %s, want %s", name, got, w)
		}
		if d, ok := darkColors[name]; !ok || hex(d) == w {
			t.Errorf("%s: dark missing or equal to light", name)
		}
	}
}
