package ui

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/bootserve"
	"github.com/Srv-02/openlamt/internal/session"
)

// fullBoot is a device that reports every boot option and has One Click Recovery on.
func fullBoot() amt.BootInfo {
	return amt.BootInfo{Caps: amt.BootCapabilities{PXE: true, SafeMode: true, BIOSSetup: true, BIOSPause: true, IDER: true,
		SOL: true, LockPower: true, LockReset: true, LockSleep: true, LockKeyboard: true, PasswordBypass: true,
		ProgressEvents: true, VerbosityQuiet: true, VerboseAll: true, ScreenBlank: true, SecureBootControl: true, HTTPSBoot: true},
		OCR: amt.OCRState{Known: true, Enabled: true, BIOSEnabled: true}}
}

type bootHarness struct {
	*deviceHarness
	shown chan struct{}
	asked chan func(string, bool)
}

// bootWindow opens a device window (power state ps) whose device reports info; consent makes
// the device ask for a code (CCM). The local addresses are 127.0.0.1 only.
func bootWindow(t *testing.T, ps models.PowerState, info amt.BootInfo, consent bool) *bootHarness {
	t.Helper()
	englishUI(t)
	dev := &session.Fake{FP: fp, St: amt.Status{PowerState: ps}, Consent: consent, ConsentCode: "123456"}
	dev.BootFake().Info = info
	oldAddrs, oldRoute := bootAddresses, bootRoute
	bootAddresses = func() []bootserve.Address {
		return []bootserve.Address{{IP: netip.MustParseAddr("127.0.0.1"), Interface: "lo"}}
	}
	bootRoute = func(string) (netip.Addr, error) { return netip.MustParseAddr("127.0.0.1"), nil }
	t.Cleanup(func() { bootAddresses, bootRoute = oldAddrs, oldRoute })
	h := &bootHarness{deviceHarness: newDevice(t, test.NewTempApp(t), dev, true), shown: make(chan struct{}, 4), asked: make(chan func(string, bool), 4)}
	h.d.onConfirmShown = func() { h.shown <- struct{}{} }
	h.d.askConsent = func(answer func(string, bool)) func() { h.asked <- answer; return func() {} }
	h.show(t)
	return h
}

// open taps "Boot Options…" and waits for the dialog.
func (h *bootHarness) open(t *testing.T) *bootDialog {
	t.Helper()
	test.Tap(h.d.powerBtn)
	recv(t, h.applied)
	tapMenuItem(t, h.d.powerPopup, h.d.powerMenu, h.d.boot.item)
	recv(t, h.shown)
	recv(t, h.applied) // the status read after the job: the window is quiet again
	if h.d.boot.dlg == nil {
		t.Fatal("no dialog")
	}
	return h.d.boot.dlg
}

// finished waits for the end of a poller job of the window (actionDone) and the status read after it.
func (h *bootHarness) finished(t *testing.T) {
	t.Helper()
	recv(t, h.actionDone)
	recv(t, h.applied)
}

func (b *bootDialog) choice(tg amt.BootTarget) *bootChoice {
	for _, c := range b.choices {
		if c.target == tg {
			return c
		}
	}
	return nil
}

func (b *bootDialog) pick(t *testing.T, tg amt.BootTarget) {
	t.Helper()
	c := b.choice(tg)
	c.radio.SetSelected(c.radio.Options[0])
	if b.target != tg || !b.chosen {
		t.Fatalf("target %v not chosen", tg)
	}
}

func TestBootOptionsMenuItem(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	if it := h.d.boot.item; it == nil || it.Label != "Boot Options…" || it.Disabled {
		t.Fatalf("item %+v", it)
	}
	h.d.setPowerBusy(true)
	if !h.d.boot.item.Disabled {
		t.Fatal("enabled while a power action runs")
	}
	h.d.setPowerBusy(false)
	h.d.boot.ocr = &ocrRun{cleaned: make(chan struct{})}
	h.d.updatePowerItems()
	if !h.d.boot.item.Disabled {
		t.Fatal("enabled while One Click Recovery serves an image")
	}
	h.d.boot.ocr = nil
}

// What the device does not report is greyed with "(not supported)"; One Click Recovery off
// shows the hint to Settings.
func TestBootDialogGreysUnsupported(t *testing.T) {
	info := amt.BootInfo{Caps: amt.BootCapabilities{HTTPSBoot: true, SOL: true}, OCR: amt.OCRState{Known: true}}
	h := bootWindow(t, models.PowerStateOn, info, false)
	b := h.open(t)
	for _, c := range []struct {
		tg      amt.BootTarget
		label   string
		enabled bool
	}{
		{amt.BootHardDisk, "Hard disk (default order)", true},
		{amt.BootPXE, "Network (PXE) (not supported)", false},
		{amt.BootHTTPS, "HTTPS boot (One Click Recovery)", false},
		{amt.BootBIOSSetup, "BIOS setup (not supported)", false},
		{amt.BootISO, "Mounted ISO (not supported)", false},
	} {
		ch := b.choice(c.tg)
		if ch.radio.Options[0] != c.label || ch.radio.Disabled() == c.enabled {
			t.Errorf("%v: %q disabled %v", c.tg, ch.radio.Options[0], ch.radio.Disabled())
		}
	}
	if !b.ocrOff.Visible() {
		t.Fatal("no hint while One Click Recovery is off")
	}
	if b.safe.check.Text != "Safe mode (not supported)" || !b.safe.check.Disabled() || b.sol.check.Disabled() {
		t.Fatalf("checks %q %v %v", b.safe.check.Text, b.safe.check.Disabled(), b.sol.check.Disabled())
	}
	if len(b.verbosity.Options) != 1 || b.verbosity.Options[0] != "System default" {
		t.Fatalf("verbosity %v", b.verbosity.Options)
	}
	if !b.action.Disabled() {
		t.Fatal("Restart enabled without a target")
	}
	b.pick(t, amt.BootHardDisk)
	if b.action.Disabled() || !b.hint.Visible() || b.warn.Visible() || b.ocrBox.Visible() {
		t.Fatal("hard disk: button or parts wrong")
	}
	b.Dismiss()
	if h.d.boot.dlg != nil || h.d.boot.item.Disabled {
		t.Fatal("dialog state after Cancel")
	}
}

func TestBootDialogMountedISO(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	b := h.open(t)
	if ch := b.choice(amt.BootISO); ch.radio.Options[0] != "Mounted ISO · mount an ISO in Media first" || !ch.radio.Disabled() {
		t.Fatalf("%q", ch.radio.Options[0])
	}
}

// The target locks the options that do not go with it.
func TestBootDialogTargetLocksOptions(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	b := h.open(t)
	b.pick(t, amt.BootHardDisk)
	b.safe.check.SetChecked(true)
	b.pick(t, amt.BootPXE)
	if !b.safe.check.Disabled() || b.safe.check.Checked || !b.pause.check.Disabled() || b.sol.check.Disabled() {
		t.Fatal("PXE: safe mode or pause not locked")
	}
	b.pick(t, amt.BootHTTPS)
	if !b.sol.check.Disabled() || !b.bypass.check.Disabled() || !b.ocrBox.Visible() || !b.fileBox.Visible() || b.urlBox.Visible() {
		t.Fatal("HTTPS: console or bypass not locked, or source box wrong")
	}
	if !b.warn.Visible() || b.hint.Visible() || !b.action.Disabled() {
		t.Fatal("file: no orange warning, or Restart enabled")
	}
	if b.choice(amt.BootPXE).radio.Selected != "" {
		t.Fatal("two targets selected")
	}
	b.source.SetSelected(sourceURL)
	b.update()
	if b.warn.Visible() || !b.hint.Visible() || !b.urlBox.Visible() {
		t.Fatal("URL: parts wrong")
	}
	for _, bad := range []string{"", "http://boot.example.com/r.efi", "boot.example.com/r.efi"} {
		b.url.SetText(bad)
		if !b.action.Disabled() {
			t.Fatalf("%q accepted", bad)
		}
	}
	b.url.SetText("https://boot.example.com/recovery.efi")
	if b.action.Disabled() {
		t.Fatal("valid URL refused")
	}
	if len(b.verbosity.Options) != 4 || b.addr.Selected != "127.0.0.1 (lo)" || b.port.Text != "8443" {
		t.Fatalf("verbosity %v addr %q port %q", b.verbosity.Options, b.addr.Selected, b.port.Text)
	}
}

// The button is red "Restart" while the device runs and blue "Power On" while it is off.
func TestBootDialogButtonFollowsPower(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	if b := h.open(t); b.action.Text != "Restart" || !b.spec.redButton || !b.spec.danger {
		t.Fatalf("on: %q", b.action.Text)
	}
	h = bootWindow(t, models.PowerStateOffSoft, fullBoot(), false)
	if b := h.open(t); b.action.Text != "Power On" || b.spec.redButton {
		t.Fatalf("off: %q", b.action.Text)
	}
}

// Restart asks for the consent first (CCM) and then sets the options of the dialog (issue #90
// pattern); the dialog is the confirmation (simple).
func TestBootRestartRunsConsentThenBoot(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), true)
	b := h.open(t)
	b.pick(t, amt.BootPXE)
	b.lockKeyboard.check.SetChecked(true)
	b.verbosity.SetSelectedIndex(2)
	test.Tap(b.action)
	answer := recvAnswer(t, h.asked)
	if len(h.dev.BootFake().Boots()) != 0 || !h.d.powerBtn.Disabled() {
		t.Fatal("boot options set before the consent")
	}
	answer("123456", true)
	h.finished(t)
	got := h.dev.BootFake().Boots()
	want := amt.BootOptions{Target: amt.BootPXE, LockKeyboard: true, Verbosity: amt.VerbosityVerbose}
	if len(got) != 1 || got[0] != want || h.dev.BootConsents() != 1 {
		t.Fatalf("boots %+v consents %d", got, h.dev.BootConsents())
	}
	if len(h.errs) != 0 || h.d.powerBtn.Disabled() || h.d.biosCancel != nil {
		t.Fatal("state after the boot")
	}
}

func TestBootConsentCancelledWritesNothing(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), true)
	b := h.open(t)
	b.pick(t, amt.BootBIOSSetup)
	test.Tap(b.action)
	recvAnswer(t, h.asked)("", false)
	h.finished(t)
	if len(h.dev.BootFake().Boots()) != 0 || len(h.errs) != 0 || h.d.powerBtn.Disabled() {
		t.Fatalf("boots %v errs %d", h.dev.BootFake().Boots(), len(h.errs))
	}
}

func TestBootErrorIsReported(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	h.dev.BootFake().BootErr = &amt.MethodError{Method: "SetBootConfigRole", ReturnValue: 2}
	b := h.open(t)
	b.pick(t, amt.BootPXE)
	test.Tap(b.action)
	h.finished(t)
	var me *amt.MethodError
	if err := <-h.errs; !errors.As(err, &me) || h.d.powerBtn.Disabled() {
		t.Fatalf("err %v", err)
	}
}

// "Show boot in the console (SOL)" switches to the Console view after the boot.
func TestBootWithSOLShowsConsole(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	selected := 0
	h.d.onConsoleSelected = func() { selected++ } // the console itself is not part of this test
	b := h.open(t)
	b.pick(t, amt.BootBIOSSetup)
	b.sol.check.SetChecked(true)
	test.Tap(b.action)
	h.finished(t)
	if !h.d.consoleActive() || selected != 1 || !h.dev.BootFake().Boots()[0].SOL {
		t.Fatal("console view not shown")
	}
}

func TestBootFromURL(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	b := h.open(t)
	b.pick(t, amt.BootHTTPS)
	b.source.SetSelected(sourceURL)
	b.update()
	b.url.SetText(" https://boot.example.com/recovery.efi ")
	test.Tap(b.action)
	h.finished(t)
	got := h.dev.BootFake().Boots()
	if len(got) != 1 || got[0].Target != amt.BootHTTPS || got[0].URL != "https://boot.example.com/recovery.efi" || len(h.dev.Cfg.Certificates) != 0 {
		t.Fatalf("boots %+v certs %d", got, len(h.dev.Cfg.Certificates))
	}
	if h.d.boot.ocr != nil {
		t.Fatal("a URL boot runs no local server")
	}
}

// fileDialog chooses HTTPS boot from a file on a free port and ticks the risk; it returns the
// image's content.
func fileDialog(t *testing.T, h *bootHarness) (*bootDialog, []byte) {
	t.Helper()
	data := []byte(strings.Repeat("EFI image ", 2000))
	path := filepath.Join(t.TempDir(), "recovery.efi")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b := h.open(t)
	b.pick(t, amt.BootHTTPS)
	b.setFile(path)
	if b.fileText.Text != "recovery.efi · 0.0 MB" || !b.action.Disabled() {
		t.Fatalf("file %q, Restart enabled before the risk was ticked", b.fileText.Text)
	}
	if b.risk.Text != "I understand the risk for Büro (device.example.com)" || !b.risk.wrap {
		t.Fatalf("risk %q wraps %v", b.risk.Text, b.risk.wrap)
	}
	b.port.SetText("70000")
	b.risk.SetChecked(true)
	if !b.action.Disabled() {
		t.Fatal("invalid port accepted")
	}
	b.port.SetText(strconv.Itoa(freeTCPPort(t)))
	if b.action.Disabled() {
		t.Fatal("Restart disabled with file, port and risk")
	}
	return b, data
}

// download loads the image like the device: over TLS, trusting only the temporary root.
func download(t *testing.T, h *bootHarness) []byte {
	t.Helper()
	certs := h.dev.Cfg.Certificates
	if len(certs) != 1 || !certs[0].TrustedRoot {
		t.Fatalf("certificates %+v", certs)
	}
	root, err := x509.ParseCertificate(certs[0].DER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := c.Get(h.dev.BootFake().Boots()[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body
}

// TestBootFromFileServesAndRemovesRoot: the consent, the temporary root and the HTTPS boot of
// the local server; the status bar shows the download; afterwards the root is removed (V7).
func TestBootFromFileServesAndRemovesRoot(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), true)
	b, data := fileDialog(t, h)
	test.Tap(b.action)
	if len(h.dev.Cfg.Certificates) != 0 {
		t.Fatal("root added before the consent")
	}
	recvAnswer(t, h.asked)("123456", true)
	h.finished(t)
	boots := h.dev.BootFake().Boots()
	if len(boots) != 1 || boots[0].Target != amt.BootHTTPS || !strings.HasPrefix(boots[0].URL, "https://127.0.0.1:") || len(h.errs) != 0 {
		t.Fatalf("boots %+v errs %d", boots, len(h.errs))
	}
	if h.d.boot.ocr == nil || !h.d.boot.line.Visible() || !strings.HasPrefix(h.d.boot.line.Text, "Serving recovery.efi · 0.0 of 0.0 MB") {
		t.Fatalf("status %q", h.d.boot.line.Text)
	}
	if !h.d.boot.item.Disabled {
		t.Fatal("Boot Options enabled while serving")
	}
	if got := download(t, h); string(got) != string(data) {
		t.Fatalf("downloaded %d bytes", len(got))
	}
	h.finished(t) // ocrEnded
	if len(h.dev.Cfg.Certificates) != 0 || h.d.boot.ocr != nil || h.d.boot.line.Visible() || len(h.errs) != 0 {
		t.Fatalf("after the download: certs %d line %q errs %d", len(h.dev.Cfg.Certificates), h.d.boot.line.Text, len(h.errs))
	}
}

// Nothing loaded within the timeout: the root goes, the firewall hint is shown.
func TestBootFromFileTimeout(t *testing.T) {
	old := startBootServer
	startBootServer = func(c bootserve.Config) (*bootserve.Server, error) {
		c.Timeout = 50 * time.Millisecond
		return old(c)
	}
	t.Cleanup(func() { startBootServer = old })
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	b, _ := fileDialog(t, h)
	test.Tap(b.action)
	h.finished(t)
	h.finished(t)
	err := <-h.errs
	if describe(err) != "Büro did not download the image. Check the firewall on this computer." || len(h.dev.Cfg.Certificates) != 0 {
		t.Fatalf("%q certs %d", describe(err), len(h.dev.Cfg.Certificates))
	}
}

// A port in use stops before anything is written to the device.
func TestBootFromFilePortInUse(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	b, _ := fileDialog(t, h)
	ln, err := net.Listen("tcp", "127.0.0.1:"+b.port.Text)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	test.Tap(b.action)
	h.finished(t)
	err = <-h.errs
	if describe(err) != "Port "+b.port.Text+" is in use. Choose another port." {
		t.Fatalf("%q", describe(err))
	}
	if len(h.dev.Cfg.Certificates) != 0 || len(h.dev.BootFake().Boots()) != 0 || h.d.boot.ocr != nil || h.dev.BootConsents() != 0 {
		t.Fatal("device changed although the server did not start")
	}
}

// A refused boot removes the root at once and stops the server.
func TestBootFromFileRefusedRemovesRoot(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	h.dev.BootFake().BootErr = &amt.MethodError{Method: "SetBootConfigRole", ReturnValue: 2}
	b, _ := fileDialog(t, h)
	test.Tap(b.action)
	h.finished(t)
	<-h.errs
	if len(h.dev.Cfg.Certificates) != 0 || h.d.boot.ocr != nil {
		t.Fatal("root or run left")
	}
}

// Closing the window while the image is served stops the server and removes the root first.
func TestBootFromFileEndsWithWindow(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, fullBoot(), false)
	b, _ := fileDialog(t, h)
	test.Tap(b.action)
	h.finished(t)
	url := h.dev.BootFake().Boots()[0].URL
	h.d.win.Close()
	recv(t, h.closed)
	waitFor(t, func() bool { return len(h.dev.ConfigWrites()) == 2 }) // AddTrustedRootCertificate, DeleteCertificate
	if w := h.dev.ConfigWrites(); w[1] != "DeleteCertificate" {
		t.Fatalf("writes %v", w)
	}
	if _, err := http.Get(strings.Replace(url, "https://", "http://", 1)); err == nil {
		t.Fatal("server still listening")
	}
}

func TestDescribeBoot(t *testing.T) {
	englishUI(t)
	for err, want := range map[error]string{
		&bootserve.PortInUseError{Port: 8443}: "Port 8443 is in use. Choose another port.",
		bootserve.ErrNotDownloaded:            "The device did not download the image. Check the firewall on this computer.",
		errors.Join(amt.ErrOCROffInBIOS):      "Turn it on in the BIOS first.",
	} {
		if got := describe(err); got != want {
			t.Errorf("%v: %q", err, got)
		}
	}
}
