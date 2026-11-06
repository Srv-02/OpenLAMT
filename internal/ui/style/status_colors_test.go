package style

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

var allStatus = []Status{StatusOn, StatusStandby, StatusOff, StatusUnknown}

// TestStatusColorPalettes pins the four palettes of HANDOFF.md 5.6 (state x variant x color
// blind), and that every other variant (Fyne's test app reports 2) answers dark.
func TestStatusColorPalettes(t *testing.T) {
	want := map[Status][4]string{ // dark, dark color blind, light, light color blind
		StatusOn:      {"#30D158FF", "#63E6BEFF", "#1E9E5AFF", "#20C997FF"},
		StatusStandby: {"#FF9F0AFF", "#FFD60AFF", "#D97706FF", "#FFD60AFF"},
		StatusOff:     {"#FF453AFF", "#D70015FF", "#D70015FF", "#D70015FF"},
		StatusUnknown: {"#8E8E93FF", "#6E6E73FF", "#8E8E93FF", "#48484AFF"},
	}
	for s, w := range want {
		for i, c := range []struct {
			v  fyne.ThemeVariant
			cb bool
		}{{theme.VariantDark, false}, {theme.VariantDark, true}, {theme.VariantLight, false}, {theme.VariantLight, true}} {
			if got := hex(StatusColor(s, c.v, c.cb)); got != w[i] {
				t.Errorf("state %d variant %d color blind %v = %s, want %s", s, c.v, c.cb, got, w[i])
			}
		}
		if hex(StatusColor(s, 2, false)) != w[0] || hex(StatusColor(s, 2, true)) != w[1] {
			t.Errorf("state %d: variant 2 is not dark", s)
		}
	}
	if hex(StatusColor(Status(99), theme.VariantDark, false)) != want[StatusUnknown][0] {
		t.Error("an unnamed state is not the unknown gray")
	}
}

// TestStatusColorsDistinct: inside each palette the four states have four different colors.
func TestStatusColorsDistinct(t *testing.T) {
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		for _, cb := range []bool{false, true} {
			seen := map[string]Status{}
			for _, s := range allStatus {
				c := hex(StatusColor(s, v, cb))
				if other, ok := seen[c]; ok {
					t.Errorf("variant %d color blind %v: states %d and %d are both %s", v, cb, s, other, c)
				}
				seen[c] = s
			}
		}
	}
}

// TestStatusColorContrast (issue #123): the standard palettes stand out from the sidebar, the
// toolbar and the status bar with at least 3:1 (WCAG non-text contrast) wherever the design
// values reach it: all four states in dark, on (3.08:1, #123) and off in light. Light standby
// (#D97706, 2.85:1) and the light gray ring (#8E8E93, 2.92:1) stay below, as do the color-blind
// palettes, which are chosen for hue distance, not contrast (dark off #D70015 2.66:1, light
// standby #FFD60A 1.26:1); in light every filled dot has the outline of TestStatusOutline instead.
func TestStatusColorContrast(t *testing.T) {
	checked := map[fyne.ThemeVariant][]Status{
		theme.VariantDark:  {StatusOn, StatusStandby, StatusOff, StatusUnknown},
		theme.VariantLight: {StatusOn, StatusOff},
	}
	for v, states := range checked {
		for _, s := range states {
			for _, bg := range []fyne.ThemeColorName{ColorNameSidebar, ColorNameToolbar, ColorNameStatusBar} {
				if c := contrast(StatusColor(s, v, false), ColorFor(bg, v)); c < 3 {
					t.Errorf("variant %d: state %d on %s = %.2f:1", v, s, bg, c)
				}
			}
		}
	}
}

// TestStatusOutline: in light every filled dot gets the 1 px outline rgba(0,0,0,0.25); dark has none.
func TestStatusOutline(t *testing.T) {
	if got := hex(StatusOutline(theme.VariantLight)); got != "#00000040" {
		t.Errorf("light outline %s, want rgba(0,0,0,0.25)", got)
	}
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, 2} {
		if _, _, _, a := StatusOutline(v).RGBA(); a != 0 {
			t.Errorf("variant %d has an outline", v)
		}
	}
}

// TestCurrentStatusColor follows SetVariant and SetColorBlind; color blind mode is off by default.
func TestCurrentStatusColor(t *testing.T) {
	t.Cleanup(func() { SetVariant(theme.VariantDark); SetColorBlind(false) })
	if ColorBlind() {
		t.Fatal("color blind mode on by default")
	}
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		for _, cb := range []bool{false, true} {
			SetVariant(v)
			SetColorBlind(cb)
			if ColorBlind() != cb {
				t.Fatalf("ColorBlind() = %v after SetColorBlind(%v)", ColorBlind(), cb)
			}
			for _, s := range allStatus {
				if hex(CurrentStatusColor(s)) != hex(StatusColor(s, v, cb)) {
					t.Errorf("variant %d color blind %v state %d: current %s", v, cb, s, hex(CurrentStatusColor(s)))
				}
			}
		}
	}
}
