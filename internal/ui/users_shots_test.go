package ui

import (
	"errors"
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

// TestUsersShots renders the Users page (sidebar in a stand-in device window, own window) and
// its dialogs, light, at 1280 x 800, into $OPENLAMT_SHOTS for review against Users.dc.html,
// Danger.dc.html and Forms.dc.html. Skipped without the variable. Only made-up data.
func TestUsersShots(t *testing.T) {
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
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW(), Cfg: session.FakeConfig{Users: exampleUsers()}})
	h.m.deps.host = riskHost
	switchVariant(h.a, theme.VariantLight)

	device := h.a.NewWindow("Lab")
	device.SetPadded(false)
	scaleWindow(device)
	stage := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "")
	device.SetContent(container.NewBorder(nil, nil, nil, h.m.Sidebar(), stage))
	device.Resize(fyne.NewSize(1280, 800))
	h.wait(t)
	h.m.side.tabs.Tap(4)
	h.wait(t)
	shot("users-sidebar.png", device)

	h.m.OpenWindow()
	w := h.m.win
	scaleWindow(w.win)
	w.win.Resize(fyne.NewSize(1280, 800))
	test.Tap(w.nav[4])
	p := w.view.panes[4].impl.(*usersPane)
	shot("users-window.png", w.win)

	test.Tap(p.table.lines[0].edit)
	d := p.adminDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	d.check.SetChecked(false) // not ticked yet, as in the mockup
	shot("users-dialog-admin.png", w.win)
	d.check.SetChecked(true)
	shot("users-dialog-admin-ready.png", w.win)
	d.Dismiss()

	test.Tap(p.add)
	a := p.userDlg
	a.name.SetText("helpdesk2")
	a.pw.SetText(newPassword)
	a.repeat.SetText(newPassword)
	test.Tap(a.checks[0])
	test.Tap(a.checks[1])
	test.Tap(a.checks[2])
	shot("users-dialog-add.png", w.win)
	a.Dismiss()

	test.Tap(p.table.lines[1].edit)
	shot("users-dialog-edit.png", w.win)
	p.userDlg.Dismiss()

	ns := showNotSavedDialog(w.win, "Lab", newPassword)
	shot("users-dialog-not-saved.png", w.win)
	ns.display.SetRevealed(true)
	shot("users-dialog-not-saved-revealed.png", w.win)
	ns.Dismiss()

	switchVariant(h.a, theme.VariantDark)
	shot("users-window-dark.png", w.win)
	switchVariant(h.a, theme.VariantLight)

	p.writeDone("", []error{errors.New("example failure")})
	shot("users-dialog-error.png", w.win)
}
