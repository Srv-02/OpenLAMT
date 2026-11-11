package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
)

// manageDevice is a device window whose device management applies its read results on the test
// goroutine (wait) and runs without its clock (tests call tick).
type manageDevice struct {
	*deviceHarness
	loaded chan func()
}

func newManageDevice(t *testing.T, dev *session.Fake) *manageDevice {
	t.Helper()
	englishUI(t)
	dev.FP = fp
	h := &manageDevice{deviceHarness: newDevice(t, test.NewTempApp(t), dev, true), loaded: make(chan func(), 64)}
	h.d.onManager = func(m *manager) {
		m.tickEvery = 0
		m.later = func(f func()) { h.loaded <- f }
	}
	return h
}

// wait applies the next read result of device management.
func (h *manageDevice) wait(t *testing.T) {
	t.Helper()
	select {
	case f := <-h.loaded:
		f()
	case <-time.After(2 * time.Second):
		t.Fatal("no read result")
	}
}

// TestManageOpensSidebarWithHardware: the first Manage creates the device management and shows
// its sidebar, which reads the hardware; the manager gets host and the window's last status.
func TestManageOpensSidebarWithHardware(t *testing.T) {
	on := amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}
	h := newManageDevice(t, &session.Fake{St: on, HW: exampleHW()})
	if h.d.manager != nil {
		t.Fatal("device management created before Manage")
	}
	test.Tap(h.d.manageBtn)
	m := h.d.manager
	if m == nil || !h.d.manageOpen() || !h.d.manageBtn.Active() {
		t.Fatal("Manage did not open the device management")
	}
	if len(h.d.manageSlot.Objects) != 1 || h.d.manageSlot.Objects[0] != m.Sidebar() {
		t.Fatal("the sidebar is not in the slot")
	}
	h.wait(t)
	if !m.hw.loaded() || m.hw.reads != 1 || m.side.view.current != 0 {
		t.Fatalf("hardware: loaded %v reads %d page %d", m.hw.loaded(), m.hw.reads, m.side.view.current)
	}
	if m.deps.host != "device.example.com" || m.name != "Büro" || m.parent != h.d.win {
		t.Fatalf("host %q name %q", m.deps.host, m.name)
	}
	if st, known := m.status(); !known || st.AMTVersion != "16.1.25" || m.controlMode() != amt.ControlModeAdmin {
		t.Fatalf("status %+v known %v", st, known)
	}
	if h.d.manageEdge.Visible() {
		t.Fatal("the sidebar draws its own left line; the window's would double it")
	}

	// A failed read makes the status unknown, a good one known again.
	h.d.apply(amt.Status{}, errors.New("read failed"))
	if _, known := m.status(); known {
		t.Fatal("status known after a failed read")
	}
	h.d.apply(amt.Status{PowerState: models.PowerStateOffSoft, AMTVersion: "16.1.27", ControlMode: amt.ControlModeClient}, nil)
	if st, known := m.status(); !known || st.AMTVersion != "16.1.27" || st.PowerState != models.PowerStateOffSoft {
		t.Fatalf("status %+v known %v", st, known)
	}

	// Errors go to the window.
	m.deps.onError(errors.New("boom"))
	if err := <-h.errs; err.Error() != "boom" {
		t.Fatalf("error %v", err)
	}

	test.Tap(h.d.manageBtn)
	test.Tap(h.d.manageBtn)
	if h.d.manager != m || m.hw.reads != 1 || !h.d.manageOpen() {
		t.Fatal("reopening the sidebar must keep the device management")
	}
}

// TestManagePopOut: the pop-out symbol closes the sidebar and opens the window; Manage still
// opens and closes the sidebar while the window is open, and focuses nothing else.
func TestManagePopOut(t *testing.T) {
	h := newManageDevice(t, &session.Fake{HW: exampleHW()})
	test.Tap(h.d.manageBtn)
	m := h.d.manager
	h.wait(t)
	test.Tap(m.side.popOut)
	if h.d.manageOpen() || h.d.manageBtn.Active() {
		t.Fatal("the sidebar stayed open after pop-out")
	}
	if m.win == nil || m.win.view.current != 0 {
		t.Fatal("no device management window")
	}
	// The head of the window follows the device window's status.
	if got := m.win.sub.Text; got != "device.example.com" {
		t.Fatalf("head %q", got)
	}
	h.d.apply(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}, nil)
	m.tick()
	if got := m.win.sub.Text; got != "device.example.com · AMT 16.1 · ACM" {
		t.Fatalf("head after status %q", got)
	}
	win := m.win
	test.Tap(h.d.manageBtn)
	if !h.d.manageOpen() || m.win != win {
		t.Fatal("Manage must open the sidebar beside the open window")
	}
	test.Tap(h.d.manageBtn)
	if h.d.manageOpen() || m.win != win {
		t.Fatal("Manage must close the sidebar and leave the window")
	}
}

// TestCloseDeviceWindowClosesManagement: closing the device window closes the device
// management window and ends device management.
func TestCloseDeviceWindowClosesManagement(t *testing.T) {
	h := newManageDevice(t, &session.Fake{HW: exampleHW()})
	test.Tap(h.d.manageBtn)
	m := h.d.manager
	h.wait(t)
	test.Tap(m.side.popOut)
	if m.win == nil {
		t.Fatal("no device management window")
	}
	h.d.win.Close()
	if !m.closed || m.win != nil {
		t.Fatalf("closed %v window %v", m.closed, m.win != nil)
	}
}

// TestMismatchClosesManagement: a certificate mismatch ends device management with the session.
func TestMismatchClosesManagement(t *testing.T) {
	h := newManageDevice(t, &session.Fake{HW: exampleHW()})
	test.Tap(h.d.manageBtn)
	m := h.d.manager
	h.wait(t)
	test.Tap(m.side.popOut)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h.d.apply(amt.Status{}, mm)
	<-h.errs
	if !m.closed || m.win != nil {
		t.Fatalf("closed %v window %v", m.closed, m.win != nil)
	}
	if _, known := m.status(); known {
		t.Fatal("status known after the abort")
	}
}

// TestAbortLocksClosedManage: after a certificate mismatch, Manage does not open device
// management on the dead session; an open sidebar stays closable and then Manage is locked too.
func TestAbortLocksClosedManage(t *testing.T) {
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}

	h := newManageDevice(t, &session.Fake{HW: exampleHW()})
	h.d.apply(amt.Status{}, mm)
	<-h.errs
	if !h.d.manageBtn.Disabled() {
		t.Fatal("Manage enabled after the abort")
	}
	test.Tap(h.d.manageBtn)
	if h.d.manager != nil || h.d.manageOpen() {
		t.Fatal("Manage opened device management on the aborted session")
	}

	h2 := newManageDevice(t, &session.Fake{HW: exampleHW()})
	test.Tap(h2.d.manageBtn)
	h2.wait(t)
	h2.d.apply(amt.Status{}, mm)
	<-h2.errs
	if h2.d.manageBtn.Disabled() || !h2.d.manageOpen() {
		t.Fatalf("open sidebar: Manage disabled %v, open %v", h2.d.manageBtn.Disabled(), h2.d.manageOpen())
	}
	test.Tap(h2.d.manageBtn)
	if h2.d.manageOpen() || !h2.d.manageBtn.Disabled() {
		t.Fatalf("after closing: open %v, Manage disabled %v", h2.d.manageOpen(), h2.d.manageBtn.Disabled())
	}
	test.Tap(h2.d.manageBtn)
	if h2.d.manageOpen() {
		t.Fatal("the closed sidebar opened again after the abort")
	}
}

// TestDeviceWindowGetsLoginStore: the start window hands its favorites manager to the device
// window, which passes it to device management.
func TestDeviceWindowGetsLoginStore(t *testing.T) {
	h := newStart(t)
	sess, st, err := session.Open(context.Background(), amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true, Fingerprint: fp},
		nil, session.ConnectFake(h.dev))
	if err != nil {
		t.Fatal(err)
	}
	d := h.s.addDevice(sess, st, "Büro")
	d.onManager = func(m *manager) { m.tickEvery = 0; m.later = func(func()) {} }
	if mgr, ok := d.loginStore.(*favorites.Manager); !ok || mgr != h.s.mgr {
		t.Fatalf("login store %T", d.loginStore)
	}
	d.setManageOpen(true)
	if d.manager == nil || d.manager.deps.loginStore != d.loginStore {
		t.Fatal("device management without the login store")
	}

	// Without favorites (tests) nothing is stored: a nil interface, not a typed nil.
	h2 := newManageDevice(t, &session.Fake{})
	test.Tap(h2.d.manageBtn)
	if h2.d.manager.deps.loginStore != nil {
		t.Fatalf("login store %T", h2.d.manager.deps.loginStore)
	}
}
