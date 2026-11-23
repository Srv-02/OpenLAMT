package ui

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// exampleAuditSettings: recording, overwrite oldest, 34 entries; the policy records Remote
// Control, two Wi-Fi events (AMT's default) and the audit log events.
func exampleAuditSettings() amt.AuditSettings {
	return amt.AuditSettings{Recording: true, Storage: amt.AuditOverwriteOldest, Records: 34, PercentUsed: 9,
		Policy: []amt.AuditEvent{{App: 17, Event: 0}, {App: 17, Event: 4}, {App: 20, Event: 0}, {App: 27, Event: 3}, {App: 27, Event: 4}}}
}

// logHarness opens the sidebar on a log page (1 Event log, 2 Audit log) and returns its pane.
func logHarness(t *testing.T, dev *session.Fake, useTLS bool, page int) (*manageHarness, *logPane) {
	t.Helper()
	englishUI(t)
	h := newManageHarnessTLS(t, dev, useTLS)
	h.m.Sidebar()
	h.wait(t) // hardware
	h.m.side.tabs.Tap(page)
	h.wait(t) // the log
	return h, h.m.side.view.panes[page].impl.(*logPane)
}

// openAuditDialog taps "Settings…" and waits for the read.
func openAuditDialog(t *testing.T, s amt.AuditSettings) (*manageHarness, *auditSettingsDialog) {
	t.Helper()
	dev := &session.Fake{FP: fp, Audit: exampleAudit()}
	dev.AuditState().Settings = s
	h, p := logHarness(t, dev, true, 2)
	if p.head == nil || p.head.Text != "Settings…" {
		t.Fatal("no Settings… in the Audit log head")
	}
	test.Tap(p.head)
	if !p.head.Disabled() {
		t.Error("Settings… stays enabled while reading")
	}
	h.wait(t)
	if p.head.Disabled() {
		t.Error("Settings… disabled after the read")
	}
	d := h.m.logDlg.audit
	if d == nil || !open(h.m.parent) {
		t.Fatal("no dialog")
	}
	return h, d
}

func (d *auditSettingsDialog) checkStates() []bool {
	out := make([]bool, len(d.checks))
	for i, c := range d.checks {
		out[i] = c.Checked
	}
	return out
}

// Issue #15 (V3): the dialog shows the device's values; the design's Pause control is not built.
func TestAuditSettingsDialogShowsTheDeviceValues(t *testing.T) {
	h, d := openAuditDialog(t, exampleAuditSettings())
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Audit log settings")
	if !d.record.Checked() || d.storage.Selected != "Overwrite oldest entries" || d.daysRow.visibleRow() {
		t.Errorf("record %v, storage %q, days row %v", d.record.Checked(), d.storage.Selected, d.daysRow.visibleRow())
	}
	if !reflect.DeepEqual(d.storage.Options, []string{"Overwrite oldest entries", "Overwrite after a minimum age", "Stop recording"}) {
		t.Errorf("options %q", d.storage.Options)
	}
	for _, s := range []string{"Record events", "When the log is full", "Space used", "34 entries · 9 %", "Recorded events", "Clear the log", "Deletes all 34 entries."} {
		if !hasText(top, s) {
			t.Errorf("text %q missing", s)
		}
	}
	for _, s := range []string{"Pause the log", "Pause…"} {
		if hasText(top, s) {
			t.Errorf("%q is built", s)
		}
	}
	var labels []string
	for _, c := range d.checks {
		labels = append(labels, c.Text)
	}
	want := []string{"Accounts and security", "Power and boot (remote control)", "KVM, media and console sessions", "Firmware updates",
		"Network settings", "Clock", "Event forwarding", "System Defense", "Agent watchdog", "Wi-Fi", "User consent", "Audit log changes"}
	if !slices.Equal(labels, want) {
		t.Errorf("groups %q", labels)
	}
	if got := d.checkStates(); !slices.Equal(got, []bool{false, true, false, false, false, false, false, false, false, true, false, true}) {
		t.Errorf("states %v", got)
	}
	if !d.action.Disabled() || d.action.Text != "Save" || d.clear.Text != "Clear Log…" {
		t.Errorf("Save enabled %v, %q, %q", !d.action.Disabled(), d.action.Text, d.clear.Text)
	}
}

// Save writes only the changed groups: a group switched on gets every known event of its
// applications, one switched off loses the events the device records; storage and days follow.
func TestAuditSettingsSaveWritesTheChange(t *testing.T) {
	h, d := openAuditDialog(t, exampleAuditSettings())
	reads := h.m.audit.reads
	test.Tap(d.checks[1]) // Power off
	test.Tap(d.checks[2]) // KVM on
	d.storage.SetSelectedIndex(1)
	if !d.daysRow.visibleRow() || !d.action.Disabled() {
		t.Fatal("days row hidden, or Save enabled without days")
	}
	for _, bad := range []string{"0", "101", "x"} {
		d.days.SetText(bad)
		if !d.action.Disabled() {
			t.Errorf("Save enabled with %q days", bad)
		}
	}
	d.days.SetText("14")
	if d.action.Disabled() {
		t.Fatal("Save disabled")
	}
	test.Tap(d.action)
	h.written(t)
	changes := h.dev.AuditState().Changes
	if len(changes) != 1 {
		t.Fatalf("%d writes", len(changes))
	}
	c := changes[0]
	var enable []amt.AuditEvent
	for _, app := range []int{18, 29, 32} {
		enable = append(enable, amt.KnownAuditEvents(app)...)
	}
	if !reflect.DeepEqual(c.Enable, enable) || !reflect.DeepEqual(c.Disable, []amt.AuditEvent{{App: 17, Event: 0}, {App: 17, Event: 4}}) {
		t.Errorf("enable %v, disable %v", c.Enable, c.Disable)
	}
	if c.Recording != nil || c.Storage == nil || *c.Storage != amt.AuditOverwriteAfterDays || *c.MinDaysToKeep != 14 {
		t.Errorf("change %+v", c)
	}
	if h.m.audit.reads != reads+1 || open(h.m.parent) {
		t.Errorf("audit log not read again (%d), or a dialog left open", h.m.audit.reads-reads)
	}
}

// Switching the recording off is orange (OpAuditSettings): nothing is written before the
// checkbox; Cancel writes nothing.
func TestAuditRecordingOffAsksOrange(t *testing.T) {
	h, d := openAuditDialog(t, exampleAuditSettings())
	test.Tap(d.record)
	test.Tap(d.action)
	c := d.confirm
	if c == nil || len(h.dev.AuditState().Changes) != 0 {
		t.Fatal("no confirmation, or a write before it")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Stop recording on Lab?")
	if !hasRichText(t, top, "Changes to Lab are no longer recorded.", false) || c.check.Text != "I understand the risk for Lab (device.example.com)" {
		t.Errorf("warning or checkbox %q", c.check.Text)
	}
	if !c.action.Disabled() || c.warnBox == nil {
		t.Error("button enabled before the checkbox")
	}
	test.Tap(c.check)
	test.Tap(c.action)
	h.written(t)
	got := h.dev.AuditState().Changes
	if len(got) != 1 || got[0].Recording == nil || *got[0].Recording || h.dev.AuditState().Settings.Recording {
		t.Fatalf("changes %+v", got)
	}

	h, d = openAuditDialog(t, exampleAuditSettings())
	test.Tap(d.record)
	test.Tap(d.action)
	d.confirm.Dismiss()
	if open(h.m.parent) || len(h.dev.AuditState().Changes) != 0 {
		t.Fatal("cancel wrote or left a dialog")
	}
}

// Clear Log… is red (OpAuditClear): bold first sentence, the button waits for the checkbox,
// then both dialogs close and the log is cleared and read again.
func TestAuditClearAsksRed(t *testing.T) {
	h, d := openAuditDialog(t, exampleAuditSettings())
	test.Tap(d.clear)
	c := d.confirm
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Clear the audit log of Lab?")
	if !hasRichText(t, top, "This cannot be undone. ", true) ||
		!hasRichText(t, top, "All 34 entries are deleted. The log keeps one new entry that says it was cleared.", false) {
		t.Error("red warning texts")
	}
	if c.action.Text != "Clear Log" || !c.action.Disabled() || h.dev.AuditState().Clears != 0 {
		t.Fatal("Clear Log enabled before the checkbox, or cleared")
	}
	reads := h.m.audit.reads
	test.Tap(c.check)
	test.Tap(c.action)
	if open(h.m.parent) {
		t.Fatal("a dialog stays open")
	}
	h.written(t)
	if h.dev.AuditState().Clears != 1 || h.m.audit.reads != reads+1 || len(h.m.audit.val) != 0 {
		t.Fatalf("clears %d, reads %d, rows %d", h.dev.AuditState().Clears, h.m.audit.reads-reads, len(h.m.audit.val))
	}
}

// Without the audit permission the read shows "No permission for audit settings", not an
// empty dialog; a refused write shows it too. Other errors show the error dialog.
func TestAuditSettingsPermissionAndErrors(t *testing.T) {
	dev := &session.Fake{FP: fp, Audit: exampleAudit()}
	dev.AuditState().ReadErr = amt.ErrAccessDenied
	h, p := logHarness(t, dev, true, 2)
	test.Tap(p.head)
	h.wait(t)
	if h.m.logDlg.audit != nil || h.m.logDlg.denied == nil {
		t.Fatal("no permission dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "No permission for audit settings")
	findLabel(t, top, "The device does not let admin read the audit log settings. Sign in with an account that has the Audit log permission.")

	other := &session.Fake{FP: fp, Audit: exampleAudit()}
	other.AuditState().ReadErr = errors.New("boom")
	h, p = logHarness(t, other, true, 2)
	test.Tap(p.head)
	h.wait(t)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Something went wrong")
	if h.m.logDlg.audit != nil || h.m.logDlg.denied != nil {
		t.Error("another error opened a settings or permission dialog")
	}

	h, d := openAuditDialog(t, exampleAuditSettings())
	h.dev.AuditState().WriteErr = amt.ErrAccessDenied
	test.Tap(d.checks[0])
	test.Tap(d.action)
	h.written(t)
	if h.m.logDlg.denied == nil {
		t.Fatal("a refused write shows no permission dialog")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "The account admin may read the audit log but not change its settings. Sign in with an account that has the Audit log permission.")
}

// The Audit log head of the window has "Settings…" before Refresh.
func TestAuditSettingsButtonInTheWindow(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, Audit: exampleAudit()})
	h.m.openWindowAt(2)
	h.wait(t)
	p := h.m.win.view.panes[2].impl.(*logPane)
	if p.head == nil || p.head.Text != "Settings…" {
		t.Fatal("no Settings… in the window head")
	}
	ev := newManageHarness(t, &session.Fake{FP: fp})
	ev.m.openWindowAt(1)
	ev.wait(t)
	if q := ev.m.win.view.panes[1].impl.(*logPane); q.head == nil || q.head.Text != "Forwarding…" {
		t.Fatal("no Forwarding… in the Event log head")
	}
}
