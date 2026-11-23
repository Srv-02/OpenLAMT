package ui

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/localme"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The "Setup" card of "This computer" (#11, design section "local"): below the check result
// exactly one setup offer for the stored state (localme.Result.Setup). Turn On and Activate
// confirm in orange, Deactivate in red; each confirmation runs one elevated helper that changes
// Intel AMT and reads it again, so the card shows the new state afterwards. Buttons and dialogs
// carry the "Beta" pill of Check.

// localSetupGap is the space between the result card and the Setup heading.
const localSetupGap float32 = 18

// newLocalSetup is the "Setup" heading and the card with the setup offer for r; nil when there
// is none. While a helper runs, the button is disabled.
func (s *StartWindow) newLocalSetup(r localme.Result) fyne.CanvasObject {
	turnOnText := lang.L("Intel AMT then answers on the network. Turning it on from here needs AMT 16.1 or newer.")
	deactivateText := lang.L("Deletes all AMT settings and accounts on this computer.")
	var title, text, hint string
	var button *widgets.PrimaryButton
	switch r.Setup() {
	case localme.SetupTurnOn:
		title, text = lang.L("Turn on Intel AMT"), turnOnText
		button = settingsButton(lang.L("Turn On…"), s.confirmTurnOn)
	case localme.SetupTurnOnBIOS:
		title, text, hint = lang.L("Turn on Intel AMT"), turnOnText, lang.L("Turn on Intel AMT in the BIOS (MEBx).")
	case localme.SetupActivate:
		title = lang.L("Activate in Client Control Mode (CCM)")
		text = lang.L("Each KVM session and remote boot then asks for a code shown on this screen. For headless machines, set up Admin Control Mode (ACM) in the BIOS (MEBx).")
		button = settingsButton(lang.L("Activate…"), s.showActivate)
	case localme.SetupDeactivate:
		title, text = lang.L("Deactivate Intel AMT"), deactivateText
		button = settingsButton(lang.L("Deactivate…"), s.confirmDeactivate)
		m := *button.Metrics // the red tinted button of the design (.b.rtint)
		m.Neutral, m.NeutralBorder, m.NeutralText = style.ColorNameSignedInSurface, style.ColorNameSignedInBorder, style.ColorNameDangerZoneText
		button.Metrics = &m
	case localme.SetupACM:
		title, text = lang.L("Deactivate Intel AMT"), deactivateText
		hint = lang.L("Deactivate Intel AMT in the BIOS (MEBx). Admin Control Mode cannot be deactivated from the app.")
	default:
		return nil
	}
	pad := theme.InnerPadding()
	lines := []fyne.CanvasObject{designText(title, 14, theme.ColorNameForeground, true),
		container.New(layout.NewCustomPaddedLayout(0, -pad, -pad, -pad), richLine(textSeg(text, style.ColorNameTextSecondary, false)))}
	if hint != "" {
		lines = append(lines, container.New(layout.NewCustomPaddedLayout(0, -pad, -pad, -pad), richLine(textSeg(hint, theme.ColorNameForeground, false))))
	}
	col := container.New(layout.NewCustomPaddedVBoxLayout(3-pad), lines...)
	var row fyne.CanvasObject = col
	if button != nil {
		if s.localChecking {
			button.Disable()
		}
		s.setupBtn = button
		right := container.New(layout.NewCustomPaddedHBoxLayout(8), container.NewCenter(newBetaPill()), container.NewCenter(button))
		row = container.NewBorder(nil, nil, nil, container.New(layout.NewCustomPaddedLayout(0, 0, 24, 0), right), col)
	}
	card := widgets.NewPanel(style.ColorNameCard, style.ColorNameCardBorder, 12, row, 14, 16, 14, 16)
	head := container.New(layout.NewCustomPaddedLayout(0, 0, 14, 0), sectionLabel(lang.L("Setup")))
	return container.New(layout.NewCustomPaddedVBoxLayout(6), head, card)
}

// localRiskDevice names this computer in the checkbox of the confirmations: "this computer
// (<host name>)".
func localRiskDevice() string {
	if h, err := hostname(); err == nil && strings.TrimSpace(h) != "" {
		return lang.L("this computer ({{.Name}})", map[string]any{"Name": strings.TrimSpace(h)})
	}
	return lang.L("this computer")
}

// betaNote is the "Beta" pill with its hint inside a setup confirmation.
func betaNote() fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedHBoxLayout(8), container.NewCenter(newBetaPill()),
		designText(betaHint(), 11.5, style.ColorNameTextSecondary, false))
}

// confirmTurnOn is the orange confirmation of "Turn On…".
func (s *StartWindow) confirmTurnOn() {
	s.localDialog = showOrangeConfirm(s.win, orangeSpec{
		device:  localRiskDevice(),
		title:   lang.L("Turn on Intel AMT on this computer?"),
		warning: lang.L("Intel AMT starts answering on the network (ports 16992 and 16993). Anyone with an AMT password can then control this computer, also while it is switched off."),
		extra:   func(*riskDialog) []fyne.CanvasObject { return []fyne.CanvasObject{betaNote()} },
		button:  lang.L("Turn On AMT"),
		onConfirm: func(amt.Confirmation) {
			s.runLocalAction(localme.ActionTurnOn, nil)
		},
	})
}

// confirmDeactivate is the red confirmation of "Deactivate…" (V16).
func (s *StartWindow) confirmDeactivate() {
	s.localDialog = showRedConfirm(s.win, redSpec{
		device:      localRiskDevice(),
		title:       lang.L("Deactivate Intel AMT on this computer?"),
		warningBold: lang.L("This deletes all Intel AMT settings."),
		warningRest: lang.L("Accounts, certificates, Wi-Fi profiles and network settings in AMT are removed. Remote access stops until AMT is activated again."),
		extra:       func(*riskDialog) []fyne.CanvasObject { return []fyne.CanvasObject{betaNote()} },
		button:      lang.L("Deactivate"),
		onConfirm: func(amt.Confirmation) {
			s.runLocalAction(localme.ActionDeactivate, nil)
		},
	})
}

// activateFields are the password fields of the Activate dialog.
type activateFields struct {
	pw, repeat *widgets.PasswordField
}

// valid: the password follows AMT's rule and both fields match.
func (f *activateFields) valid() bool {
	return f.pw != nil && localme.ValidatePassword([]byte(f.pw.Text())) == nil && f.pw.Text() == f.repeat.Text()
}

// showActivate is the orange confirmation of "Activate…" with the new admin password. The
// password is checked here before anything is elevated; it is not stored anywhere.
func (s *StartWindow) showActivate() {
	f := &activateFields{}
	s.localDialog = showOrangeConfirm(s.win, orangeSpec{
		device: localRiskDevice(),
		title:  lang.L("Activate Intel AMT in Client Control Mode?"),
		lead: func(d *riskDialog) []fyne.CanvasObject {
			f.pw = userPasswordField(d.panel, d.Revalidate)
			f.repeat = userPasswordField(d.panel, d.Revalidate)
			f.pw.Entry.SetPlaceHolder(lang.L("Password"))
			f.repeat.Entry.SetPlaceHolder(lang.L("Repeat password"))
			return []fyne.CanvasObject{
				formField(lang.L("Admin password"), container.NewGridWithColumns(2, f.pw, f.repeat)),
				formHint(lang.L("8–32 ASCII characters with upper and lower case, a digit and a symbol. The user name is admin.")),
			}
		},
		warning: lang.L("In Client Control Mode, every KVM session and every remote boot asks for a 6-digit code shown on this computer’s screen. Without someone at the screen, KVM does not work. Admin Control Mode (ACM) without the code is set up in the BIOS (MEBx)."),
		extra:   func(*riskDialog) []fyne.CanvasObject { return []fyne.CanvasObject{betaNote()} },
		ready:   f.valid,
		button:  lang.L("Activate"),
		width:   520,
		onConfirm: func(amt.Confirmation) {
			pw := []byte(f.pw.Text())
			f.pw.SetText("")
			f.repeat.SetText("")
			s.runLocalAction(localme.ActionActivateCCM, pw)
		},
	})
}

// runLocalAction runs a setup action like Check: the operating system asks for administrator
// rights, the helper changes Intel AMT and answers the new state, which is stored and shown.
// A refusal of the firmware shows "Intel AMT refused the change" next to the new state; when
// the Intel ME no longer reports Intel AMT, the card "No Intel AMT found" says it all. A foreign
// program on an LMS port stops the activation with its own error.
// password (activation only) is wiped once the helper got it.
func (s *StartWindow) runLocalAction(a localme.Action, password []byte) {
	if s.localChecking || s.localAction == nil {
		clear(password)
		return
	}
	s.localChecking = true
	s.refreshLocal()
	run := s.localAction
	s.localRun(func() {
		res, err := run(a, password)
		clear(password)
		fyne.Do(func() {
			s.localDone(res.State, err)
			var foreign *localme.ForeignLMSError
			if refused := res.Err(); err == nil && refused != nil && (res.State.Found || errors.As(refused, &foreign)) {
				s.onError(refused)
			}
		})
	})
}
