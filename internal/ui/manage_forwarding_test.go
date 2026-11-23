package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

func exampleForwarding() amt.Forwarding {
	return amt.Forwarding{
		Filters: []amt.EventFilter{{ID: "Intel(r) AMT:User", Name: "User"}, {ID: "Intel(r) AMT:All", Name: "All"}, {ID: "Intel(r) AMT:Platform", Name: "Platform"}},
		Targets: []amt.ForwardingTarget{
			{Address: "http://192.0.2.5:16997/events", Delivery: amt.DeliveryPush, FilterID: "Intel(r) AMT:All", FilterName: "All", Listener: "Listener Destination 0"},
			{Address: "http://siem.example.com/amt", Delivery: amt.DeliveryPushWithAck, FilterID: "Intel(r) AMT:Platform", FilterName: "Platform", Listener: "Listener Destination 1"},
		},
	}
}

// openForwardingDialog taps "Forwarding…" on the Event log page and waits for the read.
func openForwardingDialog(t *testing.T, f amt.Forwarding, useTLS bool) (*manageHarness, *forwardingDialog) {
	t.Helper()
	dev := &session.Fake{FP: fp, Events: exampleEvents()}
	dev.Forwarding().Data = f
	h, p := logHarness(t, dev, useTLS, 1)
	if p.head == nil || p.head.Text != "Forwarding…" {
		t.Fatal("no Forwarding… in the Event log head")
	}
	test.Tap(p.head)
	if !p.head.Disabled() {
		t.Error("Forwarding… stays enabled while reading")
	}
	h.wait(t)
	d := h.m.logDlg.forwarding
	if d == nil || !open(h.m.parent) || p.head.Disabled() {
		t.Fatal("no list, or the button stays disabled")
	}
	return h, d
}

func forwardingRows(d *forwardingDialog) [][]string {
	var out [][]string
	for _, l := range d.table.lines {
		var r []string
		for _, c := range l.cells {
			r = append(r, richText(c))
		}
		out = append(out, r)
	}
	return out
}

// Issue #18 (V2): the list names target, delivery and filter, each row has Edit.
func TestForwardingListShowsTheTargets(t *testing.T) {
	h, d := openForwardingDialog(t, exampleForwarding(), true)
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Event forwarding")
	if !hasRichText(t, top, "Intel AMT sends events to these targets. OpenLAMT only sets the target; it does not receive events.", false) {
		t.Error("intro")
	}
	want := [][]string{{"http://192.0.2.5:16997/events", "Push", "All"}, {"http://siem.example.com/amt", "Push with acknowledgement", "Platform"}}
	if got := forwardingRows(d); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q", got)
	}
	if !d.table.Visible() || d.empty.Visible() || len(d.edits) != 2 || d.edits[0].Text != "Edit" {
		t.Error("table, empty state or Edit")
	}
	if d.action.Text != "Done" || d.add.Text != "+ Add Target" || d.cancel != nil {
		t.Errorf("buttons %q %q", d.action.Text, d.add.Text)
	}
	test.Tap(d.action)
	if open(h.m.parent) {
		t.Error("Done does not close")
	}
}

func TestForwardingListEmpty(t *testing.T) {
	h, d := openForwardingDialog(t, amt.Forwarding{Filters: exampleForwarding().Filters}, true)
	if d.table.Visible() || !d.empty.Visible() {
		t.Fatal("no empty state")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	if !hasText(top, "No targets") || !hasRichText(t, top, "Intel AMT keeps events only in its own event log.", false) {
		t.Error("empty texts")
	}
}

func TestForwardingReadError(t *testing.T) {
	dev := &session.Fake{FP: fp}
	dev.Forwarding().ReadErr = errors.New("boom")
	h, p := logHarness(t, dev, true, 1)
	test.Tap(p.head)
	h.wait(t)
	if h.m.logDlg.forwarding != nil {
		t.Fatal("list shown after a failed read")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Something went wrong")
}

// Add Target: the address must start with http:// or https:// (error text), the filter defaults
// to "All"; the new target appears in the list.
func TestForwardingAddTarget(t *testing.T) {
	h, d := openForwardingDialog(t, exampleForwarding(), true)
	test.Tap(d.add)
	td := h.m.logDlg.target
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Add forwarding target")
	if td.action.Text != "Add Target" || !td.action.Disabled() || td.filter.Selected != "All" || td.delivery.Selected() != 0 || td.auth.Selected() != 0 {
		t.Fatalf("start: %q disabled %v filter %q", td.action.Text, td.action.Disabled(), td.filter.Selected)
	}
	if !td.user.Disabled() || !td.password.Entry.Disabled() {
		t.Error("sign-in fields enabled without Digest")
	}
	td.address.SetText("192.0.2.5:16997")
	if !td.addrErr.box.Visible() || td.addrErr.Text() != "Enter an address that starts with http:// or https://." || !td.action.Disabled() {
		t.Fatalf("error %q", td.addrErr.Text())
	}
	td.address.SetText("http://192.0.2.7:16997/events")
	if td.addrErr.box.Visible() || td.action.Disabled() {
		t.Fatal("valid address refused")
	}
	td.delivery.Tap(1)
	td.filter.SetSelectedIndex(2)
	test.Tap(td.action)
	h.wait(t)
	added := h.dev.Forwarding().Added
	want := amt.TargetSpec{Address: "http://192.0.2.7:16997/events", Delivery: amt.DeliveryPushWithAck, FilterID: "Intel(r) AMT:Platform"}
	if len(added) != 1 || added[0] != want {
		t.Fatalf("added %+v", added)
	}
	if got := forwardingRows(d); len(got) != 3 || got[2][0] != "http://192.0.2.7:16997/events" || got[2][2] != "Platform" {
		t.Errorf("list not updated: %q", got)
	}
	if h.m.tlsAsk != nil {
		t.Error("TLS question without a password")
	}
}

// Digest over a session without TLS asks the question of #37 (key variant) before anything is
// sent; Cancel sends nothing, Send Anyway sends user and password.
func TestForwardingDigestWithoutTLSAsksFirst(t *testing.T) {
	fill := func(h *manageHarness, d *forwardingDialog) *targetDialog {
		test.Tap(d.add)
		td := h.m.logDlg.target
		td.address.SetText("https://siem.example.com/amt")
		td.auth.Tap(1)
		if td.user.Disabled() || !td.action.Disabled() {
			t.Fatal("Digest: fields disabled or Add enabled without sign-in")
		}
		td.user.SetText("collector")
		td.password.SetText("Secret-1")
		if td.action.Disabled() {
			t.Fatal("Add disabled")
		}
		test.Tap(td.action)
		return td
	}
	h, d := openForwardingDialog(t, exampleForwarding(), false)
	td := fill(h, d)
	if h.m.tlsAsk == nil || len(h.dev.Forwarding().Added) != 0 || td.password.Text() != "" {
		t.Fatal("no question, a write before it, or the password kept in the dialog")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Send the key without TLS?")
	test.Tap(overlayButton(t, h.m.parent, "Send Anyway"))
	h.wait(t)
	if a := h.dev.Forwarding().Added; len(a) != 1 || a[0].User != "collector" || a[0].Password != "Secret-1" {
		t.Fatalf("added %+v", a)
	}

	h, d = openForwardingDialog(t, exampleForwarding(), false)
	fill(h, d)
	h.m.tlsAsk.Dismiss()
	if len(h.dev.Forwarding().Added) != 0 {
		t.Fatal("cancel sent the target")
	}

	h, d = openForwardingDialog(t, exampleForwarding(), true)
	fill(h, d)
	h.wait(t)
	if h.m.tlsAsk != nil || len(h.dev.Forwarding().Added) != 1 {
		t.Fatal("TLS session asked, or did not send")
	}
}

// A refusal of the device names its reason: "Lab refused the target: <fault reason>."
func TestForwardingRefusedTarget(t *testing.T) {
	h, d := openForwardingDialog(t, exampleForwarding(), true)
	h.dev.Forwarding().AddErr = amterror.NewAMTError("e:DeliveryModeRequestedUnavailable", "The requested delivery mode is not supported.", "")
	test.Tap(d.add)
	td := h.m.logDlg.target
	td.address.SetText("http://example.com/events")
	test.Tap(td.action)
	h.wait(t)
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Lab refused the target: The requested delivery mode is not supported.")
	if len(forwardingRows(d)) != 2 {
		t.Error("list changed")
	}
	if got := refusedText("Lab", amt.ErrAccessDenied); got != "This account is not allowed to do this on the device." {
		t.Errorf("denied: %q", got)
	}
}

// Edit shows the target and "Delete Target…"; the delete asks the simple confirmation
// (OpSubscriptionDelete) and removes the subscription.
func TestForwardingEditAndDelete(t *testing.T) {
	h, d := openForwardingDialog(t, exampleForwarding(), true)
	test.Tap(d.edits[1])
	td := h.m.logDlg.target
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Edit forwarding target")
	for _, s := range []string{"http://siem.example.com/amt", "Push with acknowledgement", "Platform"} {
		if !hasText(top, s) {
			t.Errorf("value %q missing", s)
		}
	}
	if td.del == nil || td.del.Text != "Delete Target…" || td.action.Text != "Done" {
		t.Fatal("buttons")
	}
	test.Tap(td.del)
	if h.m.logDlg.deleteAsk == nil || len(h.dev.Forwarding().Deleted) != 0 {
		t.Fatal("no confirmation, or deleted before it")
	}
	top = h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete forwarding target?")
	findLabel(t, top, "Intel AMT stops sending events to http://siem.example.com/amt.")
	if amt.RiskOf(amt.OpSubscriptionDelete).Level != amt.LevelSimple {
		t.Error("level")
	}
	test.Tap(overlayButton(t, h.m.parent, "Delete Target"))
	h.wait(t)
	del := h.dev.Forwarding().Deleted
	if len(del) != 1 || del[0].Listener != "Listener Destination 1" || del[0].FilterID != "Intel(r) AMT:Platform" {
		t.Fatalf("deleted %+v", del)
	}
	if got := forwardingRows(d); len(got) != 1 {
		t.Errorf("rows %q", got)
	}
	if h.m.parent.Canvas().Overlays().Top() == nil {
		t.Error("the list closed")
	}
}

// A write error that is no fault of the device does not read as a refused target (#139): a
// timeout or sign-in failure shows its own text, for Add and for Delete.
func TestForwardingWriteErrorsGoThroughShowError(t *testing.T) {
	h, d := openForwardingDialog(t, exampleForwarding(), true)
	h.dev.Forwarding().AddErr = session.ErrAuth
	test.Tap(d.add)
	td := h.m.logDlg.target
	td.address.SetText("http://example.com/events")
	test.Tap(td.action)
	h.wait(t)
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Something went wrong")
	if !hasText(top, describe(session.ErrAuth)) || hasText(top, refusedText("Lab", session.ErrAuth)) {
		t.Errorf("Add: want %q", describe(session.ErrAuth))
	}
	test.Tap(overlayButton(t, h.m.parent, "OK"))

	h.dev.Forwarding().DeleteErr = context.DeadlineExceeded
	test.Tap(d.edits[0])
	test.Tap(h.m.logDlg.target.del)
	test.Tap(overlayButton(t, h.m.parent, "Delete Target"))
	h.wait(t)
	top = h.m.parent.Canvas().Overlays().Top()
	if !hasText(top, describe(context.DeadlineExceeded)) || hasText(top, refusedText("Lab", context.DeadlineExceeded)) {
		t.Errorf("Delete: want %q", describe(context.DeadlineExceeded))
	}
}

// A changed certificate on a write opens the mismatch dialog (#139).
func TestForwardingWriteMismatchOpensTheDialog(t *testing.T) {
	h, d := openForwardingDialog(t, exampleForwarding(), true)
	h.dev.Forwarding().AddErr = &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	test.Tap(d.add)
	td := h.m.logDlg.target
	td.address.SetText("http://example.com/events")
	test.Tap(td.action)
	h.wait(t)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Certificate changed")
}

// The filter comes from the list the dropdown was built with, not from a list that a reload
// replaced meanwhile (#139, Q10).
func TestForwardingAddKeepsTheFiltersOfTheDropdown(t *testing.T) {
	h, d := openForwardingDialog(t, exampleForwarding(), true)
	test.Tap(d.add)
	td := h.m.logDlg.target
	td.address.SetText("http://example.com/events")
	td.filter.SetSelectedIndex(2) // Platform
	f := exampleForwarding()
	slices.Reverse(f.Filters)
	d.show(f)
	test.Tap(td.action)
	h.wait(t)
	added := h.dev.Forwarding().Added
	if len(added) != 1 || added[0].FilterID != "Intel(r) AMT:Platform" {
		t.Fatalf("added %+v", added)
	}
}
