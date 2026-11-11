package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// TestManageShots renders the device management sidebar (in a stand-in device window) and its
// own window, light, at the mockups' 1280 x 800, into $OPENLAMT_SHOTS for review against
// Main.dc.html and WindowB.dc.html. Skipped without the variable. Only made-up data.
func TestManageShots(t *testing.T) {
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
	events := exampleEvents()
	for i := 0; i < 40; i++ {
		events = append(events, amt.EventLogEntry{Time: time.Date(2026, 11, 4, 12, 0, 59-i, 0, time.UTC), Source: "Intel(r) ME",
			Description: fmt.Sprintf("Example event number %d with a longer description that does not fit", i)})
	}
	audit := exampleAudit()
	audit = append(audit, amt.AuditLogEntry{Time: time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC), InitiatorType: 1,
		Initiator: "S-1-5-21-1111111111-2222222222-3333333333-1001", Address: "2001:db8::1", Event: "KVM Session Started"})
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW(), Events: events, Audit: audit})
	switchVariant(h.a, theme.VariantLight)
	h.m.now = func() time.Time { return h.m.hw.at.Add(12 * time.Second) }

	device := h.a.NewWindow("Lab")
	device.SetPadded(false)
	scaleWindow(device)
	stage := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "")
	device.SetContent(container.NewBorder(nil, nil, nil, h.m.Sidebar(), stage))
	device.Resize(fyne.NewSize(1280, 800))
	h.wait(t)
	h.m.tick()
	shot("manage-sidebar-hardware.png", device)
	for i, name := range []string{"events", "audit", "settings", "users"} {
		h.m.side.tabs.Tap(i + 1)
		if i < 2 {
			h.wait(t)
		}
		shot("manage-sidebar-"+name+".png", device)
	}

	h.m.OpenWindow()
	w := h.m.win
	scaleWindow(w.win)
	w.win.Resize(fyne.NewSize(1280, 800))
	for i, name := range []string{"hardware", "events", "audit", "settings", "users"} {
		test.Tap(w.nav[i])
		if i == 1 {
			lp := w.view.panes[1].impl.(*logPane)
			lp.filter.SetText("boot")
		}
		shot("manage-window-"+name+".png", w.win)
	}
	switchVariant(h.a, theme.VariantDark)
	shot("manage-window-users-dark.png", w.win)
	test.Tap(w.nav[1])
	shot("manage-window-events-dark.png", w.win)
}
