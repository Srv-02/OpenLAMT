package ui

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// TestImageRectAndMappingAtEveryScale: fitted, smaller and larger than the area, a point on the
// image maps to the same framebuffer pixel, and points beside the image clamp to its edge.
func TestImageRectAndMappingAtEveryScale(t *testing.T) {
	cases := []struct {
		area    fyne.Size
		unit    float32
		pos     fyne.Position
		size    fyne.Size
		at      fyne.Position // a point in the view
		x, y    int           // its framebuffer pixel
		outside fyne.Position // left of and above the image
	}{
		{fyne.NewSize(400, 300), 0, fyne.NewPos(0, 50), fyne.NewSize(400, 200), fyne.NewPos(200, 150), 64, 32, fyne.NewPos(0, 0)},
		{fyne.NewSize(400, 300), 1, fyne.NewPos(136, 118), fyne.NewSize(128, 64), fyne.NewPos(136+10, 118+20), 10, 20, fyne.NewPos(100, 100)},
		{fyne.NewSize(400, 300), 0.5, fyne.NewPos(168, 134), fyne.NewSize(64, 32), fyne.NewPos(168+5, 134+10), 10, 20, fyne.NewPos(0, 0)},
		{fyne.NewSize(400, 300), 2, fyne.NewPos(72, 86), fyne.NewSize(256, 128), fyne.NewPos(72+100, 86+50), 50, 25, fyne.NewPos(10, 10)},
		// larger than the view's height (the scroll container made the view as wide as the
		// image): at the top, the scroll container shows the rest
		{fyne.NewSize(512, 200), 4, fyne.NewPos(0, 0), fyne.NewSize(512, 256), fyne.NewPos(400, 255), 100, 63, fyne.NewPos(0, 0)},
	}
	for _, c := range cases {
		pos, size := imageRect(c.area, 128, 64, c.unit)
		if pos != c.pos || size != c.size {
			t.Errorf("unit %v: rect %v %v, want %v %v", c.unit, pos, size, c.pos, c.size)
		}
		a := c.area
		if x, y := toFramebuffer(c.at, a, 128, 64, c.unit); x != c.x || y != c.y {
			t.Errorf("unit %v: %v maps to %d,%d, want %d,%d", c.unit, c.at, x, y, c.x, c.y)
		}
		if x, y := toFramebuffer(c.outside, a, 128, 64, c.unit); x != 0 || y != 0 {
			t.Errorf("unit %v: outside maps to %d,%d", c.unit, x, y)
		}
	}
}

// TestViewPointerAtFixedScale: at 200% the view needs the scaled size (the scroll container
// scrolls), and a click is sent at the framebuffer pixel under the pointer.
func TestViewPointerAtFixedScale(t *testing.T) {
	h := newView(t)
	c, srv := startKVM(t)
	h.v.attach(c, func(err error) { h.ended <- err })
	recv(t, h.frames)
	h.v.setScale(2)
	if got := h.v.MinSize(); got != fyne.NewSize(256, 128) {
		t.Fatalf("MinSize at 200%% %v", got)
	}
	h.w.Canvas().Focus(h.v)
	pos, _ := imageRect(h.v.Size(), 128, 64, 2)
	h.v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: pos.Add(fyne.NewPos(100, 50))}, Button: desktop.MouseButtonPrimary})
	got := waitInput(t, srv, 1)
	if want := pointerMsg(50, 25, 1); string(got[len(got)-1]) != string(want) {
		t.Fatalf("pointer %x, want %x", got[len(got)-1], want)
	}
	h.v.setScale(0)
	if got := h.v.MinSize(); got != (fyne.Size{}) {
		t.Fatalf("fitted MinSize %v", got)
	}
}

// TestReverseMouseWheel: with Reverse Mouse Wheel a scroll up is sent as wheel down (button 5).
func TestReverseMouseWheel(t *testing.T) {
	h := newView(t)
	c, srv := startKVM(t)
	h.v.attach(c, func(err error) { h.ended <- err })
	recv(t, h.frames)
	h.w.Canvas().Focus(h.v)
	h.v.reverseWheel = true
	h.v.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(200, 150)}, Scrolled: fyne.NewDelta(0, wheelStep("linux"))})
	got := waitInput(t, srv, 2)
	if got[0][1] != byte(wheelDown) || got[1][1] != 0 {
		t.Fatalf("masks %x %x, want wheel down", got[0][1], got[1][1])
	}
}

// TestViewSwallowsShortcuts: the view consumes shortcuts, so the Scale shortcuts of the window
// do not fire while the focus is in the remote image.
func TestViewSwallowsShortcuts(t *testing.T) {
	var _ fyne.Shortcutable = (*kvmView)(nil)
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.connect(t, true)
	if h.d.win.Canvas().Focused() != h.d.view {
		t.Fatal("view not focused")
	}
	h.d.view.TypedShortcut(shortcutActual)
	if h.d.scale.choice != 0 {
		t.Fatal("a shortcut in the view changed the scale")
	}
}

func scaleItem(t *testing.T, d *DeviceWindow, label string) *fyne.MenuItem {
	t.Helper()
	for _, it := range d.scale.menu.Items {
		if it.Label == label {
			return it
		}
	}
	t.Fatalf("no item %q", label)
	return nil
}

func pickScale(t *testing.T, d *DeviceWindow, label string) {
	t.Helper()
	test.Tap(d.scale.btn)
	if d.scale.popup == nil {
		t.Fatal("Scale menu not shown")
	}
	tapMenuItem(t, d.scale.popup, d.scale.menu, scaleItem(t, d, label))
}

// TestScaleMenu: the items of the design in order with their shortcuts, Fit to Window checked
// at start; a fixed step zooms the view in its scroll container, Zoom In and Out step from the
// shown scale and stop at the ends; the window shortcuts work; full screen fits and gives the
// choice back afterwards; the Console view locks the menu.
func TestScaleMenu(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	d := h.d
	var labels []string
	for _, it := range d.scale.menu.Items {
		if it.IsSeparator {
			labels = append(labels, "|")
			continue
		}
		labels = append(labels, it.Label)
	}
	want := []string{"Fit to Window", "Actual Size (100%)", "|", "50%", "75%", "150%", "200%", "|", "Zoom In", "Zoom Out", "|", "Reverse Mouse Wheel"}
	if len(labels) != len(want) {
		t.Fatalf("items %q", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("items %q, want %q", labels, want)
		}
	}
	for label, keys := range map[string]string{"Fit to Window": "Ctrl+0", "Actual Size (100%)": "Ctrl+1", "Zoom In": "Ctrl++", "Zoom Out": "Ctrl+-"} {
		if got := shortcutLabel(scaleItem(t, d, label)); got != keys {
			t.Errorf("%s shortcut %q, want %q", label, got, keys)
		}
	}
	if !scaleItem(t, d, "Fit to Window").Checked || d.scale.btn.Disabled() || d.scale.btn.Caption() != "Scale" {
		t.Fatal("Fit to Window must be the checked default; Scale enabled")
	}

	h.connect(t, true)
	pickScale(t, d, "200%")
	if d.view.scale != 2 || !scaleItem(t, d, "200%").Checked || scaleItem(t, d, "Fit to Window").Checked {
		t.Fatalf("scale %v after 200%%", d.view.scale)
	}
	if !scaleItem(t, d, "Zoom In").Disabled || scaleItem(t, d, "Zoom Out").Disabled {
		t.Fatal("at 200% only Zoom Out applies")
	}
	if d.kvmScroll.Content != d.view || d.view.Size().Width < 256 {
		t.Fatalf("view %v in its scroll container", d.view.Size())
	}
	pickScale(t, d, "Zoom Out")
	if d.view.scale != 1.5 {
		t.Fatalf("Zoom Out from 200%%: %v", d.view.scale)
	}
	pickScale(t, d, "50%")
	if !scaleItem(t, d, "Zoom Out").Disabled || scaleItem(t, d, "Zoom In").Disabled {
		t.Fatal("at 50% only Zoom In applies")
	}

	typed := shortcutTyper(t, d.win.Canvas())
	typed.TypedShortcut(shortcutActual)
	if d.view.scale != 1 {
		t.Fatalf("Ctrl+1: %v", d.view.scale)
	}
	typed.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEqual, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift})
	if d.view.scale != 1.5 {
		t.Fatalf("Ctrl+Shift+= (Ctrl++): %v", d.view.scale)
	}
	typed.TypedShortcut(shortcutZoomOut)
	if d.view.scale != 1 {
		t.Fatalf("Ctrl+-: %v", d.view.scale)
	}

	test.Tap(d.fullBtn)
	if d.view.scale != 0 || d.scale.choice != 1 {
		t.Fatalf("full screen: view %v choice %v, want fitted", d.view.scale, d.scale.choice)
	}
	typed.TypedShortcut(shortcutFit) // no Scale in full screen
	if d.scale.choice != 1 {
		t.Fatal("shortcut changed the scale in full screen")
	}
	test.Tap(d.exitFullBtn)
	if d.view.scale != 1 {
		t.Fatalf("after full screen: %v", d.view.scale)
	}
	typed.TypedShortcut(shortcutFit)
	if d.view.scale != 0 || !scaleItem(t, d, "Fit to Window").Checked {
		t.Fatalf("Ctrl+0: %v", d.view.scale)
	}

	d.setConsoleView(true)
	if !d.scale.btn.Disabled() {
		t.Fatal("Scale enabled in the Console view")
	}
	typed.TypedShortcut(shortcutActual)
	if d.view.scale != 0 {
		t.Fatal("shortcut worked in the Console view")
	}
}

// TestZoomFromFit: Zoom In and Out from Fit to Window step from the scale fitting gives.
func TestZoomFromFit(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.connect(t, true)
	h.d.win.Resize(fyne.NewSize(1120, 760))
	fit := h.d.shownScale() // 128x64 fitted into the stage: well above 200%
	if fit <= 2 {
		t.Fatalf("fit scale %v", fit)
	}
	if !h.d.scale.zoomIn.Disabled && h.d.nextStep(1) != 0 {
		t.Fatal("Zoom In above 200% must not apply")
	}
	h.d.zoom(-1)
	if h.d.view.scale != 2 {
		t.Fatalf("Zoom Out from fit %v: %v", fit, h.d.view.scale)
	}
}

// TestReverseMouseWheelIsStoredPerFavorite: the check mark follows the favorite, a click turns
// the wheel around at once and stores it with the favorite.
func TestReverseMouseWheelIsStoredPerFavorite(t *testing.T) {
	h, store, saved := keyboardDevice(t, &favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", ReverseWheel: true})
	if !h.d.view.reverseWheel || !scaleItem(t, h.d, "Reverse Mouse Wheel").Checked {
		t.Fatal("stored Reverse Mouse Wheel not loaded")
	}
	pickScale(t, h.d, "Reverse Mouse Wheel")
	recv(t, saved)
	if h.d.view.reverseWheel || scaleItem(t, h.d, "Reverse Mouse Wheel").Checked || store.stored().ReverseWheel {
		t.Fatal("Reverse Mouse Wheel not turned off and stored")
	}
	pickScale(t, h.d, "Reverse Mouse Wheel")
	recv(t, saved)
	if !h.d.view.reverseWheel || !store.stored().ReverseWheel {
		t.Fatal("Reverse Mouse Wheel not turned on and stored")
	}
}

// shortcutTyper returns the canvas' shortcut dispatch as the glfw driver calls it when the
// focused object takes no shortcuts. Fyne's test canvas wraps the software canvas, whose
// TypedShortcut it does not forward, so it is reached through the embedded field.
func shortcutTyper(t *testing.T, c fyne.Canvas) interface{ TypedShortcut(fyne.Shortcut) } {
	t.Helper()
	inner := reflect.ValueOf(c).Elem().FieldByName("WindowlessCanvas")
	if !inner.IsValid() {
		t.Fatal("test canvas without WindowlessCanvas")
	}
	typed, ok := inner.Interface().(interface{ TypedShortcut(fyne.Shortcut) })
	if !ok {
		t.Fatal("canvas has no TypedShortcut")
	}
	return typed
}

func shortcutLabel(it *fyne.MenuItem) string { return widgets.ShortcutText(it.Shortcut) }

// TestToolbarFitsTheWindow: with Scale, Screenshot and Record the toolbar still fits the
// window's 1120 px (gesamtdesign V23: about 1,090 px).
func TestToolbarFitsTheWindow(t *testing.T) {
	d := newOnDevice(t)
	w := d.content.MinSize().Width
	t.Logf("window content needs %v px", w)
	if w > 1120 {
		t.Fatalf("toolbar needs %v px, window is 1120", w)
	}
}
