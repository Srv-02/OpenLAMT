package ui

import (
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The dialogs of the Wi-Fi page (gesamtdesign "Verwaltung: Wi-Fi"): Add and Edit share one
// dialog; "Delete Profile…" asks with the simple confirmation (amt.OpWiFiDelete) or, while the
// session runs over Wi-Fi, with the red one (amt.OpWiFiLockout), as does switching Wi-Fi off.
// A passphrase sent without TLS asks first (warnWithoutTLS, key variant, V18). Passphrases
// live only in the password fields and the write that sends them; they are never logged or
// stored.

// wifiProfileDialog is Add Wi-Fi profile, or Edit Wi-Fi profile for edit.
type wifiProfileDialog struct {
	*panel
	edit       *amt.WiFiProfile // nil: Add
	options    []amt.WiFiSecurity
	name, ssid *dialogEntry
	priority   *dialogEntry
	security   *widgets.Dropdown
	pw, repeat *widgets.PasswordField
	del        *escButton // Edit only
}

// openAdd is "+ Add Profile".
func (p *wifiPane) openAdd() { p.profileDlg = p.m.showWiFiProfileDialog(p, nil) }

// openEdit is the Edit button of profile pr.
func (p *wifiPane) openEdit(pr amt.WiFiProfile) { p.profileDlg = p.m.showWiFiProfileDialog(p, &pr) }

// showWiFiProfileDialog shows Add (pr nil) or Edit. Edit keeps the name (Intel AMT cannot rename
// a profile) and the password may stay empty to keep the current one; a profile whose security
// cannot be created here (802.1X) needs a new security and password.
func (m *manager) showWiFiProfileDialog(v *wifiPane, pr *amt.WiFiProfile) *wifiProfileDialog {
	w := v.d.src.val
	d := &wifiProfileDialog{edit: pr, options: slices.Clone(w.Securities)}
	if len(d.options) == 0 {
		d.options = slices.Clone(amt.WiFiSecurities)
	}
	if pr != nil && !slices.Contains(d.options, pr.Security) {
		d.options = append(d.options, pr.Security) // shown as it is; Save needs another one
	}
	title, button := lang.L("Add Wi-Fi profile"), lang.L("Add Profile")
	if pr != nil {
		title, button = lang.L("Edit Wi-Fi profile"), lang.L("Save")
	}
	d.panel = showPanel(v.window(), panelSpec{
		title: title, action: button, cancel: lang.L("Cancel"), flush: true,
		onAction: func() { d.save(m, v) },
		onCancel: d.forget,
	}, func(p *panel) []fyne.CanvasObject {
		d.panel = p
		d.name = p.newEntry(false)
		d.ssid = p.newEntry(false)
		d.priority = p.newEntry(false)
		labels := make([]string, len(d.options))
		for i, s := range d.options {
			labels[i] = securityText(s)
		}
		d.security = widgets.NewDropdown(labels)
		d.security.OnEscape = p.Dismiss
		d.pw = userPasswordField(p, d.validate)
		d.repeat = userPasswordField(p, d.validate)
		d.repeat.Entry.SetPlaceHolder(lang.L("Repeat password"))
		hint := lang.L("8–63 characters.")
		if pr != nil {
			d.name.SetText(pr.Name)
			d.name.Disable()
			d.ssid.SetText(pr.SSID)
			d.priority.SetText(strconv.Itoa(pr.Priority))
			d.security.SetSelectedIndex(slices.Index(d.options, pr.Security))
			d.pw.Entry.SetPlaceHolder(lang.L("Unchanged"))
			hint = lang.L("8–63 characters. Leave empty to keep the current password.")
			d.del = p.newButton(lang.L("Delete Profile…"), func() { v.confirmDelete(d) })
			d.del.Importance = widget.DangerImportance
			p.extras = []fyne.CanvasObject{container.NewThemeOverride(d.del, dangerButtonTheme{overrideTheme})}
		} else {
			d.priority.SetText(strconv.Itoa(nextPriority(w.Profiles)))
			if i := slices.Index(d.options, amt.WiFiWPA2Personal); i >= 0 {
				d.security.SetSelectedIndex(i)
			}
		}
		for _, e := range []*dialogEntry{d.name, d.ssid, d.priority} {
			e.OnChanged = func(string) { d.validate() }
		}
		d.security.OnChanged = func(string) { d.validate() }
		return []fyne.CanvasObject{
			formField(lang.L("Profile name"), framedEntry(d.name)),
			formField(lang.L("Network name (SSID)"), framedEntry(d.ssid)),
			container.NewGridWithColumns(2, formField(lang.L("Priority"), framedEntry(d.priority)),
				formField(lang.L("Security"), d.security)),
			formField(lang.L("Password"), container.NewGridWithColumns(2, d.pw, d.repeat)),
			formHint(hint),
		}
	})
	d.validate()
	return d
}

// nextPriority is one more than the highest priority of profiles (1 for none), at most 255.
func nextPriority(profiles []amt.WiFiProfile) int {
	n := 0
	for _, p := range profiles {
		n = max(n, p.Priority)
	}
	return min(n+1, 255)
}

// selected is the chosen security.
func (d *wifiProfileDialog) selected() amt.WiFiSecurity {
	if i := d.security.SelectedIndex(); i >= 0 && i < len(d.options) {
		return d.options[i]
	}
	return amt.WiFiSecurityOther
}

// spec is the profile the dialog describes, with the typed password.
func (d *wifiProfileDialog) spec() amt.WiFiProfileSpec {
	prio, err := strconv.Atoi(strings.TrimSpace(d.priority.Text))
	if err != nil {
		prio = 0
	}
	return amt.WiFiProfileSpec{Name: d.name.Text, SSID: d.ssid.Text, Priority: prio, Security: d.selected(), Passphrase: d.pw.Text()}
}

// needsPassword: Add, and an edited profile without a passphrase of its own (802.1X).
func (d *wifiProfileDialog) needsPassword() bool {
	return d.edit == nil || d.edit.Security == amt.WiFiSecurityOther
}

// changed reports whether Edit changes anything.
func (d *wifiProfileDialog) changed() bool {
	if d.edit == nil {
		return true
	}
	s := d.spec()
	return s.SSID != d.edit.SSID || s.Priority != d.edit.Priority || s.Security != d.edit.Security || s.Passphrase != ""
}

// ready reports whether Save may run: valid values, the password typed twice, and a change.
func (d *wifiProfileDialog) ready() bool {
	return d.spec().Validate(d.needsPassword()) == nil && d.pw.Text() == d.repeat.Text() && d.changed()
}

func (d *wifiProfileDialog) validate() {
	if d.panel == nil || d.action == nil || d.security == nil || d.repeat == nil {
		return
	}
	setEnabled(d.action, d.ready())
}

// forget clears the password fields.
func (d *wifiProfileDialog) forget() {
	d.pw.SetText("")
	d.repeat.SetText("")
}

// save writes the profile: a passphrase over a session without TLS asks first.
func (d *wifiProfileDialog) save(m *manager, v *wifiPane) {
	spec := d.spec()
	d.forget()
	var id string
	if d.edit != nil {
		id = d.edit.InstanceID
	}
	write := func() {
		v.d.write(v.window, func() error {
			if id == "" {
				return m.sess.AddWiFiProfile(spec)
			}
			return m.sess.UpdateWiFiProfile(id, spec)
		})
	}
	if spec.Passphrase == "" {
		write()
		return
	}
	m.warnWithoutTLS(v.window(), secretKey, "", write)
}

// confirmDelete asks before the profile of the Edit dialog d is deleted: the simple question
// (amt.OpWiFiDelete), or the red one (amt.OpWiFiLockout) while the session runs over Wi-Fi.
// Cancel returns to d; Delete Profile closes d as well.
func (p *wifiPane) confirmDelete(d *wifiProfileDialog) {
	pr := *d.edit
	title := lang.L("Delete the profile “{{.Profile}}”?", map[string]any{"Profile": pr.Name})
	run := func(c amt.Confirmation) {
		d.Dismiss()
		p.d.write(p.window, func() error { return p.m.sess.DeleteWiFiProfile(pr.InstanceID, c) })
	}
	op := amt.OpWiFiDelete
	if p.d.src.val.OverWiFi {
		op = amt.OpWiFiLockout
	}
	if amt.RiskOf(op).Level == amt.LevelRed {
		p.lockDlg = showRedConfirm(p.window(), redSpec{
			op: op, host: p.m.confirmHost(), device: p.m.confirmDevice(), title: title,
			warningBold: lang.L("Risk of lockout."),
			warningRest: lang.L("OpenLAMT reaches {{.Name}} over Wi-Fi right now. Without the profile, {{.Name}} can lose the connection and only a cable or the device itself brings it back.",
				map[string]any{"Name": p.m.name}),
			button: lang.L("Delete Profile"), onConfirm: run,
		})
		return
	}
	p.deleteDlg = showPanel(p.window(), panelSpec{
		icon: icons.Delete, danger: true, title: title, action: lang.L("Delete Profile"), cancel: lang.L("Cancel"),
		onAction: func() { run(amt.Confirmation{}) },
	}, func(*panel) []fyne.CanvasObject {
		msg := widget.NewLabel(lang.L("Intel AMT no longer connects to {{.SSID}} with this profile.", map[string]any{"SSID": pr.SSID}))
		msg.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{msg}
	})
}

// confirmStateOff asks the red question before Wi-Fi is switched off while the session runs
// over it (amt.OpWiFiLockout).
func (p *wifiPane) confirmStateOff() {
	p.stateLockDlg = showRedConfirm(p.window(), redSpec{
		op: amt.OpWiFiLockout, host: p.m.confirmHost(), device: p.m.confirmDevice(),
		title:       lang.L("Turn off Wi-Fi in Intel AMT?"),
		warningBold: lang.L("Risk of lockout."),
		warningRest: lang.L("OpenLAMT reaches {{.Name}} over Wi-Fi right now. Without Wi-Fi, {{.Name}} loses the connection and only a cable or the device itself brings it back.",
			map[string]any{"Name": p.m.name}),
		button: lang.L("Turn Off Wi-Fi"),
		onConfirm: func(c amt.Confirmation) {
			p.d.write(p.window, func() error { return p.m.sess.SetWiFiState(amt.WiFiOff, c) })
		},
	})
}
