package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// secretKind is what a dialog sends to the device: the question without TLS names it.
type secretKind int

const (
	// secretPassword is the digest of an account password (Add user, Edit user, admin account).
	// The digest is the HTTP digest credential itself, so capturing it is as good as the password.
	secretPassword secretKind = iota
	// secretKey is any other password or key sent to the device (Wi-Fi key, erase password,
	// target password).
	secretKey
)

// warnWithoutTLS runs proceed after the question "Send the password without TLS?" (issue #37,
// Users.dc.html): at once when the session has TLS or the question was hidden with "Don’t show
// again", else after "Send Anyway". Cancel ends there. user names the account the digest
// signs in as (secretPassword); every dialog that sends a secret to the device goes through
// here, so one box hides the question for all of them. It returns the question, nil if none
// was shown.
func (m *manager) warnWithoutTLS(parent fyne.Window, kind secretKind, user string, proceed func()) *panel {
	m.tlsAsk = nil
	settings := NewSettings(m.app.Preferences())
	if m.sess.Config().UseTLS || settings.HideTLSWarning() {
		proceed()
		return nil
	}
	title := lang.L("Send the password without TLS?")
	text := lang.L("The connection to {{.Name}} has no TLS. The password travels as a digest that works like the password itself. Anyone who captures it on the network can sign in as {{.User}}.",
		map[string]any{"Name": m.name, "User": user})
	if kind == secretKey {
		title = lang.L("Send the key without TLS?")
		text = lang.L("The connection to {{.Name}} has no TLS. The key travels unencrypted. Anyone who captures it on the network can use it.",
			map[string]any{"Name": m.name})
	}
	var hide *dialogCheck
	m.tlsAsk = showPanel(parent, panelSpec{
		icon: icons.Warning, tone: toneWarning, tileAbove: true, noEnter: true,
		title: title, action: lang.L("Send Anyway"), cancel: lang.L("Cancel"),
		onAction: func() {
			if hide.Checked {
				settings.SetHideTLSWarning(true)
			}
			proceed()
		},
	}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(text)
		msg.Wrapping = fyne.TextWrapWord
		hide = p.newCheck(lang.L("Don’t show again"), false)
		return []fyne.CanvasObject{msg, hide}
	})
	return m.tlsAsk
}
