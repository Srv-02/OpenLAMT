package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// TestScanConfigShots writes screenshots of the network scan and of Export and Import Config for
// review against the design. Skipped without OPENLAMT_SHOTS. Only made-up data.
func TestScanConfigShots(t *testing.T) {
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

	h := newConfigHarness(t)
	h.s.app.Settings().SetTheme(style.Theme{})
	h.s.win.Resize(fyne.NewSize(1000, 760))
	shot("scan-config-main.png", h.s.win)

	h.openSettings(t)
	shot("config-settings-data.png", h.s.win)
	test.Tap(overlayPrimary(t, h.s.win, "Export…"))
	shot("config-export.png", h.s.win)
	test.Tap(overlayButton(t, h.s.win, "Export…"))
	shot("config-exported.png", h.s.win)
	test.Tap(overlayButton(t, h.s.win, "OK"))
	h.madeFile(t, incoming(t))
	test.Tap(overlayPrimary(t, h.s.win, "Import…"))
	shot("config-import.png", h.s.win)
	test.Tap(overlayButton(t, h.s.win, "Cancel"))

	sh := newScanHarness(t, fakeDevices(t))
	sh.s.app.Settings().SetTheme(style.Theme{})
	sh.s.win.Resize(fyne.NewSize(1000, 760))
	shot("scan-empty-list.png", sh.s.win)
	sh.open()
	shot("scan-idle.png", sh.s.win)
	sh.scan(t, "127.0.0.1-127.0.0.4")
	shot("scan-done.png", sh.s.win)
	sh.sd.entry.SetText("192.0.2.0/16")
	test.Tap(sh.sd.goBtn)
	shot("scan-error.png", sh.s.win)
	sh.scan(t, "127.0.0.10-127.0.0.12")
	shot("scan-nothing.png", sh.s.win)
}
