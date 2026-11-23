package ui

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Audit log settings (issue #15, overall design V3): "Settings…" in the head of the Audit log page
// opens a dialog with the recording switch, the storage policy, the recorded event groups and
// "Clear Log…". Switching the recording off asks the orange confirmation (amt.OpAuditSettings),
// clearing the red one (amt.OpAuditClear). The design's "Pause…" is left out: AMT's audit lock
// holds at most 5 minutes and refuses critical actions meanwhile; it is used only inside the clear.

// auditGroup is one checkbox of "Recorded events" and the AMT audit applications it stands for.
// Applications outside every group (23 Storage Administration) keep their policy.
type auditGroup struct {
	label string
	apps  []int
}

// auditGroups are the twelve groups of the design, in its order (two columns, row by row).
func auditGroups() []auditGroup {
	return []auditGroup{
		{lang.L("Accounts and security"), []int{16, 28}},               // Security Admin, Endpoint Access Control
		{lang.L("Power and boot (remote control)"), []int{17}},         // Remote Control
		{lang.L("KVM, media and console sessions"), []int{18, 29, 32}}, // Redirection Manager, KVM, Screen Blanking
		{lang.L("Firmware updates"), []int{19}},
		{lang.L("Network settings"), []int{22}},
		{lang.L("Clock"), []int{21}},
		{lang.L("Event forwarding"), []int{24}},   // Event Manager
		{lang.L("System Defense"), []int{25}},     // System Defense Manager
		{lang.L("Agent watchdog"), []int{26, 33}}, // Agent Presence Manager, Watchdog
		{lang.L("Wi-Fi"), []int{27}},              // Wireless Configuration
		{lang.L("User consent"), []int{30}},       // User Opt-In
		{lang.L("Audit log changes"), []int{20}},  // Security Audit Log
	}
}

// groupRecorded reports whether the policy records any event of g: AMT's defaults record only
// some events of a group (Wi-Fi), and such a group still counts as recorded.
func groupRecorded(s amt.AuditSettings, g auditGroup) bool {
	return slices.ContainsFunc(s.Policy, func(e amt.AuditEvent) bool { return slices.Contains(g.apps, e.App) })
}

// storageOrder maps the index of "When the log is full" to the storage policy.
var storageOrder = []amt.AuditStorage{amt.AuditOverwriteOldest, amt.AuditOverwriteAfterDays, amt.AuditStopWhenFull}

// storageChoices are the options of "When the log is full", in the order of storageOrder.
func storageChoices() []string {
	return []string{lang.L("Overwrite oldest entries"), lang.L("Overwrite after a minimum age"), lang.L("Stop recording")}
}

// logDialogs are the dialogs last opened from the log pages (tests).
type logDialogs struct {
	audit      *auditSettingsDialog
	denied     *panel
	forwarding *forwardingDialog
	target     *targetDialog
	deleteAsk  *widget.PopUp
}

// auditSettingsButton is "Settings…" of the Audit log page head.
func (m *manager) auditSettingsButton(win func() fyne.Window) *widgets.PrimaryButton {
	var b *widgets.PrimaryButton
	b = newManageButton(lang.L("Settings…"), func() { m.openAuditSettings(win(), b) })
	return b
}

// openAuditSettings reads the settings (the button waits meanwhile) and shows the dialog, or the
// permission dialog or the error.
func (m *manager) openAuditSettings(w fyne.Window, b *widgets.PrimaryButton) {
	type result struct {
		s   amt.AuditSettings
		err error
	}
	b.Disable()
	manageWrite(m, nil, func() result {
		s, err := m.sess.AuditSettings()
		return result{s, err}
	}, func(r result) {
		b.Enable()
		if r.err != nil {
			m.auditFailed(w, r.err, false)
			return
		}
		m.logDlg.audit = m.showAuditSettings(w, r.s)
	}, nil)
}

// auditFailed shows a missing permission as "No permission for audit settings", any other error
// in the error dialog. write tells a refused change from a refused read of the settings.
func (m *manager) auditFailed(w fyne.Window, err error, write bool) {
	if errors.Is(err, amt.ErrAccessDenied) {
		m.logDlg.denied = showAuditDenied(w, m.sess.Config().Username, write)
		return
	}
	showError(w, err)
}

// showAuditDenied is the error dialog of an account without the audit log permission. A refused
// write says the account may read the log; a refused read of the settings knows no more than
// the refusal.
func showAuditDenied(w fyne.Window, user string, write bool) *panel {
	return showPanel(w, panelSpec{icon: icons.Lock, tone: toneDanger, title: lang.L("No permission for audit settings"), action: lang.L("OK")},
		func(p *panel) []fyne.CanvasObject {
			text := lang.L("The device does not let {{.User}} read the audit log settings. Sign in with an account that has the Audit log permission.", map[string]any{"User": user})
			if write {
				text = lang.L("The account {{.User}} may read the audit log but not change its settings. Sign in with an account that has the Audit log permission.", map[string]any{"User": user})
			}
			msg := widget.NewLabel(text)
			msg.Wrapping = fyne.TextWrapWord
			return []fyne.CanvasObject{msg}
		})
}

// auditSettingsDialog is the shown "Audit log settings".
type auditSettingsDialog struct {
	*panel
	cur     amt.AuditSettings
	record  *widgets.Switch
	storage *widgets.Dropdown
	days    *dialogEntry
	daysRow *settingsRowRef
	card    *settingsCard
	space   fyne.CanvasObject
	checks  []*dialogCheck
	initial []bool
	clear   *widgets.PrimaryButton

	confirm  *riskDialog // the orange "stop recording" or the red "clear" confirmation (tests)
	cleared  bool
	lastSave *amt.AuditChange
}

// auditInfoRow is a card row whose label has the ⓘ help.
func auditInfoRow(label string, help widgets.HelpContent, right fyne.CanvasObject) fyne.CanvasObject {
	left := container.New(&rowLayout{gap: 6, fill: -1}, designText(label, 13, style.ColorNameControlText, false), widgets.NewInfoButton(help))
	return container.New(settingsRowLayout{}, left, right)
}

// showAuditSettings shows the dialog for the settings s.
func (m *manager) showAuditSettings(w fyne.Window, s amt.AuditSettings) *auditSettingsDialog {
	d := &auditSettingsDialog{cur: s}
	d.panel = showPanel(w, panelSpec{
		title: lang.L("Audit log settings"), action: lang.L("Save"), cancel: lang.L("Cancel"), flush: true, width: 560,
		onAction: func() { m.saveAuditSettings(w, d) },
	}, func(p *panel) []fyne.CanvasObject {
		d.panel = p
		d.record = widgets.NewSwitch(func(bool) { d.validate() })
		d.record.SetChecked(s.Recording)
		d.record.OnEscape = p.Dismiss
		d.storage = widgets.NewDropdown(storageChoices())
		d.storage.SetSelectedIndex(max(0, slices.Index(storageOrder, s.Storage)))
		d.storage.OnEscape = p.Dismiss
		d.days = p.newEntry(false)
		if s.MinDaysToKeep > 0 {
			d.days.SetText(strconv.Itoa(s.MinDaysToKeep))
		}
		d.days.OnChanged = func(string) { d.validate() }
		days := container.New(&manageSizeLayout{w: 64}, framedEntry(d.days))
		space := lang.L("{{.Count}} entries · {{.Percent}} %", map[string]any{"Count": s.Records, "Percent": s.PercentUsed})
		d.card = newSettingsCard(
			auditInfoRow(lang.L("Record events"), widgets.HelpContent{
				Title:   lang.L("Record events"),
				What:    lang.L("Intel AMT writes an entry for each recorded event: who did what, from where and when."),
				When:    lang.L("Leave it on. Switch it off only for a short test."),
				Wrong:   lang.L("While it is off, changes to the device leave no trace in the audit log. A few events are always recorded."),
				Recover: lang.L("Switch it on again."),
			}, d.record),
			auditInfoRow(lang.L("When the log is full"), widgets.HelpContent{
				Title:   lang.L("When the log is full"),
				What:    lang.L("Decides what Intel AMT does when the audit log has no free space."),
				When:    lang.L("Overwrite oldest entries keeps recording. Stop recording keeps every entry until you clear the log."),
				Wrong:   lang.L("With Stop recording, Intel AMT blocks critical actions while the log is full."),
				Recover: lang.L("Clear the log or choose an option that overwrites entries."),
			}, d.storage),
			amtSettingsRow(lang.L("Days to keep"), days),
			amtSettingsRow(lang.L("Space used"), designText(space, 13, style.ColorNameTextSecondary, false)),
		)
		d.daysRow = d.card.ref(2)
		d.storage.OnChanged = func(string) { d.syncDays(); d.validate() }
		d.syncDays()

		var checks []fyne.CanvasObject
		for _, g := range auditGroups() {
			on := groupRecorded(s, g)
			c := p.newCheck(g.label, on)
			c.OnChanged = func(bool) { d.validate() }
			d.checks = append(d.checks, c)
			d.initial = append(d.initial, on)
			checks = append(checks, c)
		}

		d.clear = widgets.NewPrimaryButton(lang.L("Clear Log…"), nil, func() { m.confirmAuditClear(w, d) })
		d.clear.Importance = widget.DangerImportance
		d.clear.Metrics = &widgets.ButtonMetrics{Height: 26, Radius: 7, PaddingH: 12, TextSize: 13}
		clearText := container.New(layout.NewCustomPaddedVBoxLayout(2),
			designText(lang.L("Clear the log"), 13, style.ColorNameControlText, false),
			designText(lang.L("Deletes all {{.Count}} entries.", map[string]any{"Count": s.Records}), 12, style.ColorNameTextSecondary, false))
		clearCard := newSettingsCard(container.New(settingsRowLayout{}, clearText, d.clear))

		return []fyne.CanvasObject{d.card.box, formSubhead(lang.L("Recorded events")),
			container.NewGridWithColumns(2, checks...), clearCard.box}
	})
	d.validate()
	return d
}

// syncDays shows "Days to keep" only for "Overwrite after a minimum age".
func (d *auditSettingsDialog) syncDays() {
	d.daysRow.show(d.storageValue() == amt.AuditOverwriteAfterDays)
	d.card.sync()
	if d.pop != nil {
		d.pop.Refresh()
	}
}

func (d *auditSettingsDialog) storageValue() amt.AuditStorage {
	return storageOrder[max(0, d.storage.SelectedIndex())]
}

// daysValue is the typed number of days, 0 if it is none.
func (d *auditSettingsDialog) daysValue() int {
	n, err := strconv.Atoi(strings.TrimSpace(d.days.Text))
	if err != nil {
		return 0
	}
	return n
}

// change is what Save writes: only the parts the user changed.
func (d *auditSettingsDialog) change() amt.AuditChange {
	var c amt.AuditChange
	if on := d.record.Checked(); on != d.cur.Recording {
		c.Recording = &on
	}
	st, days := d.storageValue(), d.daysValue()
	if st != d.cur.Storage || st == amt.AuditOverwriteAfterDays && days != d.cur.MinDaysToKeep {
		c.Storage = &st
		if st == amt.AuditOverwriteAfterDays {
			c.MinDaysToKeep = &days
		}
	}
	for i, g := range auditGroups() {
		on := d.checks[i].Checked
		if on == d.initial[i] {
			continue
		}
		for _, app := range g.apps {
			if on {
				for _, e := range amt.KnownAuditEvents(app) {
					if !d.cur.Recorded(e) {
						c.Enable = append(c.Enable, e)
					}
				}
				continue
			}
			for _, e := range d.cur.Policy {
				if e.App == app {
					c.Disable = append(c.Disable, e)
				}
			}
		}
	}
	return c
}

func emptyAuditChange(c amt.AuditChange) bool {
	return c.Recording == nil && c.Storage == nil && len(c.Enable) == 0 && len(c.Disable) == 0
}

// validate enables Save for a valid change.
func (d *auditSettingsDialog) validate() {
	if d.panel == nil || d.action == nil || d.checks == nil {
		return
	}
	c := d.change()
	setEnabled(d.action, !emptyAuditChange(c) && c.Validate() == nil)
}

// saveAuditSettings writes the change of d; switching the recording off asks first.
func (m *manager) saveAuditSettings(w fyne.Window, d *auditSettingsDialog) {
	c := d.change()
	if emptyAuditChange(c) {
		return
	}
	run := func(confirm amt.Confirmation) {
		d.lastSave = &c
		m.auditWrite(w, func() error { return m.sess.SetAuditSettings(c, confirm) })
	}
	if !c.Risky() {
		run(amt.Confirmation{})
		return
	}
	d.confirm = showOrangeConfirm(w, orangeSpec{
		op: amt.OpAuditSettings, host: m.confirmHost(), device: m.confirmDevice(),
		title:     lang.L("Stop recording on {{.Name}}?", map[string]any{"Name": m.name}),
		warning:   lang.L("Changes to {{.Name}} are no longer recorded.", map[string]any{"Name": m.name}),
		button:    lang.L("Save"),
		onConfirm: run,
	})
}

// confirmAuditClear asks the red confirmation above the settings dialog; Clear Log closes both
// and clears the log.
func (m *manager) confirmAuditClear(w fyne.Window, d *auditSettingsDialog) {
	d.confirm = showRedConfirm(w, redSpec{
		op: amt.OpAuditClear, host: m.confirmHost(), device: m.confirmDevice(),
		title:       lang.L("Clear the audit log of {{.Name}}?", map[string]any{"Name": m.name}),
		warningBold: lang.L("This cannot be undone."),
		warningRest: lang.L("All {{.Count}} entries are deleted. The log keeps one new entry that says it was cleared.", map[string]any{"Count": d.cur.Records}),
		button:      lang.L("Clear Log"),
		onConfirm: func(c amt.Confirmation) {
			d.Dismiss()
			d.cleared = true
			m.auditWrite(w, func() error { return m.sess.ClearAuditLog(c) })
		},
	})
}

// auditWrite runs write off the Fyne thread, reads the audit log again and shows an error.
func (m *manager) auditWrite(w fyne.Window, write func() error) {
	manageWrite(m, m.audit, write, func(err error) {
		if err != nil {
			m.auditFailed(w, err, true)
		}
	}, nil)
}
