package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Tests for accounts with limited AMT permissions (issue #35): the device refuses reads, the
// status carries Denied, and the UI shows "not allowed" in its existing text fields.

const allDenied = amt.StatusPower | amt.StatusVersion | amt.StatusControlMode

func deniedStatus() amt.Status { return amt.Status{Denied: allDenied} }

func TestConnectLimitedAccountOpensWindow(t *testing.T) {
	h := newHarness(t)
	h.dev.St = deniedStatus()
	h.addFavorite(t)
	h.c.start(favRequest())
	o := h.waitOpened(t)
	if o.st.Denied != allDenied || len(h.asked) != 0 || len(h.errs) != 0 {
		t.Fatalf("denied=%b asked=%d errs=%d", o.st.Denied, len(h.asked), len(h.errs))
	}
}

func TestConnectForbiddenStatusReportsPermissionAndKeepsNoPassword(t *testing.T) {
	h := newHarness(t)
	h.addFavorite(t)
	h.dev.SetStatusErr(fmt.Errorf("enumerate power state: %w", fmt.Errorf("%w: 403 Forbidden\n<html/>", errors.New("wsman.Client post received"))))
	h.c.start(favRequest())
	err := h.waitError(t)
	if !errors.Is(err, amt.ErrAccessDenied) || errors.Is(err, session.ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	h.waitDone(t)
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "" {
		t.Fatal("password saved although the login is not proven")
	}
	if len(h.asked) != 0 {
		t.Fatal("asked for a password again")
	}
}

func TestProberLimitedAccountIsReadWithGrayRing(t *testing.T) {
	h := newProbeHarness(t, 1, time.Hour, nil)
	h.dev.St = deniedStatus()
	f := favorites.Favorite{Host: "device.example.com", Username: "kvm", UseTLS: true}
	key := deviceKey(f.Host, f.Username)
	h.keys.Set(f.Host, f.Username, "secret-1")
	h.keys.SetFingerprint(f.Host, f.Username, fp)
	h.p.SetFavorites([]favorites.Favorite{f})
	h.p.Start()
	r := h.next(t)[key]
	if !r.known() || r.dot != widgets.DotUnknown || r.st.Allowed(amt.StatusPower) {
		t.Fatalf("%+v", r)
	}
	h.p.Wake()
	h.next(t)
	if n := h.dev.Calls(); n != 2 {
		t.Fatalf("%d status calls, want 2: a limited account is checked again", n)
	}
}

func TestReadProbeDeniedPowerIsGrayEvenWithAState(t *testing.T) {
	r := readProbe(amt.Status{PowerState: models.PowerStateOn, Denied: amt.StatusPower})
	if r.dot != widgets.DotUnknown || !r.known() {
		t.Fatalf("%+v", r)
	}
}

func TestDetailStatusRowLimitedAccount(t *testing.T) {
	h := newStart(t)
	s := h.s
	fav := favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "kvm", UseTLS: true}
	addFavorites(t, h, fav)
	k := deviceKey(fav.Host, fav.Username)
	test.Tap(s.rows[k])
	check := func(label, text string) {
		t.Helper()
		if s.statusDot.State() != widgets.DotUnknown || s.statusText.Text != text {
			t.Errorf("%s: dot %d text %q, want gray ring and %q", label, s.statusDot.State(), s.statusText.Text, text)
		}
	}
	s.applyProbe(k, readProbe(deniedStatus()))
	check("limited account", "Not allowed for this account")
	if hasText(s.meta, "AMT") || hasText(s.meta, "ACM") || !hasText(s.meta, "TLS") {
		t.Error("meta shows denied values")
	}
	s.applyProbe(k, probeResult{dot: widgets.DotUnknown})
	check("neutral result", "Unknown")
	s.windowStatus(k, deniedStatus(), true)
	check("open device window", "Not allowed for this account")
	s.windowStatus(k, amt.Status{}, false)
	check("device window, not read", "Unknown")
	// An admin result is unchanged.
	s.applyProbe(k, readProbe(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}))
	if s.statusDot.State() != widgets.DotOn || s.statusText.Text != "Powered on" || !hasText(s.meta, "AMT 16.1.25") || !hasText(s.meta, "ACM") {
		t.Errorf("admin: dot %d text %q", s.statusDot.State(), s.statusText.Text)
	}
}

func TestDeviceWindowLimitedAccountTexts(t *testing.T) {
	englishUI(t)
	dev := &session.Fake{FP: fp, St: deniedStatus()}
	h := newDevice(t, test.NewTempApp(t), dev, true)
	d := h.d
	if d.power.Text != "Power: not allowed" || d.version.Text != "AMT: not allowed" || d.mode.Text != "Control mode: not allowed" {
		t.Fatalf("%q %q %q", d.power.Text, d.version.Text, d.mode.Text)
	}
	if !d.version.Visible() || !d.mode.Visible() {
		t.Fatal("denied texts hidden")
	}
	if d.powerDot.State() != widgets.DotUnknown || d.headDot.State() != widgets.DotUnknown {
		t.Fatalf("dots %v %v", d.powerDot.State(), d.headDot.State())
	}
	if d.warning.Visible() || d.messageBanner.Visible() || d.powerBtn.Disabled() {
		t.Fatal("banner shown or Power button disabled")
	}
	if !d.powerAllowed(amt.PowerOn) || !d.kvmToggleEnabled() || d.emptyBtn.Disabled() || !d.emptyBtnBox.Visible() {
		t.Fatal("actions are limited for a limited account")
	}
	// A following full status looks like the admin case.
	d.apply(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}, nil)
	if d.power.Text != "Powered on" || d.version.Text != "AMT 16.1.25" || d.mode.Text != "Control mode ACM" || d.powerDot.State() != widgets.DotOn {
		t.Fatalf("%q %q %q", d.power.Text, d.version.Text, d.mode.Text)
	}
	// And back: a failed read is "Unknown" with the banner, not "not allowed".
	d.apply(deniedStatus(), nil)
	d.apply(amt.Status{}, errors.New("unreachable"))
	if d.power.Text != "Unknown" || !d.messageBanner.Visible() {
		t.Fatalf("%q banner=%v", d.power.Text, d.messageBanner.Visible())
	}
}

func TestDeviceWindowPartlyDenied(t *testing.T) {
	englishUI(t)
	st := amt.Status{PowerState: models.PowerStateOn, ControlMode: amt.ControlModeAdmin, Denied: amt.StatusVersion}
	d := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: st}, true).d
	if d.power.Text != "Powered on" || d.version.Text != "AMT: not allowed" || d.mode.Text != "Control mode ACM" {
		t.Fatalf("%q %q %q", d.power.Text, d.version.Text, d.mode.Text)
	}
}

// translations returns the texts of a shipped language file.
func translations(t *testing.T, lang string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("i18n", "translations", lang+".json"))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The three "not allowed" texts must not widen the window. The status bar's left group does
// not shrink, but the toolbar decides the minimum width: measured, the window is 803 px wide for
// an admin account and 806 px for any gray-ring (unknown) state, because of the ring's size,
// whatever the status bar says. So the reference is a window in the unknown state without the
// texts, in English and in German (the German texts are longer). If the texts ever decide the
// width, only the power text is shown (spec, section 6).
func TestDeviceWindowLimitedAccountIsNotWider(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	ref := newDevice(t, a, &session.Fake{FP: fp, St: amt.Status{}}, true).d
	limited := newDevice(t, a, &session.Fake{FP: fp, St: deniedStatus()}, true).d
	if ref.version.Visible() || !limited.version.Visible() {
		t.Fatal("the reference must show no version and the limited window must")
	}
	if rw, lw := ref.win.Content().MinSize().Width, limited.win.Content().MinSize().Width; lw > rw {
		t.Errorf("English: limited %v wider than the unknown state %v", lw, rw)
	}
	de := translations(t, "de")
	for _, key := range []string{"Power: not allowed", "AMT: not allowed", "Control mode: not allowed"} {
		if de[key] == "" || de[key] == key {
			t.Fatalf("de.json lacks a translation of %q", key)
		}
	}
	limited.power.SetText(de["Power: not allowed"])
	limited.version.SetText(de["AMT: not allowed"])
	limited.mode.SetText(de["Control mode: not allowed"])
	limited.relayoutStatus()
	if rw, lw := ref.win.Content().MinSize().Width, limited.win.Content().MinSize().Width; lw > rw {
		t.Errorf("German: limited %v wider than the unknown state %v", lw, rw)
	}
}

func TestManageHeadLimitedAccount(t *testing.T) {
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	h.m.OpenWindow()
	h.wait(t) // Hardware
	w := h.m.win
	// Denied power with a leftover state would show a dot if the head ignored Denied.
	h.st = amt.Status{PowerState: models.PowerStateOn, Denied: allDenied}
	h.m.tick()
	if w.dot.State() != widgets.DotUnknown || w.sub.Text != "192.0.2.10" {
		t.Fatalf("dot %v head %q", w.dot.State(), w.sub.Text)
	}
	h.st = amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	h.m.tick()
	if w.dot.State() != widgets.DotOn || w.sub.Text != "192.0.2.10 · AMT 16.1 · ACM" {
		t.Fatalf("admin: dot %v head %q", w.dot.State(), w.sub.Text)
	}
}

// A raw AccessDenied fault from the device (amt.Power passes it through) shows the permission
// sentence, not "Unexpected error".
func TestPowerRawAccessDeniedFaultShowsPermissionMessage(t *testing.T) {
	englishUI(t)
	raw := fmt.Errorf("power on: %w", amterror.NewAMTError("e:AccessDenied", "The caller is not authorized.", ""))
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, PowerErr: raw}, true)
	h.show(t)
	h.tapPower(t, amt.PowerOn)
	recv(t, h.actionDone)
	recv(t, h.applied)
	err := <-h.errs
	if got := describe(err); got != "This account is not allowed to do this on the device." {
		t.Fatalf("describe = %q (err %v)", got, err)
	}
}

// A denied power state ends the wait of the automatic KVM connect at the first status read: the
// KVM account is the main case of limited accounts.
func TestAutoConnectWithDeniedPowerState(t *testing.T) {
	h, _ := autoDevice(t, &sessionFake{St: deniedStatus()}, true, nil)
	h.d.apply(deniedStatus(), nil)
	wantConnected(t, h)
	h2, _ := autoDevice(t, &sessionFake{St: deniedStatus()}, false, nil)
	h2.d.apply(deniedStatus(), nil)
	wantIdle(t, h2)
}

// A failed read is no first successful read, also after a denied one.
func TestAutoConnectDeniedWaitsForSuccessfulRead(t *testing.T) {
	h, _ := autoDeviceErr(t, &sessionFake{St: deniedStatus()}, true, nil, errors.New("unreachable"))
	wantIdle(t, h)
	h.d.apply(deniedStatus(), nil)
	wantConnected(t, h)
}
