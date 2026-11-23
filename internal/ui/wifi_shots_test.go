package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// TestWiFiShots renders the Wi-Fi page (sidebar in a stand-in device window, own window), its
// states and dialogs, light, at 1280 x 800, into $OPENLAMT_SHOTS for review against the overall
// design ("Verwaltung: Wi-Fi"). Skipped without the variable. Only made-up data.
func TestWiFiShots(t *testing.T) {
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
	withManagePages(t, wifiPageDef(t))
	dev := &session.Fake{FP: fp, HW: exampleHW()}
	st := exampleWiFi()
	st.OverWiFi = true
	dev.WiFiFake().State = st
	h := newManageHarness(t, dev)
	h.m.deps.host = riskHost
	switchVariant(h.a, theme.VariantLight)

	device := h.a.NewWindow("Lab")
	device.SetPadded(false)
	scaleWindow(device)
	stage := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "")
	device.SetContent(container.NewBorder(nil, nil, nil, h.m.Sidebar(), stage))
	device.Resize(fyne.NewSize(1280, 800))
	h.wait(t)
	side := h.m.side
	side.tabs.Tap(side.more)
	side.morePopup.Item(0).Tapped(nil)
	h.wait(t)
	shot("wifi-sidebar.png", device)

	h.m.OpenWindow()
	w := h.m.win
	scaleWindow(w.win)
	w.win.Resize(fyne.NewSize(1280, 800))
	i := slices.IndexFunc(h.m.pages, func(pg managePage) bool { return pg.title == "Wi-Fi" })
	test.Tap(w.nav[i])
	p := w.view.panes[i].impl.(*wifiPane)
	shot("wifi-window.png", w.win)

	test.Tap(p.list.table.lines[0].action)
	shot("wifi-dialog-edit.png", w.win)
	test.Tap(p.profileDlg.del)
	shot("wifi-dialog-lockout.png", w.win)
	p.lockDlg.Dismiss()
	p.profileDlg.Dismiss()

	test.Tap(p.add)
	shot("wifi-dialog-add.png", w.win)
	p.profileDlg.Dismiss()

	p.state.Tap(0)
	shot("wifi-dialog-off.png", w.win)
	p.stateLockDlg.Dismiss()

	h.dev.WiFiFake().State.OverWiFi = false
	p.d.src.reload()
	h.wait(t)
	test.Tap(p.list.table.lines[0].action)
	test.Tap(p.profileDlg.del)
	shot("wifi-dialog-delete.png", w.win)
	p.deleteDlg.Dismiss()
	p.profileDlg.Dismiss()

	h.dev.WiFiFake().State = amt.WiFiSettings{}
	p.d.src.reload()
	h.wait(t)
	shot("wifi-window-none.png", w.win)
	shot("wifi-sidebar-none.png", device)
}
