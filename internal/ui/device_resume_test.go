package ui

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// resumeWait shortens kvmResumeWait for one test.
func resumeWait(t *testing.T, wait time.Duration) {
	t.Helper()
	old := kvmResumeWait
	kvmResumeWait = wait
	t.Cleanup(func() { kvmResumeWait = old })
}

var (
	statusOn  = amt.Status{PowerState: models.PowerStateOn}
	statusOff = amt.Status{PowerState: models.PowerStateOffSoft}
)

// readNow drops earlier read signals, asks for a status read and waits for it.
func (h *kvmHarness) readNow(t *testing.T) {
	t.Helper()
	for len(h.applied) > 0 {
		<-h.applied
	}
	h.d.poller.Refresh()
	recv(t, h.applied)
}

// noResume waits past the resume wait, reads the status once more and checks that KVM did not
// connect again: calls OpenKVM calls in total, no session.
func (h *kvmHarness) noResume(t *testing.T, calls int) {
	t.Helper()
	time.Sleep(3 * kvmResumeWait)
	h.readNow(t)
	if q := h.dev.Qualities(); len(q) != calls || h.d.kvmClient != nil || h.d.kvmBusy {
		t.Fatalf("OpenKVM calls %d, want %d; client %v busy %v", len(q), calls, h.d.kvmClient, h.d.kvmBusy)
	}
}

// Power Cycle closes KVM first; once the device reads as on after the wait, KVM connects again
// with the last quality, without wake key and regardless of the auto-connect setting (off here).
func TestPowerCycleResumesKVM(t *testing.T) {
	resumeWait(t, 60*time.Millisecond)
	dev := &sessionFake{St: statusOn}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	test.Tap(h.d.quality) // switch to Low while connected: the session reconnects with it
	tapMenuItem(t, h.d.qualityPopup, h.d.qualityMenu, h.d.qualityItems[kvm.QualityLow])
	recv(t, h.changed) // reconnect started
	recv(t, h.changed) // reconnected
	c := h.d.kvmClient
	if c == nil || c.Quality() != kvm.QualityLow {
		t.Fatalf("precondition: low quality session, got %v", c)
	}
	var woke atomic.Int32
	h.d.wake = func(*kvm.Client, time.Duration) { woke.Add(1) }
	start := time.Now()
	h.d.execute(amt.PowerCycle)
	recv(t, h.actionDone)
	recv(t, h.changed) // closed for the action
	recv(t, h.changed) // connected again
	if h.d.kvmClient == nil || h.d.kvmClient == c {
		t.Fatalf("no new session: status %q", h.d.kvmStatus.Text)
	}
	if time.Since(start) < kvmResumeWait {
		t.Fatal("reconnected before the wait was over")
	}
	if q := dev.Qualities(); len(q) != 3 || q[2] != kvm.QualityLow {
		t.Fatalf("qualities %v", q)
	}
	if woke.Load() != 0 || len(h.errs) != 0 || h.d.resumeKVM || h.d.idleTimer == nil && h.d.settings.KVMIdleTimeout() > 0 {
		t.Fatalf("woke %d errs %d resume %v", woke.Load(), len(h.errs), h.d.resumeKVM)
	}
	h.disconnect(t)
}

// After Power Off KVM stays closed while the device is off and comes back after "Power On".
func TestPowerOffResumesKVMAfterPowerOn(t *testing.T) {
	resumeWait(t, 30*time.Millisecond)
	dev := &sessionFake{St: statusOn}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	dev.SetStatus(statusOff)
	h.d.execute(amt.PowerOff)
	recv(t, h.actionDone)
	recv(t, h.changed)
	h.noResume(t, 1)
	dev.SetStatus(statusOn)
	h.d.execute(amt.PowerOn)
	recv(t, h.actionDone)
	recv(t, h.changed)
	if h.d.kvmClient == nil || len(dev.Qualities()) != 2 || len(h.errs) != 0 {
		t.Fatalf("not resumed after power on: client %v calls %d", h.d.kvmClient, len(dev.Qualities()))
	}
	h.disconnect(t)
}

// Shutdown: while the operating system still shuts down the device reads as on; KVM connects
// again only once it was off and is on again.
func TestShutdownResumesKVMOnlyAfterOff(t *testing.T) {
	resumeWait(t, 30*time.Millisecond)
	dev := &sessionFake{St: statusOn}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	h.d.execute(amt.PowerShutdown)
	recv(t, h.actionDone)
	recv(t, h.changed)
	h.noResume(t, 1) // still on
	dev.SetStatus(statusOff)
	h.readNow(t)
	dev.SetStatus(statusOn) // switched on at the device
	h.readNow(t)
	recv(t, h.changed)
	if h.d.kvmClient == nil || len(dev.Qualities()) != 2 {
		t.Fatalf("not resumed: client %v calls %d", h.d.kvmClient, len(dev.Qualities()))
	}
	h.disconnect(t)
}

// Without a KVM session before the action nothing connects afterwards.
func TestPowerCycleWithoutKVMDoesNotConnect(t *testing.T) {
	resumeWait(t, 30*time.Millisecond)
	dev := &sessionFake{St: statusOn}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.d.execute(amt.PowerCycle)
	recv(t, h.actionDone)
	h.noResume(t, 0)
}

// A manual disconnect before the action, or a manual connect and disconnect while the resume is
// pending, means the user decided: no resume.
func TestManualKVMChoiceEndsResume(t *testing.T) {
	resumeWait(t, 30*time.Millisecond)
	dev := &sessionFake{St: statusOn}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	h.disconnect(t)
	h.d.execute(amt.PowerCycle)
	recv(t, h.actionDone)
	h.noResume(t, 1)

	h.connect(t, false)
	dev.SetStatus(statusOff)
	h.d.execute(amt.PowerOff)
	recv(t, h.actionDone)
	recv(t, h.changed)
	h.connect(t, false) // the user connects while the device is off (the fake accepts) ...
	h.disconnect(t)     // ... and disconnects again
	dev.SetStatus(statusOn)
	h.d.execute(amt.PowerOn)
	recv(t, h.actionDone)
	h.noResume(t, 3)
}

// A refused power action does not cost the session: KVM connects again at once (the wait is an
// hour here) when the device is still on, without a second error.
func TestFailedPowerActionResumesKVMAtOnce(t *testing.T) {
	resumeWait(t, time.Hour)
	dev := &sessionFake{St: statusOn, PowerErr: errors.New("refused")}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	h.d.execute(amt.PowerOff)
	recv(t, h.actionDone)
	recv(t, h.changed) // closed
	recv(t, h.changed) // connected again
	if h.d.kvmClient == nil || len(dev.Qualities()) != 2 {
		t.Fatalf("not resumed: client %v calls %d", h.d.kvmClient, len(dev.Qualities()))
	}
	if err := <-h.errs; err == nil || len(h.errs) != 0 {
		t.Fatalf("errors: %v and %d more", err, len(h.errs))
	}
	h.disconnect(t)
}

// A failed resume is tried once more, without error dialog; then the window gives up.
func TestResumeRetriesOnce(t *testing.T) {
	resumeWait(t, 30*time.Millisecond)
	dev := &sessionFake{St: statusOn}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	dev.KVMErr = &amt.KVMError{Step: "KVMRedirectionSAP.RequestStateChange", ReturnValue: 2}
	h.d.execute(amt.PowerCycle)
	recv(t, h.actionDone)
	recv(t, h.changed) // closed
	recv(t, h.changed) // first attempt failed
	recv(t, h.changed) // second attempt failed
	h.noResume(t, 3)
	if len(h.errs) != 0 || h.d.kvmStatus.Text == "" || h.d.kvmStatus.Text == "KVM session closed." {
		t.Fatalf("errs %d status %q", len(h.errs), h.d.kvmStatus.Text)
	}
}

// Closing the window drops a pending resume.
func TestCloseWindowEndsResume(t *testing.T) {
	resumeWait(t, 30*time.Millisecond)
	dev := &sessionFake{St: statusOn}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	dev.SetStatus(statusOff)
	h.d.execute(amt.PowerOff)
	recv(t, h.actionDone)
	recv(t, h.changed)
	if !h.d.resumeKVM {
		t.Fatal("precondition: resume pending")
	}
	h.d.win.Close()
	recv(t, h.closed)
	if h.d.resumeKVM {
		t.Fatal("resume pending after close")
	}
	waitStopped(t, h.d)
	if len(dev.Qualities()) != 1 {
		t.Fatalf("OpenKVM calls %d", len(dev.Qualities()))
	}
}

// Without the right to read the power state (powerDenied) a refused action must not cost the
// session either: KVM connects again at once (the wait is an hour here).
func TestDeniedPowerStateRefusedActionResumesKVM(t *testing.T) {
	resumeWait(t, time.Hour)
	dev := &sessionFake{St: amt.Status{Denied: amt.StatusPower}, PowerErr: errors.New("refused")}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	if !h.d.powerDenied {
		t.Fatal("precondition: power state denied")
	}
	h.d.execute(amt.PowerOff)
	recv(t, h.actionDone)
	recv(t, h.changed) // closed
	recv(t, h.changed) // connected again
	if h.d.kvmClient == nil || len(dev.Qualities()) != 2 {
		t.Fatalf("not resumed: client %v calls %d", h.d.kvmClient, len(dev.Qualities()))
	}
	h.disconnect(t)
}

// After a successful action KVM connects again once the wait is over, even after Power Off:
// the device cannot be read as off, so nothing else can be waited for.
func TestDeniedPowerStateResumesKVMAfterWait(t *testing.T) {
	resumeWait(t, 60*time.Millisecond)
	dev := &sessionFake{St: amt.Status{Denied: amt.StatusPower}}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	h.connect(t, true)
	start := time.Now()
	h.d.execute(amt.PowerOff)
	recv(t, h.actionDone)
	recv(t, h.changed) // closed
	recv(t, h.changed) // connected again
	if h.d.kvmClient == nil || len(dev.Qualities()) != 2 || len(h.errs) != 0 {
		t.Fatalf("not resumed: client %v calls %d", h.d.kvmClient, len(dev.Qualities()))
	}
	if time.Since(start) < kvmResumeWait {
		t.Fatal("reconnected before the wait was over")
	}
	h.disconnect(t)
}
