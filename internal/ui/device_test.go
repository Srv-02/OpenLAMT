package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

type deviceHarness struct {
	d          *DeviceWindow
	dev        *session.Fake
	applied    chan struct{}
	actionDone chan struct{}
	errs       chan error
	closed     chan struct{}
	wasClosed  bool
	live       bool // poller started (show): opening the Power menu triggers a read
}

// show starts the poller and waits for its first read. The test driver runs fyne.Do on the
// calling goroutine, so tests touch the window only between such signals.
func (h *deviceHarness) show(t *testing.T) {
	t.Helper()
	h.d.Show()
	h.live = true
	recv(t, h.applied)
}

func newDevice(t *testing.T, a fyne.App, dev *session.Fake, tls bool) *deviceHarness {
	t.Helper()
	a.Settings().SetTheme(style.Theme{}) // design sizes and colors, as in production
	cfg := amt.Config{Host: "device.example.com", Username: "admin", UseTLS: tls, Fingerprint: dev.FP}
	s, st, err := session.Open(context.Background(), cfg, nil, session.ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	h := &deviceHarness{dev: dev, applied: make(chan struct{}, 64), actionDone: make(chan struct{}, 8), errs: make(chan error, 8), closed: make(chan struct{}, 1)}
	h.d = NewDeviceWindow(a, s, "Büro", st, NewSettings(a.Preferences()), func() { h.wasClosed = true; h.closed <- struct{}{} })
	h.d.onApplied = func() { h.applied <- struct{}{} }
	h.d.onActionDone = func() { h.actionDone <- struct{}{} }
	h.d.onError = func(err error) { h.errs <- err }
	t.Cleanup(func() {
		if !h.wasClosed { // the test driver panics when a window is closed twice
			h.d.win.Close()
		}
		h.d.poller.Stop()
	})
	return h
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func recv(t *testing.T, ch chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHeaderShowsStatusAndTLSWarning(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	dev := &session.Fake{St: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}, FP: fp}
	d := newDevice(t, a, dev, true).d
	if d.win.Title() != "Büro" || d.title.Text != "Büro" || d.host.Text != "device.example.com" || d.power.Text != "Powered on" ||
		d.version.Text != "AMT 16.1.25" || d.mode.Text != "Control mode ACM" {
		t.Fatalf("%q %q %q %q %q", d.title.Text, d.host.Text, d.power.Text, d.version.Text, d.mode.Text)
	}
	if d.powerDot.State() != widgets.DotOn || d.headDot.State() != widgets.DotOn || d.powerBtn.Disabled() {
		t.Fatalf("power dot %v %v", d.powerDot.State(), d.headDot.State())
	}
	if d.warning.Visible() || d.messageBanner.Visible() || d.tls.Text != "TLS" {
		t.Fatalf("banner shown with TLS: tls=%q", d.tls.Text)
	}
	if !d.kvmToggleEnabled() || d.kvmOn() || d.kvmQuality != kvm.QualityHigh || d.quality.Disabled() || d.kvmLine.Text != "KVM not connected" {
		t.Fatalf("KVM controls: disabled=%v active=%v quality=%v line=%q", !d.kvmToggleEnabled(), d.kvmOn(), d.kvmQuality, d.kvmLine.Text)
	}
	if !d.fullBtn.Disabled() || !d.cadBtn.Disabled() || !d.clipBtn.Disabled() || d.view.Client() != nil {
		t.Fatal("KVM actions must be disabled without a session")
	}
	if !d.emptyState.Visible() || !strings.HasPrefix(d.emptyText.String(), "Büro is powered on.") || d.emptyBtn.Text != "Connect KVM" {
		t.Fatalf("empty state %q %q", d.emptyText.String(), d.emptyBtn.Text)
	}
	if d.poller.Interval() != 10*time.Second {
		t.Fatalf("interval %v", d.poller.Interval())
	}
	d2 := newDevice(t, a, dev, false).d
	if !d2.warning.Visible() || d2.tls.Text != "No TLS" {
		t.Fatalf("warning hidden without TLS: tls=%q", d2.tls.Text)
	}
}

// tapPower opens the Power menu with the toolbar button and taps the item of act.
func (h *deviceHarness) tapPower(t *testing.T, act amt.PowerAction) {
	t.Helper()
	test.Tap(h.d.powerBtn)
	if h.d.powerPopup == nil {
		t.Fatal("Power menu not open")
	}
	if h.live {
		recv(t, h.applied) // the read the menu requested
	}
	tapMenuItem(t, h.d.powerPopup, h.d.powerMenu, h.d.powerItems[act])
	if h.d.powerPopup != nil {
		t.Fatal("Power menu still open after the tap")
	}
}

// tapMenuItem taps the shown entry of item in the open popup p of menu m.
func tapMenuItem(t *testing.T, p *widgets.MenuPopup, m *fyne.Menu, item *fyne.MenuItem) {
	t.Helper()
	for i, it := range m.Items {
		if it == item {
			test.Tap(p.Item(i))
			return
		}
	}
	t.Fatalf("item %q not in the menu", item.Label)
}

func TestPowerStateText(t *testing.T) {
	englishUI(t)
	cases := map[models.PowerState]string{models.PowerStateOn: "Powered on", models.PowerStateOffHard: "Powered off", models.PowerStateOffSoft: "Powered off",
		models.PowerStateSleepDeep: "Sleeping", models.PowerStateHibernate: "Hibernating"}
	for ps, want := range cases {
		if got := powerStateText(ps); got != want {
			t.Errorf("%d: %q", ps, got)
		}
	}
	if got := powerStateText(models.PowerState(99)); !strings.Contains(got, "99") {
		t.Errorf("unknown: %q", got)
	}
}

func TestPowerOnRunsWithoutConfirm(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	h.d.confirm = func(amt.PowerAction, func()) { t.Fatal("confirm called for power on") }
	h.show(t)
	h.tapPower(t, amt.PowerOn)
	recv(t, h.actionDone)
	recv(t, h.applied) // the read after the action
	if got := h.dev.Powered(); len(got) != 1 || got[0] != amt.PowerOn {
		t.Fatalf("powered %v", got)
	}
}

// Reboot to BIOS on a device that is off only powers it on, like Power On: no question
// (issue #120). On a running device it resets and still asks.
func TestRebootToBIOSAsksOnlyWhenOn(t *testing.T) {
	englishUI(t)
	for _, st := range []models.PowerState{models.PowerStateOffSoft, models.PowerStateOffHard} {
		h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: amt.Status{PowerState: st}}, true)
		h.d.confirm = func(amt.PowerAction, func()) { t.Fatalf("%v: confirm for Reboot to BIOS on a device that is off", st) }
		h.show(t)
		h.tapPower(t, amt.BootToBIOS)
		recv(t, h.actionDone)
		if got := h.dev.Powered(); len(got) != 1 || got[0] != amt.BootToBIOS {
			t.Fatalf("%v: powered %v", st, got)
		}
	}
	for _, st := range []models.PowerState{models.PowerStateOn, models.PowerStateSleepDeep, models.PowerStateHibernate} {
		h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: amt.Status{PowerState: st}}, true)
		var asked []amt.PowerAction
		h.d.confirm = func(act amt.PowerAction, _ func()) { asked = append(asked, act) }
		h.show(t)
		h.tapPower(t, amt.BootToBIOS)
		if len(asked) != 1 || asked[0] != amt.BootToBIOS || len(h.dev.Powered()) != 0 {
			t.Fatalf("%v: asked %v powered %v", st, asked, h.dev.Powered())
		}
	}
}

func TestDestructiveActionsAskFirst(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	var asked []amt.PowerAction
	h.d.confirm = func(act amt.PowerAction, _ func()) { asked = append(asked, act) }
	h.d.canShutdown = true // as if the device listed Off - Soft Graceful (12)
	for _, act := range []amt.PowerAction{amt.PowerOff, amt.PowerReset, amt.PowerCycle, amt.BootToBIOS, amt.PowerShutdown} {
		h.tapPower(t, act)
	}
	if len(asked) != 5 || len(h.dev.Powered()) != 0 {
		t.Fatalf("asked %v powered %v", asked, h.dev.Powered())
	}
}

func TestButtonsDisabledDuringActionAndStatusReread(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, PowerGate: make(chan struct{})}, true)
	h.d.confirm = func(_ amt.PowerAction, proceed func()) { proceed() }
	h.show(t)
	test.Tap(h.d.powerBtn)
	recv(t, h.applied) // opening the menu reads the state once more
	before := h.dev.Calls()
	tapMenuItem(t, h.d.powerPopup, h.d.powerMenu, h.d.powerItems[amt.PowerReset])
	if !h.d.powerBtn.Disabled() || !h.d.powerItems[amt.PowerOn].Disabled || !h.d.powerItems[amt.BootToBIOS].Disabled {
		t.Fatal("Power menu enabled during action")
	}
	test.Tap(h.d.powerBtn)
	if h.d.powerPopup != nil {
		t.Fatal("Power menu opened during action")
	}
	waitFor(t, h.dev.PowerStarted)
	if h.dev.Calls() != before {
		t.Fatal("status read during action")
	}
	close(h.dev.PowerGate)
	recv(t, h.actionDone)
	recv(t, h.applied)
	if h.d.powerBtn.Disabled() || h.d.powerItems[amt.PowerOn].Disabled || h.dev.Calls() != before+1 {
		t.Fatalf("disabled=%v calls=%d want %d", h.d.powerBtn.Disabled(), h.dev.Calls(), before+1)
	}
}

func TestPowerErrorReported(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, PowerErr: errors.New("boom")}, true)
	h.show(t)
	h.tapPower(t, amt.PowerOn)
	recv(t, h.actionDone)
	recv(t, h.applied) // the read after the action
	if err := <-h.errs; err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
	if h.d.powerBtn.Disabled() || h.d.powerItems[amt.PowerOn].Disabled {
		t.Fatal("Power menu stays disabled after error")
	}
}

func TestStatusErrorShownInHeader(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn}}, true)
	h.dev.SetStatusErr(fmt.Errorf("%w: 401 Unauthorized\n<html>body</html>", errors.New("wsman.Client post received")))
	h.show(t)
	if h.d.power.Text != "Unknown" || !strings.Contains(h.d.message.Text, "Authentication failed") || strings.Contains(h.d.message.Text, "body") {
		t.Fatalf("%q %q", h.d.power.Text, h.d.message.Text)
	}
	if !h.d.messageBanner.Visible() || h.d.powerDot.State() != widgets.DotUnknown || h.d.headDot.State() != widgets.DotUnknown {
		t.Fatal("failed read: banner hidden or dot not unknown")
	}
	for _, act := range powerOrder {
		if act == amt.PowerShutdown {
			continue // needs the device's power states, which are read only after a good status
		}
		if h.d.powerBtn.Disabled() || h.d.powerItems[act].Disabled {
			t.Fatalf("a failed read must not lock the Power menu (%s)", act)
		}
	}
	h.dev.SetStatusErr(nil)
	h.d.poller.Refresh()
	recv(t, h.applied)
	if h.d.messageBanner.Visible() || h.d.power.Text != "Powered on" {
		t.Fatalf("banner stays after a good read: %q", h.d.power.Text)
	}
}

func TestMismatchDuringSessionAbortsWithWarning(t *testing.T) {
	englishUI(t)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	h.show(t)
	h.dev.SetStatusErr(mm)
	h.d.poller.Refresh()
	recv(t, h.applied)
	if err := <-h.errs; !errors.Is(err, mm) {
		t.Fatalf("err = %v", err)
	}
	if !h.d.aborted || !h.d.powerBtn.Disabled() || !h.d.powerItems[amt.PowerOn].Disabled || !strings.Contains(h.d.message.Text, "device.example.com") {
		t.Fatalf("aborted=%v disabled=%v message=%q", h.d.aborted, h.d.powerBtn.Disabled(), h.d.message.Text)
	}
	h.dev.SetStatusErr(nil)
	h.d.apply(amt.Status{PowerState: models.PowerStateOn}, nil) // successful read after the abort
	if h.d.power.Text != "Unknown" || !strings.Contains(h.d.message.Text, "device.example.com") {
		t.Fatalf("warning overwritten: %q %q", h.d.power.Text, h.d.message.Text)
	}
	waitStopped(t, h.d) // abort stops on a goroutine; wait for it (synchronous ErrStopped is the proof)
	h.d.poller.Stop()   // idempotent, waits for the loop to end; waitStopped itself enqueues jobs
	h.d.poller.Refresh()
	calls := h.dev.Calls()
	powered := len(h.dev.Powered())
	test.Tap(h.d.powerBtn) // disabled: no menu opens
	if h.d.powerPopup != nil {
		t.Fatal("Power menu opened after abort")
	}
	h.d.run(amt.PowerOn) // a stale menu entry: refused as well
	time.Sleep(20 * time.Millisecond)
	if h.dev.Calls() != calls || len(h.dev.Powered()) != powered || len(h.errs) != 0 {
		t.Fatal("session still active after mismatch")
	}
}

func TestMismatchReleasesRegistryOnce(t *testing.T) {
	englishUI(t)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	h.d.apply(amt.Status{}, mm)
	recv(t, h.closed) // abort told the start window
	<-h.errs
	h.d.poller.Stop()
	h.d.win.Close() // user closes the dead window afterwards: no second notification
	if len(h.closed) != 0 {
		t.Fatal("onClosed called twice")
	}
}

func TestStoppedActionIsSilent(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	h.d.poller.Stop() // window closed or session aborted before the action ran
	h.d.execute(amt.PowerOn)
	recv(t, h.actionDone)
	if len(h.errs) != 0 {
		t.Fatalf("ErrStopped reported: %v", <-h.errs)
	}
}

func TestMismatchOnPowerActionAborts(t *testing.T) {
	englishUI(t)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, PowerErr: mm}, true)
	h.show(t)
	h.tapPower(t, amt.PowerOn)
	recv(t, h.actionDone)
	recv(t, h.applied) // the read after the action (ignored: aborted)
	if err := <-h.errs; !errors.Is(err, mm) {
		t.Fatalf("err = %v", err)
	}
	if !h.d.aborted || !h.d.powerBtn.Disabled() || !h.d.powerItems[amt.PowerOff].Disabled {
		t.Fatal("Power menu re-enabled after mismatch")
	}
	h.d.apply(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "1.0"}, nil) // a read racing the stop
	if h.d.power.Text != "Unknown" || !strings.Contains(h.d.message.Text, "device.example.com") {
		t.Fatalf("warning overwritten: %q %q", h.d.power.Text, h.d.message.Text)
	}
}

func TestCloseStopsPollerAndNotifies(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	h.show(t)
	h.d.win.Close()
	recv(t, h.closed)
	waitStopped(t, h.d)
	h.d.poller.Stop() // idempotent, waits for the loop to end before the snapshot
	h.d.poller.Refresh()
	calls := h.dev.Calls()
	time.Sleep(20 * time.Millisecond)
	if h.dev.Calls() != calls {
		t.Fatal("poller still running after close")
	}
}

// waitStopped waits until the poller refuses new actions (ErrStopped, synchronously).
func waitStopped(t *testing.T, d *DeviceWindow) {
	t.Helper()
	waitFor(t, func() bool {
		got := make(chan error, 1)
		d.poller.Power(amt.PowerOn, func(err error) { got <- err })
		select {
		case err := <-got:
			return errors.Is(err, session.ErrStopped)
		default:
			return false
		}
	})
}

func TestMismatchReportedOnce(t *testing.T) {
	englishUI(t)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	h.d.apply(amt.Status{}, mm)
	h.d.apply(amt.Status{}, mm)
	if len(h.errs) != 1 {
		t.Fatalf("%d error reports", len(h.errs))
	}
}

func TestErrorAfterCloseDuringActionIsSilent(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp, PowerGate: make(chan struct{}), PowerErr: errors.New("boom")}, true)
	h.show(t)
	h.tapPower(t, amt.PowerOn)
	waitFor(t, h.dev.PowerStarted)
	h.d.win.Close()
	close(h.dev.PowerGate)
	recv(t, h.actionDone)
	if len(h.errs) != 0 {
		t.Fatalf("error dialog on a closed window: %v", <-h.errs)
	}
}

func TestIntervalValidatorRange(t *testing.T) {
	for in, ok := range map[string]bool{"0": false, "1": true, "3600": true, "3601": false, "9999": false, "": false, "abc": false, "-5": false, "10": true} {
		if err := validateInterval(in); (err == nil) != ok {
			t.Errorf("%q: err=%v", in, err)
		}
	}
}

func TestClosingWindowClosesIdleConnections(t *testing.T) {
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, false)
	before := h.dev.IdleClosed() // Open of a healthy session closes nothing
	h.d.win.Close()
	waitFor(t, func() bool { return h.dev.IdleClosed() == before+1 })
}

func TestAbortClosesIdleConnections(t *testing.T) {
	englishUI(t)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	h.d.apply(amt.Status{}, mm)
	<-h.errs
	waitFor(t, func() bool { return h.dev.IdleClosed() == 1 })
}
