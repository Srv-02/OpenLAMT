package ui

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

func TestAuditDetailFields(t *testing.T) {
	englishUI(t)
	e := amt.AuditLogEntry{Time: time.Date(2026, 11, 11, 22, 3, 46, 0, time.UTC), AppID: 18, EventID: 0, App: "KVM Events",
		Event: "KVM Session Started", InitiatorType: 0, Initiator: "admin", Address: "192.0.2.200", Details: "a long text"}
	d := auditDetail(e, time.FixedZone("CET", 3600))
	want := [][2]string{
		{"Time", "Nov 11, 2026 23:03:46 (UTC+01:00)"},
		{"Application", "KVM Events"},
		{"Initiator", "admin (digest user)"},
		{"Address", "192.0.2.200"},
	}
	if d.title != "KVM Session Started" || d.details != "a long text" || d.raw[1] != "Application 18 · event 0" {
		t.Fatalf("detail %+v", d)
	}
	for i, f := range want {
		if d.fields[i] != f {
			t.Errorf("field %d %q, want %q", i, d.fields[i], f)
		}
	}
	if txt := d.text(); !strings.Contains(txt, "Initiator: admin (digest user)\n") || !strings.Contains(txt, "Details: a long text\n") {
		t.Errorf("clipboard text %q", txt)
	}
}

func TestEventDetailRawData(t *testing.T) {
	englishUI(t)
	d := eventDetail(amt.EventLogEntry{Severity: amt.SeverityWarning, Source: "Watchdog", Description: "Agent heartbeat timeout",
		SensorType: 0x23, EventOffset: 1, EventData: [8]byte{0x40, 0xff, 0xff}}, time.UTC)
	if d.title != "Agent heartbeat timeout" || d.raw[1] != "Sensor type 0x23 · offset 0x01 · data 40 FF FF 00 00 00 00 00" {
		t.Fatalf("detail %+v", d)
	}
	if d.fields[0][1] != "" { // no time: the field is left out of the dialog
		t.Errorf("time %q", d.fields[0][1])
	}
}

// A click on a row opens its details; Up and Down step through the filtered rows, Copy puts
// the entry on the clipboard and Esc closes the dialog.
func TestLogRowOpensDetails(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW(), Events: exampleEvents(), Audit: exampleAudit()})
	h.m.parent.SetContent(h.m.Sidebar())
	h.m.parent.Resize(fyne.NewSize(manageSidebarWidth, 800))
	h.wait(t) // hardware
	h.m.side.tabs.Tap(1)
	h.wait(t)
	p := h.m.side.view.panes[1].impl.(*logPane)
	p.list.Select(1)
	d := p.detail
	if d == nil || !open(h.m.parent) || d.pos != 1 {
		t.Fatal("no detail dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Agent heartbeat timeout")
	findLabel(t, top, "Watchdog")
	if !hasText(top, "Event log · entry 2 of 4") {
		t.Error("subtitle missing")
	}
	h.m.parent.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyDown})
	if d.pos != 2 || !open(h.m.parent) {
		t.Fatalf("Down: pos %d", d.pos)
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Power supply AC lost")
	h.m.parent.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyUp})
	h.m.parent.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyUp})
	if d.pos != 0 || !d.newer.Disabled() || d.older.Disabled() {
		t.Fatalf("Up twice: pos %d newer disabled %v", d.pos, d.newer.Disabled())
	}
	h.m.parent.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyUp}) // already the newest
	if d.pos != 0 {
		t.Fatalf("Up past the newest: pos %d", d.pos)
	}
	test.Tap(d.copyB)
	if got := h.a.Clipboard().Content(); !strings.HasPrefix(got, "Starting operating system boot process\n") || !strings.Contains(got, "Source: BIOS\n") {
		t.Errorf("clipboard %q", got)
	}
	h.m.parent.Canvas().OnTypedKey()(esc())
	if open(h.m.parent) {
		t.Fatal("Esc did not close")
	}
	if p.list.Length() != 4 {
		t.Fatalf("rows %d", p.list.Length())
	}
}
