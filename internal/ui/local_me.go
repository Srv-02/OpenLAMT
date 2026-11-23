package ui

import (
	"errors"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/localme"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// "This computer" (#11, design V15): a sidebar section above "Favorites" that shows only when
// this computer has an Intel ME device. The detection runs once at start, without
// administrator rights (internal/localme). "Check" reads Intel AMT through the Intel ME in a
// short elevated helper process; the app itself never runs elevated. The last result is stored
// and shown as the row's subline. Below the result the "Setup" card offers exactly one action
// (local_setup.go): turn Intel AMT on, activate it in Client Control Mode or deactivate it.

// buildLocalSection returns the hidden section: heading and the "This computer" row.
func (s *StartWindow) buildLocalSection() fyne.CanvasObject {
	s.localRow = widgets.NewSidebarRow(lang.L("This computer"), lang.L("Intel ME found, not checked"))
	s.localRow.SetIcon(icons.Hardware)
	s.localRow.SetDot(widgets.DotUnknown)
	s.localRow.OnTapped = s.selectLocal
	heading := container.New(layout.NewCustomPaddedLayout(8, 6, 20, 20), sectionLabel(lang.L("This computer")))
	row := container.New(layout.NewCustomPaddedLayout(0, 4, 10, 10), s.localRow)
	s.localSection = container.NewVBox(heading, row)
	s.localSection.Hide()
	return s.localSection
}

// detectLocal runs the detection, shows the section when an Intel ME device is present and
// loads the last check result.
func (s *StartWindow) detectLocal() {
	s.meFound = s.localME != nil && s.localME()
	setShown(s.localSection, s.meFound)
	s.localLast = nil
	if r, ok := s.settings.LastLocalCheck(); ok {
		s.localLast = &r
	}
	s.refreshLocal()
}

// selectLocal shows the detail of "This computer"; an edit in progress is discarded.
func (s *StartWindow) selectLocal() {
	s.selKey, s.dupOf = "", nil
	s.markSelection()
	s.setMode(modeLocal)
}

// localMeta is the metadata line of the "This computer" detail: "<hostname> · Intel ME found".
func localMeta() string {
	found := lang.L("Intel ME found")
	if h, err := os.Hostname(); err == nil && h != "" {
		return h + " · " + found
	}
	return found
}

// buildLocalActions builds the header of the detail: the "Beta" pill and Check, below them
// the two hints, all right-aligned.
func (s *StartWindow) buildLocalActions() {
	s.checkBtn = widgets.NewPrimaryButton(lang.L("Check"), icons.GetStroke(icons.AuditLog, icons.White, 2), s.runLocalCheck)
	s.betaPill = newBetaPill()
	hint := func(t string) fyne.CanvasObject {
		c := designText(t, 11.5, style.ColorNameTextSecondary, false)
		c.Alignment = fyne.TextAlignTrailing
		return c
	}
	row := container.NewBorder(nil, nil, nil,
		container.New(layout.NewCustomPaddedHBoxLayout(8), container.NewCenter(s.betaPill), s.checkBtn))
	s.localActions = container.New(layout.NewCustomPaddedVBoxLayout(4), row,
		hint(lang.L("Asks for administrator rights.")),
		hint(betaHint()))
	s.localCard = container.NewStack()
}

// newBetaPill is the gray "Beta" pill of everything that uses rpc-go's preview (#11): Check, the
// setup buttons and their dialogs.
func newBetaPill() fyne.CanvasObject { return wsmanPill(lang.L("Beta")) }

// betaHint is the note that goes with the "Beta" pill.
func betaHint() string { return lang.L("Uses a preview of Intel’s rpc-go library.") }

// refreshLocal shows the last result in the row, the button and the card.
func (s *StartWindow) refreshLocal() {
	if s.localRow != nil {
		s.localRow.Set(lang.L("This computer"), localSubline(s.localLast))
	}
	if s.checkBtn == nil {
		return
	}
	if s.localChecking {
		s.checkBtn.Disable()
	} else {
		s.checkBtn.Enable()
	}
	var card fyne.CanvasObject
	s.setupBtn = nil
	switch r := s.localLast; {
	case r == nil:
		card = newEmptyCard(lang.L("Not checked yet"),
			lang.L("Check reads Intel AMT through the Intel ME driver of this computer. Nothing is changed."))
	case !r.Found:
		card = newLocalNoneCard()
	default:
		card = newLocalResultCard(*r)
		if setup := s.newLocalSetup(*r); setup != nil {
			card = container.New(layout.NewCustomPaddedVBoxLayout(localSetupGap), card, setup)
		}
	}
	s.localCard.Objects = []fyne.CanvasObject{card}
	if s.cards != nil { // lays out the header again for the new card
		s.cards.Refresh()
	}
}

// runLocalCheck asks the operating system for administrator rights and runs the helper; no
// dialog comes first (V25). Declined rights change nothing.
func (s *StartWindow) runLocalCheck() {
	if s.localChecking || s.localCheck == nil {
		return
	}
	s.localChecking = true
	s.refreshLocal()
	check := s.localCheck
	s.localRun(func() {
		r, err := check()
		fyne.Do(func() { s.localDone(r, err) })
	})
}

// localDone shows the end of a helper run on the Fyne thread: refused rights as information,
// a failure as an error, a result as the new stored state.
func (s *StartWindow) localDone(r localme.Result, err error) {
	s.localChecking = false
	switch {
	case errors.Is(err, localme.ErrDeclined):
		showMessage(s.win, lang.L("This computer"), describe(err), "")
	case err != nil:
		s.onError(err)
	default:
		r.Checked = time.Now()
		s.settings.SetLastLocalCheck(r)
		s.localLast = &r
	}
	s.refreshLocal()
}

// localSubline is the row's subline: the stored result, e.g. "AMT 16.1 · not activated".
func localSubline(r *localme.Result) string {
	switch {
	case r == nil:
		return lang.L("Intel ME found, not checked")
	case !r.Found:
		return lang.L("No Intel AMT found")
	}
	v := "AMT " + r.ShortVersion()
	switch {
	case r.TurnedOff:
		return v + " · " + lang.L("turned off")
	case r.ControlMode == localme.ModeNone:
		return v + " · " + lang.L("not activated")
	case r.ControlMode == localme.ModeCCM:
		return v + " · CCM"
	case r.ControlMode == localme.ModeACM:
		return v + " · ACM"
	}
	return v
}

// localActivation is the value of the Activation row.
func localActivation(mode string) string {
	switch mode {
	case localme.ModeNone:
		return lang.L("Not activated")
	case localme.ModeCCM:
		return lang.L("Client Control Mode (CCM)")
	case localme.ModeACM:
		return lang.L("Admin Control Mode (ACM)")
	}
	return lang.L("Unknown")
}

// localNetworkText is the value of the Network row: the connected interface (wired first), its
// address and DHCP or Static, e.g. "Wired · 192.0.2.50 · DHCP".
func localNetworkText(r localme.Result) string {
	type iface struct {
		l    *localme.Link
		name string
	}
	all := []iface{{r.Wired, lang.L("Wired")}, {r.Wireless, lang.L("Wireless")}}
	var pick *iface
	for i := range all {
		if all[i].l != nil && all[i].l.LinkUp {
			pick = &all[i]
			break
		}
	}
	if pick == nil {
		for i := range all {
			if all[i].l != nil {
				return all[i].name + " · " + lang.L("No link")
			}
		}
		return lang.L("Unknown")
	}
	out := pick.name
	if pick.l.IP != "" {
		out += " · " + pick.l.IP
	}
	if pick.l.DHCP {
		return out + " · " + lang.L("DHCP")
	}
	return out + " · " + lang.L("Static")
}

// formatCheckTime is the time of the last check in local time: "Nov 23, 2026 10:14".
func formatCheckTime(t time.Time) string {
	if t.IsZero() {
		return lang.L("Unknown")
	}
	return t.Local().Format("Jan 2, 2006 15:04")
}

// newLocalResultCard is the card of a found Intel AMT: state, version, activation, network and
// the time of the check.
func newLocalResultCard(r localme.Result) fyne.CanvasObject {
	value := func(t string) fyne.CanvasObject {
		l := widget.NewLabel(t)
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	dot, state := widgets.DotOn, lang.L("On")
	if r.TurnedOff {
		dot, state = widgets.DotOff, lang.L("Turned off")
	}
	status := container.New(statusLineLayout{}, widgets.NewStatusDot(dot, style.StatusDotSize), value(state))
	card := widgets.NewFormCard()
	card.SetRows(
		widgets.NewFormRow(lang.L("Intel AMT"), status),
		widgets.NewFormRow(lang.L("Version"), value(r.Version)),
		widgets.NewFormRowHelp(lang.L("Activation"), activationHelp(), value(localActivation(r.ControlMode))),
		widgets.NewFormRow(lang.L("Network"), value(localNetworkText(r))),
		widgets.NewFormRow(lang.L("Last check"), value(formatCheckTime(r.Checked))),
	)
	return card
}

// activationHelp is the ⓘ of the Activation row (#8).
func activationHelp() *widgets.InfoButton {
	return widgets.NewInfoButton(widgets.HelpContent{
		Title: lang.L("Activation"),
		What:  lang.L("Intel AMT answers remote requests only once it is activated. In Client Control Mode (CCM) every KVM session and every remote boot asks for a code shown on this computer’s screen. Admin Control Mode (ACM) needs no code; it is set up in the BIOS (MEBx)."),
		When:  lang.L("Activate Intel AMT before you add this computer as a favorite on another computer."),
	})
}

// newLocalNoneCard is the card "No Intel AMT found" with the possible reasons. It never says
// that the hardware does not support Intel AMT.
func newLocalNoneCard() fyne.CanvasObject {
	text := func(t string) *widget.TextSegment {
		return &widget.TextSegment{Text: t, Style: widget.RichTextStyle{ColorName: style.ColorNameTextSecondary, SizeName: style.SizeNameSmallText}}
	}
	title := designText(lang.L("No Intel AMT found"), 14, theme.ColorNameForeground, true)
	body := widget.NewRichText(text(lang.L("The Intel ME did not report Intel AMT. Possible reasons:")),
		&widget.ListSegment{Items: []widget.RichTextSegment{
			text(lang.L("Intel AMT is turned off in the BIOS.")),
			text(lang.L("The Intel ME driver is missing or outdated.")),
			text(lang.L("This computer is not a vPro platform.")),
		}})
	body.Wrapping = fyne.TextWrapWord
	col := container.New(layout.NewCustomPaddedVBoxLayout(4), title, body)
	return widgets.NewPanel(style.ColorNameCard, style.ColorNameCardBorder, 10, col, 18, 16, 18, 16)
}
