package ui

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
)

// TestDeviceShots renders the device window in its main states into $OPENLAMT_SHOTS (PNG) for
// review against the design. Skipped without the variable. Only made-up data.
func TestDeviceShots(t *testing.T) {
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
	size := fyne.NewSize(1120, 732) // the mockups' 760 minus the native title bar
	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}

	// KVM disconnected, Power menu open.
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: on}, true)
	h.d.win.SetPadded(false)
	scaleWindow(h.d.win)
	h.d.win.Resize(size)
	h.d.apply(on, nil)
	shot("session-disconnected.png", h.d.win)
	h.d.showPowerMenu()
	shot("session-power-menu.png", h.d.win)
	h.d.powerPopup.Dismiss()
	h.d.showQualityMenu()
	shot("session-quality-menu.png", h.d.win)
	h.d.qualityPopup.Dismiss()
	showPowerConfirm(h.d.win, amt.PowerOff, h.d.name, func() {})
	shot("session-confirm-power-off.png", h.d.win)

	// No TLS, failed status read.
	h2 := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: on}, false)
	h2.d.win.SetPadded(false)
	scaleWindow(h2.d.win)
	h2.d.win.Resize(size)
	h2.d.apply(amt.Status{}, errors.New("read failed"))
	shot("session-no-tls-error.png", h2.d.win)

	// KVM connected (the fake server's red test frame), then full screen with the bar.
	k := newKVMDevice(t, &sessionFake{St: on}, kvmtest.Options{Updates: [][]byte{redUpdate(1024, 768)}})
	k.d.win.SetPadded(false)
	scaleWindow(k.d.win)
	k.d.win.Resize(size)
	k.connect(t, true)
	shot("session-connected.png", k.d.win)
	k.d.kvmStatus.SetText("Typing clipboard: 12 of 40 characters")
	shot("session-typing.png", k.d.win)
	k.d.kvmStatus.SetText("")
	k.d.setFullScreen(true)
	k.d.win.Resize(size)
	shot("session-fullscreen.png", k.d.win)
	k.d.setFullScreen(false)
	k.disconnect(t)
}
