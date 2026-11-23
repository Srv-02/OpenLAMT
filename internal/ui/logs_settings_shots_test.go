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

// TestLogSettingsShots renders the audit log settings (#15) and event forwarding (#18) dialogs,
// light, at 1280 x 800, into $OPENLAMT_SHOTS for review against the overall design (V2, V3).
// Skipped without the variable. Only made-up data.
func TestLogSettingsShots(t *testing.T) {
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
	dev := &session.Fake{FP: fp, HW: exampleHW(), Events: exampleEvents(), Audit: exampleAudit()}
	dev.AuditState().Settings = exampleAuditSettings()
	dev.Forwarding().Data = exampleForwarding()
	h := newManageHarnessTLS(t, dev, false)
	switchVariant(h.a, theme.VariantLight)

	device := h.a.NewWindow("Lab")
	device.SetPadded(false)
	scaleWindow(device)
	stage := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "")
	device.SetContent(container.NewBorder(nil, nil, nil, h.m.Sidebar(), stage))
	device.Resize(fyne.NewSize(1280, 800))
	h.wait(t)
	h.m.side.tabs.Tap(2)
	h.wait(t)
	shot("logs-audit-sidebar.png", device)
	h.m.side.tabs.Tap(1)
	h.wait(t)
	shot("logs-events-sidebar.png", device)

	h.m.openWindowAt(2)
	w := h.m.win
	scaleWindow(w.win)
	w.win.Resize(fyne.NewSize(1280, 800))
	shot("logs-audit-window.png", w.win)
	p := w.view.panes[2].impl.(*logPane)
	test.Tap(p.head)
	h.wait(t)
	d := h.m.logDlg.audit
	shot("logs-audit-settings.png", w.win)
	d.storage.SetSelectedIndex(1)
	d.days.SetText("14")
	test.Tap(d.record)
	shot("logs-audit-settings-changed.png", w.win)
	test.Tap(d.action)
	shot("logs-audit-stop-recording.png", w.win)
	d.confirm.Dismiss()

	test.Tap(p.head)
	h.wait(t)
	d = h.m.logDlg.audit
	test.Tap(d.clear)
	shot("logs-audit-clear.png", w.win)
	d.confirm.Dismiss()
	d.Dismiss()
	h.m.logDlg.denied = showAuditDenied(w.win, "admin")
	shot("logs-audit-denied.png", w.win)
	h.m.logDlg.denied.Dismiss()

	test.Tap(w.nav[1]) // read already by the sidebar
	shot("logs-events-window.png", w.win)
	e := w.view.panes[1].impl.(*logPane)
	test.Tap(e.head)
	h.wait(t)
	f := h.m.logDlg.forwarding
	shot("logs-forwarding.png", w.win)
	test.Tap(f.add)
	td := h.m.logDlg.target
	td.address.SetText("192.0.2.5")
	shot("logs-forwarding-add-error.png", w.win)
	td.address.SetText("http://192.0.2.5:16997/events")
	td.auth.Tap(1)
	td.user.SetText("collector")
	shot("logs-forwarding-add.png", w.win)
	td.Dismiss()
	test.Tap(f.edits[0])
	shot("logs-forwarding-edit.png", w.win)
	test.Tap(h.m.logDlg.target.del)
	shot("logs-forwarding-delete.png", w.win)
	test.Tap(overlayButton(t, w.win, "Delete Target"))
	h.wait(t)
	test.Tap(f.edits[0])
	test.Tap(h.m.logDlg.target.del)
	test.Tap(overlayButton(t, w.win, "Delete Target"))
	h.wait(t)
	shot("logs-forwarding-empty.png", w.win)
}
