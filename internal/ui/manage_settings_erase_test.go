package ui

import (
	"fmt"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// rpeInfo is a device with Remote Platform Erase (caps), allowed in the boot service or not.
func rpeInfo(caps amt.EraseActions, enabled bool) amt.BootInfo {
	return amt.BootInfo{OCR: amt.OCRState{Known: true},
		Erase: amt.EraseState{Platform: amt.EraseSupported | caps, Enabled: enabled, BIOSEnabled: true}}
}

// rseInfo is an older device with Remote Secure Erase only.
func rseInfo() amt.BootInfo { return amt.BootInfo{Erase: amt.EraseState{Secure: true}} }

// allRPE is every action OpenLAMT offers.
const allRPE = amt.EraseSSDs | amt.EraseVerify | amt.EraseTPM | amt.EraseBIOSVariables | amt.EraseBIOSReload | amt.EraseUnconfigure

// eraseHarness is settingsHarness with the boot info and a session with or without TLS.
func eraseHarness(t *testing.T, tls bool, edit func(*session.Fake)) (*manageHarness, *settingsPane) {
	t.Helper()
	englishUI(t)
	dev := &session.Fake{FP: certFP}
	dev.Cfg.Settings = exampleSettings()
	dev.Cfg.Certificates = exampleCerts()
	if edit != nil {
		edit(dev)
	}
	h := newManageHarnessTLS(t, dev, tls)
	h.m.parent.SetContent(h.m.Sidebar())
	h.m.parent.Resize(fyne.NewSize(400, 800))
	h.wait(t) // hardware
	h.m.side.tabs.Tap(3)
	h.wait(t)
	return h, h.m.side.view.panes[3].impl.(*settingsPane)
}

func withBoot(info amt.BootInfo) func(*session.Fake) {
	return func(f *session.Fake) { f.BootFake().Info = info }
}

// nothingErased fails if anything reached the device's erase or the consent.
func nothingErased(t *testing.T, h *manageHarness) {
	t.Helper()
	if n := len(h.dev.BootFake().Erases()); n != 0 || h.dev.BootConsents() != 0 {
		t.Fatalf("%d erases, %d consents", n, h.dev.BootConsents())
	}
}

// The switch "Remote Platform Erase" shows where the device offers it, with its state.
func TestSettingsRPERowFollowsTheDevice(t *testing.T) {
	for name, c := range map[string]struct {
		info          amt.BootInfo
		shown, active bool
	}{
		"none":            {amt.BootInfo{}, false, false},
		"secure erase":    {rseInfo(), false, false},
		"no boot service": {amt.BootInfo{Erase: amt.EraseState{Platform: amt.EraseSupported | amt.EraseSSDs}}, false, false},
		"off":             {rpeInfo(allRPE, false), true, false},
		"on":              {rpeInfo(allRPE, true), true, true},
	} {
		_, p := eraseHarness(t, true, withBoot(c.info))
		if p.rpeRow.visibleRow() != c.shown || p.rpe.Checked() != c.active || p.rpe.Disabled() {
			t.Errorf("%s: shown %v checked %v disabled %v", name, p.rpeRow.visibleRow(), p.rpe.Checked(), p.rpe.Disabled())
		}
	}
}

// The switch writes at once in both directions and reads again.
func TestSettingsRPESwitchWrites(t *testing.T) {
	h, p := eraseHarness(t, true, withBoot(rpeInfo(allRPE, false)))
	test.Tap(p.rpe)
	h.written(t)
	if got := h.dev.BootFake().RPEWrites(); !slices.Equal(got, []bool{true}) || !p.rpe.Checked() {
		t.Fatalf("writes %v checked %v", got, p.rpe.Checked())
	}
	test.Tap(p.rpe)
	h.written(t)
	if got := h.dev.BootFake().RPEWrites(); !slices.Equal(got, []bool{true, false}) || p.rpe.Checked() {
		t.Fatalf("writes %v checked %v", got, p.rpe.Checked())
	}
}

// A BIOS with Remote Platform Erase off refuses: "Turn it on in the BIOS first."
func TestSettingsRPERefusedByBIOS(t *testing.T) {
	h, p := eraseHarness(t, true, func(f *session.Fake) {
		f.BootFake().Info = rpeInfo(allRPE, false)
		f.BootFake().RPEErr = fmt.Errorf("set remote platform erase: %w", amt.ErrRPEOffInBIOS)
	})
	test.Tap(p.rpe)
	h.written(t)
	if p.rpe.Checked() || p.d.writeErr == nil || describe(p.d.writeErr) != "Turn it on in the BIOS first." {
		t.Fatalf("checked %v err %v", p.rpe.Checked(), p.d.writeErr)
	}
}

// The danger zone row: hidden while the boot capabilities are unknown, grey with the reason
// where the device cannot erase, active otherwise.
func TestSettingsEraseRowStates(t *testing.T) {
	_, p := eraseHarness(t, true, func(f *session.Fake) { f.BootFake().InfoErr = amt.ErrAccessDenied })
	if p.eraseRow.Visible() || p.eraseLine.Visible() {
		t.Error("row shown without boot capabilities")
	}
	_, p = eraseHarness(t, true, nil)
	if !p.eraseRow.Visible() || !p.erase.Disabled() || !hasRichText(t, p.eraseRow, "Lab does not support remote erase.", false) ||
		!hasText(p.eraseRow, "Erase drives") {
		t.Errorf("not supported: shown %v disabled %v", p.eraseRow.Visible(), p.erase.Disabled())
	}
	for name, info := range map[string]amt.BootInfo{"platform": rpeInfo(allRPE, true), "platform off": rpeInfo(allRPE, false), "secure": rseInfo()} {
		_, p = eraseHarness(t, true, withBoot(info))
		if !p.eraseRow.Visible() || p.erase.Disabled() || p.erase.Text != "Erase…" ||
			!hasRichText(t, p.eraseRow, "Restarts the device and erases its drives.", false) {
			t.Errorf("%s: shown %v disabled %v", name, p.eraseRow.Visible(), p.erase.Disabled())
		}
	}
}

// The red dialog of Remote Platform Erase: every action of the design, the missing ones greyed
// with "(not supported)", the drives and their check ticked; the button needs the risk box and
// an action; nothing is sent before it.
func TestEraseDialogPlatform(t *testing.T) {
	caps := allRPE &^ amt.EraseBIOSReload
	h, p := eraseHarness(t, true, withBoot(rpeInfo(caps, true)))
	test.Tap(p.erase)
	e := p.eraseDlg
	if e == nil || !hasText(e.frame, "Erase drives on Lab?") || !hasRichText(t, e.frame, "This cannot be undone. ", true) ||
		!hasRichText(t, e.frame, "Lab restarts and erases what you select below. The operating system and all files on the drives are lost.", false) {
		t.Fatal("no red dialog")
	}
	want := map[amt.EraseActions]struct {
		text             string
		checked, enabled bool
	}{
		amt.EraseSSDs:          {"Securely erase all SSDs", true, true},
		amt.EraseVerify:        {"Verify the erase", true, true},
		amt.EraseTPM:           {"Clear the TPM", false, true},
		amt.EraseBIOSVariables: {"Reset BIOS settings (clear BIOS variables)", false, true},
		amt.EraseBIOSReload:    {"Reload the BIOS factory configuration (not supported)", false, false},
		amt.EraseUnconfigure:   {"Unconfigure Intel AMT · remote access ends", false, true},
	}
	if len(e.checks) != len(want) {
		t.Fatalf("%d checks", len(e.checks))
	}
	for a, w := range want {
		c := e.checks[a]
		if c.Text != w.text || c.Checked != w.checked || c.Disabled() == w.enabled {
			t.Errorf("%v: %q checked %v disabled %v", a, c.Text, c.Checked, c.Disabled())
		}
	}
	if !hasText(e.frame, "Drive password") || e.password.Entry.PlaceHolder != "Only if the drive has a password" ||
		!hasRichText(t, e.frame, "Sent over TLS. Without TLS, OpenLAMT warns first.", false) && !hasText(e.frame, "Sent over TLS. Without TLS, OpenLAMT warns first.") {
		t.Error("password field")
	}
	if e.check.Text != "I understand the risk for Lab (device.example.com)" || e.action.Text != "Erase Drives" || !e.action.Disabled() {
		t.Fatalf("checkbox %q button %q disabled %v", e.check.Text, e.action.Text, e.action.Disabled())
	}
	e.check.SetChecked(true)
	if e.action.Disabled() {
		t.Fatal("button stays disabled with the risk box")
	}
	e.checks[amt.EraseSSDs].SetChecked(false)
	e.checks[amt.EraseVerify].SetChecked(false)
	if !e.action.Disabled() {
		t.Fatal("button enabled without an action")
	}
	e.checks[amt.EraseTPM].SetChecked(true)
	e.checks[amt.EraseSSDs].SetChecked(true)
	e.password.SetText(string(make([]byte, 33)))
	if !e.action.Disabled() {
		t.Fatal("button enabled with an invalid password")
	}
	e.password.SetText("master-1")
	nothingErased(t, h)

	test.Tap(e.action)
	h.wait(t) // the erase (no device window: the page runs it)
	got := h.dev.BootFake().Erases()
	if len(got) != 1 || got[0] != (amt.EraseOptions{Platform: true, Actions: amt.EraseSSDs | amt.EraseTPM, Password: "master-1"}) {
		t.Fatalf("erases %+v", got)
	}
	if e.password.Text() != "" {
		t.Error("password kept in the closed dialog")
	}
	if p.eraseInfo == nil || !hasText(p.eraseInfo.frame, "Erase started") ||
		!hasText(p.eraseInfo.frame, "Lab restarts and erases the drives. The BIOS reports progress and the result in the event log.") {
		t.Fatal("no \"Erase started\"")
	}
	open := p.eraseInfo.extras[0].(*escButton)
	if open.Text != "Open Event Log" {
		t.Fatalf("button %q", open.Text)
	}
	test.Tap(open)
	if h.m.side.view.current != 1 || h.m.pages[1].title != "Event log" {
		t.Fatalf("page %d", h.m.side.view.current)
	}
}

// Cancel sends nothing.
func TestEraseDialogCancelWritesNothing(t *testing.T) {
	h, p := eraseHarness(t, true, withBoot(rpeInfo(allRPE, true)))
	test.Tap(p.erase)
	p.eraseDlg.check.SetChecked(true)
	test.Tap(p.eraseDlg.cancel)
	nothingErased(t, h)
}

// Remote Secure Erase: one action; a drive password over a session without TLS asks first
// (#37, the key variant), and nothing is sent before "Send Anyway".
func TestEraseDialogSecureEraseWarnsWithoutTLS(t *testing.T) {
	h, p := eraseHarness(t, false, withBoot(rseInfo()))
	test.Tap(p.erase)
	e := p.eraseDlg
	if len(e.checks) != 0 || e.boot == nil || e.boot.Text != "Securely erase the boot drive" || !e.boot.Checked {
		t.Fatal("secure erase choices")
	}
	e.check.SetChecked(true)
	e.boot.SetChecked(false)
	if !e.action.Disabled() {
		t.Fatal("button enabled without the boot drive")
	}
	e.boot.SetChecked(true)
	e.password.SetText("rse-pass")
	test.Tap(e.action)
	if h.m.tlsAsk == nil || !hasText(h.m.tlsAsk.frame, "Send the key without TLS?") {
		t.Fatal("no TLS warning")
	}
	nothingErased(t, h)
	test.Tap(h.m.tlsAsk.action)
	h.wait(t)
	if got := h.dev.BootFake().Erases(); len(got) != 1 || got[0] != (amt.EraseOptions{Password: "rse-pass"}) {
		t.Fatalf("erases %+v", got)
	}
}

// Without a password there is no TLS question.
func TestEraseSecureEraseWithoutPasswordAsksNothing(t *testing.T) {
	h, p := eraseHarness(t, false, withBoot(rseInfo()))
	test.Tap(p.erase)
	p.eraseDlg.check.SetChecked(true)
	test.Tap(p.eraseDlg.action)
	if h.m.tlsAsk != nil {
		t.Fatal("TLS question without a password")
	}
	h.wait(t)
	if got := h.dev.BootFake().Erases(); len(got) != 1 || got[0] != (amt.EraseOptions{}) {
		t.Fatalf("erases %+v", got)
	}
}

// Remote Platform Erase off in the boot service: the hint instead of the dialog.
func TestEraseRPEOffShowsHint(t *testing.T) {
	h, p := eraseHarness(t, true, withBoot(rpeInfo(allRPE, false)))
	test.Tap(p.erase)
	if p.eraseDlg != nil || p.eraseInfo == nil || !hasText(p.eraseInfo.frame, "Remote Platform Erase is off") ||
		!hasText(p.eraseInfo.frame, "Turn on Remote Platform Erase under Remote features, then try again. If the switch fails, turn it on in the BIOS first.") ||
		p.eraseInfo.action.Text != "OK" || p.eraseInfo.cancel != nil {
		t.Fatal("no hint")
	}
	nothingErased(t, h)
}

// Remote Platform Erase without TLS: the error text, no dialog, nothing sent.
func TestErasePlatformWithoutTLSShowsError(t *testing.T) {
	h, p := eraseHarness(t, false, withBoot(rpeInfo(allRPE, true)))
	test.Tap(p.erase)
	if p.eraseDlg != nil || !p.err.box.Visible() ||
		p.err.Text() != "Remote Platform Erase needs a TLS connection. Connect to the device with TLS and try again." {
		t.Fatalf("dialog %v error %q", p.eraseDlg != nil, p.err.Text())
	}
	nothingErased(t, h)
}

// A refused erase shows the error and no "Erase started".
func TestEraseErrorIsShown(t *testing.T) {
	h, p := eraseHarness(t, true, func(f *session.Fake) {
		f.BootFake().Info = rpeInfo(allRPE, true)
		f.BootFake().EraseErr = &amt.MethodError{Method: "SetBootConfigRole", ReturnValue: 2}
	})
	test.Tap(p.erase)
	p.eraseDlg.check.SetChecked(true)
	test.Tap(p.eraseDlg.action)
	h.wait(t)
	if p.eraseInfo != nil || p.d.writeErr == nil || !p.err.box.Visible() {
		t.Fatalf("started %v err %v", p.eraseInfo != nil, p.d.writeErr)
	}
}

// Erase Drives in the device window: the red confirmation, then the KVM consent dialog before
// anything is armed; a cancelled consent writes nothing (the Boot pattern of #98/#102).
func TestEraseRunsConsentInTheDeviceWindow(t *testing.T) {
	h := bootWindow(t, models.PowerStateOn, rpeInfo(allRPE, true), true)
	if h.d.ensureManager().deps.erase == nil {
		t.Fatal("device management runs the erase without the device window")
	}
	c, err := amt.ConfirmChecked(amt.OpErase, "device.example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	o := amt.EraseOptions{Platform: true, Actions: amt.EraseSSDs}
	done := make(chan error, 2)

	h.d.startErase(o, c, func(err error) { done <- err })
	recvAnswer(t, h.asked)("", false)
	h.finished(t)
	if err := <-done; err == nil || len(h.dev.BootFake().Erases()) != 0 || len(h.errs) != 0 {
		t.Fatalf("cancelled: err %v erases %v", err, h.dev.BootFake().Erases())
	}

	h.d.startErase(o, c, func(err error) { done <- err })
	answer := recvAnswer(t, h.asked)
	if len(h.dev.BootFake().Erases()) != 0 || !h.d.powerBtn.Disabled() {
		t.Fatal("erase armed before the consent")
	}
	answer("123456", true)
	h.finished(t)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := h.dev.BootFake().Erases(); len(got) != 1 || got[0] != o || h.dev.BootConsents() != 2 {
		t.Fatalf("erases %+v consents %d", got, h.dev.BootConsents())
	}
	if h.d.powerBtn.Disabled() || h.d.biosCancel != nil {
		t.Fatal("state after the erase")
	}
}
