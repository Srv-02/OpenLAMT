package ui

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The Power page of device management (overall design 2026-11-23, issues #14 and #27): the power
// package, the idle timeout and what the device supports of sleep and hibernate in a card, below
// it the wake alarms as a list with "+ Add Alarm". Dialogs: Add wake alarm (V24: local time),
// Delete wake alarm (simple), Change the power package (V21: orange when AMT turns off in S4-S5)
// and the idle timeout.

// powerData is the data of the Power page for one device, shared by the sidebar and the window.
type powerData struct {
	policy *manageSource[amt.PowerPolicy]
	alarms *manageSource[[]amt.Alarm]
	states *manageSource[[]power.PowerState]
}

func (m *manager) powerData() *powerData {
	if m.power == nil {
		m.power = &powerData{
			policy: newManageSource(m, m.sess.PowerPolicy),
			alarms: newManageSource(m, m.sess.Alarms),
			states: newManageSource(m, m.sess.PowerStates),
		}
	}
	return m.power
}

func (d *powerData) ensure() {
	d.policy.ensure()
	d.alarms.ensure()
	d.states.ensure()
}

func (d *powerData) reload() {
	d.policy.reload()
	d.alarms.reload()
	d.states.reload()
}

// loading reports whether a read runs.
func (d *powerData) loading() bool { return d.policy.loading || d.alarms.loading || d.states.loading }

func init() {
	registerManagePage(managePageDef{section: sectionMore, order: 10, icon: icons.Power, build: (*manager).powerPane,
		names: func() (string, string) { return lang.L("Power"), lang.L("Power") }})
}

// powerPane is one view of the Power page.
type powerPane struct {
	m       *manager
	compact bool
	d       *powerData
	list    *listPage
	table   *listTable
	add     *widgets.PrimaryButton
	err     *manageError

	pkg       *widgets.Dropdown
	pkgNone   *canvas.Text // "<device> reports no power packages."
	pkgRow    *settingsRowRef
	pkgBox    *fyne.Container // the right side of the package row
	idleText  *canvas.Text
	idleBtn   *widgets.PrimaryButton
	statesTxt *canvas.Text
	note      *widget.RichText
	card      *settingsCard
	pkgIDs    []string // schemes behind pkg.Options
	applying  bool     // render sets the dropdown: its callback does nothing

	// the dialogs last opened from this view (tests)
	addDlg    *alarmDialog
	deleteDlg *widget.PopUp
	policyDlg *riskDialog
	idleDlg   *idleDialog
}

// powerPane builds the Power page.
func (m *manager) powerPane(compact bool) *managePane {
	d := m.powerData()
	p := &powerPane{m: m, compact: compact, d: d}
	p.add = newManageButton(lang.L("+ Add Alarm"), p.openAdd)
	if compact {
		p.add.Metrics.Height, p.add.Metrics.PaddingH, p.add.Metrics.TextSize = 26, 10, 12
	}

	p.pkg = widgets.NewDropdown(nil)
	p.pkg.OnChanged = func(string) {
		if !p.applying {
			p.choosePackage()
		}
	}
	p.pkgNone = designText(lang.L("{{.Name}} reports no power packages.", map[string]any{"Name": m.name}), 13, style.ColorNameTextSecondary, false)
	p.pkgBox = container.NewStack(p.pkg, p.pkgNone)
	p.idleText = designText("", 13, theme.ColorNameForeground, false)
	p.idleBtn = settingsButton(lang.L("Change…"), p.openIdle)
	p.statesTxt = designText("", 13, theme.ColorNameForeground, false)
	p.card = newSettingsCard(
		p.row(lang.L("Power package"), p.pkgHelp(), p.pkgBox),
		p.row(lang.L("Idle timeout"), p.idleHelp(), container.NewHBox(p.idleText, p.idleBtn)),
		p.row(lang.L("Sleep and hibernate"), nil, p.statesTxt))
	p.pkgRow = p.card.ref(0)

	p.note = widget.NewRichText(cellSeg("", style.ColorNameTextSecondary, style.SizeNameSmallText, false))
	p.note.Wrapping = fyne.TextWrapWord
	ip := theme.InnerPadding()
	p.list = m.newListPage(compact, listSpec{
		columns: p.columns(), action: &listAction{width: 100, compactWidth: 74},
		refresh: d.reload, src: d.alarms,
		emptyTitle: lang.L("No wake alarms"),
		emptyText:  lang.L("Add an alarm to power on {{.Name}} at a set time, for example for backups.", map[string]any{"Name": m.name}),
		note:       container.New(layout.NewCustomPaddedLayout(-ip, 0, -ip, 0), p.note),
	})
	p.table, p.err = p.list.table, p.list.err
	head := container.New(&rowLayout{gap: 8, fill: 0}, designText(lang.L("Wake alarms"), 12, style.ColorNameTextSecondary, true), spacer(), p.add)
	pane := p.list.pane
	pane.body = container.New(layout.NewCustomPaddedVBoxLayout(12),
		powerHeading(lang.L("Power policy")), p.card.box, head, pane.body)
	pane.shown, pane.impl = d.ensure, p
	render := func() { p.render() }
	cancel := []func(){d.policy.subscribe(render), d.alarms.subscribe(render), d.states.subscribe(render)}
	pane.close = func() {
		for _, c := range cancel {
			c()
		}
	}
	render()
	return pane
}

// columns are Name and Next wake (fixed widths), Repeat (the rest); Delete… is the action.
func (p *powerPane) columns() []listColumn {
	return []listColumn{
		{title: lang.L("Name"), width: 220, compactWidth: 76, compactGrow: 110},
		{title: lang.L("Next wake"), width: 170, compactWidth: 82, compactGrow: 112},
		{title: lang.L("Repeat")},
	}
}

func powerHeading(text string) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(0, 0, 4, 0), designText(text, 12, style.ColorNameTextSecondary, true))
}

// row is a card row: the label with an optional info button, and the control on the right.
func (p *powerPane) row(label string, help *widgets.InfoButton, right fyne.CanvasObject) fyne.CanvasObject {
	var left fyne.CanvasObject = designText(label, 13, style.ColorNameControlText, false)
	if help != nil {
		left = container.NewHBox(left, help)
	}
	return container.New(settingsRowLayout{}, left, right)
}

func (p *powerPane) pkgHelp() *widgets.InfoButton {
	return widgets.NewInfoButton(widgets.HelpContent{
		Title:   lang.L("Power package"),
		What:    lang.L("Decides when Intel AMT stays on: only while the computer runs, or also while it sleeps or is switched off."),
		When:    lang.L("Change it when you want to power the computer on remotely, or when AMT should not stay on."),
		Wrong:   lang.L("A package that turns AMT off while the computer sleeps or is switched off means you cannot power it on remotely."),
		Recover: lang.L("Switch the computer on at the device, then choose another package."),
	})
}

func (p *powerPane) idleHelp() *widgets.InfoButton {
	return widgets.NewInfoButton(widgets.HelpContent{
		Title: lang.L("Idle timeout"),
		What:  lang.L("Minutes after which the management engine goes to power saving. The timer starts again whenever AMT serves a request. Some power packages ignore it."),
		When:  lang.L("Raise it if AMT turns off before you finish working. Lower it to save power."),
	})
}

// render shows the sources in the card, the list and the footer line.
func (p *powerPane) render() {
	d := p.d
	// package row
	pol := d.policy.val
	p.applying = true
	if d.policy.loaded() && len(pol.Schemes) == 0 {
		p.pkg.Hide()
		p.pkgNone.Show()
	} else {
		p.pkgNone.Hide()
		p.pkg.Show()
		p.pkgIDs = p.pkgIDs[:0]
		var opts []string
		for _, s := range pol.Schemes {
			opts = append(opts, schemeLabel(s))
			p.pkgIDs = append(p.pkgIDs, s.ID)
		}
		p.pkg.Options = opts
		p.pkg.Selected = ""
		if i := slices.Index(p.pkgIDs, pol.Current); i >= 0 {
			p.pkg.Selected = opts[i]
		}
		p.pkg.Refresh()
	}
	p.applying = false
	p.pkgBox.Refresh()
	p.idleText.Text = ""
	if d.policy.loaded() {
		p.idleText.Text = idleTimeoutText(pol.IdleTimeoutMinutes)
	}
	p.idleText.Refresh()
	setEnabled(p.idleBtn, d.policy.loaded() && !d.policy.loading)
	p.statesTxt.Text = ""
	if d.states.loaded() {
		p.statesTxt.Text = supportText(d.states.val)
	}
	p.statesTxt.Refresh()
	p.card.sync()

	// alarms
	rows := make([]listRow, 0, len(d.alarms.val))
	loc := p.m.now().Location()
	for _, a := range d.alarms.val {
		rows = append(rows, p.alarmRow(a, loc))
	}
	err := d.alarms.err
	if err == nil {
		err = d.policy.err
	}
	p.list.update(rows, d.alarms.loaded(), d.loading(), err)
	zone, off := p.m.now().Zone()
	p.note.Segments = []widget.RichTextSegment{cellSeg(zoneNote(zone, off), style.ColorNameTextSecondary, style.SizeNameSmallText, false)}
	p.note.Refresh()
	p.list.pane.changed()
}

// schemeLabel is the text of a power package in the dropdown.
func schemeLabel(s amt.PowerScheme) string {
	if s.Description != "" {
		return s.Description
	}
	return s.ID
}

// idleTimeoutText is "1 minute" or "30 minutes".
func idleTimeoutText(minutes int) string {
	if minutes == 1 {
		return lang.L("1 minute")
	}
	return lang.L("{{.Count}} minutes", map[string]any{"Count": minutes})
}

// supportText is "Sleep supported · Hibernate not supported".
func supportText(states []power.PowerState) string {
	sleep, hib := lang.L("Sleep not supported"), lang.L("Hibernate not supported")
	if slices.Contains(states, power.SleepDeep) {
		sleep = lang.L("Sleep supported")
	}
	if slices.Contains(states, power.Hibernate) {
		hib = lang.L("Hibernate supported")
	}
	return sleep + " · " + hib
}

// zoneNote is the line under the list: the zone the times are shown and typed in.
func zoneNote(zone string, offset int) string {
	sign, off := "+", offset
	if off < 0 {
		sign, off = "-", -off
	}
	return lang.L("Times in this computer’s time zone ({{.Zone}}, UTC{{.Offset}}). Intel AMT stores them in UTC.",
		map[string]any{"Zone": zone, "Offset": sign + twoDigits(off/3600) + ":" + twoDigits(off%3600/60)})
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// repeatText is the Repeat column: "Once", "Once, then deleted", "Every day", "Every 28 days".
func repeatText(a amt.Alarm) string {
	if a.Interval <= 0 {
		if a.DeleteOnCompletion {
			return lang.L("Once, then deleted")
		}
		return lang.L("Once")
	}
	mins := int(a.Interval / time.Minute)
	n := func(key string, count int) string { return lang.L(key, map[string]any{"Count": count}) }
	switch {
	case mins == 24*60:
		return lang.L("Every day")
	case mins == 7*24*60:
		return lang.L("Every week")
	case mins == 60:
		return lang.L("Every hour")
	case mins == 1:
		return lang.L("Every minute")
	case mins%(24*60) == 0:
		return n("Every {{.Count}} days", mins/(24*60))
	case mins%60 == 0:
		return n("Every {{.Count}} hours", mins/60)
	}
	return n("Every {{.Count}} minutes", mins)
}

// wakeText is the Next wake column in the local zone.
func wakeText(t time.Time, loc *time.Location, compact bool) string {
	if t.IsZero() {
		return "–"
	}
	if compact {
		return t.In(loc).Format("Jan 02 15:04")
	}
	return t.In(loc).Format("Jan 02, 2006 15:04")
}

// alarmRow is one line of the list: Name, Next wake, Repeat and its Delete… button.
func (p *powerPane) alarmRow(a amt.Alarm, loc *time.Location) listRow {
	size := theme.SizeNameText
	if p.compact {
		size = style.SizeNameSmallText
	}
	cell := func(s string) []widget.RichTextSegment {
		return []widget.RichTextSegment{cellSeg(s, theme.ColorNameForeground, size, false)}
	}
	return listRow{
		cells:  [][]widget.RichTextSegment{cell(a.Name), cell(wakeText(a.Start, loc, p.compact)), cell(repeatText(a))},
		action: newRowButton(lang.L("Delete…"), p.compact, func() { p.confirmDelete(a) }),
	}
}

// newRowButton is the white bordered button at the end of a list row (the Edit button of Users).
func newRowButton(text string, compact bool, tapped func()) *widgets.PrimaryButton {
	b := widgets.NewPrimaryButton(text, nil, tapped)
	b.Importance = widget.MediumImportance
	m := &widgets.ButtonMetrics{Height: 28, Radius: 8, PaddingH: 12, TextSize: 13, Regular: true,
		Neutral: style.ColorNameField, NeutralBorder: style.ColorNameControlBorder}
	if compact {
		m.Height, m.PaddingH, m.TextSize = 26, 10, 12
	}
	b.Metrics = m
	return b
}

// window is the window the dialogs of this view open on.
func (p *powerPane) window() fyne.Window { return p.list.window() }

// confirmDelete asks before a wake alarm is deleted (simple confirmation, OpAlarmDelete).
func (p *powerPane) confirmDelete(a amt.Alarm) {
	p.deleteDlg = showConfirm(p.window(), icons.Delete,
		lang.L("Delete “{{.Name}}”?", map[string]any{"Name": a.Name}),
		lang.L("{{.Name}} no longer powers on at this time.", map[string]any{"Name": p.m.name}),
		lang.L("Delete"), func() { p.list.write(func() error { return p.m.sess.DeleteAlarm(a.Name) }) }, nil)
}

// choosePackage reacts to a new choice in the dropdown: a change that keeps the remote start
// is written at once, one that turns AMT off in S4-S5 asks first (orange).
func (p *powerPane) choosePackage() {
	i := p.pkg.SelectedIndex()
	pol := p.d.policy.val
	if i < 0 || i >= len(p.pkgIDs) || p.pkgIDs[i] == pol.Current {
		return
	}
	id := p.pkgIDs[i]
	to, _ := pol.Scheme(id)
	apply := func(c amt.Confirmation) {
		manageWrite(p.m, p.d.policy, func() error { return p.m.sess.SetPowerScheme(id, c) }, func(err error) {
			if err != nil {
				showError(p.window(), err)
			}
		}, nil)
	}
	if !pol.RisksRemoteStart(id) {
		apply(amt.Confirmation{})
		return
	}
	cur, _ := pol.CurrentScheme()
	oldText := lang.L("Unknown")
	if cur.ID != "" {
		oldText = schemeLabel(cur)
	}
	p.policyDlg = showOrangeConfirm(p.window(), orangeSpec{
		op: amt.OpPowerPolicy, host: p.m.confirmHost(), device: p.m.confirmDevice(),
		title: lang.L("Change the power package?"),
		warning: lang.L("With this package, Intel AMT is off while {{.Name}} sleeps or is switched off. You cannot power it on remotely until someone switches it on at the device.",
			map[string]any{"Name": p.m.name}),
		oldValue: oldText, newValue: schemeLabel(to),
		button:    lang.L("Change Package"),
		onConfirm: apply,
		onCancel:  p.render, // the dropdown shows the device's package again
	})
}

// idleDialog is the dialog of "Change…" for the idle timeout.
type idleDialog struct {
	*panel
	field *dialogEntry
}

// parseIdle reads the minutes of the field; ok only for MinIdleTimeout to MaxIdleTimeout.
func parseIdle(text string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	return n, err == nil && n >= amt.MinIdleTimeout && n <= amt.MaxIdleTimeout
}

// openIdle shows the dialog with a number field; Save needs a valid, new value.
func (p *powerPane) openIdle() {
	cur := p.d.policy.val.IdleTimeoutMinutes
	d := &idleDialog{}
	validate := func() {
		n, ok := parseIdle(d.field.Text)
		if ok && n != cur {
			d.action.Enable()
		} else {
			d.action.Disable()
		}
	}
	d.panel = showPanel(p.window(), panelSpec{
		title: lang.L("Idle timeout"), action: lang.L("Save"), cancel: lang.L("Cancel"), flush: true,
		onAction: func() {
			n, _ := parseIdle(d.field.Text)
			manageWrite(p.m, p.d.policy, func() error { return p.m.sess.SetIdleTimeout(n) }, func(err error) {
				if err != nil {
					showError(p.window(), err)
				}
			}, nil)
		},
	}, func(pn *panel) []fyne.CanvasObject {
		d.panel = pn
		d.field = pn.newEntry(false)
		d.field.SetText(strconv.Itoa(cur))
		d.field.OnChanged = func(string) { validate() }
		pn.focus = d.field
		return []fyne.CanvasObject{
			formField(lang.L("Minutes"), framedEntry(d.field)),
			formHint(lang.L("Between 1 and 65535 minutes.")),
		}
	})
	validate()
	p.idleDlg = d
}

// alarmErrorText is the text of a refused wake alarm (design: "Fehler beim Anlegen").
func (m *manager) alarmErrorText(err error) string {
	switch {
	case errors.Is(err, amt.ErrAlarmsFull):
		return lang.L("{{.Name}} stores no more alarms. Delete one first.", map[string]any{"Name": m.name})
	case errors.Is(err, amt.ErrInvalidValue):
		var ve *amt.ValidationError
		if errors.As(err, &ve) && ve.Field == "Name" {
			return lang.L("Enter a name of up to {{.Count}} characters.", map[string]any{"Count": amt.MaxAlarmName})
		}
	}
	return describeManage(err)
}
