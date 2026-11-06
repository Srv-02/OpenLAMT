package style

import (
	"runtime"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
)

func restoreDark(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetVariant(theme.VariantDark) })
	SetVariant(theme.VariantDark)
}

func TestBoundObjectsFollowTheVariant(t *testing.T) {
	restoreDark(t)
	rect := BindRect(&canvas.Rectangle{}, ColorNameSidebar, ColorNameCardBorder)
	text := BindText(canvas.NewText("x", nil), "foreground")
	circle := BindCircle(&canvas.Circle{}, ColorNameAvatar, ColorNameCardBorder)
	check := func(v fyne.ThemeVariant) {
		t.Helper()
		if hex(rect.FillColor) != hex(ColorFor(ColorNameSidebar, v)) || hex(rect.StrokeColor) != hex(ColorFor(ColorNameCardBorder, v)) {
			t.Errorf("rect %s/%s in variant %d", hex(rect.FillColor), hex(rect.StrokeColor), v)
		}
		if hex(text.Color) != hex(ColorFor("foreground", v)) {
			t.Errorf("text %s in variant %d", hex(text.Color), v)
		}
		if hex(circle.FillColor) != hex(ColorFor(ColorNameAvatar, v)) || hex(circle.StrokeColor) != hex(ColorFor(ColorNameCardBorder, v)) {
			t.Errorf("circle %s/%s in variant %d", hex(circle.FillColor), hex(circle.StrokeColor), v)
		}
	}
	check(theme.VariantDark)
	SetVariant(theme.VariantLight)
	check(theme.VariantLight)
	SetVariant(theme.VariantDark)
	check(theme.VariantDark)
}

func TestBindingKeepsNoObjectAlive(t *testing.T) {
	restoreDark(t)
	for range 100 {
		BindRect(&canvas.Rectangle{}, ColorNameSidebar, "")
	}
	runtime.GC()
	SetVariant(theme.VariantLight) // a change prunes what the garbage collector took
	if n := boundCount(); n > 10 {
		t.Errorf("%d bound objects left after collecting 100 unused ones", n)
	}
}

func TestEmptyTokenLeavesTheColorAlone(t *testing.T) {
	restoreDark(t)
	rect := BindRect(&canvas.Rectangle{}, ColorNameSidebar, "")
	if rect.StrokeColor != nil {
		t.Errorf("stroke = %v, want untouched", rect.StrokeColor)
	}
}

func TestRegistryStaysBoundedWithoutVariantChange(t *testing.T) {
	restoreDark(t)
	live := []*canvas.Text{BindText(canvas.NewText("live", nil), "foreground")}
	for i := range 3000 {
		BindText(canvas.NewText("gone", nil), "foreground")
		BindRect(&canvas.Rectangle{}, ColorNameSidebar, "")
		BindCircle(&canvas.Circle{}, ColorNameAvatar, "")
		if len(live) < 50 {
			live = append(live, BindText(canvas.NewText("live", nil), "foreground"))
		}
		if i%100 == 0 {
			runtime.GC()
		}
	}
	runtime.GC()
	BindText(canvas.NewText("trigger", nil), "foreground")
	if n := boundCount(); n > 600 {
		t.Errorf("%d entries registered after 9000 short-lived binds", n)
	}
	SetVariant(theme.VariantLight)
	for _, l := range live {
		if hex(l.Color) != hex(ColorFor("foreground", theme.VariantLight)) {
			t.Fatal("a live bound text lost its binding")
		}
	}
	runtime.KeepAlive(live)
}
