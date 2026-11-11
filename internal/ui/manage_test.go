package ui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// manageHarness is a manager on a session.Fake with made-up data.
type manageHarness struct {
	a      fyne.App
	m      *manager
	dev    *session.Fake
	loaded chan struct{}
	st     amt.Status
	known  bool
}

func newManageHarness(t *testing.T, dev *session.Fake) *manageHarness {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(style.Theme{})
	cfg := amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true, Fingerprint: dev.FP}
	s, _, err := session.Open(context.Background(), cfg, nil, session.ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	h := &manageHarness{a: a, dev: dev, loaded: make(chan struct{}, 64), known: true,
		st: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}}
	parent := a.NewWindow("Lab")
	h.m = newManager(a, parent, s, "Lab", manageDeps{host: "192.0.2.10", status: func() (amt.Status, bool) { return h.st, h.known }})
	h.m.tickEvery = 0 // tests call tick themselves
	h.m.onLoaded = func() { h.loaded <- struct{}{} }
	t.Cleanup(h.m.Close)
	return h
}

func (h *manageHarness) wait(t *testing.T) {
	t.Helper()
	recv(t, h.loaded)
}

func TestManageSidebarReadsEachPageOnce(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	h.m.Sidebar()
	h.wait(t)
	side := h.m.side
	if side.view.current != 0 || h.m.hw.reads != 1 {
		t.Fatalf("start: page %d reads %d", side.view.current, h.m.hw.reads)
	}
	for _, i := range []int{1, 2, 0, 3, 4, 0} {
		side.tabs.Tap(i)
	}
	if h.m.hw.reads != 1 {
		t.Fatalf("reads %d", h.m.hw.reads)
	}
	side.tabs.Tap(4)
	if !side.view.slots[4].Visible() || side.view.slots[0].Visible() || side.view.slots[1].Visible() {
		t.Fatal("only the selected page is visible")
	}
	if got := h.m.Sidebar().MinSize().Width; got != manageSidebarWidth {
		t.Fatalf("sidebar width %v", got)
	}
}

func TestManageHardwarePageShowsCardsAndRefreshes(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	h.m.now = func() time.Time { return start }
	h.m.Sidebar()
	h.wait(t)
	hp := h.m.side.view.panes[0].impl.(*hwPane)
	if len(hp.cards.Objects) != 4 || hp.ago.Text != "Read 0 s ago" || hp.err.box.Visible() {
		t.Fatalf("cards %d ago %q", len(hp.cards.Objects), hp.ago.Text)
	}
	h.m.now = func() time.Time { return start.Add(12 * time.Second) }
	h.m.tick()
	if hp.ago.Text != "Read 12 s ago" {
		t.Fatalf("ago %q", hp.ago.Text)
	}
	h.dev.HW.Drives = nil
	test.Tap(hp.refresh)
	h.wait(t)
	if h.m.hw.reads != 2 || len(hp.cards.Objects) != 3 || hp.ago.Text != "Read 0 s ago" {
		t.Fatalf("after refresh: reads %d cards %d ago %q", h.m.hw.reads, len(hp.cards.Objects), hp.ago.Text)
	}
}

func TestManagePageShowsErrors(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HWErr: fmt.Errorf("hardware info: CIM_Chassis: %w", amt.ErrAccessDenied)})
	h.m.Sidebar()
	h.wait(t)
	hp := h.m.side.view.panes[0].impl.(*hwPane)
	if !hp.err.box.Visible() || hp.err.Text() != "This account is not allowed to read this information on the device." || len(hp.cards.Objects) != 0 {
		t.Fatalf("hardware error %v %q", hp.err.box.Visible(), hp.err.Text())
	}
	h.dev.HWErr = session.ErrAuth
	test.Tap(hp.refresh)
	h.wait(t)
	if hp.err.Text() != describe(session.ErrAuth) || hp.ago.Text != "" {
		t.Fatalf("auth error %q ago %q", hp.err.Text(), hp.ago.Text)
	}
	// a later good read clears the error
	h.dev.HWErr = nil
	h.dev.HW = exampleHW()
	test.Tap(hp.refresh)
	h.wait(t)
	if hp.err.box.Visible() || len(hp.cards.Objects) != 4 {
		t.Fatalf("after good read: err %v cards %d", hp.err.box.Visible(), len(hp.cards.Objects))
	}
}

func TestManagePopOutOpensOneWindow(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	popped := 0
	h.m.SetOnPopOut(func() { popped++ })
	h.m.Sidebar()
	h.wait(t)
	h.m.side.tabs.Tap(1)
	before := len(h.a.Driver().AllWindows())
	test.Tap(h.m.side.popOut)
	if popped != 1 || h.m.win == nil || len(h.a.Driver().AllWindows()) != before+1 {
		t.Fatalf("popped %d window %v", popped, h.m.win != nil)
	}
	w := h.m.win
	if w.win.Title() != "Lab" || w.view.current != 1 || !w.nav[1].Active() || w.nav[0].Active() {
		t.Fatalf("title %q page %d", w.win.Title(), w.view.current)
	}
	test.Tap(w.nav[0])
	if h.m.hw.reads != 1 || w.title.Text != "Hardware" {
		t.Fatalf("the window reuses the data: reads %d, title %q", h.m.hw.reads, w.title.Text)
	}
	if w.sub.Text != "192.0.2.10 · AMT 16.1 · ACM" {
		t.Fatalf("head %q", w.sub.Text)
	}
	h.m.OpenWindow()
	if h.m.win != w || len(h.a.Driver().AllWindows()) != before+1 {
		t.Fatal("OpenWindow opened a second window")
	}
	// the head follows the device window's status
	h.st.ControlMode = amt.ControlModeClient
	h.m.tick()
	if w.sub.Text != "192.0.2.10 · AMT 16.1 · CCM" {
		t.Fatalf("head %q", w.sub.Text)
	}
	h.known = false
	h.m.tick()
	if w.sub.Text != "192.0.2.10" {
		t.Fatalf("head without status %q", w.sub.Text)
	}
	// closing the window by hand forgets it; the next OpenWindow builds a new one
	w.win.Close()
	if h.m.win != nil {
		t.Fatal("closed window kept")
	}
	h.m.OpenWindow()
	if h.m.win == nil || h.m.win == w {
		t.Fatal("no new window")
	}
}

func TestManageBothViewsFollowNewData(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	h.m.Sidebar()
	h.wait(t)
	h.m.OpenWindow()
	side := h.m.side.view.panes[0].impl.(*hwPane)
	win := h.m.win.view.panes[0].impl.(*hwPane)
	if len(side.cards.Objects) != 4 || len(win.cards.Objects) != 4 || side.cards == win.cards {
		t.Fatal("each view has its own cards on the same data")
	}
	h.dev.HW = amt.HardwareInfo{System: amt.SystemInfo{Manufacturer: "Example Corp"}}
	test.Tap(win.refresh)
	h.wait(t)
	if len(side.cards.Objects) != 2 || len(win.cards.Objects) != 2 { // System and AMT (mode)
		t.Fatalf("side %d window %d", len(side.cards.Objects), len(win.cards.Objects))
	}
}

func TestManageCloseDropsLateResultsAndClosesWindow(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	gate := make(chan struct{})
	reading := make(chan struct{}, 1)
	h.m.hw.read = func() (amt.HardwareInfo, error) {
		reading <- struct{}{}
		<-gate
		return exampleHW(), nil
	}
	h.m.Sidebar()
	h.m.OpenWindow()
	recv(t, reading)
	before := len(h.a.Driver().AllWindows())
	h.m.Close()
	if h.m.win != nil || len(h.a.Driver().AllWindows()) != before-1 {
		t.Fatal("Close keeps the window open")
	}
	close(gate)
	select {
	case <-h.loaded:
		t.Fatal("late result applied")
	case <-time.After(50 * time.Millisecond):
	}
	if h.m.hw.loaded() {
		t.Fatal("late result stored")
	}
	h.m.OpenWindow()
	h.m.hw.reload()
	if h.m.win != nil || h.m.hw.reads != 1 {
		t.Fatal("closed manager opened a window or read again")
	}
}

func TestDescribeManage(t *testing.T) {
	englishUI(t)
	if got := describeManage(fmt.Errorf("read audit log: %w", amt.ErrAccessDenied)); got != "This account is not allowed to read this information on the device." {
		t.Fatal(got)
	}
	if got, want := describeManage(errors.New("boom")), describe(errors.New("boom")); got != want {
		t.Fatal(got)
	}
}
