package ui

import (
	"errors"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
)

// ownUsers is exampleUsers where the signed-in account is the digest account "helpdesk".
func ownUsers() amt.UserList {
	l := exampleUsers()
	l.Users[0].Self = true
	return l
}

// Issue #32: the Edit button of the signed-in, non-admin account is enabled and opens the
// dialog "Edit your account" (Users.dc.html): hint line, no "Enabled", no "Delete User…".
func TestOwnAccountEditOpensItsDialog(t *testing.T) {
	h, p := usersHarness(t, ownUsers())
	own := p.table.lines[1]
	if own.edit.Disabled() || !own.row.signedIn || !own.row.editable {
		t.Fatal("Edit of the own, non-admin account is disabled")
	}
	if !p.table.lines[0].row.editable || !p.table.lines[2].row.editable || p.table.lines[3].row.editable {
		t.Error("admin and other users stay editable, system accounts do not")
	}
	test.Tap(own.edit)
	d := p.userDlg
	if d == nil || !open(h.m.parent) {
		t.Fatal("no dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Edit your account")
	if !hasRichText(t, top, "OpenLAMT is signed in to Lab with this account.", false) {
		t.Error("hint line missing")
	}
	if d.name.Text != "helpdesk" || d.enabled != nil || d.del != nil || len(d.extras) != 0 || d.action.Text != "Save" {
		t.Errorf("name %q, enabled %v, delete %v, button %q", d.name.Text, d.enabled, d.del, d.action.Text)
	}
	if !d.action.Disabled() {
		t.Error("Save enabled without a change")
	}
	if got := d.checkStates(); !slices.Equal(got, []bool{true, true, true, false, false, false}) {
		t.Errorf("permissions %v", got)
	}
}

// Another user keeps "Enabled" and "Delete User…".
func TestOtherUserKeepsEnabledAndDelete(t *testing.T) {
	_, p := usersHarness(t, ownUsers())
	test.Tap(p.table.lines[2].edit)
	if d := p.userDlg; d.enabled == nil || d.del == nil {
		t.Error("another account lost Enabled or Delete User…")
	}
}

// Issue #32: Save asks for the orange confirmation before anything is written; the list names
// the changes; the write then goes through amt.OpOwnAccount and the keychain follows.
func TestOwnAccountSaveAsksBeforeWriting(t *testing.T) {
	h, p := usersHarness(t, ownUsers())
	store := &countingStore{}
	h.m.deps.loginStore = store
	test.Tap(p.table.lines[1].edit)
	d := p.userDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.checks[2]) // Hardware off
	test.Tap(d.action)
	c := p.ownDlg
	if c == nil || len(h.dev.ConfigWrites()) != 0 {
		t.Fatalf("no confirmation, or a write before it: %v", h.dev.ConfigWrites())
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Change your own account?")
	if !hasRichText(t, top, "Password: changed", false) || !hasRichText(t, top, "\nPermissions: Hardware removed", false) {
		t.Error("list of changes")
	}
	if c.action.Text != "Change Account" || !c.action.Disabled() || c.check.Text != "I understand the risk for Lab (device.example.com)" {
		t.Fatalf("button %q disabled %v, box %q", c.action.Text, c.action.Disabled(), c.check.Text)
	}
	test.Tap(c.check)
	test.Tap(c.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"UpdateUser"}) {
		t.Fatalf("writes %v", got)
	}
	if got := h.dev.Cfg.Users.Users[0].Realms; slices.Contains(got, amt.RealmHardwareAsset) {
		t.Errorf("realms %v still have Hardware", got)
	}
	if store.logins != 1 || p.notSaved != nil {
		t.Errorf("keychain updated %d times, not-saved dialog %v", store.logins, p.notSaved != nil)
	}
	if got := h.m.sess.Config().Password; got != newPassword {
		t.Error("the session did not continue with the new password")
	}
}

func TestOwnAccountCancelWritesNothing(t *testing.T) {
	h, p := usersHarness(t, ownUsers())
	test.Tap(p.table.lines[1].edit)
	d := p.userDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.action)
	p.ownDlg.Dismiss()
	if open(h.m.parent) || len(h.dev.ConfigWrites()) != 0 {
		t.Fatalf("dialog open %v, writes %v", open(h.m.parent), h.dev.ConfigWrites())
	}
}

// Issue #32: after a password change the keychain entry follows, or the user is told it was
// not saved, as for the admin account.
func TestOwnAccountPasswordNotSavedShowsIt(t *testing.T) {
	h, p := usersHarness(t, ownUsers())
	h.m.deps.loginStore = &countingStore{err: errors.New("keychain locked")}
	test.Tap(p.table.lines[1].edit)
	d := p.userDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.action)
	test.Tap(p.ownDlg.check)
	test.Tap(p.ownDlg.action)
	h.wait(t)
	h.wait(t)
	if p.notSaved == nil || p.notSaved.display.Text() != newPassword {
		t.Fatal("no \"New password not saved\" dialog")
	}
	if !slices.Equal(h.dev.ConfigWrites(), []string{"UpdateUser"}) {
		t.Errorf("writes %v", h.dev.ConfigWrites())
	}
}

// The own account refuses the access "Local" (the device needs network access for this connection).
func TestOwnAccountLocalAccessIsAnErrorNotAWrite(t *testing.T) {
	h, p := usersHarness(t, ownUsers())
	test.Tap(p.table.lines[1].edit)
	d := p.userDlg
	d.access.SetSelected(1)
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.action)
	test.Tap(p.ownDlg.check)
	test.Tap(p.ownDlg.action)
	h.wait(t)
	h.wait(t)
	if len(h.dev.ConfigWrites()) != 0 {
		t.Fatalf("writes %v", h.dev.ConfigWrites())
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Something went wrong")
}

// Issue #37: adding an account over a session without TLS asks before anything is sent.
func TestAddUserWithoutTLSAsksFirst(t *testing.T) {
	h, p := usersHarnessTLS(t, exampleUsers(), false)
	fillAddUser(p)
	test.Tap(p.userDlg.action)
	q := h.m.tlsAsk
	if q == nil || len(h.dev.ConfigWrites()) != 0 {
		t.Fatalf("no question, or a write before it: %v", h.dev.ConfigWrites())
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Send the password without TLS?")
	findLabel(t, top, "The connection to Lab has no TLS. The password travels as a digest that works like the password itself. Anyone who captures it on the network can sign in as ops.")
	if q.action.Text != "Send Anyway" || q.cancel.Text != "Cancel" || !q.spec.noEnter {
		t.Errorf("button %q, cancel %q, Enter confirms %v", q.action.Text, q.cancel.Text, !q.spec.noEnter)
	}
	test.Tap(q.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"AddUser"}) {
		t.Fatalf("writes %v", got)
	}
}

func TestAddUserWithoutTLSCancelSendsNothing(t *testing.T) {
	h, p := usersHarnessTLS(t, exampleUsers(), false)
	fillAddUser(p)
	test.Tap(p.userDlg.action)
	h.m.tlsAsk.Dismiss()
	if open(h.m.parent) || len(h.dev.ConfigWrites()) != 0 {
		t.Fatalf("dialog open %v, writes %v", open(h.m.parent), h.dev.ConfigWrites())
	}
	if NewSettings(h.m.app.Preferences()).HideTLSWarning() {
		t.Error("Cancel stored \"Don’t show again\"")
	}
}

// Issue #37: sessions with TLS show no warning.
func TestAddUserWithTLSShowsNoWarning(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	fillAddUser(p)
	test.Tap(p.userDlg.action)
	if h.m.tlsAsk != nil || open(h.m.parent) {
		t.Fatal("question shown with TLS")
	}
	h.wait(t)
	h.wait(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"AddUser"}) {
		t.Fatalf("writes %v", got)
	}
}

// Issue #37: "Don’t show again" hides the question for later sessions, and "Reset Warnings"
// (ResetHiddenMessages) brings it back.
func TestTLSWarningDontShowAgainAndReset(t *testing.T) {
	h, p := usersHarnessTLS(t, exampleUsers(), false)
	settings := NewSettings(h.m.app.Preferences())
	if !slices.Contains(hiddenMessagePrefs, prefHideTLSWarning) {
		t.Fatal("the warning is not registered in hiddenMessagePrefs")
	}
	fillAddUser(p)
	test.Tap(p.userDlg.action)
	test.Tap(h.m.tlsAsk.action) // without the box: stays visible
	h.wait(t)
	h.wait(t)
	if settings.HideTLSWarning() {
		t.Fatal("hidden without the box")
	}
	test.Tap(p.add)
	d := p.userDlg
	d.name.SetText("ops2")
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.checks[0])
	test.Tap(d.action)
	test.Tap(findCheck(t, h.m.parent.Canvas().Overlays().Top(), "Don’t show again"))
	test.Tap(h.m.tlsAsk.action)
	h.wait(t)
	h.wait(t)
	if !settings.HideTLSWarning() || !settings.HiddenMessages() {
		t.Fatal("\"Don’t show again\" not stored")
	}
	test.Tap(p.add)
	d = p.userDlg
	d.name.SetText("ops3")
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.checks[0])
	test.Tap(d.action)
	if h.m.tlsAsk != nil || open(h.m.parent) {
		t.Fatal("question shown although hidden")
	}
	h.wait(t)
	h.wait(t)
	settings.ResetHiddenMessages()
	if settings.HideTLSWarning() || settings.HiddenMessages() {
		t.Fatal("Reset Warnings left the warning hidden")
	}
	test.Tap(p.add)
	d = p.userDlg
	d.name.SetText("ops4")
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.checks[0])
	test.Tap(d.action)
	if h.m.tlsAsk == nil {
		t.Fatal("the warning did not come back after the reset")
	}
}

// Issue #37: changing a password asks; an edit without a password (only Enabled) does not.
func TestEditUserWithoutTLSAsksOnlyForAPassword(t *testing.T) {
	h, p := usersHarnessTLS(t, exampleUsers(), false)
	test.Tap(p.table.lines[1].edit)
	d := p.userDlg
	test.Tap(d.enabled)
	test.Tap(d.action)
	if h.m.tlsAsk != nil {
		t.Fatal("question without a password")
	}
	h.wait(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetUserEnabled"}) {
		t.Fatalf("writes %v", got)
	}
	test.Tap(p.table.lines[1].edit)
	d = p.userDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.action)
	if h.m.tlsAsk == nil {
		t.Fatal("no question for the password change")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(),
		"The connection to Lab has no TLS. The password travels as a digest that works like the password itself. Anyone who captures it on the network can sign in as helpdesk.")
	if len(h.dev.ConfigWrites()) != 1 {
		t.Fatalf("written before the answer: %v", h.dev.ConfigWrites())
	}
}

// Issue #37, design V18: the admin account's password goes through the same question.
func TestAdminPasswordWithoutTLSAsksFirst(t *testing.T) {
	h, p := usersHarnessTLS(t, exampleUsers(), false)
	test.Tap(p.table.lines[0].edit)
	d := p.adminDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.check)
	test.Tap(d.action)
	if h.m.tlsAsk == nil || len(h.dev.ConfigWrites()) != 0 {
		t.Fatalf("no question, or a write before it: %v", h.dev.ConfigWrites())
	}
	test.Tap(h.m.tlsAsk.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetAdminAccount"}) {
		t.Fatalf("writes %v", got)
	}
}

// The own account over a session without TLS: the TLS question first, then the orange one.
func TestOwnAccountWithoutTLSAsksTLSBeforeTheConfirmation(t *testing.T) {
	h, p := usersHarnessTLS(t, ownUsers(), false)
	test.Tap(p.table.lines[1].edit)
	d := p.userDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.action)
	if h.m.tlsAsk == nil || p.ownDlg != nil {
		t.Fatal("the TLS question must come first")
	}
	test.Tap(h.m.tlsAsk.action)
	if p.ownDlg == nil || len(h.dev.ConfigWrites()) != 0 {
		t.Fatal("no orange confirmation after the TLS question")
	}
}

// "Send the key without TLS?" (design V18) is the same question with another text and the
// same "Don’t show again".
func TestKeyWithoutTLSQuestion(t *testing.T) {
	h, _ := usersHarnessTLS(t, exampleUsers(), false)
	sent := 0
	h.m.warnWithoutTLS(h.m.parent, secretKey, "", func() { sent++ })
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Send the key without TLS?")
	if sent != 0 {
		t.Fatal("sent before the answer")
	}
	test.Tap(h.m.tlsAsk.action)
	if sent != 1 {
		t.Fatal("Send Anyway did not send")
	}
}

// findCheck returns the checkbox with text below top.
func findCheck(t *testing.T, top fyne.CanvasObject, text string) *dialogCheck {
	t.Helper()
	var c *dialogCheck
	walk(top, func(o fyne.CanvasObject) {
		if v, ok := o.(*dialogCheck); ok && v.Text == text {
			c = v
		}
	})
	if c == nil {
		t.Fatalf("no checkbox %q", text)
	}
	return c
}

func fillAddUser(p *usersPane) {
	test.Tap(p.add)
	d := p.userDlg
	d.name.SetText("ops")
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.checks[0])
}
