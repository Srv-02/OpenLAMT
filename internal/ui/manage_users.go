package ui

import (
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The Users page of device management (Users.dc.html): the admin account and the user ACL of
// the device in a table Name | Status | Access | Permissions | Edit, "+ Add user" in the page
// head (window) or above the table (sidebar). The dialogs are in user_dialogs.go.

// permGroup is one permission checkbox of the user dialog (Forms.dc.html) and the realms it
// stands for. Realms outside every group are kept unchanged and are not named in the table.
type permGroup struct {
	label  string
	realms []amt.Realm
}

// permGroups are the six permissions in the order of the dialog. "Settings" is what the
// Settings page reads and writes: PT Administration (general, network, TLS, remote features,
// consent, environment detection), General Info (general settings and versions) and Network
// Time (the clock).
func permGroups() []permGroup {
	return []permGroup{
		{lang.L("Power control"), []amt.Realm{amt.RealmRemoteControl}},
		{lang.L("KVM, media, console"), []amt.Realm{amt.RealmRedirection}},
		{lang.L("Hardware"), []amt.Realm{amt.RealmHardwareAsset}},
		{lang.L("Event log"), []amt.Realm{amt.RealmEventLogReader}},
		{lang.L("Audit log"), []amt.Realm{amt.RealmAuditLog}},
		{lang.L("Settings"), []amt.Realm{amt.RealmPTAdministration, amt.RealmGeneralInfo, amt.RealmNetworkTime}},
	}
}

// hasRealms reports whether have contains every realm of want.
func hasRealms(have, want []amt.Realm) bool {
	for _, r := range want {
		if !slices.Contains(have, r) {
			return false
		}
	}
	return true
}

// permissionsText names the permission groups an account has completely, joined with ", ".
func permissionsText(realms []amt.Realm) string {
	var names []string
	for _, g := range permGroups() {
		if hasRealms(realms, g.realms) {
			names = append(names, g.label)
		}
	}
	return strings.Join(names, ", ")
}

// accessText is the Access column: Network, Local, Network and local.
func accessText(a amt.AccessPermission) string {
	switch a {
	case amt.AccessNetwork:
		return lang.L("Network")
	case amt.AccessLocal:
		return lang.L("Local")
	case amt.AccessLocalAndNetwork:
		return lang.L("Network and local")
	}
	return ""
}

// userRow is one formatted line of the Users table.
type userRow struct {
	name, status, access, perms string
	user                        *amt.User // nil for the admin row
	admin                       bool
	system                      bool // "$$" account: gray, no Edit
	signedIn                    bool // the account of this session: red asterisk, light red Edit
	editable                    bool // Edit is enabled
}

// userRows formats the admin account and the user ACL; login is the session's user name,
// compared without case as the backend does.
func userRows(l amt.UserList, login string) []userRow {
	var rows []userRow
	if l.Admin != "" {
		rows = append(rows, userRow{name: l.Admin, status: lang.L("Enabled"), access: accessText(amt.AccessLocalAndNetwork),
			perms: lang.L("Administrator"), admin: true, signedIn: strings.EqualFold(l.Admin, login), editable: true})
	}
	for _, u := range l.Users {
		r := userRow{name: u.Name, access: accessText(u.Access), perms: permissionsText(u.Realms), user: &u,
			signedIn: u.Self, editable: u.Name != "" && !u.System}
		if u.Name == "" {
			r.name = u.KerberosSID
		}
		switch {
		case u.System:
			r.status, r.perms, r.system = lang.L("System"), lang.L("System account"), true
		case !u.EnabledKnown:
		case u.Enabled:
			r.status = lang.L("Enabled")
		default:
			r.status = lang.L("Disabled")
		}
		rows = append(rows, r)
	}
	return rows
}

// usersPane is the state of one view of the Users page.
type usersPane struct {
	m        *manager
	compact  bool
	list     *listPage
	table    *listTable
	add      *widgets.PrimaryButton
	footnote *widget.RichText
	err      *manageError

	// the dialogs last opened from this view (tests)
	adminDlg  *adminDialog
	userDlg   *userDialog
	deleteDlg *riskDialog
	ownDlg    *riskDialog
	notSaved  *notSavedDialog
}

func init() {
	registerManagePage(managePageDef{section: sectionMain, order: 50, icon: icons.Users, build: (*manager).usersPane,
		names: func() (string, string) { return lang.L("Users"), lang.L("Users") }})
}

// userColumns are Name, Status and Access (fixed widths), Permissions (the rest); Edit is the action.
func userColumns() []listColumn {
	return []listColumn{
		{title: lang.L("Name"), width: 200, compactWidth: 72, compactGrow: 88},
		{title: lang.L("Status"), width: 120, compactWidth: 56, compactGrow: 84},
		{title: lang.L("Access"), width: 180, compactWidth: 60, compactGrow: 84},
		{title: lang.L("Permissions")},
	}
}

// usersPane builds the Users page. The data (m.users) is shared by both views and read when
// the page is first shown; every write reads it again.
func (m *manager) usersPane(compact bool) *managePane {
	if m.users == nil {
		m.users = newManageSource(m, m.sess.Users)
	}
	p := &usersPane{m: m, compact: compact}
	p.add = newManageButton(lang.L("+ Add user"), p.openAdd)
	p.footnote = widget.NewRichText(cellSeg("*", style.ColorNameDanger, style.SizeNameSmallText, true),
		cellSeg(" "+lang.L("Signed-in account. Changing its name, password or permissions can lock you out of this device."),
			style.ColorNameTextSecondary, style.SizeNameSmallText, false))
	p.footnote.Wrapping = fyne.TextWrapWord

	// Users.dc.html: the footnote 4 px closer to the table (margin -4px) and aligned with it; the
	// rich text's own padding is taken back
	ip := theme.InnerPadding()
	p.list = m.newListPage(compact, listSpec{columns: userColumns(), action: &listAction{width: 90, compactWidth: 54},
		tools: []fyne.CanvasObject{p.add}, src: m.users,
		note: container.New(layout.NewCustomPaddedLayout(-4-ip, 0, -ip, 0), p.footnote)})
	p.table, p.err = p.list.table, p.list.err
	pane := p.list.pane
	pane.shown, pane.impl = m.users.ensure, p
	render := func() {
		var rows []userRow
		if m.users.loaded() {
			rows = userRows(m.users.val, m.sess.Config().Username)
		}
		lr := make([]listRow, len(rows))
		for i, r := range rows {
			lr[i] = p.listRow(r)
		}
		p.list.update(lr, m.users.loaded(), m.users.loading, m.users.err)
		signedIn := slices.ContainsFunc(rows, func(r userRow) bool { return r.signedIn })
		setShown(p.footnote, signedIn)
		pane.changed()
	}
	pane.close = m.users.subscribe(render)
	render()
	return pane
}

// listRow is row r of the table: four text cells and its Edit button.
func (p *usersPane) listRow(r userRow) listRow {
	size := theme.SizeNameText
	if p.compact {
		size = style.SizeNameSmallText
	}
	c := theme.ColorNameForeground
	if r.system {
		c = style.ColorNameTextSecondary
	}
	name := []widget.RichTextSegment{cellSeg(r.name, c, size, false)}
	if r.signedIn {
		name = append(name, cellSeg("*", style.ColorNameDanger, size, true))
	}
	cells := [][]widget.RichTextSegment{name}
	for _, s := range []string{r.status, r.access, r.perms} {
		cells = append(cells, []widget.RichTextSegment{cellSeg(s, c, size, false)})
	}
	b := newUserEditButton(p.compact, r.signedIn, func() { p.edit(r) })
	if !r.editable {
		b.Disable()
	}
	return listRow{cells: cells, action: b}
}

// window is the window the dialogs of this view open on.
func (p *usersPane) window() fyne.Window { return p.list.window() }

// edit is the Edit button of row r.
func (p *usersPane) edit(r userRow) {
	switch {
	case r.admin:
		p.adminDlg = p.m.showAdminDialog(p, r.name, r.signedIn)
	default:
		p.userDlg = p.m.showUserDialog(p, r.user)
	}
}

// openAdd is "+ Add user".
func (p *usersPane) openAdd() { p.userDlg = p.m.showUserDialog(p, nil) }

// cellSeg is an inline text segment in color c and size s.
func cellSeg(text string, c fyne.ThemeColorName, s fyne.ThemeSizeName, bold bool) *widget.TextSegment {
	return &widget.TextSegment{Text: text, Style: widget.RichTextStyle{
		Inline: true, ColorName: c, SizeName: s, TextStyle: fyne.TextStyle{Bold: bold},
	}}
}

// newUserEditButton is the Edit button of a row (Users.dc.html .btn): white with a gray border,
// light red for the signed-in account.
func newUserEditButton(compact, signedIn bool, tapped func()) *widgets.PrimaryButton {
	b := widgets.NewPrimaryButton(lang.L("Edit"), nil, tapped)
	b.Importance = widget.MediumImportance
	m := &widgets.ButtonMetrics{Height: 28, Radius: 8, PaddingH: 12, TextSize: 13, Regular: true,
		Neutral: style.ColorNameField, NeutralBorder: style.ColorNameControlBorder}
	if compact {
		m.Height, m.PaddingH, m.TextSize = 26, 10, 12
	}
	if signedIn {
		m.Neutral, m.NeutralBorder, m.NeutralText = style.ColorNameSignedInSurface, style.ColorNameSignedInBorder, style.ColorNameDangerZoneText
	}
	b.Metrics = m
	return b
}
