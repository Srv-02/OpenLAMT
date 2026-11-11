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
	loaded chan func() // results of reads, applied by wait
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
	h := &manageHarness{a: a, dev: dev, loaded: make(chan func(), 64), known: true,
		st: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}}
	parent := a.NewWindow("Lab")
	h.m = newManager(a, parent, s, "Lab", manageDeps{host: "192.0.2.10", status: func() (amt.Status, bool) { return h.st, h.known }})
	h.m.tickEvery = 0 // tests call tick themselves
	h.m.later = func(f func()) { h.loaded <- f }
	t.Cleanup(h.m.Close)
	return h
}

// wait applies the next read result on the test goroutine.
func (h *manageHarness) wait(t *testing.T) {
	t.Helper()
	select {
	case f := <-h.loaded:
		f()
	case <-time.After(2 * time.Second):
		t.Fatal("no read result")
	}
}

func exampleEvents() []amt.EventLogEntry {
	at := func(d, hh, mm, ss int) time.Time { return time.Date(2026, 11, d, hh, mm, ss, 0, time.UTC) }
	return []amt.EventLogEntry{
		{Time: at(11, 16, 41, 52), Severity: amt.SeverityInfo, Source: "BIOS", Description: "Starting operating system boot process"},
		{Time: at(10, 3, 12, 8), Severity: amt.SeverityWarning, Source: "Watchdog", Description: "Agent heartbeat timeout"},
		{Time: at(9, 22, 47, 15), Severity: amt.SeverityCritical, Source: "Power", Description: "Power supply AC lost"},
		{Time: at(5, 14, 1, 57), Severity: amt.SeverityInfo, Source: "BIOS", Description: "Starting operating system boot process"},
	}
}

func exampleAudit() []amt.AuditLogEntry {
	return []amt.AuditLogEntry{
		{Time: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC), InitiatorType: 0, Initiator: "admin", Address: "192.0.2.20", Event: "KVM Session Started"},
		{Time: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), InitiatorType: 2, Event: "AMT Provisioning Started", Details: "example detail"},
	}
}

func TestManageSidebarReadsEachPageOnce(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW(), Events: exampleEvents(), Audit: exampleAudit()})
	h.m.Sidebar()
	h.wait(t)
	side := h.m.side
	if side.view.current != 0 || h.m.hw.reads != 1 || h.m.events.reads != 0 || h.m.audit.reads != 0 {
		t.Fatalf("start: page %d reads hw=%d ev=%d au=%d", side.view.current, h.m.hw.reads, h.m.events.reads, h.m.audit.reads)
	}
	side.tabs.Tap(1)
	h.wait(t)
	side.tabs.Tap(2)
	h.wait(t)
	side.tabs.Tap(0)
	side.tabs.Tap(1)
	side.tabs.Tap(3)
	side.tabs.Tap(4)
	if h.m.hw.reads != 1 || h.m.events.reads != 1 || h.m.audit.reads != 1 {
		t.Fatalf("reads hw=%d ev=%d au=%d", h.m.hw.reads, h.m.events.reads, h.m.audit.reads)
	}
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
	h := newManageHarness(t, &session.Fake{FP: fp, HWErr: fmt.Errorf("hardware info: CIM_Chassis: %w", amt.ErrAccessDenied),
		AuditErr: session.ErrAuth})
	h.m.Sidebar()
	h.wait(t)
	hp := h.m.side.view.panes[0].impl.(*hwPane)
	if !hp.err.box.Visible() || hp.err.Text() != "This account is not allowed to read this information on the device." || len(hp.cards.Objects) != 0 {
		t.Fatalf("hardware error %v %q", hp.err.box.Visible(), hp.err.Text())
	}
	h.m.side.tabs.Tap(2)
	h.wait(t)
	lp := h.m.side.view.panes[2].impl.(*logPane)
	if !lp.err.box.Visible() || lp.err.Text() != describe(session.ErrAuth) || lp.count.Text != "" {
		t.Fatalf("audit error %v %q count %q", lp.err.box.Visible(), lp.err.Text(), lp.count.Text)
	}
	// a later good read clears the error
	h.dev.AuditErr = nil
	h.dev.Audit = exampleAudit()
	test.Tap(lp.refresh)
	h.wait(t)
	if lp.err.box.Visible() || lp.count.Text != "2 entries" || len(lp.shown) != 2 {
		t.Fatalf("after good read: err %v count %q shown %d", lp.err.box.Visible(), lp.count.Text, len(lp.shown))
	}
}

func TestManageEventLogFilters(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, Events: exampleEvents()})
	h.m.OpenWindow()
	h.wait(t) // Hardware
	w := h.m.win
	test.Tap(w.nav[1])
	h.wait(t)
	lp := w.view.panes[1].impl.(*logPane)
	if w.title.Text != "Event log" || lp.count.Text != "· 4 entries" || len(lp.shown) != 4 {
		t.Fatalf("title %q count %q shown %d", w.title.Text, lp.count.Text, len(lp.shown))
	}
	if len(w.heading.Objects) != 2 || w.heading.Objects[1] != lp.count || len(w.tools.Objects) != 1 {
		t.Fatal("count and tools belong into the page head")
	}
	if lp.note.Text != "Newest first. Read in 0.0 s." {
		t.Fatalf("note %q", lp.note.Text)
	}
	test.Tap(lp.chips[1]) // Warning
	if len(lp.shown) != 1 || lp.rows[lp.shown[0]].cells[2] != "Watchdog" || !lp.chips[1].Active() || lp.chips[0].Active() {
		t.Fatalf("warning: %v", lp.shown)
	}
	test.Tap(lp.chips[2]) // Critical
	if len(lp.shown) != 1 || lp.rows[lp.shown[0]].cells[1] != "Critical" {
		t.Fatalf("critical: %v", lp.shown)
	}
	test.Tap(lp.chips[0]) // All
	test.Type(lp.filter.Entry, "BOOT")
	if len(lp.shown) != 2 || lp.count.Text != "· 4 entries" {
		t.Fatalf("text filter: %v count %q", lp.shown, lp.count.Text)
	}
	test.Tap(lp.chips[2])
	if len(lp.shown) != 0 {
		t.Fatalf("critical and boot: %v", lp.shown)
	}
	lp.filter.SetText("")
	if len(lp.shown) != 1 {
		t.Fatalf("critical only: %v", lp.shown)
	}
	// the list shows the filtered rows
	line := lp.list.CreateItem().(*logLine)
	lp.list.UpdateItem(0, line)
	if line.Text(3) != "Power supply AC lost" {
		t.Fatalf("row 0 %q", line.Text(3))
	}
}

func TestManageAuditLogRowsAndFilter(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, Audit: exampleAudit()})
	h.m.Sidebar()
	h.wait(t)
	h.m.side.tabs.Tap(2)
	h.wait(t)
	lp := h.m.side.view.panes[2].impl.(*logPane)
	if len(lp.chips) != 0 || lp.count.Text != "2 entries" {
		t.Fatalf("chips %d count %q", len(lp.chips), lp.count.Text)
	}
	if got := lp.rows[1].cells; got[1] != "Local" || got[2] != "" || got[3] != "AMT Provisioning Started · example detail" {
		t.Fatalf("local row %q", got)
	}
	test.Type(lp.filter.Entry, "192.0.2.20")
	if len(lp.shown) != 1 || lp.rows[lp.shown[0]].cells[1] != "admin" {
		t.Fatalf("address filter %v", lp.shown)
	}
}

func TestManageCountSingular(t *testing.T) {
	englishUI(t)
	if entriesText(1) != "1 entry" || entriesText(0) != "0 entries" || entriesText(209) != "209 entries" {
		t.Fatal(entriesText(1), entriesText(0), entriesText(209))
	}
}

func TestManageLargeAuditLog(t *testing.T) {
	englishUI(t)
	audit := make([]amt.AuditLogEntry, 8000)
	for i := range audit {
		audit[i] = amt.AuditLogEntry{Time: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC), Initiator: fmt.Sprintf("user%d", i), Event: "Example event"}
	}
	h := newManageHarness(t, &session.Fake{FP: fp, Audit: audit})
	h.m.OpenWindow()
	h.wait(t) // Hardware
	test.Tap(h.m.win.nav[2])
	h.wait(t)
	lp := h.m.win.view.panes[2].impl.(*logPane)
	h.m.win.win.Resize(fyne.NewSize(1120, 720))
	start := time.Now()
	test.Type(lp.filter.Entry, "user79")
	if len(lp.shown) != 111 { // user79, user790..799, user7900..7999
		t.Fatalf("shown %d", len(lp.shown))
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("filtering 8000 rows took %v", d)
	}
}

func TestManagePopOutOpensOneWindow(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW(), Events: exampleEvents()})
	popped := 0
	h.m.SetOnPopOut(func() { popped++ })
	h.m.Sidebar()
	h.wait(t)
	h.m.side.tabs.Tap(1)
	h.wait(t)
	before := len(h.a.Driver().AllWindows())
	test.Tap(h.m.side.popOut)
	if popped != 1 || h.m.win == nil || len(h.a.Driver().AllWindows()) != before+1 {
		t.Fatalf("popped %d window %v", popped, h.m.win != nil)
	}
	w := h.m.win
	if w.win.Title() != "Lab" || w.view.current != 1 || !w.nav[1].Active() || w.nav[0].Active() {
		t.Fatalf("title %q page %d", w.win.Title(), w.view.current)
	}
	if h.m.events.reads != 1 {
		t.Fatalf("the window reuses the data: reads %d", h.m.events.reads)
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
	h.wait(t) // the late result arrives and is dropped
	if h.m.hw.loaded() {
		t.Fatal("late result stored")
	}
	h.m.OpenWindow()
	h.m.hw.reload()
	if h.m.win != nil || h.m.hw.reads != 1 {
		t.Fatal("closed manager opened a window or read again")
	}
}

func TestManageRefreshWhileReadingIsIgnored(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, Events: exampleEvents()})
	gate := make(chan struct{})
	h.m.events.read = func() ([]logRow, error) {
		<-gate
		return eventRows(exampleEvents(), time.UTC), nil
	}
	h.m.OpenWindow()
	h.wait(t) // Hardware
	test.Tap(h.m.win.nav[1])
	lp := h.m.win.view.panes[1].impl.(*logPane)
	if !lp.refresh.Disabled() || lp.note.Text != "Reading…" {
		t.Fatalf("while reading: disabled %v note %q", lp.refresh.Disabled(), lp.note.Text)
	}
	h.m.events.reload()
	if h.m.events.reads != 1 {
		t.Fatalf("reads %d", h.m.events.reads)
	}
	close(gate)
	h.wait(t)
	if lp.refresh.Disabled() || len(lp.shown) != 4 {
		t.Fatal("after the read")
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
