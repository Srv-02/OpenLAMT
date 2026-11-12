package ui

import (
	"image/color"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
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
			// Editing the own, non-admin account has no approved design yet (issue #32): its Edit
			// is disabled like that of a "$$" account.
			signedIn: u.Self, editable: u.Name != "" && !u.System && !u.Self}
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
	table    *userTable
	add      *widgets.PrimaryButton
	footnote *widget.RichText
	err      *manageError

	// the dialogs last opened from this view (tests)
	adminDlg  *adminDialog
	userDlg   *userDialog
	deleteDlg *riskDialog
	notSaved  *notSavedDialog
}

// usersPane builds the Users page. The data (m.users) is shared by both views and read when
// the page is first shown; every write reads it again.
func (m *manager) usersPane(compact bool) *managePane {
	if m.users == nil {
		m.users = newManageSource(m, m.sess.Users)
	}
	p := &usersPane{m: m, compact: compact, table: newUserTable(compact), err: newManageError()}
	p.add = newManageButton(lang.L("+ Add user"), p.openAdd)
	p.footnote = widget.NewRichText(cellSeg("*", style.ColorNameDanger, style.SizeNameSmallText, true),
		cellSeg(" "+lang.L("Signed-in account. Changing its name or password can lock you out of this device."),
			style.ColorNameTextSecondary, style.SizeNameSmallText, false))
	p.footnote.Wrapping = fyne.TextWrapWord

	// Users.dc.html: the footnote 4 px closer to the table (margin -4px) and aligned with it; the
	// rich text's own padding is taken back
	ip := theme.InnerPadding()
	objs := []fyne.CanvasObject{p.err.box, p.table, container.New(layout.NewCustomPaddedLayout(-4-ip, 0, -ip, 0), p.footnote)}
	if compact {
		objs = append([]fyne.CanvasObject{container.New(&rowLayout{gap: 8, fill: 0}, spacer(), p.add)}, objs...)
	}
	pane := &managePane{body: container.New(layout.NewCustomPaddedVBoxLayout(12), objs...), scroll: true,
		shown: m.users.ensure, impl: p}
	if !compact {
		pane.tools = p.add
	}
	p.table.onHeight = pane.changed
	render := func() {
		var rows []userRow
		if m.users.loaded() {
			rows = userRows(m.users.val, m.sess.Config().Username)
		}
		p.table.setRows(rows, p.edit)
		signedIn := slices.ContainsFunc(rows, func(r userRow) bool { return r.signedIn })
		setShown(p.footnote, signedIn)
		p.err.set(m.users.err)
		pane.changed()
	}
	pane.close = m.users.subscribe(render)
	render()
	return pane
}

// window is the window the dialogs of this view open on.
func (p *usersPane) window() fyne.Window {
	if !p.compact && p.m.win != nil {
		return p.m.win.win
	}
	return p.m.parent
}

// edit is the Edit button of row r.
func (p *usersPane) edit(r userRow) {
	switch {
	case r.admin:
		p.adminDlg = p.m.showAdminDialog(p, r.name, r.signedIn)
	case r.user.Self:
		// Edit is disabled for the own, non-admin account until it has a dialog (issue #32).
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

// userLine is one row of the table: four wrapping text cells and the Edit button.
type userLine struct {
	row   userRow
	cells [4]*widget.RichText
	edit  *widgets.PrimaryButton
}

// userTable is the Users table (Users.dc.html): a 1 px frame with radius 10, a gray header,
// hairlines between the rows. Name, Status and Access have fixed widths, Permissions takes the
// rest, Edit sits at the right; long texts wrap, so a row grows. The table reports a new
// height through onHeight after its width changed the wrapping.
type userTable struct {
	widget.BaseWidget
	compact  bool
	lines    []*userLine
	words    [3]float32 // longest word of Name, Status and Access (header included)
	onHeight func()

	width    float32 // width of the last layout; 0 before the first one
	reported float32 // height MinSize returned last
}

func newUserTable(compact bool) *userTable {
	t := &userTable{compact: compact}
	t.ExtendBaseWidget(t)
	return t
}

// setRows replaces the rows; edit runs for the Edit button of a row.
func (t *userTable) setRows(rows []userRow, edit func(userRow)) {
	size := theme.SizeNameText
	if t.compact {
		size = style.SizeNameSmallText
	}
	t.lines = nil
	ts := style.Size(size)
	t.words = [3]float32{
		longestWord(12, true, lang.L("Name")), longestWord(12, true, lang.L("Status")), longestWord(12, true, lang.L("Access")),
	}
	for _, r := range rows {
		name := r.name
		if r.signedIn {
			name += "*"
		}
		t.words[0] = max(t.words[0], longestWord(ts, false, name))
		t.words[1] = max(t.words[1], longestWord(ts, false, r.status))
		t.words[2] = max(t.words[2], longestWord(ts, false, r.access))
	}
	for _, r := range rows {
		c := theme.ColorNameForeground
		if r.system {
			c = style.ColorNameTextSecondary
		}
		l := &userLine{row: r}
		name := []widget.RichTextSegment{cellSeg(r.name, c, size, false)}
		if r.signedIn {
			name = append(name, cellSeg("*", style.ColorNameDanger, size, true))
		}
		l.cells[0] = widget.NewRichText(name...)
		for i, s := range []string{r.status, r.access, r.perms} {
			l.cells[i+1] = widget.NewRichText(cellSeg(s, c, size, false))
		}
		for _, cell := range l.cells {
			cell.Wrapping = fyne.TextWrapWord
		}
		l.edit = newUserEditButton(t.compact, r.signedIn, func() { edit(r) })
		if !r.editable {
			l.edit.Disable()
		}
		t.lines = append(t.lines, l)
	}
	t.Refresh()
}

// Geometry (Users.dc.html .th/.td): header 8 x 12, cells 9 x 12, 13 px text; in the sidebar
// 7 x 6 and 12 px. Fixed columns in the window: Name 200, Status 120, Access 180, Edit 90. In
// the sidebar Name, Status and Access are at least 72, 56 and 60 wide and grow up to 88, 84
// and 84 so that their longest word does not break (a long name still does); Edit is 54.
func (t *userTable) pads() (padV, padH, headV float32) {
	if t.compact {
		return 7, 6, 7
	}
	return 9, 12, 8
}

func (t *userTable) fixed() (name, status, access, edit float32) {
	if t.compact {
		return 72, 56, 60, 54
	}
	return 200, 120, 180, 90
}

// columns returns the widths of Name, Status, Access, Permissions and Edit for an inner width.
func (t *userTable) columns(inner float32) [5]float32 {
	n, s, a, e := t.fixed()
	if t.compact {
		_, padH, _ := t.pads()
		fit := func(i int, lo, hi float32) float32 { return min(hi, max(lo, t.words[i]+2*padH+1)) }
		n, s, a = fit(0, n, 88), fit(1, s, 84), fit(2, a, 84)
	}
	return [5]float32{n, s, a, max(48, inner-n-s-a-e), e}
}

// longestWord is the width of the longest word of texts at size.
func longestWord(size float32, bold bool, texts ...string) float32 {
	var w float32
	for _, s := range texts {
		for _, word := range strings.Fields(s) {
			w = max(w, fyne.MeasureText(word, size, fyne.TextStyle{Bold: bold}).Width)
		}
	}
	return w
}

func (t *userTable) headHeight() float32 {
	_, _, headV := t.pads()
	return fyne.MeasureText("Mg", 12, fyne.TextStyle{Bold: true}).Height + 2*headV
}

// rowHeight wraps the cells of l at the column widths and returns the row height.
func (t *userTable) rowHeight(l *userLine, cols [5]float32) float32 {
	padV, padH, _ := t.pads()
	h := l.edit.MinSize().Height
	for i, c := range l.cells {
		ip := c.Theme().Size(theme.SizeNameInnerPadding)
		w := max(0, cols[i]-2*padH) + 2*ip
		for range 2 {
			c.Resize(fyne.NewSize(w, c.MinSize().Height))
		}
		h = max(h, c.MinSize().Height-2*ip)
	}
	return h + 2*padV
}

// height is the full height of the table at width.
func (t *userTable) height(width float32) float32 {
	cols := t.columns(width - 2)
	h := 1 + t.headHeight() + 1 + 1 // frame, header, line under it, frame
	for i, l := range t.lines {
		if i > 0 {
			h++
		}
		h += t.rowHeight(l, cols)
	}
	return h
}

// defaultWidth is assumed before the first layout: the sidebar's content width, or a window.
func (t *userTable) defaultWidth() float32 {
	if t.compact {
		return manageSidebarWidth - 2*16
	}
	return 800
}

func (t *userTable) CreateRenderer() fyne.WidgetRenderer {
	r := &userTableRenderer{t: t,
		bg:       style.BindRect(&canvas.Rectangle{CornerRadius: 10}, "background", ""),
		outline:  style.BindRect(&canvas.Rectangle{CornerRadius: 10, StrokeWidth: 1, FillColor: color.Transparent}, "", style.ColorNameCardBorder),
		headBg:   style.BindRect(&canvas.Rectangle{TopLeftCornerRadius: 9, TopRightCornerRadius: 9}, style.ColorNameTableHeader, ""),
		headLine: style.BindRect(canvas.NewRectangle(nil), style.ColorNameCardBorder, ""),
	}
	r.names = [4]string{lang.L("Name"), lang.L("Status"), lang.L("Access"), lang.L("Permissions")}
	for i, s := range r.names {
		r.head[i] = designText(s, 12, style.ColorNameTextSecondary, true)
	}
	r.Refresh()
	return r
}

type userTableRenderer struct {
	t                   *userTable
	bg, outline, headBg *canvas.Rectangle
	headLine            *canvas.Rectangle
	names               [4]string
	head                [4]*canvas.Text
	seps                []*canvas.Rectangle
	objects             []fyne.CanvasObject
	inHeightCall        bool // onHeight runs; a layout it causes does not report again
}

func (r *userTableRenderer) MinSize() fyne.Size {
	t := r.t
	w := t.width
	if w <= 0 {
		w = t.defaultWidth()
	}
	t.reported = t.height(w)
	n, s, a, e := t.fixed()
	return fyne.NewSize(n+s+a+e+48+2, t.reported)
}

func (r *userTableRenderer) Layout(size fyne.Size) {
	t := r.t
	t.width = size.Width
	r.bg.Resize(size)
	r.outline.Resize(size)
	_, padH, headV := t.pads()
	inner := size.Width - 2
	cols := t.columns(inner)
	headH := t.headHeight()
	r.headBg.Move(fyne.NewPos(1, 1))
	r.headBg.Resize(fyne.NewSize(inner, headH))
	x := float32(1)
	for i, h := range r.head {
		h.Text = ellipsize(r.names[i], 12, h.TextStyle, max(0, cols[i]-2*padH))
		h.Move(fyne.NewPos(x+padH, 1+headV))
		h.Resize(h.MinSize())
		x += cols[i]
	}
	r.headLine.Move(fyne.NewPos(1, 1+headH))
	r.headLine.Resize(fyne.NewSize(inner, 1))
	y := 1 + headH + 1
	for i, l := range t.lines {
		if i > 0 {
			s := r.seps[i-1]
			s.Move(fyne.NewPos(1, y))
			s.Resize(fyne.NewSize(inner, 1))
			y++
		}
		rh := t.rowHeight(l, cols)
		x := float32(1)
		for c, cell := range l.cells {
			ip := cell.Theme().Size(theme.SizeNameInnerPadding)
			ms := cell.MinSize()
			cell.Move(fyne.NewPos(x+padH-ip, y+(rh-ms.Height)/2))
			cell.Resize(fyne.NewSize(max(0, cols[c]-2*padH)+2*ip, ms.Height))
			x += cols[c]
		}
		bs := l.edit.MinSize()
		l.edit.Move(fyne.NewPos(x+cols[4]-padH-bs.Width, y+(rh-bs.Height)/2))
		l.edit.Resize(bs)
		y += rh
	}
	total := y + 1
	if total != t.reported && !r.inHeightCall {
		t.reported = total
		if t.onHeight != nil {
			r.inHeightCall = true
			t.onHeight()
			r.inHeightCall = false
		}
	}
}

func (r *userTableRenderer) Refresh() {
	t := r.t
	for len(r.seps) < max(0, len(t.lines)-1) {
		r.seps = append(r.seps, style.BindRect(canvas.NewRectangle(nil), style.ColorNameCardSeparator, ""))
	}
	r.objects = []fyne.CanvasObject{r.bg, r.headBg, r.headLine}
	for _, h := range r.head {
		r.objects = append(r.objects, h)
	}
	for i, l := range t.lines {
		if i > 0 {
			r.objects = append(r.objects, r.seps[i-1])
		}
		for _, c := range l.cells {
			r.objects = append(r.objects, c)
		}
		r.objects = append(r.objects, l.edit)
	}
	r.objects = append(r.objects, r.outline)
	r.Layout(t.Size())
	canvas.Refresh(t)
}

func (r *userTableRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *userTableRenderer) Destroy()                     {}
