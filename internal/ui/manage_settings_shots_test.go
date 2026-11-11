package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// TestManageSettingsShots renders the Settings page (sidebar, also scrolled to the end, and window) and
// its dialogs, light, into $OPENLAMT_SHOTS for review against Settings.dc.html, Danger.dc.html and
// Forms.dc.html. Skipped without the variable. Only made-up data.
func TestManageSettingsShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := func(name string, w fyne.Window) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, w.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
	englishUI(t)
	restoreDarkVariant(t)
	dev := &session.Fake{FP: certFP}
	dev.Cfg.Settings, dev.Cfg.Certificates = exampleSettings(), exampleCerts()
	h := newManageHarness(t, dev)
	h.m.name = "Lab Server"
	switchVariant(h.a, theme.VariantLight)

	device := h.m.parent
	device.SetPadded(false)
	scaleWindow(device)
	stage := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "")
	device.SetContent(container.NewBorder(nil, nil, nil, h.m.Sidebar(), stage))
	device.Resize(fyne.NewSize(1280, 800))
	h.wait(t)
	h.m.side.tabs.Tap(3)
	h.wait(t)
	shot("settings-sidebar.png", device)
	h.m.side.view.slots[3].(*container.Scroll).ScrollToBottom()
	shot("settings-sidebar-bottom.png", device)

	h.m.OpenWindow()
	w := h.m.win
	scaleWindow(w.win)
	w.win.Resize(fyne.NewSize(1280, 880))
	test.Tap(w.nav[3])
	shot("settings-window.png", w.win)
	wp := w.view.panes[3].impl.(*settingsPane)

	// the dialogs open on the window of the view
	wp.host.SetText("lab01")
	wp.host.OnSubmitted(wp.host.Text)
	test.Tap(wp.dialog.check)
	shot("settings-dialog-host-name.png", w.win)
	wp.dialog.Dismiss()

	wp.address.Tap(1)
	wp.dialog.entry.SetText("device.example.com")
	shot("settings-dialog-network.png", w.win)
	wp.dialog.Dismiss()

	test.Tap(wp.change)
	h.wait(t)
	shot("settings-dialog-tls.png", w.win)
	wp.dialog.Dismiss()

	test.Tap(wp.turnOff)
	wp.dialog.entry.SetText("device.example.com")
	shot("settings-dialog-turn-off.png", w.win)
	wp.dialog.Dismiss()
}
