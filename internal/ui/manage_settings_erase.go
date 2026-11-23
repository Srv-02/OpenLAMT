package ui

import (
	"context"
	"errors"
	"fmt"

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

// Erase Drives (issue #20, gesamtdesign "Verwaltung: Settings" and "Erase Drives", V5): the row
// "Erase drives" in the danger zone opens the red dialog; the switch "Remote Platform Erase"
// under Remote features allows it in the AMT boot service.
//
//   - Remote Platform Erase (AMT 16 and later): the actions the BIOS reports in
//     AMT_BootCapabilities.PlatformErase, the others greyed with "(not supported)". Off in the
//     boot service: the hint "Remote Platform Erase is off" instead of the dialog. It needs TLS:
//     without it the error says so (no dialog).
//   - Remote Secure Erase (older devices): "Securely erase the boot drive".
//   - Neither: the button is grey and the row says why.
//
// The dialog is the red confirmation (amt.OpErase); nothing is sent before its button. A drive
// password over a session without TLS asks first (#37, the key variant). The erase then runs as
// a power action of the device window: the user consent (KVM's dialog), the erase, the restart.
// "Erase started" follows, with "Open Event Log".

// eraseActionText is the label of an action of Remote Platform Erase in the dialog.
func eraseActionText(a amt.EraseActions) string {
	switch a {
	case amt.EraseSSDs:
		return lang.L("Securely erase all SSDs")
	case amt.EraseVerify:
		return lang.L("Verify the erase")
	case amt.EraseTPM:
		return lang.L("Clear the TPM")
	case amt.EraseBIOSVariables:
		return lang.L("Reset BIOS settings (clear BIOS variables)")
	case amt.EraseBIOSReload:
		return lang.L("Reload the BIOS factory configuration")
	case amt.EraseUnconfigure:
		return lang.L("Unconfigure Intel AMT · remote access ends")
	}
	return fmt.Sprintf("0x%x", uint32(a))
}

// eraseDefault is checked when the dialog opens: the drives and their check.
const eraseDefault = amt.EraseSSDs | amt.EraseVerify

// rpeSettingsRow builds the row "Remote Platform Erase" of Remote features; showErase fills it.
func (p *settingsPane) rpeSettingsRow() fyne.CanvasObject {
	p.rpe = p.newSwitch(func(on bool) {
		p.d.write(func() error { return p.m.sess.SetRPE(on) }, true)
	})
	title := container.NewHBox(designText(lang.L("Remote Platform Erase"), 13, style.ColorNameControlText, false), rpeHelpButton())
	desc := designText(lang.L("Allows Erase drives in the danger zone."), 12, style.ColorNameTextSecondary, false)
	left := container.New(layout.NewCustomPaddedVBoxLayout(0), title, desc)
	return container.New(settingsRowLayout{}, left, p.rpe)
}

// rpeHelpButton is the info button of the switch (#8).
func rpeHelpButton() *widgets.InfoButton {
	return widgets.NewInfoButton(widgets.HelpContent{
		Title:   lang.L("Remote Platform Erase"),
		What:    lang.L("Allows Intel AMT to have the BIOS erase the drives and more at the next start, chosen in Erase drives in the danger zone."),
		When:    lang.L("Turn it on before you erase a device you pass on or dispose of. It must also be on in the BIOS."),
		Wrong:   lang.L("While it is on, anyone with an account that may set boot options can erase the device."),
		Recover: lang.L("Turn it off again here."),
	})
}

// eraseDangerRow builds the row "Erase drives" of the danger zone.
func (p *settingsPane) eraseDangerRow() fyne.CanvasObject {
	p.erase = widgets.NewPrimaryButton(lang.L("Erase…"), nil, p.openErase)
	p.erase.Importance = widget.DangerImportance
	p.erase.Metrics = &widgets.ButtonMetrics{Height: 30, Radius: 7, PaddingH: 16, TextSize: 13}
	p.eraseText = richLine(textSeg(lang.L("Restarts the device and erases its drives."), style.ColorNameDangerZoneText, false))
	title := designText(lang.L("Erase drives"), 14, style.ColorNameDangerZoneText, true)
	p.eraseRow = dangerRow(title, p.eraseText, p.erase)
	return p.eraseRow
}

// showErase shows the switch where the device offers Remote Platform Erase and the danger zone
// row where the boot capabilities were read: active where the device can erase, else grey with
// the reason.
func (p *settingsPane) showErase() {
	info, ok := p.d.boot.get()
	p.rpeRow.show(ok && info.PlatformEraseSupported())
	p.rpe.SetChecked(info.Erase.Enabled)
	setShown(p.eraseRow, ok)
	setShown(p.eraseLine, ok)
	text := lang.L("Restarts the device and erases its drives.")
	if !info.EraseSupported() {
		text = lang.L("{{.Name}} does not support remote erase.", map[string]any{"Name": p.m.name})
	}
	p.eraseText.Segments = []widget.RichTextSegment{textSeg(text, style.ColorNameDangerZoneText, false)}
	p.eraseText.Refresh()
}

// eraseAllowed reports whether "Erase…" can be tapped (lock: not while a write runs): not while
// the device is read in S3, which waking does not restart (issue #142).
func (p *settingsPane) eraseAllowed() bool {
	info, ok := p.d.boot.get()
	return ok && info.EraseSupported() && !p.m.readS3()
}

// openErase is "Erase…": the hint while Remote Platform Erase is off, the TLS error without TLS,
// else the red dialog.
func (p *settingsPane) openErase() {
	info, ok := p.d.boot.get()
	if !ok || !p.eraseAllowed() || p.d.locked() {
		return
	}
	platform := info.PlatformEraseSupported()
	switch {
	case platform && !info.Erase.Enabled:
		p.eraseInfo = showPanel(p.window(), panelSpec{
			icon: icons.Info, tone: toneInfo, tileAbove: true, action: lang.L("OK"),
			title: lang.L("Remote Platform Erase is off"),
		}, func(*panel) []fyne.CanvasObject {
			msg := widget.NewLabel(lang.L("Turn on Remote Platform Erase under Remote features, then try again. If the switch fails, turn it on in the BIOS first."))
			msg.Wrapping = fyne.TextWrapWord
			return []fyne.CanvasObject{msg}
		})
	case platform && !p.m.sess.Config().UseTLS:
		p.d.fail(fmt.Errorf("erase: %w", amt.ErrEraseNeedsTLS))
		p.d.src.notify()
	default:
		p.eraseDlg = p.showEraseDialog(info)
	}
}

// eraseDialog is the shown red dialog.
type eraseDialog struct {
	*riskDialog
	platform bool
	checks   map[amt.EraseActions]*dialogCheck // Remote Platform Erase
	boot     *dialogCheck                      // Remote Secure Erase: "Securely erase the boot drive"
	password *widgets.PasswordField
}

// options is the erase the dialog describes.
func (e *eraseDialog) options() amt.EraseOptions {
	o := amt.EraseOptions{Platform: e.platform, Password: e.password.Text()}
	for a, c := range e.checks {
		if c.Checked && !c.Disabled() {
			o.Actions |= a
		}
	}
	return o
}

// ready: at least one thing to erase and a valid password.
func (e *eraseDialog) ready() bool {
	if amt.ValidateErasePassword(e.password.Text()) != nil {
		return false
	}
	if !e.platform {
		return e.boot.Checked
	}
	return e.options().Actions != 0
}

func (p *settingsPane) showEraseDialog(info amt.BootInfo) *eraseDialog {
	args := map[string]any{"Name": p.m.name}
	e := &eraseDialog{platform: info.PlatformEraseSupported(), checks: map[amt.EraseActions]*dialogCheck{}}
	e.riskDialog = showRedConfirm(p.window(), redSpec{
		op: amt.OpErase, host: p.m.confirmHost(), device: p.m.confirmDevice(), width: 520,
		title:       lang.L("Erase drives on {{.Name}}?", args),
		warningBold: lang.L("This cannot be undone."),
		warningRest: lang.L("{{.Name}} restarts and erases what you select below. The operating system and all files on the drives are lost.", args),
		extra: func(d *riskDialog) []fyne.CanvasObject {
			list := container.New(layout.NewCustomPaddedVBoxLayout(0))
			if e.platform {
				for _, a := range amt.EraseOffered {
					supported := info.Erase.Platform&a != 0
					c := d.newCheck(notSupported(eraseActionText(a), supported), supported && a&eraseDefault != 0)
					if !supported {
						c.Disable()
					}
					c.OnChanged = func(bool) { d.Revalidate() }
					e.checks[a] = c
					list.Add(c)
				}
			} else {
				e.boot = d.newCheck(lang.L("Securely erase the boot drive"), true)
				e.boot.OnChanged = func(bool) { d.Revalidate() }
				list.Add(e.boot)
			}
			e.password = userPasswordField(d.panel, d.Revalidate)
			e.password.Entry.SetPlaceHolder(lang.L("Only if the drive has a password"))
			return []fyne.CanvasObject{
				formField(lang.L("Erase"), list),
				formField(lang.L("Drive password"), e.password),
				formHint(lang.L("Sent over TLS. Without TLS, OpenLAMT warns first.")),
			}
		},
		ready:  func() bool { return e.password != nil && e.ready() },
		button: lang.L("Erase Drives"),
		onConfirm: func(c amt.Confirmation) {
			o := e.options()
			e.password.SetText("")
			run := func() { p.runErase(o, c) }
			if o.Password == "" {
				run()
				return
			}
			p.m.warnWithoutTLS(p.window(), secretKey, "", run)
		},
		onCancel: func() { e.password.SetText("") },
	})
	return e
}

// runErase runs the confirmed erase: through the device window (consent, power action), or on
// the page itself. The page stays locked until it ends; then "Erase started".
func (p *settingsPane) runErase(o amt.EraseOptions, c amt.Confirmation) {
	d := p.d
	if d.busy || d.m.closed {
		return
	}
	ended := func(err error) {
		if err == nil && !d.m.closed {
			p.showEraseStarted()
		}
	}
	if d.m.deps.erase == nil {
		d.run(func() error { return d.m.sess.Erase(nil, o, c) }, func(err error) {
			if err != nil {
				d.fail(err)
			}
			ended(err)
		})
		return
	}
	d.busy, d.writeErr = true, nil
	d.src.notify()
	d.m.deps.erase(o, c, func(err error) {
		if d.m.closed {
			return
		}
		d.busy = false
		d.src.notify()
		ended(err) // the device window reports the errors
	})
}

// showEraseStarted is "Erase started" with "Open Event Log".
func (p *settingsPane) showEraseStarted() {
	p.eraseInfo = showPanel(p.window(), panelSpec{
		icon: icons.Check, tone: toneInfo, tileAbove: true, action: lang.L("OK"),
		title: lang.L("Erase started"),
	}, func(pn *panel) []fyne.CanvasObject {
		open := pn.newButton(lang.L("Open Event Log"), func() {
			pn.close()
			p.m.showPageWith(icons.EventLog, p.compact)
		})
		pn.extras = []fyne.CanvasObject{open}
		msg := widget.NewLabel(lang.L("{{.Name}} restarts and erases the drives. The BIOS reports progress and the result in the event log.",
			map[string]any{"Name": p.m.name}))
		msg.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{msg}
	})
}

// startErase is Erase Drives as a power action of the device window (manageDeps.erase): a
// poller job with the user consent (KVM's dialog) and the erase, the Power menu locked; errors
// go to the window's error report. done runs on the Fyne thread.
func (d *DeviceWindow) startErase(o amt.EraseOptions, c amt.Confirmation, done func(error)) {
	if d.powerBusy { // another power action holds the shared consent cancel (biosCancel)
		err := userError(lang.L("Wait until the running power action ends, then erase the drives."))
		d.onError(err)
		done(err)
		return
	}
	d.setPowerBusy(true)
	ctx, cancel := context.WithCancel(context.Background())
	d.biosCancel = cancel // shutdown ends a consent dialog through it, as for Reboot to BIOS
	prompt := &consentPrompt{ctx: ctx, ask: d.askConsent}
	d.poller.Do(func() error { return d.sess.Erase(prompt, o, c) }, func(err error) {
		cancel()
		fyne.Do(func() {
			d.biosCancel = nil
			d.powerDone(amt.PowerReset, err)
			done(err)
		})
	})
}

// describeErase covers the errors of Erase Drives and the Remote Platform Erase switch.
func describeErase(err error) (string, bool) {
	switch {
	case errors.Is(err, amt.ErrEraseNeedsTLS):
		return lang.L("Remote Platform Erase needs a TLS connection. Connect to the device with TLS and try again."), true
	case errors.Is(err, amt.ErrRPEOff):
		return lang.L("Turn on Remote Platform Erase under Remote features, then try again. If the switch fails, turn it on in the BIOS first."), true
	case errors.Is(err, amt.ErrRPEOffInBIOS):
		return lang.L("Turn it on in the BIOS first."), true
	}
	return "", false
}
