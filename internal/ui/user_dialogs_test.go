package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// Made-up passwords that pass the AMT rules.
const (
	newPassword   = "Xr7#mQ2v!pL9"
	otherPassword = "Zq4$wN8k!tB2"
)

// countingStore is a session.LoginStore that counts the login changes and fails with err.
type countingStore struct {
	logins int
	err    error
}

func (s *countingStore) ChangeLogin(host, oldUser, newUser, newPassword string) error {
	s.logins++
	return s.err
}

func (s *countingStore) ChangeFingerprint(host, user, oldFP, newFP string) error { return nil }

var _ session.LoginStore = (*countingStore)(nil)

// captureUsersNotApproved records the calls of usersNotApproved during the test.
func captureUsersNotApproved(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	prev := usersNotApprovedHook
	usersNotApprovedHook = func(what string) { calls = append(calls, what) }
	t.Cleanup(func() { usersNotApprovedHook = prev })
	return &calls
}

func TestAdminDialogNeedsValidFieldsAndTheHost(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	store := &countingStore{}
	h.m.deps.loginStore = store
	test.Tap(p.table.lines[0].edit)
	d := p.adminDlg
	if d == nil || !open(h.m.parent) {
		t.Fatal("no admin dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Change the signed-in admin account")
	if !hasRichText(t, top, "Risk of lockout. ", true) ||
		!hasRichText(t, top, "If the new password is lost, only the BIOS (MEBx) at the device can reset it.", false) {
		t.Error("warning text")
	}
	if d.name.Text != "admin" || d.pw.Entry == nil || !d.pw.Entry.Password || !d.keychain.Checked ||
		d.keychain.Text != "Update the password in the keychain" || d.action.Text != "Change Account" {
		t.Fatalf("fields: name %q keychain %v %q button %q", d.name.Text, d.keychain.Checked, d.keychain.Text, d.action.Text)
	}
	steps := []struct {
		do    func()
		ready bool
	}{
		{func() { d.entry.SetText(riskHost) }, false},
		{func() { d.pw.SetText(newPassword) }, false},
		{func() { d.repeat.SetText(newPassword + "x") }, false},
		{func() { d.repeat.SetText(newPassword) }, true},
		{func() { d.name.SetText("bad name") }, false},
		{func() { d.name.SetText("admin2") }, true},
		{func() { d.pw.SetText("short"); d.repeat.SetText("short") }, false},
		{func() { d.pw.SetText(newPassword); d.repeat.SetText(newPassword) }, true},
		{func() { d.entry.SetText("device.example.co") }, false},
		{func() { d.entry.SetText(riskHost) }, true},
	}
	for i, s := range steps {
		s.do()
		if d.action.Disabled() == s.ready {
			t.Fatalf("step %d: disabled %v", i, d.action.Disabled())
		}
	}
	test.Tap(d.action)
	h.wait(t) // write
	h.wait(t) // reload
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetAdminAccount"}) || h.dev.Cfg.Users.Admin != "admin2" {
		t.Fatalf("writes %v admin %q", got, h.dev.Cfg.Users.Admin)
	}
	if store.logins != 1 || h.m.sess.Config().Username != "admin2" || open(h.m.parent) {
		t.Fatalf("store %d user %q open %v", store.logins, h.m.sess.Config().Username, open(h.m.parent))
	}
	if richText(p.table.lines[0].cells[0]) != "admin2*" {
		t.Errorf("table not reloaded: %q", richText(p.table.lines[0].cells[0]))
	}
}

func TestAdminDialogWithoutKeychainPassesNoStore(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	store := &countingStore{}
	h.m.deps.loginStore = store
	test.Tap(p.table.lines[0].edit)
	d := p.adminDlg
	test.Tap(d.keychain)
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	d.entry.SetText(riskHost)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if store.logins != 0 || !slices.Equal(h.dev.ConfigWrites(), []string{"SetAdminAccount"}) {
		t.Fatalf("store %d writes %v", store.logins, h.dev.ConfigWrites())
	}
}

func TestAdminDialogOfAnotherAccount(t *testing.T) {
	list := exampleUsers()
	list.Admin = "root"
	h, p := usersHarness(t, list)
	test.Tap(p.table.lines[0].edit)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Change the admin account")
}

func TestNotSavedDialogShowsThePasswordOnce(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	h.m.deps.loginStore = &countingStore{err: errors.New("keychain locked")}
	test.Tap(p.table.lines[0].edit)
	d := p.adminDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	d.entry.SetText(riskHost)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	ns := p.notSaved
	if ns == nil || !open(h.m.parent) {
		t.Fatal("no not-saved dialog")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "New password not saved")
	findLabel(t, top, "The password on “Lab” was changed, but OpenLAMT could not save it in the keychain. Copy it now. It is shown only once.")
	if ns.display.Entry != nil || ns.display.Revealed() || ns.display.Text() != newPassword {
		t.Fatalf("display: read-only %v revealed %v", ns.display.Entry == nil, ns.display.Revealed())
	}
	ns.display.SetRevealed(true) // the eye button (its masking is tested in package widgets)
	if !ns.display.Revealed() {
		t.Error("eye does not show the password")
	}
	test.Tap(ns.copy)
	if got := fyne.CurrentApp().Clipboard().Content(); got != newPassword {
		t.Errorf("clipboard %q", got)
	}
	if ns.done.Text != "I Have Saved It" || ns.done.Importance != widget.HighImportance {
		t.Errorf("main button %q", ns.done.Text)
	}
	test.Tap(ns.done)
	if open(h.m.parent) || ns.display.Text() != "" {
		t.Errorf("after closing: open %v password kept %v", open(h.m.parent), ns.display.Text() != "")
	}
}

// TestNotSavedDialogAfterTheDeviceWindowClosed: a password change that ends after the device
// window (and with it device management) closed still shows the new password, on the start
// window.
func TestNotSavedDialogAfterTheDeviceWindowClosed(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(h *manageHarness)
	}{
		{"keychain", func(h *manageHarness) { h.m.deps.loginStore = &countingStore{err: errors.New("keychain locked")} }},
		{"uncertain", func(h *manageHarness) {
			h.dev.Cfg.WriteErr = &amt.CredentialsUncertainError{Username: "admin", Self: true, Err: errors.New("connection reset")}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, p := usersHarness(t, exampleUsers())
			main := h.a.NewWindow("OpenLAMT")
			h.m.deps.mainWin = main
			c.setup(h)
			test.Tap(p.table.lines[0].edit)
			d := p.adminDlg
			d.pw.SetText(newPassword)
			d.repeat.SetText(newPassword)
			d.entry.SetText(riskHost)
			test.Tap(d.action)
			h.m.Close() // the device window closes while the write runs
			h.wait(t)
			top := main.Canvas().Overlays().Top()
			if top == nil {
				t.Fatal("no not-saved dialog on the start window")
			}
			findLabel(t, top, "New password not saved")
			found := false
			walk(top, func(o fyne.CanvasObject) {
				if f, ok := o.(interface{ Text() string }); ok && f.Text() == newPassword {
					found = true
				}
			})
			if !found {
				t.Fatal("the dialog does not hold the new password")
			}
		})
	}
}

func TestUncertainCredentialsShowTheNotSavedDialog(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	h.dev.Cfg.WriteErr = &amt.CredentialsUncertainError{Username: "admin", Self: true, Err: errors.New("connection reset")}
	test.Tap(p.table.lines[0].edit)
	d := p.adminDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	d.entry.SetText(riskHost)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if p.notSaved == nil || p.notSaved.display.Text() != newPassword {
		t.Fatal("uncertain change must keep the new password visible")
	}
}

func TestAdminDialogOtherErrorsUseTheErrorDialog(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	h.dev.Cfg.WriteErr = errors.New("example failure")
	test.Tap(p.table.lines[0].edit)
	d := p.adminDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	d.entry.SetText(riskHost)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if p.notSaved != nil || !open(h.m.parent) {
		t.Fatal("expected the error dialog")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Something went wrong")
	if strings.Contains(describe(h.dev.Cfg.WriteErr), newPassword) {
		t.Error("password in the error text")
	}
}

func TestAddUserDialog(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	test.Tap(p.add)
	d := p.userDlg
	if d == nil || d.edit != nil || d.enabled != nil || d.del != nil {
		t.Fatal("no add dialog, or edit controls in it")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Add user")
	if d.action.Text != "Add User" || d.action.Importance != widget.HighImportance || !d.action.Disabled() {
		t.Fatalf("button %q disabled %v", d.action.Text, d.action.Disabled())
	}
	if got := strings.Join(d.checkTexts(), "|"); got != "Power control|KVM, media, console|Hardware|Event log|Audit log|Settings" {
		t.Errorf("permissions %q", got)
	}
	if d.access.Selected() != 0 {
		t.Error("Network is the default access")
	}
	d.name.SetText("ops")
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	if !d.action.Disabled() {
		t.Fatal("enabled without a permission")
	}
	test.Tap(d.checks[0])
	test.Tap(d.checks[5])
	if d.action.Disabled() {
		t.Fatal("still disabled with name, password and a permission")
	}
	d.repeat.SetText(otherPassword)
	if !d.action.Disabled() {
		t.Fatal("enabled with a different repetition")
	}
	d.repeat.SetText(newPassword)
	d.access.Tap(2)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	users := h.dev.Cfg.Users.Users
	got := users[len(users)-1]
	want := []amt.Realm{amt.RealmPTAdministration, amt.RealmRemoteControl, amt.RealmNetworkTime, amt.RealmGeneralInfo}
	if got.Name != "ops" || got.Access != amt.AccessLocalAndNetwork || !slices.Equal(got.Realms, want) {
		t.Fatalf("added %+v", got)
	}
	if len(p.table.lines) != 6 {
		t.Errorf("table not reloaded: %d lines", len(p.table.lines))
	}
}

func TestEditUserTogglesEnabledWithoutPassword(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	test.Tap(p.table.lines[2].edit) // user1, disabled
	d := p.userDlg
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Edit user")
	if d.name.Text != "user1" || d.enabled == nil || d.enabled.Checked() || d.access.Selected() != 1 || d.action.Text != "Save" {
		t.Fatalf("prefill: name %q access %d button %q", d.name.Text, d.access.Selected(), d.action.Text)
	}
	if got := d.checkStates(); !slices.Equal(got, []bool{false, false, false, true, true, true}) {
		t.Errorf("permissions %v", got)
	}
	if !d.action.Disabled() {
		t.Fatal("Save enabled without a change")
	}
	test.Tap(d.enabled)
	if d.action.Disabled() {
		t.Fatal("Save disabled after switching Enabled")
	}
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.ConfigWrites(); !slices.Equal(got, []string{"SetUserEnabled"}) || !h.dev.Cfg.Users.Users[1].Enabled {
		t.Fatalf("writes %v", got)
	}
}

func TestEditUserKeepsUnmappedRealmsAndNeedsThePassword(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	test.Tap(p.table.lines[2].edit) // user1 with realm 9 outside the mapping
	d := p.userDlg
	test.Tap(d.checks[0]) // Power control
	if !d.action.Disabled() {
		t.Fatal("a permission change needs the password (the digest contains the name)")
	}
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	if d.action.Disabled() {
		t.Fatal("still disabled with the password")
	}
	test.Tap(d.checks[5]) // Settings off
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	u := h.dev.Cfg.Users.Users[1]
	want := []amt.Realm{amt.RealmRemoteControl, amt.RealmAgentPresenceLocal, amt.RealmEventLogReader, amt.RealmAuditLog}
	if !slices.Equal(h.dev.ConfigWrites(), []string{"UpdateUser"}) || !slices.Equal(u.Realms, want) || u.Access != amt.AccessLocal {
		t.Fatalf("writes %v realms %v access %v", h.dev.ConfigWrites(), u.Realms, u.Access)
	}
}

func TestEditUserPartialGroupStaysUntouched(t *testing.T) {
	list := exampleUsers()
	list.Users[1].Realms = []amt.Realm{amt.RealmPTAdministration, amt.RealmEventLogReader}
	h, p := usersHarness(t, list)
	test.Tap(p.table.lines[2].edit)
	d := p.userDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if got := h.dev.Cfg.Users.Users[1].Realms; !slices.Equal(got, []amt.Realm{amt.RealmPTAdministration, amt.RealmEventLogReader}) {
		t.Fatalf("realms %v", got)
	}
}

func TestEditUserNotSavedAfterPasswordChange(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	h.m.deps.loginStore = &countingStore{err: errors.New("keychain locked")}
	test.Tap(p.table.lines[1].edit)
	d := p.userDlg
	d.pw.SetText(newPassword)
	d.repeat.SetText(newPassword)
	test.Tap(d.enabled) // disable as well
	test.Tap(d.action)
	h.wait(t)
	h.wait(t)
	if !slices.Equal(h.dev.ConfigWrites(), []string{"UpdateUser", "SetUserEnabled"}) {
		t.Fatalf("writes %v", h.dev.ConfigWrites())
	}
	if p.notSaved == nil || p.notSaved.display.Text() != newPassword {
		t.Fatal("no not-saved dialog after a password change that was not saved")
	}
}

func TestEditUserDeleteAndOwnAccountAreNotApproved(t *testing.T) {
	calls := captureUsersNotApproved(t)
	list := exampleUsers()
	list.Users[0].Self = true
	h, p := usersHarness(t, list)
	test.Tap(p.table.lines[1].edit) // own account (not the admin)
	if p.userDlg != nil || open(h.m.parent) || len(*calls) != 1 {
		t.Fatalf("own account: dialog %v calls %v", p.userDlg != nil, *calls)
	}
	test.Tap(p.table.lines[2].edit)
	d := p.userDlg
	if d.del == nil || d.del.Text != "Delete User…" || d.del.Importance != widget.DangerImportance {
		t.Fatal("no red Delete User… button")
	}
	test.Tap(d.del)
	if len(*calls) != 2 || h.dev.ConfigWrites() != nil || !open(h.m.parent) {
		t.Fatalf("delete: calls %v writes %v", *calls, h.dev.ConfigWrites())
	}
}

// TestDeviceManagementKnowsTheStartWindow: device windows of the start window hand it to device
// management for the late "New password not saved".
func TestDeviceManagementKnowsTheStartWindow(t *testing.T) {
	q := newQuitHarness(t)
	cfg := amt.Config{Host: "device.example.com", Username: "admin", Fingerprint: q.h.dev.FP}
	sess, st, err := session.Open(context.Background(), cfg, nil, session.ConnectFake(q.h.dev))
	if err != nil {
		t.Fatal(err)
	}
	d := q.h.s.addDevice(sess, st, "Büro")
	if m := d.ensureManager(); m.deps.mainWin != q.h.s.win {
		t.Fatal("device management has no start window")
	}
}
