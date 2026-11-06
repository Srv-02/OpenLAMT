package ui

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/validation"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

func showTrustDialog(parent fyne.Window, host, fp string, answer func(bool)) *panel {
	// Trusting a certificate is a security decision: no Enter, Cancel has the focus.
	return showPanel(parent, panelSpec{
		noEnter:  true,
		title:    lang.L("Trust this device?"),
		action:   lang.L("Trust device"),
		cancel:   lang.L("Cancel"),
		onAction: func() { answer(true) },
		onCancel: func() { answer(false) },
	}, func(p *panel) []fyne.CanvasObject {
		intro := widget.NewLabel(lang.L("OpenLAMT is connecting to {{.Host}} for the first time. The device presents this certificate (SHA-256):", map[string]any{"Host": host}))
		intro.Wrapping = fyne.TextWrapWord
		outro := widget.NewLabel(lang.L("If you trust it, OpenLAMT remembers the certificate and warns you if it ever changes."))
		outro.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{intro, fingerprintBlock(fp), outro}
	})
}

// fingerprintBlock shows a fingerprint as four monospace lines of eight bytes each, so it fits
// a narrow dialog and can be compared group by group.
func fingerprintBlock(fp string) fyne.CanvasObject {
	parts := strings.Split(amt.FormatFingerprint(fp), ":")
	var lines []string
	for i := 0; i < len(parts); i += 8 {
		lines = append(lines, strings.Join(parts[i:min(i+8, len(parts))], ":"))
	}
	block := widget.NewLabelWithStyle(strings.Join(lines, "\n"), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	return container.NewPadded(block)
}

func showMismatchDialog(parent fyne.Window, mm *amt.FingerprintMismatchError) {
	showPanel(parent, panelSpec{title: lang.L("Certificate changed"), action: lang.L("Close")},
		func(p *panel) []fyne.CanvasObject {
			warn := widget.NewLabel(lang.L("WARNING: the certificate of {{.Host}} does not match the trusted fingerprint. This may be a man-in-the-middle attack. The connection was aborted.", map[string]any{"Host": mm.Host}))
			warn.Importance = widget.DangerImportance
			warn.Wrapping = fyne.TextWrapWord
			hint := widget.NewLabel(lang.L("To trust a new certificate on purpose, edit the favorite and reset its fingerprint."))
			hint.Wrapping = fyne.TextWrapWord
			// danger labels draw in "error", the red of the button fill; the text red is lighter in dark
			return []fyne.CanvasObject{container.NewThemeOverride(warn, style.ErrorTextTheme{Theme: overrideTheme}),
				widget.NewLabel(lang.L("Expected")), fingerprintBlock(mm.Expected),
				widget.NewLabel(lang.L("Received")), fingerprintBlock(mm.Actual),
				hint}
		})
}

// showPasswordDialog asks for the password of user@host; Enter in the field connects.
func showPasswordDialog(parent fyne.Window, host, user string, offerSave bool, answer func(string, bool, bool)) {
	var pw *dialogEntry
	var save *dialogCheck
	showPanel(parent, panelSpec{
		title:    lang.L("Password for {{.User}}@{{.Host}}", map[string]any{"User": user, "Host": host}),
		action:   lang.L("Connect"),
		cancel:   lang.L("Cancel"),
		onAction: func() { answer(pw.Text, offerSave && save.Checked, true) },
		onCancel: func() { answer(pw.Text, offerSave && save.Checked, false) },
	}, func(p *panel) []fyne.CanvasObject {
		pw = p.newEntry(true)
		save = p.newCheck(lang.L("Save password"), offerSave)
		p.focus = pw
		body := []fyne.CanvasObject{container.NewBorder(nil, nil, widget.NewLabel(lang.L("Password")), nil, pw)}
		if offerSave {
			body = append(body, save)
		}
		return body
	})
}

// consentPattern is a consent code: six digits.
const consentPattern = `^[0-9]{6}$`

// showConsentDialog asks for the six digit consent code shown on the device; Continue stays
// disabled until the entry holds six digits. It returns a function that dismisses the dialog
// (answer is then called with ok false).
func showConsentDialog(parent fyne.Window, answer func(code string, ok bool)) func() {
	var code *dialogEntry
	p := showPanel(parent, panelSpec{
		title:    lang.L("User consent"),
		action:   lang.L("Continue"),
		cancel:   lang.L("Cancel"),
		onAction: func() { answer(code.Text, true) },
		onCancel: func() { answer(code.Text, false) },
	}, func(p *panel) []fyne.CanvasObject {
		code = p.newEntry(false)
		code.Validator = validation.NewRegexp(consentPattern, lang.L("The consent code must be six digits."))
		code.OnChanged = func(string) {
			if p.action == nil {
				return
			}
			if code.Validate() == nil {
				p.action.Enable()
			} else {
				p.action.Disable()
			}
		}
		p.focus = code
		hint := widget.NewLabel(lang.L("Enter the six-digit consent code shown on the device screen."))
		hint.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{hint, container.NewBorder(nil, nil, widget.NewLabel(lang.L("Consent code")), nil, code)}
	})
	p.action.Disable() // until six digits are entered
	return p.Dismiss
}

func showError(parent fyne.Window, err error) {
	var mm *amt.FingerprintMismatchError
	if errors.As(err, &mm) {
		showMismatchDialog(parent, mm)
		return
	}
	text, details := describe(err), ""
	var plain *plainError
	if !errors.As(err, &plain) && err.Error() != text {
		details = err.Error()
	}
	showMessage(parent, lang.L("Something went wrong"), text, details)
}

// showMessage is a panel dialog with a wrapped, left-aligned text and an OK button. Non-empty
// details (the technical error) are not shown but can be copied for a bug report.
func showMessage(parent fyne.Window, title, text, details string) {
	showPanel(parent, panelSpec{title: title, action: lang.L("OK")}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(text)
		msg.Wrapping = fyne.TextWrapWord
		if details != "" {
			var copyBtn *escButton
			copyBtn = p.newButton(lang.L("Copy details"), func() {
				fyne.CurrentApp().Clipboard().SetContent(details)
				copyBtn.SetText(lang.L("Copied"))
				copyBtn.Disable()
			})
			p.extras = []fyne.CanvasObject{copyBtn}
		}
		return []fyne.CanvasObject{msg}
	})
}

// confirmText is title, message and action button of a power confirmation (HANDOFF.md 5.3).
func confirmText(a amt.PowerAction, name string) (title, message, button string) {
	args := map[string]any{"Name": name}
	switch a {
	case amt.PowerReset:
		return lang.L("Reset {{.Name}}?", args),
			lang.L("The machine restarts immediately without shutting down the operating system. Unsaved data and running VMs are lost."),
			lang.L("Reset")
	case amt.PowerCycle:
		return lang.L("Power cycle {{.Name}}?", args),
			lang.L("The machine is switched off and on again immediately, without shutting down the operating system. Unsaved data and running VMs are lost."),
			lang.L("Power Cycle")
	case amt.PowerShutdown:
		return lang.L("Shut down {{.Name}}?", args),
			lang.L("The operating system is shut down in an orderly way, then the machine switches off. Unsaved data in open programs can be lost."),
			lang.L("Shut Down")
	case amt.PowerOff:
		return lang.L("Power off {{.Name}}?", args),
			lang.L("The machine is switched off immediately, without shutting down the operating system. Unsaved data and running VMs are lost."),
			lang.L("Power Off")
	case amt.BootToBIOS:
		return lang.L("Restart {{.Name}} into BIOS setup?", args),
			lang.L("The machine restarts immediately and opens the BIOS setup. The operating system is not shut down."),
			lang.L("Restart")
	}
	return a.String(), "", lang.L("Continue")
}

// showPowerConfirm asks before a destructive power action: a red symbol tile, title and message,
// then Cancel and the red action button. Cancel is the safe choice: it has the focus, Esc
// cancels and Enter does not run the action. proceed runs only for the action button.
func showPowerConfirm(parent fyne.Window, action amt.PowerAction, name string, proceed func()) *widget.PopUp {
	title, message, button := confirmText(action, name)
	return showConfirm(parent, powerIcon(action), title, message, button, proceed, nil)
}

// showConfirm is the confirm dialog with a free text and symbol; cancelled (may be nil) runs
// when the dialog closes without the action button.
func showConfirm(parent fyne.Window, icon icons.Name, title, message, button string, proceed, cancelled func()) *widget.PopUp {
	return showPanel(parent, panelSpec{
		icon: icon, danger: true, title: title, action: button, cancel: lang.L("Cancel"),
		onAction: proceed, onCancel: cancelled,
	}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(message)
		msg.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{msg}
	}).pop
}
