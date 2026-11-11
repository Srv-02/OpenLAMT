package ui

import (
	"errors"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The dialogs of the Users page: the admin account (Danger.dc.html, left), Add user and Edit
// user (Forms.dc.html, left) and "New password not saved" (Forms.dc.html, middle). Every write
// runs off the Fyne thread and reads the user list again afterwards.

// passwordHint is the rule below the password fields.
func passwordHint() string {
	return lang.L("8–32 ASCII characters with upper and lower case, a digit and a symbol.")
}

// formField is a field with its caption above it (Forms.dc.html .lbl: 12 px semibold, 5 px gap).
func formField(label string, field fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedVBoxLayout(5), designText(label, 12, style.ColorNameControlText, true), field)
}

// formSubhead is a small gray heading inside a form (Forms.dc.html .sub: "Access", "Permissions").
func formSubhead(text string) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(4, 0, 0, 0), designText(text, 12, style.ColorNameTextSecondary, true))
}

// formHint is a wrapped 12 px gray note (Forms.dc.html .hint), aligned with the fields: the
// rich text's own padding is taken back.
func formHint(text string) fyne.CanvasObject {
	rt := widget.NewRichText(cellSeg(text, style.ColorNameTextSecondary, style.SizeNameSmallText, false))
	rt.Wrapping = fyne.TextWrapWord
	ip := theme.InnerPadding()
	return container.New(layout.NewCustomPaddedLayout(-ip/2, -ip/2, -ip, -ip), rt)
}

// framedEntry puts an entry into the 1 px frame of the design's inputs (radius 7).
func framedEntry(e *dialogEntry) fyne.CanvasObject {
	border := style.BindRect(&canvas.Rectangle{CornerRadius: confirmFieldRadius, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder)
	return container.NewStack(border, container.NewThemeOverride(e, style.CardTheme{}))
}

// userPasswordField is a hidden password entry with the eye button that closes p on Esc and
// submits it on Enter.
func userPasswordField(p *panel, changed func()) *widgets.PasswordField {
	f := widgets.NewPasswordField()
	f.KeyFilter = p.escKey
	f.Entry.OnSubmitted = func(string) { p.submit() }
	f.Entry.OnChanged = func(string) { changed() }
	return f
}

// usersWrite runs write off the Fyne thread; afterwards the user list is read again and the
// errors are shown (writeDone). write returns the new password it tried to set ("" for none).
// A write that ends after device management closed shows only "New password not saved", on
// the start window (lateNotSaved).
func (m *manager) usersWrite(v *usersPane, write func() (password string, errs []error)) {
	go func() {
		pw, errs := write()
		m.later(func() {
			if m.closed {
				m.lateNotSaved(pw, errs)
				return
			}
			m.users.reload()
			v.writeDone(pw, errs)
		})
	}()
}

// writeDone shows the errors of a write: a change the keychain did not take
// (*session.NotSavedError) or whose outcome is unknown (*amt.CredentialsUncertainError) shows
// the new password once, so it is not lost; any other error the error dialog.
func (v *usersPane) writeDone(password string, errs []error) {
	show, others := notSavedPassword(password, errs)
	for _, err := range others {
		showError(v.window(), err)
	}
	if show != "" {
		v.notSaved = showNotSavedDialog(v.window(), v.m.name, show)
	}
}

// lateNotSaved shows the new password of a write that ended after device management closed
// (its device window was closed meanwhile) on the start window, which is brought to the front:
// otherwise the password would be lost. Other errors are dropped with the window.
func (m *manager) lateNotSaved(password string, errs []error) {
	show, _ := notSavedPassword(password, errs)
	if show == "" || m.deps.mainWin == nil {
		return
	}
	m.deps.mainWin.RequestFocus()
	showNotSavedDialog(m.deps.mainWin, m.name, show)
}

// notSavedPassword splits the errors of a write into the new password to show once ("" for
// none) and the errors for the error dialog.
func notSavedPassword(password string, errs []error) (show string, others []error) {
	for _, err := range errs {
		var ns *session.NotSavedError
		var ue *amt.CredentialsUncertainError
		switch {
		case errors.As(err, &ns) && ns.Password != "":
			show = ns.Password
		case errors.As(err, &ue) && password != "":
			show = password
		default:
			others = append(others, err)
		}
	}
	return show, others
}

// adminDialog is the red confirmation for the admin account with its fields.
type adminDialog struct {
	*riskDialog
	name       *dialogEntry
	pw, repeat *widgets.PasswordField
	keychain   *dialogCheck
}

// showAdminDialog changes name and password of the AMT admin account (Danger.dc.html, left):
// the button needs a valid name, a valid password typed twice and the device address. With
// "Update the password in the keychain" off, nothing is saved (store nil).
func (m *manager) showAdminDialog(v *usersPane, current string, signedIn bool) *adminDialog {
	title := lang.L("Change the signed-in admin account")
	if !signedIn {
		// TODO(design): the title for an admin account other than the signed-in one has no
		// approved design.
		title = lang.L("Change the admin account")
	}
	d := &adminDialog{}
	ready := func() bool {
		if d.name.Text != current && amt.ValidateUserName(d.name.Text) != nil {
			return false
		}
		return amt.ValidatePassword(d.pw.Text()) == nil && d.pw.Text() == d.repeat.Text()
	}
	forget := func() { d.pw.SetText(""); d.repeat.SetText("") }
	d.riskDialog = showRedConfirm(v.window(), redSpec{
		op: amt.OpAdminAccount, host: m.deps.host, title: title,
		warningBold: lang.L("Risk of lockout."),
		warningRest: lang.L("If the new password is lost, only the BIOS (MEBx) at the device can reset it."),
		extra: func(r *riskDialog) []fyne.CanvasObject {
			d.name = r.newEntry(false)
			d.name.SetText(current)
			d.name.OnChanged = func(string) { r.Revalidate() }
			d.pw = userPasswordField(r.panel, r.Revalidate)
			d.repeat = userPasswordField(r.panel, r.Revalidate)
			d.keychain = r.newCheck(lang.L("Update the password in the keychain"), true)
			return []fyne.CanvasObject{
				formField(lang.L("User name"), framedEntry(d.name)),
				formField(lang.L("New password"), d.pw),
				formField(lang.L("Repeat password"), d.repeat),
				formHint(passwordHint()),
				d.keychain,
			}
		},
		ready:  ready,
		button: lang.L("Change Account"),
		onConfirm: func(c amt.Confirmation) {
			change := amt.AccountChange{Password: d.pw.Text()}
			if d.name.Text != current {
				change.Name = d.name.Text
			}
			var store session.LoginStore
			if d.keychain.Checked {
				store = m.deps.loginStore
			}
			forget()
			m.usersWrite(v, func() (string, []error) {
				if err := m.sess.SetAdminAccount(change, c, store); err != nil {
					return change.Password, []error{err}
				}
				return change.Password, nil
			})
		},
		onCancel: forget,
	})
	return d
}

// accessOrder maps the segments Network | Local | Both to the access permissions.
var accessOrder = []amt.AccessPermission{amt.AccessNetwork, amt.AccessLocal, amt.AccessLocalAndNetwork}

// userDialog is Add user, or Edit user for edit.
type userDialog struct {
	*panel
	edit       *amt.User // nil: Add user
	name       *dialogEntry
	pw, repeat *widgets.PasswordField
	access     *widgets.Segmented
	checks     []*dialogCheck
	initial    []bool          // permission states at the start
	enabled    *widgets.Switch // Edit only
	del        *escButton      // Edit only
}

// showUserDialog shows Add user (u nil) or Edit user (Forms.dc.html, left). Edit has the name
// filled in, the switch "Enabled" above Access, "Delete User…" left of the buttons, and the
// password is optional as long as only Enabled changes: any other change rewrites the account,
// and AMT stores MD5(name:realm:password), so it needs the password.
func (m *manager) showUserDialog(v *usersPane, u *amt.User) *userDialog {
	d := &userDialog{edit: u}
	title, button := lang.L("Add user"), lang.L("Add User")
	if u != nil {
		title, button = lang.L("Edit user"), lang.L("Save")
	}
	d.panel = showPanel(v.window(), panelSpec{
		title: title, action: button, cancel: lang.L("Cancel"), flush: true,
		onAction: func() { d.write(m, v) },
		onCancel: d.forget,
	}, func(p *panel) []fyne.CanvasObject {
		d.panel = p
		d.name = p.newEntry(false)
		d.pw = userPasswordField(p, d.validate)
		d.repeat = userPasswordField(p, d.validate)
		d.access = widgets.NewSegmented(widgets.SegmentedText,
			widgets.TextOptions(lang.L("Network"), lang.L("Local"), lang.L("Both")), func(int) { d.validate() })
		var checks []fyne.CanvasObject
		for _, g := range permGroups() {
			on := u != nil && hasRealms(u.Realms, g.realms)
			c := p.newCheck(g.label, on)
			c.OnChanged = func(bool) { d.validate() }
			d.checks = append(d.checks, c)
			d.initial = append(d.initial, on)
			checks = append(checks, c)
		}
		objs := []fyne.CanvasObject{
			formField(lang.L("User name"), framedEntry(d.name)),
			container.NewGridWithColumns(2, formField(lang.L("Password"), d.pw), formField(lang.L("Repeat password"), d.repeat)),
			formHint(passwordHint()),
		}
		if u != nil {
			d.name.SetText(u.Name)
			d.access.SetSelected(slices.Index(accessOrder, u.Access))
			d.enabled = widgets.NewSwitch(nil)
			d.enabled.SetChecked(u.Enabled)
			d.enabled.OnChanged = func(bool) { d.validate() }
			d.enabled.OnEscape = p.Dismiss
			// TODO(design): the confirmation before a user is deleted has no approved design.
			d.del = p.newButton(lang.L("Delete User…"), func() { usersNotApproved("delete user") })
			d.del.Importance = widget.DangerImportance
			p.extras = []fyne.CanvasObject{container.NewThemeOverride(d.del, dangerButtonTheme{overrideTheme})}
			objs = append(objs, container.New(&rowLayout{gap: 8, fill: 1},
				designText(lang.L("Enabled"), 13, "foreground", false), spacer(), d.enabled))
		}
		d.name.OnChanged = func(string) { d.validate() }
		return append(objs,
			formSubhead(lang.L("Access")), container.NewHBox(d.access),
			formSubhead(lang.L("Permissions")), container.NewGridWithColumns(2, checks...))
	})
	d.validate()
	return d
}

// accessValue is the selected access permission.
func (d *userDialog) accessValue() amt.AccessPermission { return accessOrder[d.access.Selected()] }

// realms are the realms the account gets: a permission the user did not change keeps the realms
// it has (also a partial group), realms outside the permissions stay as they are.
func (d *userDialog) realms() []amt.Realm {
	var out []amt.Realm
	if d.edit != nil {
		out = slices.Clone(d.edit.Realms)
	}
	for i, g := range permGroups() {
		on := d.checks[i].Checked
		if on == d.initial[i] {
			continue
		}
		for _, r := range g.realms {
			if on && !slices.Contains(out, r) {
				out = append(out, r)
			}
			if !on {
				out = slices.DeleteFunc(out, func(x amt.Realm) bool { return x == r })
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// needsUpdate reports whether the account itself is written (AddUser or UpdateUser): always for
// Add, for Edit when name, password, access or permissions changed.
func (d *userDialog) needsUpdate() bool {
	u := d.edit
	if u == nil {
		return true
	}
	old := slices.Clone(u.Realms)
	slices.Sort(old)
	return d.name.Text != u.Name || d.pw.Text() != "" || d.repeat.Text() != "" ||
		d.accessValue() != u.Access || !slices.Equal(d.realms(), slices.Compact(old))
}

// enabledChanged reports whether Edit switched Enabled.
func (d *userDialog) enabledChanged() bool {
	return d.enabled != nil && d.enabled.Checked() != d.edit.Enabled
}

// ready reports whether the main button may run: a change, and for a written account a valid
// name, a valid password typed twice and at least one realm.
func (d *userDialog) ready() bool {
	if !d.needsUpdate() {
		return d.enabledChanged()
	}
	return amt.ValidateUserName(d.name.Text) == nil && amt.ValidatePassword(d.pw.Text()) == nil &&
		d.pw.Text() == d.repeat.Text() && len(d.realms()) > 0
}

func (d *userDialog) validate() {
	if d.panel == nil || d.action == nil || d.checks == nil {
		return
	}
	if d.ready() {
		d.action.Enable()
	} else {
		d.action.Disable()
	}
}

// forget clears the password fields.
func (d *userDialog) forget() {
	d.pw.SetText("")
	d.repeat.SetText("")
}

// write runs Add (AddUser) or Edit (UpdateUser, then SetUserEnabled).
func (d *userDialog) write(m *manager, v *usersPane) {
	spec := amt.UserSpec{Name: d.name.Text, Password: d.pw.Text(), Access: d.accessValue(), Realms: d.realms()}
	update, enable := d.needsUpdate(), d.enabledChanged()
	d.forget()
	if d.edit == nil {
		m.usersWrite(v, func() (string, []error) {
			if _, err := m.sess.AddUser(spec); err != nil {
				return "", []error{err}
			}
			return "", nil
		})
		return
	}
	h, on, store := d.edit.Handle, d.enabled.Checked(), m.deps.loginStore
	m.usersWrite(v, func() (string, []error) {
		var errs []error
		if update {
			err := m.sess.UpdateUser(h, spec, amt.Confirmation{}, store)
			var ns *session.NotSavedError
			if err != nil && !errors.As(err, &ns) {
				return spec.Password, []error{err}
			}
			if err != nil {
				errs = append(errs, err) // the account changed; Enabled follows
			}
		}
		if enable {
			if err := m.sess.SetUserEnabled(h, on); err != nil {
				errs = append(errs, err)
			}
		}
		return spec.Password, errs
	})
}

// checkTexts returns the captions of the permission checkboxes (tests).
func (d *userDialog) checkTexts() []string {
	out := make([]string, len(d.checks))
	for i, c := range d.checks {
		out[i] = c.Text
	}
	return out
}

// checkStates returns the states of the permission checkboxes (tests).
func (d *userDialog) checkStates() []bool {
	out := make([]bool, len(d.checks))
	for i, c := range d.checks {
		out[i] = c.Checked
	}
	return out
}

// notSavedDialog is "New password not saved".
type notSavedDialog struct {
	*panel
	display *widgets.PasswordField
	copy    *escButton
	done    *escButton
}

// showNotSavedDialog shows a password the device took but the keychain did not (Forms.dc.html):
// hidden with the eye button, Copy puts it on the clipboard. Closing drops it from the dialog;
// Enter does not close it by accident.
func showNotSavedDialog(parent fyne.Window, name, password string) *notSavedDialog {
	d := &notSavedDialog{}
	forget := func() { d.display.SetText("") }
	d.panel = showPanel(parent, panelSpec{
		title: lang.L("New password not saved"), action: lang.L("I Have Saved It"), noEnter: true, flush: true,
		onAction: forget, onCancel: forget,
	}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(lang.L("The password on “{{.Name}}” was changed, but OpenLAMT could not save it in the keychain. Copy it now. It is shown only once.",
			map[string]any{"Name": name}))
		msg.Wrapping = fyne.TextWrapWord
		d.display = widgets.NewPasswordDisplay(password)
		d.copy = p.newButton(lang.L("Copy"), func() { fyne.CurrentApp().Clipboard().SetContent(d.display.Text()) })
		return []fyne.CanvasObject{flushText(msg), container.New(&rowLayout{gap: 8, fill: 0}, d.display, d.copy)}
	})
	d.done = d.action
	return d
}
