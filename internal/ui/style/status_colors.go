package style

import (
	"image/color"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Status is a power state as the status dots show it (sidebar, detail card, toolbar, status bar).
type Status int

const (
	StatusOn      Status = iota // powered on
	StatusStandby               // standby or hibernation
	StatusOff                   // powered off
	StatusUnknown               // not read: drawn as a ring, not filled
)

// statusPalettes are the four palettes of the status dots (HANDOFF.md 5.6), the only place that
// holds status colors: dark, dark color blind, light, light color blind, each in the order of
// Status. The color-blind palettes keep the four states apart with red-green color blindness,
// where the standard green, orange and red look almost the same.
var statusPalettes = [2][2][4]color.NRGBA{
	{ // dark
		{rgb(0x30D158), rgb(0xFF9F0A), rgb(0xFF453A), rgb(0x8E8E93)},
		{rgb(0x63E6BE), rgb(0xFFD60A), rgb(0xD70015), rgb(0x6E6E73)},
	},
	{ // light
		{rgb(0x1E9E5A), rgb(0xD97706), rgb(0xD70015), rgb(0x8E8E93)},
		{rgb(0x20C997), rgb(0xFFD60A), rgb(0xD70015), rgb(0x48484A)},
	},
}

// StatusColor is the color of state s in variant v (light for theme.VariantLight, dark otherwise),
// from the color-blind palette when colorBlind is set. Unknown states answer the gray of the
// palette.
func StatusColor(s Status, v fyne.ThemeVariant, colorBlind bool) color.Color {
	if s < StatusOn || s > StatusUnknown {
		s = StatusUnknown
	}
	variantIdx, paletteIdx := 0, 0
	if v == theme.VariantLight {
		variantIdx = 1
	}
	if colorBlind {
		paletteIdx = 1
	}
	return statusPalettes[variantIdx][paletteIdx][s]
}

// StatusOutline is the 1 px outline around every filled dot in variant v: rgba(0,0,0,0.25) in
// light, so yellow and mint stay visible on white; transparent (none) in dark.
func StatusOutline(v fyne.ThemeVariant) color.Color {
	if v == theme.VariantLight {
		return black(0.25)
	}
	return color.Transparent
}

// colorBlind is the setting "Color blind mode" as the dots read it.
var colorBlind atomic.Bool

// SetColorBlind selects the color-blind palettes for every dot. Like SetVariant, call it before
// the windows are refreshed; the dots read it on Refresh.
func SetColorBlind(on bool) { colorBlind.Store(on) }

// ColorBlind reports whether the color-blind palettes are selected.
func ColorBlind() bool { return colorBlind.Load() }

// CurrentStatusColor is StatusColor in the current variant and palette.
func CurrentStatusColor(s Status) color.Color { return StatusColor(s, Variant(), ColorBlind()) }
