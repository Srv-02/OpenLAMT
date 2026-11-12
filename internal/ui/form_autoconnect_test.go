package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// The Advanced card shows the override read-only and edits it; the stored value survives
// editing something else and Duplicate.
func TestFormKVMAutoConnectOverride(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	if got := s.values["autoconnect"].Text; got != "Default" {
		t.Fatalf("view shows %q", got)
	}
	editFavorite(t, h, "device.example.com", "admin")
	if s.autoSel.Selected != "Default" || len(s.autoSel.Options) != 3 {
		t.Fatalf("%q %v", s.autoSel.Selected, s.autoSel.Options)
	}
	s.autoSel.SetSelected("On")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].AutoConnect != favorites.AutoConnectOn || s.values["autoconnect"].Text != "On" {
		t.Fatalf("%+v %q", s.items[0], s.values["autoconnect"].Text)
	}
	// Editing the name keeps the override; resetting to Default removes it.
	editFavorite(t, h, "device.example.com", "admin")
	if s.autoSel.Selected != "On" {
		t.Fatalf("edit mode shows %q", s.autoSel.Selected)
	}
	s.name.SetText("Lab")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].AutoConnect != favorites.AutoConnectOn {
		t.Fatalf("%+v", s.items[0])
	}
	editFavorite(t, h, "device.example.com", "admin")
	s.autoSel.SetSelected("Default")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].AutoConnect != "" {
		t.Fatalf("%+v", s.items[0])
	}
}

// A quality changed in the form reaches an open device window; without a session it is the
// quality of the next connection.
func TestFormQualityChangeReachesOpenWindowWithoutSession(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	d := openFakeDevice(t, h, "device.example.com", &session.Fake{FP: fp})
	editFavorite(t, h, "device.example.com", "admin")
	s.qualitySel.SetSelectedIndex(2)
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if d.kvmQuality != kvm.QualityLow || d.kvmClient != nil || d.qualitySwitch != nil {
		t.Fatalf("quality %v client %v", d.kvmQuality, d.kvmClient)
	}
	// Saving without touching the quality leaves the window alone.
	d.applyQuality(kvm.QualityHigh)
	editFavorite(t, h, "device.example.com", "admin")
	s.name.SetText("Lab")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if d.kvmQuality != kvm.QualityHigh {
		t.Fatalf("quality %v", d.kvmQuality)
	}
}

// With a running KVM session the form's quality reconnects it directly with the new quality.
func TestFormQualityChangeReconnectsRunningSession(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	srv := kvmtest.Start(t, kvmtest.Options{Updates: [][]byte{redUpdate(128, 64)}, Username: "admin", Password: "pw"})
	dev := &session.Fake{FP: fp, KVMAddr: srv.Addr, KVMUser: "admin", KVMPassword: "pw"}
	d := openFakeDevice(t, h, "device.example.com", dev)
	changed := make(chan struct{}, 16)
	d.onKVMChanged = func() { changed <- struct{}{} }
	d.wake = nil
	d.connectKVM(false)
	recv(t, changed)
	if d.kvmClient == nil {
		t.Fatal("not connected")
	}
	editFavorite(t, h, "device.example.com", "admin")
	s.qualitySel.SetSelectedIndex(2)
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	recv(t, changed) // reconnect started (the new session replaces the old one)
	recv(t, changed) // reconnect finished
	waitFor(t, func() bool { q := dev.Qualities(); return len(q) == 2 })
	if q := dev.Qualities(); q[0] != kvm.QualityHigh || q[1] != kvm.QualityLow {
		t.Fatalf("qualities %v", q)
	}
	tapKVM(d) // end the session and wait for its end, so nothing runs when the window closes
	recv(t, changed)
}

// TestAutoConnectShots renders the settings dialog and the Advanced card into $OPENLAMT_SHOTS.
// Made-up data.
func TestAutoConnectShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newStart(t)
	s := h.s
	s.app.Settings().SetTheme(style.Theme{})
	shot := func(name string) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		c := software.NewCanvas()
		c.SetPadded(false)
		c.SetScale(shotScale())
		c.SetContent(s.win.Content())
		c.Resize(fyne.NewSize(1000, 800))
		if o := s.win.Canvas().Overlays().Top(); o != nil {
			c.Overlays().Add(o)
		}
		if err := png.Encode(f, c.Capture()); err != nil {
			t.Fatal(err)
		}
	}
	s.settings.SetKVMAutoConnect(true)
	showSettingsDialog(s.win, &s.settings)
	shot("settings.png")
	s.win.Canvas().Overlays().Remove(s.win.Canvas().Overlays().Top())
	addFavorites(t, h, favorites.Favorite{Name: "Lab-Server", Host: "192.0.2.10", Username: "admin", UseTLS: true, AutoConnect: favorites.AutoConnectOn})
	test.Tap(s.rows[deviceKey("192.0.2.10", "admin")])
	shot("advanced-view.png")
	test.Tap(s.editBtn)
	shot("advanced-edit.png")
}
