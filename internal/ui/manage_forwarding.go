package ui

import (
	"errors"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Event forwarding (issue #18, overall design V2): "Forwarding…" in the head of the Event log page
// lists the targets Intel AMT sends its events to (WS-Eventing subscriptions), "+ Add Target"
// adds one, a row's "Edit" shows it with "Delete Target…" (simple confirmation,
// amt.OpSubscriptionDelete). AMT cannot change a subscription (Renew is not supported), so the
// Edit dialog shows the values and offers only the delete. OpenLAMT runs no listener.

// forwardingButton is "Forwarding…" of the Event log page head.
func (m *manager) forwardingButton(win func() fyne.Window) *widgets.PrimaryButton {
	var b *widgets.PrimaryButton
	b = newManageButton(lang.L("Forwarding…"), func() { m.openForwarding(win(), b) })
	return b
}

// forwardingResult is a read of the targets, after a write if there was one.
type forwardingResult struct {
	data     amt.Forwarding
	err      error // the read
	writeErr error // the write before it
}

// openForwarding reads the targets (the button waits meanwhile) and shows the list.
func (m *manager) openForwarding(w fyne.Window, b *widgets.PrimaryButton) {
	b.Disable()
	manageWrite(m, nil, func() forwardingResult {
		f, err := m.sess.EventForwarding()
		return forwardingResult{data: f, err: err}
	}, func(r forwardingResult) {
		b.Enable()
		if r.err != nil {
			showError(w, r.err)
			return
		}
		m.logDlg.forwarding = m.showForwarding(w, r.data)
	}, nil)
}

// deliveryText is the Delivery column: Push, Push with acknowledgement.
func deliveryText(d amt.Delivery) string {
	if d == amt.DeliveryPushWithAck {
		return lang.L("Push with acknowledgement")
	}
	return lang.L("Push")
}

// forwardingDialog is the shown "Event forwarding" list.
type forwardingDialog struct {
	*panel
	m     *manager
	w     fyne.Window
	data  amt.Forwarding
	table *listTable
	empty fyne.CanvasObject
	add   *escButton
	edits []*widgets.PrimaryButton // the Edit buttons of the rows (tests)
}

// showForwarding shows the list of targets f.
func (m *manager) showForwarding(w fyne.Window, f amt.Forwarding) *forwardingDialog {
	d := &forwardingDialog{m: m, w: w}
	d.panel = showPanel(w, panelSpec{title: lang.L("Event forwarding"), action: lang.L("Done"), flush: true, width: 600},
		func(p *panel) []fyne.CanvasObject {
			d.panel = p
			intro := richLine(textSeg(lang.L("Intel AMT sends events to these targets. OpenLAMT only sets the target; it does not receive events."),
				style.ColorNameTextSecondary, false))
			d.table = newListTable(false, []listColumn{
				{title: lang.L("Target")},
				{title: lang.L("Delivery"), width: 150},
				{title: lang.L("Events"), width: 90},
			}, &listAction{width: 64})
			d.table.onHeight = d.relayout
			d.empty = newEmptyCard(lang.L("No targets"), lang.L("Intel AMT keeps events only in its own event log."))
			d.add = p.newButton(lang.L("+ Add Target"), func() { m.logDlg.target = m.showTarget(d, nil) })
			p.extras = []fyne.CanvasObject{d.add}
			d.show(f)
			return []fyne.CanvasObject{flushText(intro), d.table, d.empty}
		})
	return d
}

// relayout fits the dialog to a changed list.
func (d *forwardingDialog) relayout() {
	if d.pop != nil {
		d.pop.Refresh()
	}
}

// show puts the targets of f into the list, or the empty state.
func (d *forwardingDialog) show(f amt.Forwarding) {
	d.data = f
	d.edits = nil
	var rows []listRow
	for _, t := range f.Targets {
		cell := func(s string, mono bool) []widget.RichTextSegment {
			seg := cellSeg(s, theme.ColorNameForeground, theme.SizeNameText, false)
			seg.Style.TextStyle.Monospace = mono
			return []widget.RichTextSegment{seg}
		}
		b := newUserEditButton(false, false, func() { d.m.logDlg.target = d.m.showTarget(d, &t) })
		d.edits = append(d.edits, b)
		rows = append(rows, listRow{cells: [][]widget.RichTextSegment{cell(t.Address, true), cell(deliveryText(t.Delivery), false), cell(t.FilterName, false)}, action: b})
	}
	d.table.setRows(rows)
	setShown(d.table, len(rows) > 0)
	setShown(d.empty, len(rows) == 0)
	d.relayout()
}

// write runs write, reads the targets again and shows them. A fault of the device reads as a
// refused target; any other error goes through showError (timeout text, mismatch dialog).
func (d *forwardingDialog) write(write func() error) {
	m := d.m
	manageWrite(m, nil, func() forwardingResult {
		werr := write()
		f, err := m.sess.EventForwarding()
		return forwardingResult{data: f, err: err, writeErr: werr}
	}, func(r forwardingResult) {
		if r.err == nil && !d.done {
			d.show(r.data)
		}
		switch {
		case r.writeErr != nil:
			var ae *amterror.AMTError
			if errors.As(r.writeErr, &ae) || errors.Is(r.writeErr, amt.ErrAccessDenied) {
				showMessage(d.w, lang.L("Something went wrong"), refusedText(m.name, r.writeErr), r.writeErr.Error())
			} else {
				showError(d.w, r.writeErr) // timeout, sign-in, stopped session, certificate mismatch
			}
		case r.err != nil:
			showError(d.w, r.err)
		}
	}, nil)
}

// refusedText is a refused target: "Lab-Server refused the target: <reason>." with the device's
// fault reason, or the text of describe for other errors.
func refusedText(name string, err error) string {
	if errors.Is(err, amt.ErrAccessDenied) {
		return describe(err)
	}
	reason := describe(err)
	var ae *amterror.AMTError
	if errors.As(err, &ae) && strings.TrimSpace(ae.Message) != "" {
		reason = strings.TrimSpace(ae.Message)
	}
	return lang.L("{{.Name}} refused the target: {{.Reason}}.", map[string]any{"Name": name, "Reason": strings.TrimSuffix(reason, ".")})
}

// targetDialog is "Add forwarding target", or the Edit view of an existing target.
type targetDialog struct {
	*panel
	edit     *amt.ForwardingTarget // nil: Add
	address  *dialogEntry
	addrErr  *manageError
	delivery *widgets.Segmented
	filter   *widgets.Dropdown
	auth     *widgets.Segmented
	user     *dialogEntry
	password *widgets.PasswordField
	del      *escButton
	filters  []amt.EventFilter // the filters the dropdown was built with
}

// showTarget shows "Add forwarding target" (t nil) or the target t with "Delete Target…".
func (m *manager) showTarget(list *forwardingDialog, t *amt.ForwardingTarget) *targetDialog {
	if t != nil {
		return m.showTargetEdit(list, *t)
	}
	d := &targetDialog{}
	filters := list.data.Filters
	d.filters = filters
	d.panel = showPanel(list.w, panelSpec{
		title: lang.L("Add forwarding target"), action: lang.L("Add Target"), cancel: lang.L("Cancel"), flush: true, width: 520,
		onAction: func() { m.addTarget(list, d) },
		onCancel: d.forget,
	}, func(p *panel) []fyne.CanvasObject {
		d.panel = p
		d.address = p.newEntry(false)
		d.address.SetPlaceHolder("http://")
		d.address.OnChanged = func(string) { d.validate() }
		d.addrErr = newManageError()
		d.delivery = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("Push"), lang.L("With acknowledgement")), nil)
		var names []string
		for _, f := range filters {
			names = append(names, f.Name)
		}
		d.filter = widgets.NewDropdown(names)
		if i := slices.IndexFunc(filters, func(f amt.EventFilter) bool { return f.Name == "All" }); i >= 0 {
			d.filter.SetSelectedIndex(i)
		}
		d.filter.OnEscape = p.Dismiss
		d.auth = widgets.NewSegmented(widgets.SegmentedText, widgets.TextOptions(lang.L("None"), lang.L("Digest")), func(int) { d.syncAuth() })
		d.user = p.newEntry(false)
		d.user.OnChanged = func(string) { d.validate() }
		d.password = userPasswordField(p, d.validate)
		p.focus = d.address
		return []fyne.CanvasObject{
			formField(lang.L("Address"), framedEntry(d.address)),
			d.addrErr.box,
			formHint(lang.L("A listener that accepts WS-Management events, for example a SIEM collector.")),
			container.NewGridWithColumns(2, formField(lang.L("Delivery"), container.NewHBox(d.delivery)), formField(lang.L("Events"), d.filter)),
			formField(lang.L("Sign-in at the target"), container.NewHBox(d.auth)),
			container.NewGridWithColumns(2, formField(lang.L("User name"), framedEntry(d.user)), formField(lang.L("Password"), d.password)),
		}
	})
	d.syncAuth()
	return d
}

// spec is the target the dialog describes.
func (d *targetDialog) spec(filters []amt.EventFilter) amt.TargetSpec {
	s := amt.TargetSpec{Address: strings.TrimSpace(d.address.Text), Delivery: amt.DeliveryPush}
	if d.delivery.Selected() == 1 {
		s.Delivery = amt.DeliveryPushWithAck
	}
	if i := d.filter.SelectedIndex(); i >= 0 && i < len(filters) {
		s.FilterID = filters[i].ID
	}
	if d.auth.Selected() == 1 {
		s.User, s.Password = d.user.Text, d.password.Text()
	}
	return s
}

// syncAuth enables the sign-in fields for Digest only.
func (d *targetDialog) syncAuth() {
	on := d.auth.Selected() == 1
	setEnabled(d.user, on)
	setEnabled(d.password.Entry, on)
	d.validate()
}

// validate shows the address error for a typed address that is not valid and enables the main
// button for a complete target.
func (d *targetDialog) validate() {
	if d.panel == nil || d.action == nil || d.auth == nil {
		return
	}
	a := strings.TrimSpace(d.address.Text)
	if a != "" && !amt.ValidTargetAddress(a) {
		d.addrErr.label.SetText(lang.L("Enter an address that starts with http:// or https://."))
		d.addrErr.box.Show()
	} else {
		d.addrErr.set(nil)
	}
	ok := amt.ValidTargetAddress(a) && d.filter.SelectedIndex() >= 0
	if d.auth.Selected() == 1 {
		ok = ok && d.user.Text != "" && d.password.Text() != "" && len(d.user.Text) <= amt.MaxTargetCredential && len(d.password.Text()) <= amt.MaxTargetCredential
	}
	setEnabled(d.action, ok)
	if d.pop != nil {
		d.pop.Refresh()
	}
}

// forget clears the password field.
func (d *targetDialog) forget() {
	if d.password != nil {
		d.password.SetText("")
	}
}

// addTarget subscribes the target of d; a password goes to the device only after the question
// without TLS (issue #37, the key variant of V18).
func (m *manager) addTarget(list *forwardingDialog, d *targetDialog) {
	spec := d.spec(d.filters) // not list.data: a reload may have replaced it since the dropdown was built
	d.forget()
	run := func() { list.write(func() error { return m.sess.AddForwardingTarget(spec) }) }
	if spec.Password == "" {
		run()
		return
	}
	m.warnWithoutTLS(list.w, secretKey, spec.User, run)
}

// showTargetEdit shows a target with its values and "Delete Target…".
func (m *manager) showTargetEdit(list *forwardingDialog, t amt.ForwardingTarget) *targetDialog {
	d := &targetDialog{edit: &t}
	d.panel = showPanel(list.w, panelSpec{title: lang.L("Edit forwarding target"), action: lang.L("Done"), flush: true, width: 520},
		func(p *panel) []fyne.CanvasObject {
			d.panel = p
			d.del = p.newButton(lang.L("Delete Target…"), func() { m.confirmTargetDelete(list, d) })
			d.del.Importance = widget.DangerImportance
			p.extras = []fyne.CanvasObject{container.NewThemeOverride(d.del, dangerButtonTheme{overrideTheme})}
			card := targetEditCard(t)
			return []fyne.CanvasObject{card.box, formHint(lang.L("Intel AMT cannot change a target. Delete it and add a new one."))}
		})
	return d
}

// confirmTargetDelete asks the simple confirmation (amt.OpSubscriptionDelete) above the Edit
// dialog; Delete Target closes it and removes the subscription.
func (m *manager) confirmTargetDelete(list *forwardingDialog, d *targetDialog) {
	t := *d.edit
	m.logDlg.deleteAsk = showConfirm(list.w, icons.Delete, lang.L("Delete forwarding target?"),
		lang.L("Intel AMT stops sending events to {{.Address}}.", map[string]any{"Address": t.Address}),
		lang.L("Delete Target"), func() {
			d.Dismiss()
			list.write(func() error { return m.sess.DeleteForwardingTarget(t) })
		}, nil)
}

// targetEditCard is the read-only card of the Edit dialog: address, delivery and events.
func targetEditCard(t amt.ForwardingTarget) *settingsCard {
	value := func(s string, mono bool) fyne.CanvasObject {
		v := newSettingsValue(s, theme.ColorNameForeground)
		v.mono = mono
		return v
	}
	return newSettingsCard(
		amtSettingsRow(lang.L("Address"), value(t.Address, true)),
		amtSettingsRow(lang.L("Delivery"), value(deliveryText(t.Delivery), false)),
		amtSettingsRow(lang.L("Events"), value(t.FilterName, false)))
}
