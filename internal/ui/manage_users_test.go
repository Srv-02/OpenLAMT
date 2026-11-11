package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

const exampleSID = "S-1-5-21-1111111111-2222222222-3333333333-1001"

// exampleUsers is a made-up user list: the admin account "admin" (the harness logs in as
// "admin"), two digest accounts, a firmware account and a Kerberos account.
func exampleUsers() amt.UserList {
	return amt.UserList{Admin: "admin", Users: []amt.User{
		{Handle: 1, Name: "helpdesk", Access: amt.AccessNetwork, Enabled: true, EnabledKnown: true,
			Realms: []amt.Realm{amt.RealmRemoteControl, amt.RealmRedirection, amt.RealmHardwareAsset}},
		{Handle: 2, Name: "user1", Access: amt.AccessLocal, EnabledKnown: true,
			Realms: []amt.Realm{amt.RealmEventLogReader, amt.RealmAuditLog, amt.RealmPTAdministration, amt.RealmNetworkTime,
				amt.RealmGeneralInfo, amt.RealmAgentPresenceLocal}},
		{Handle: 3, Name: "$$OsAdmin", Access: amt.AccessLocal, System: true, Realms: []amt.Realm{amt.RealmLocalUN}},
		{Handle: 4, KerberosSID: exampleSID, Access: amt.AccessLocalAndNetwork, Enabled: true, EnabledKnown: true,
			Realms: []amt.Realm{amt.RealmRemoteControl}},
	}}
}

// usersHarness opens the Users page in the sidebar on a fake with exampleUsers; the device
// address the red confirmation asks for is the session's host.
func usersHarness(t *testing.T, list amt.UserList) (*manageHarness, *usersPane) {
	t.Helper()
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, Cfg: session.FakeConfig{Users: list}})
	h.m.deps.host = riskHost
	h.m.Sidebar()
	h.wait(t) // hardware
	h.m.side.tabs.Tap(4)
	h.wait(t) // users
	return h, h.m.side.view.panes[4].impl.(*usersPane)
}

func TestUserRowTexts(t *testing.T) {
	englishUI(t)
	rows := userRows(exampleUsers(), "ADMIN")
	type want struct{ name, status, access, perms string }
	wants := []want{
		{"admin", "Enabled", "Network and local", "Administrator"},
		{"helpdesk", "Enabled", "Network", "Power control, KVM, media, console, Hardware"},
		{"user1", "Disabled", "Local", "Event log, Audit log, Settings"},
		{"$$OsAdmin", "System", "Local", "System account"},
		{exampleSID, "Enabled", "Network and local", "Power control"},
	}
	if len(rows) != len(wants) {
		t.Fatalf("%d rows", len(rows))
	}
	for i, w := range wants {
		r := rows[i]
		if (want{r.name, r.status, r.access, r.perms}) != w {
			t.Errorf("row %d = %q %q %q %q, want %+v", i, r.name, r.status, r.access, r.perms, w)
		}
	}
	if !rows[0].admin || !rows[0].signedIn || !rows[0].editable {
		t.Errorf("admin row %+v: signed in (case as the backend) and editable", rows[0])
	}
	if !rows[3].system || rows[3].editable || rows[4].editable || !rows[1].editable {
		t.Error("system and Kerberos rows must not be editable, digest rows must")
	}
	unknown := amt.User{Handle: 9, Name: "x", Realms: []amt.Realm{amt.RealmRedirection}}
	if got := userRows(amt.UserList{Users: []amt.User{unknown}}, "admin")[0].status; got != "" {
		t.Errorf("unknown enabled state shows %q", got)
	}
	self := amt.User{Handle: 9, Name: "ops", Self: true, Enabled: true, EnabledKnown: true, Realms: []amt.Realm{amt.RealmRedirection}}
	if r := userRows(amt.UserList{Admin: "admin", Users: []amt.User{self}}, "ops"); r[0].signedIn || !r[1].signedIn {
		t.Errorf("signed-in marks: admin %v ops %v", r[0].signedIn, r[1].signedIn)
	}
}

// A partly present permission counts as missing, the realm 9 outside the mapping is not named.
func TestPermissionsTextNeedsTheWholeGroup(t *testing.T) {
	englishUI(t)
	if got := permissionsText([]amt.Realm{amt.RealmPTAdministration, amt.RealmAgentPresenceLocal}); got != "" {
		t.Errorf("partial settings group: %q", got)
	}
}

func TestUsersPageTable(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	if h.m.users.reads != 1 || len(p.table.lines) != 5 {
		t.Fatalf("reads %d lines %d", h.m.users.reads, len(p.table.lines))
	}
	if got := richText(p.table.lines[0].cells[0]); got != "admin*" {
		t.Errorf("admin name %q", got)
	}
	star := p.table.lines[0].cells[0].Segments[1].(*widget.TextSegment)
	if star.Style.ColorName != style.ColorNameDanger || !star.Style.TextStyle.Bold {
		t.Errorf("asterisk style %+v", star.Style)
	}
	if got := richText(p.table.lines[1].cells[3]); got != "Power control, KVM, media, console, Hardware" {
		t.Errorf("helpdesk permissions %q", got)
	}
	sys := p.table.lines[3]
	if !sys.edit.Disabled() || sys.cells[0].Segments[0].(*widget.TextSegment).Style.ColorName != style.ColorNameTextSecondary {
		t.Error("$$ row must be gray with Edit disabled")
	}
	if !p.table.lines[4].edit.Disabled() || p.table.lines[1].edit.Disabled() {
		t.Error("Kerberos Edit disabled, helpdesk Edit enabled")
	}
	admin := p.table.lines[0].edit.Metrics
	if admin.Neutral != style.ColorNameSignedInSurface || admin.NeutralBorder != style.ColorNameSignedInBorder ||
		admin.NeutralText != style.ColorNameDangerZoneText {
		t.Errorf("signed-in Edit metrics %+v", admin)
	}
	if p.table.lines[1].edit.Metrics.NeutralText != "" {
		t.Error("other Edit buttons are not red")
	}
	if !p.footnote.Visible() || richText(p.footnote) != "* Signed-in account. Changing its name or password can lock you out of this device." {
		t.Errorf("footnote %v %q", p.footnote.Visible(), richText(p.footnote))
	}
	if p.add.Text != "+ Add user" || !p.add.Visible() {
		t.Errorf("add button %q", p.add.Text)
	}
	// every row is placed below the previous one, inside the table
	var y float32
	for i, l := range p.table.lines {
		if py := l.cells[0].Position().Y; py <= y {
			t.Errorf("row %d at %v, previous at %v", i, py, y)
		} else {
			y = py
		}
	}
	if p.table.Size().Height < p.table.MinSize().Height || p.table.MinSize().Height < 6*20 {
		t.Errorf("table %v min %v", p.table.Size(), p.table.MinSize())
	}
}

func TestUsersFootnoteOnlyForTheSignedInAccount(t *testing.T) {
	list := exampleUsers()
	list.Admin = "root"
	_, p := usersHarness(t, list)
	if p.footnote.Visible() {
		t.Error("footnote without a signed-in account")
	}
	if richText(p.table.lines[0].cells[0]) != "root" || p.table.lines[0].edit.Metrics.NeutralText != "" {
		t.Error("admin row of another account is not marked")
	}
}

func TestUsersWindowHasAddInTheHead(t *testing.T) {
	h, _ := usersHarness(t, exampleUsers())
	h.m.OpenWindow()
	w := h.m.win
	test.Tap(w.nav[4])
	p := w.view.panes[4].impl.(*usersPane)
	if w.view.panes[4].tools != p.add || h.m.users.reads != 1 || len(p.table.lines) != 5 {
		t.Fatalf("window: tools %v reads %d lines %d", w.view.panes[4].tools, h.m.users.reads, len(p.table.lines))
	}
	if p.window() != w.win || h.m.side.view.panes[4].impl.(*usersPane).window() != h.m.parent {
		t.Error("dialogs open on the window of their view")
	}
}

func TestUsersPageShowsReadErrors(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, Cfg: session.FakeConfig{UsersErr: amt.ErrAccessDenied}})
	h.m.Sidebar()
	h.wait(t)
	h.m.side.tabs.Tap(4)
	h.wait(t)
	p := h.m.side.view.panes[4].impl.(*usersPane)
	if !p.err.box.Visible() || len(p.table.lines) != 0 || p.footnote.Visible() {
		t.Fatalf("error %v lines %d", p.err.box.Visible(), len(p.table.lines))
	}
}

// richText returns the text of all segments.
func richText(rt *widget.RichText) string {
	s := ""
	for _, seg := range rt.Segments {
		if ts, ok := seg.(*widget.TextSegment); ok {
			s += ts.Text
		}
	}
	return s
}

var _ fyne.Widget = (*userTable)(nil)
