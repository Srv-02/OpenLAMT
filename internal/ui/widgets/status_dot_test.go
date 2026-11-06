package widgets

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// lookCleanup puts variant and palette back after a test that changes them.
func lookCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { style.SetVariant(theme.VariantDark); style.SetColorBlind(false) })
}

var dotStatus = map[DotState]style.Status{DotOn: style.StatusOn, DotAsleep: style.StatusStandby, DotOff: style.StatusOff}

// TestStatusDotPalettes: a dot draws its state in the palette of the current variant and color
// blind mode, and follows a change of either on Refresh, without being built again.
func TestStatusDotPalettes(t *testing.T) {
	themedApp(t)
	lookCleanup(t)
	d := NewStatusDot(DotOn, 8)
	test.NewTempWindow(t, d)
	r := test.WidgetRenderer(d).(*dotRenderer)
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		for _, cb := range []bool{false, true} {
			style.SetVariant(v)
			style.SetColorBlind(cb)
			for s, st := range dotStatus {
				d.SetState(s)
				d.Refresh()
				if !same(r.circle.FillColor, style.StatusColor(st, v, cb)) {
					t.Errorf("variant %d color blind %v state %d: fill %v", v, cb, s, r.circle.FillColor)
				}
			}
			d.SetState(DotUnknown)
			d.Refresh()
			if !same(r.circle.StrokeColor, style.StatusColor(style.StatusUnknown, v, cb)) || !same(r.circle.FillColor, color.Transparent) {
				t.Errorf("variant %d color blind %v: unknown is not a gray ring: fill %v stroke %v", v, cb, r.circle.FillColor, r.circle.StrokeColor)
			}
		}
	}
}

// TestStatusDotLightOutline: in light every filled dot gets a 1 px outline rgba(0,0,0,0.25), also
// inside the ring of the Power button; the size stays. Dark has none, and the gray ring of the
// unknown state needs none. On a selected row the white ring takes its place.
func TestStatusDotLightOutline(t *testing.T) {
	themedApp(t)
	lookCleanup(t)
	d := NewStatusDot(DotOn, 8)
	test.NewTempWindow(t, d)
	r := test.WidgetRenderer(d).(*dotRenderer)
	outline := style.StatusOutline(theme.VariantLight)

	style.SetVariant(theme.VariantDark)
	d.Refresh()
	if !r.inner.Hidden {
		t.Error("dark: filled dot has an outline")
	}
	style.SetVariant(theme.VariantLight)
	for s := range dotStatus {
		d.SetState(s)
		d.Refresh()
		if r.inner.Hidden || !same(r.inner.StrokeColor, outline) || r.inner.StrokeWidth != 1 ||
			!same(r.inner.FillColor, color.Transparent) || r.inner.Size().Width != 8 || d.MinSize().Width != 8 {
			t.Errorf("light state %d: outline hidden %v color %v width %v size %v", s, r.inner.Hidden, r.inner.StrokeColor, r.inner.StrokeWidth, r.inner.Size())
		}
	}
	d.SetState(DotUnknown)
	d.Refresh()
	if !r.inner.Hidden {
		t.Error("light: the unknown ring got an outline")
	}
	d.SetState(DotOn)
	d.SetSelected(true)
	if !r.inner.Hidden || !same(r.circle.StrokeColor, style.Color(style.ColorNameWhite)) {
		t.Error("light selected: the white ring must replace the outline")
	}

	b := NewToolbarMenuButton(icons.Power, "Power", nil)
	test.NewTempWindow(t, b)
	b.SetDot(DotAsleep)
	br := test.WidgetRenderer(b.dot).(*dotRenderer)
	if br.inner.Hidden || !same(br.inner.StrokeColor, outline) || br.inner.Size().Width != style.StatusDotSizeSmall {
		t.Errorf("light Power button: outline hidden %v color %v size %v", br.inner.Hidden, br.inner.StrokeColor, br.inner.Size())
	}
}

// TestColorBlindPreviewDot: the preview dots of the settings always show the color-blind palette
// of the current variant, whatever the setting says.
func TestColorBlindPreviewDot(t *testing.T) {
	themedApp(t)
	lookCleanup(t)
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		style.SetVariant(v)
		style.SetColorBlind(false)
		for s, st := range dotStatus {
			d := NewColorBlindPreviewDot(s, 8)
			test.NewTempWindow(t, d)
			if fill, _, _ := d.colors(); !same(fill, style.StatusColor(st, v, true)) {
				t.Errorf("variant %d state %d: preview fill %v", v, s, fill)
			}
		}
		d := NewColorBlindPreviewDot(DotUnknown, 8)
		if _, stroke, _ := d.colors(); !same(stroke, style.StatusColor(style.StatusUnknown, v, true)) {
			t.Errorf("variant %d: preview ring %v", v, stroke)
		}
	}
}
