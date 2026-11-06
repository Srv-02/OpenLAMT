package ui

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/update"
)

// TestShotSeries renders every window and dialog in both theme variants (issue #51) into
// $OPENLAMT_SHOTS/<dark|light>/<scale>x/ for review against the design, and with the appearance
// pinned against the system (issue #103) into light-on-dark-system and dark-on-light-system, with the software
// painter and the test renderer. The scale is $OPENLAMT_SHOT_SCALE (default 1). Skipped without
// $OPENLAMT_SHOTS. Only made-up data (example.com, 192.0.2.0/24). Run it for both scales:
//
//	for s in 1 2; do OPENLAMT_SHOTS=$PWD/workbench/ui-shots/series OPENLAMT_SHOT_SCALE=$s go test ./internal/ui -run TestShotSeries; done
func TestShotSeries(t *testing.T) {
	root := os.Getenv("OPENLAMT_SHOTS")
	if root == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	for _, v := range []struct {
		name       string
		variant    fyne.ThemeVariant // the system's
		appearance string
	}{{"dark", theme.VariantDark, AppearanceSystem}, {"light", theme.VariantLight, AppearanceSystem},
		{"light-on-dark-system", theme.VariantDark, AppearanceLight}, {"dark-on-light-system", theme.VariantLight, AppearanceDark}} {
		t.Run(v.name, func(t *testing.T) {
			dir := filepath.Join(root, v.name, fmt.Sprintf("%gx", shotScale()))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			restoreDarkVariant(t)
			apply := func(a fyne.App) {
				NewSettings(a.Preferences()).SetAppearance(v.appearance)
				reportVariant(t, a, v.variant)
				applyAppearance(variantApp{a, v.variant})
			}
			seriesManager(t, dir, apply)
			seriesCoffee(t, dir, apply)
			seriesUpdate(t, dir, apply)
			seriesDevice(t, dir, apply)
			seriesKVM(t, dir, apply)
		})
	}
}

// variantApp is the app with the system setting the test app lacks: Fyne's test app always
// reports variant 2 (dark). Replacing the current app makes every widget, theme override and
// popup see v as the system's variant, and FollowTheme and applyAppearance read it from there.
type variantApp struct {
	fyne.App
	v fyne.ThemeVariant
}

func (a variantApp) Settings() fyne.Settings { return variantSettings{a.App.Settings(), a.v} }

type variantSettings struct {
	fyne.Settings
	v fyne.ThemeVariant
}

func (s variantSettings) ThemeVariant() fyne.ThemeVariant { return s.v }

// reportVariant lets a report variant v like a system setting would and refreshes the windows.
func reportVariant(t *testing.T, a fyne.App, v fyne.ThemeVariant) {
	t.Helper()
	style.SetVariant(v)
	fyne.SetCurrentApp(variantApp{a, v})
	t.Cleanup(func() { fyne.SetCurrentApp(a) })
	a.Settings().SetTheme(style.Theme{})
}

// closeOverlays removes every overlay of w, so the next dialog is the only one.
func closeOverlays(w fyne.Window) {
	for o := w.Canvas().Overlays().Top(); o != nil; o = w.Canvas().Overlays().Top() {
		w.Canvas().Overlays().Remove(o)
	}
}

// softwareShot renders the content of w plus its overlays at size.
func softwareShot(t *testing.T, dir, name string, w fyne.Window, size fyne.Size) {
	t.Helper()
	c := software.NewCanvas()
	c.SetPadded(false)
	c.SetScale(shotScale())
	c.SetContent(w.Content())
	c.Resize(size)
	for _, o := range w.Canvas().Overlays().List() {
		c.Overlays().Add(o)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, c.Capture()); err != nil {
		t.Fatal(err)
	}
}

// windowShot captures a device window (its popups live on the window canvas).
func windowShot(t *testing.T, dir, name string, w fyne.Window) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

func seriesManager(t *testing.T, dir string, apply func(fyne.App)) {
	h := newStart(t)
	s := h.s
	apply(s.app)
	size := fyne.NewSize(1000, 660)
	shot := func(name string) { t.Helper(); softwareShot(t, dir, name, s.win, size) }

	shot("manager-empty")
	addFavorites(t, h,
		favorites.Favorite{Name: "Lab-Server", Host: "192.0.2.10", Username: "admin", UseTLS: true, AutoConnect: favorites.AutoConnectOn},
		favorites.Favorite{Name: "Build-Host", Host: "build.example.com", Username: "operator"},
		favorites.Favorite{Host: "192.0.2.25", Username: "admin", UseTLS: true},
		favorites.Favorite{Name: "Backup-NAS", Host: "nas.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("192.0.2.10", "admin", "secret-1")
	k := deviceKey("192.0.2.10", "admin")
	s.applyProbe(k, readProbe(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}))
	s.applyProbe(deviceKey("build.example.com", "operator"), readProbe(amt.Status{PowerState: models.PowerStateOffSoft}))
	s.applyProbe(deviceKey("nas.example.com", "admin"), readProbe(amt.Status{PowerState: models.PowerStateSleepDeep}))
	s.search.SetText("lab")
	shot("manager-search")
	s.search.SetText("")
	test.Tap(s.rows[k])
	shot("manager-favorite")
	test.Tap(s.editBtn)
	shot("manager-edit-advanced")
	test.Tap(s.editCancelBtn)
	test.Tap(s.newConnBtn)
	s.host.SetText("192.0.2.30")
	s.name.SetText("Test-Rechner")
	shot("manager-quick-connect")
	test.Tap(s.rows[k])
	test.Tap(s.delBtn)
	shot("dialog-delete-confirm")
	closeOverlays(s.win)
	showSettingsDialog(s.win, &s.settings)
	shot("dialog-settings")
	closeOverlays(s.win)
	// Color blind mode: the detail card and the settings with the setting on.
	restoreColorBlind(t)
	s.settings.SetColorBlind(true)
	apply(s.app)
	shot("manager-favorite-colorblind")
	showSettingsDialog(s.win, &s.settings)
	shot("dialog-settings-colorblind")
	closeOverlays(s.win)
	s.settings.SetColorBlind(false)
	apply(s.app)
	showAboutDialog(s.win, s.app)
	shot("dialog-about")
	closeOverlays(s.win)
	showTrustDialog(s.win, "192.0.2.10", fp, func(bool) {})
	shot("dialog-trust")
	closeOverlays(s.win)
	showPasswordDialog(s.win, "192.0.2.10", "admin", true, func(string, bool, bool) {})
	shot("dialog-password")
	closeOverlays(s.win)
	showMessage(s.win, "Something went wrong", "The device at 192.0.2.10 did not answer.", "dial tcp 192.0.2.10:16993: i/o timeout")
	shot("dialog-error")
	closeOverlays(s.win)
	showMessage(s.win, "Information", "The settings were saved.", "")
	shot("dialog-info")
	closeOverlays(s.win)
	showMismatchDialog(s.win, &amt.FingerprintMismatchError{Host: "192.0.2.10", Expected: fp, Actual: fp})
	shot("dialog-mismatch")
	closeOverlays(s.win)
	showConsentDialog(s.win, func(string, bool) {})
	shot("dialog-consent")
	closeOverlays(s.win)
	title, message, button := quitText(2)
	showConfirm(s.win, icons.Power, title, message, button, func() {}, nil)
	shot("dialog-quit-confirm")
	closeOverlays(s.win)
}

func seriesCoffee(t *testing.T, dir string, apply func(fyne.App)) {
	h, _ := nudgeHarness(t, 3)
	apply(h.s.app)
	h.s.coffeeFire()
	softwareShot(t, dir, "dialog-coffee", h.s.win, fyne.NewSize(1000, 700))
}

// seriesUpdate shows the update dialogs with a made-up release: available, downloading, downloaded.
func seriesUpdate(t *testing.T, dir string, apply func(fyne.App)) {
	withBuildVersion(t, "0.1.0")
	h := newStart(t)
	apply(h.s.app)
	size := fyne.NewSize(1000, 700)
	rel := update.Release{Tag: "v0.2.0", HTMLURL: "https://example.com/releases/v0.2.0",
		Body: "## What's Changed\n* Language and appearance in the settings by @someone in https://example.com/pull/1\n" +
			"* Power Off works with an open KVM session\n* Windows icon in Explorer\n\n**Full Changelog**: https://example.com/compare",
		Assets: []update.Asset{{Name: "OpenLAMT-v0.2.0-linux-x64.deb", URL: "https://example.com/pkg"}}}
	prevOS, prevArch := updateGOOS, updateGOARCH
	updateGOOS, updateGOARCH = "linux", "amd64"
	t.Cleanup(func() { updateGOOS, updateGOARCH = prevOS, prevArch })
	n := &updateNotice{ctx: context.Background(), cancel: func() {}, async: func(func()) {}}
	p := h.s.showUpdateAvailable(n, rel)
	softwareShot(t, dir, "dialog-update-available", h.s.win, size)
	p.act()
	softwareShot(t, dir, "dialog-update-downloading", h.s.win, size)
	closeOverlays(h.s.win)
	showUpdateDownloaded(h.s.win, "0.2.0", filepath.Join(string(filepath.Separator)+"home", "user", "Downloads", "OpenLAMT-v0.2.0-macos-arm64.zip"))
	softwareShot(t, dir, "dialog-update-downloaded", h.s.win, size)
}

func seriesDevice(t *testing.T, dir string, apply func(fyne.App)) {
	englishUI(t)
	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	size := fyne.NewSize(1120, 732)
	a := test.NewTempApp(t)
	h := newDevice(t, a, &session.Fake{FP: fp, St: on}, true)
	apply(a)
	h.d.win.SetPadded(false)
	scaleWindow(h.d.win)
	h.d.win.Resize(size)
	h.d.apply(on, nil)
	windowShot(t, dir, "device-disconnected", h.d.win)
	h.d.showPowerMenu()
	windowShot(t, dir, "device-power-menu", h.d.win) // without LMS: Shutdown greyed
	h.d.powerPopup.Dismiss()
	withLMS := on
	withLMS.PowerStates = []power.PowerState{2, 5, 8, 10, 11, power.PowerOffSoftGraceful}
	h.d.apply(withLMS, nil)
	h.d.showPowerMenu()
	windowShot(t, dir, "device-power-menu-shutdown", h.d.win)
	h.d.powerPopup.Dismiss()
	h.d.apply(on, nil)
	h.d.isFavorite, h.d.layoutChoice, h.d.autoOverride = true, "us", favorites.AutoConnectOn // as a favorite with its own choices
	h.d.refreshKeyboardItems()
	h.d.showKeyboardMenu()
	windowShot(t, dir, "device-keyboard-menu", h.d.win)
	h.d.keyboardPopup.Dismiss()
	showPowerConfirm(h.d.win, amt.PowerOff, h.d.name, func() {})
	windowShot(t, dir, "dialog-power-confirm", h.d.win)
	closeOverlays(h.d.win)
	showPowerConfirm(h.d.win, amt.PowerShutdown, h.d.name, func() {})
	windowShot(t, dir, "dialog-power-shutdown", h.d.win)
	closeOverlays(h.d.win)

	a2 := test.NewTempApp(t)
	h2 := newDevice(t, a2, &session.Fake{FP: fp, St: on}, false)
	apply(a2)
	h2.d.win.SetPadded(false)
	scaleWindow(h2.d.win)
	h2.d.win.Resize(size)
	h2.d.apply(amt.Status{}, errors.New("read failed"))
	windowShot(t, dir, "device-no-tls-error", h2.d.win)
}

func seriesKVM(t *testing.T, dir string, apply func(fyne.App)) {
	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	k := newKVMDevice(t, &sessionFake{St: on}, kvmtest.Options{Updates: [][]byte{redUpdate(1024, 768)}})
	apply(fyne.CurrentApp())
	k.d.win.SetPadded(false)
	scaleWindow(k.d.win)
	k.d.win.Resize(fyne.NewSize(1120, 732))
	k.connect(t, true)
	windowShot(t, dir, "device-kvm-connected", k.d.win)
	k.d.showQualityMenu()
	windowShot(t, dir, "device-toolbar-menu", k.d.win)
	k.d.qualityPopup.Dismiss()
	k.d.setFullScreen(true)
	k.d.win.Resize(fyne.NewSize(1120, 732))
	windowShot(t, dir, "device-fullscreen", k.d.win)
	k.d.setFullScreen(false)
	k.disconnect(t)
}
