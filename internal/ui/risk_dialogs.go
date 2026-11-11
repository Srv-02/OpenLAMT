package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The dialogs of the device management design, built on showPanel (Esc cancels, the order of
// the buttons, the width):
//
//   - showQuestion (Dialogs.dc.html): a tinted symbol tile above the title, a text, a secondary
//     button and the blue main button; Enter confirms.
//   - showOrangeConfirm (Danger.dc.html, right): an orange warning box, an optional "old → new"
//     line and the checkbox "I understand the risk"; the red button stays disabled until it is
//     ticked and hands amt.ConfirmChecked's confirmation to onConfirm.
//   - showRedConfirm (Danger.dc.html, left and middle): a red warning box, optional fields and
//     "Type <phrase> to confirm"; the red button stays disabled until amt.Confirm accepts the
//     typed text and the extra condition holds.
//
// The orange and red dialogs are destructive: Enter never confirms them.

// showQuestion shows a question with a symbol tile of tone (toneInfo or toneWarning) above the
// title, text, the buttons cancel and confirm; onConfirm runs after the dialog closed through
// confirm.
func showQuestion(parent fyne.Window, icon icons.Name, tone tileTone, title, text, cancel, confirm string, onConfirm func()) *panel {
	return showPanel(parent, panelSpec{
		icon: icon, tone: tone, tileAbove: true,
		title: title, action: confirm, cancel: cancel, onAction: onConfirm,
	}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(text)
		msg.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{msg}
	})
}

// riskDialog is a shown orange or red confirmation. Its controls are exposed for the callers
// (extra fields call Revalidate) and for tests.
type riskDialog struct {
	*panel
	warnBox *canvas.Rectangle // background of the warning box
	check   *dialogCheck      // orange: "I understand the risk"
	entry   *dialogEntry      // red: the typed confirmation phrase
	border  *canvas.Rectangle // red: frame of entry, red while it holds a wrong phrase
	alarm   *canvas.Rectangle // red: the red frame again, over the entry's focus border
	field   *fyne.Container   // red: border, entry and alarm
	prefix  *canvas.Text      // red: text before the phrase in "Type … to confirm"
	phrase  *canvas.Text      // red: the phrase in code style

	validate func()
}

// Revalidate checks again whether the main button may be enabled; extra fields of a red
// confirmation call it when they change.
func (d *riskDialog) Revalidate() {
	if d.validate != nil && d.action != nil {
		d.validate()
	}
}

// setEnabled enables the main button while ok holds.
func (d *riskDialog) setEnabled(ok bool) {
	if ok {
		d.action.Enable()
	} else {
		d.action.Disable()
	}
}

// orangeSpec describes an orange confirmation (SeverityHigh operations).
type orangeSpec struct {
	op                 amt.Operation
	host               string // Session.Config().Host
	title, warning     string
	oldValue, newValue string // optional line "old → new" with the new value in semibold; both empty for none
	// danger draws the red warning box with the semibold warningBold before warning (Forms.dc.html
	// "Change TLS certificate"); otherwise the box is orange and warningBold is ignored.
	danger      bool
	warningBold string
	// extra builds content shown between the warning and the checkbox; it calls d.Revalidate
	// when it changes. May be nil.
	extra func(d *riskDialog) []fyne.CanvasObject
	// ready is the extra condition for the button; nil means none.
	ready     func() bool
	button    string
	onConfirm func(amt.Confirmation) // runs after the dialog closed through the button
	onCancel  func()                 // may be nil
}

// showOrangeConfirm shows an orange confirmation. The button stays disabled until the checkbox
// is ticked and amt.ConfirmChecked accepts op (only SeverityHigh operations).
func showOrangeConfirm(parent fyne.Window, s orangeSpec) *riskDialog {
	d := &riskDialog{}
	confirmation := func() (amt.Confirmation, error) {
		return amt.ConfirmChecked(s.op, s.host, d.check != nil && d.check.Checked)
	}
	d.panel = showPanel(parent, panelSpec{
		danger: true, redButton: true, flush: true, title: s.title, action: s.button, cancel: lang.L("Cancel"),
		onAction: func() {
			if s.ready != nil && !s.ready() {
				return
			}
			if c, err := confirmation(); err == nil && s.onConfirm != nil {
				s.onConfirm(c)
			}
		},
		onCancel: s.onCancel,
	}, func(p *panel) []fyne.CanvasObject {
		d.panel = p
		bold := ""
		if s.danger {
			bold = s.warningBold
		}
		var box fyne.CanvasObject
		box, d.warnBox = warningBox(s.danger, bold, s.warning)
		body := []fyne.CanvasObject{box}
		if s.oldValue != "" || s.newValue != "" {
			body = append(body, flushText(richLine(
				textSeg(s.oldValue+" → ", style.ColorNameControlText, false),
				textSeg(s.newValue, theme.ColorNameForeground, true))))
		}
		if s.extra != nil {
			body = append(body, s.extra(d)...)
		}
		d.check = p.newCheck(lang.L("I understand the risk"), false)
		d.check.OnChanged = func(bool) { d.Revalidate() }
		return append(body, d.check)
	})
	d.validate = func() {
		_, err := confirmation()
		d.setEnabled(err == nil && (s.ready == nil || s.ready()))
	}
	d.Revalidate()
	return d
}

// redSpec describes a red confirmation (SeverityLockout and more severe operations).
type redSpec struct {
	op                       amt.Operation
	host                     string // Session.Config().Host
	title                    string
	warningBold, warningRest string // the warning: a semibold first sentence and the rest
	// extra builds fields shown between the warning and the confirmation (the admin account
	// dialog); they call d.Revalidate when they change. May be nil.
	extra func(d *riskDialog) []fyne.CanvasObject
	// ready is the extra condition for the button (fields valid); nil means none.
	ready     func() bool
	button    string
	onConfirm func(amt.Confirmation) // runs after the dialog closed through the button
	onCancel  func()                 // may be nil
}

// confirmPhraseMark stands for the phrase while "Type {{.Phrase}} to confirm" is translated, so
// the translation can put the phrase anywhere and it is still drawn in code style.
const confirmPhraseMark = ""

// Geometry of the confirmation of Danger.dc.html: label 12 px semibold, the phrase in 12 px
// monospace on a gray chip (radius 4, 1 x 5 px padding), the entry framed with radius 7 and a
// 1 px border that turns red and 1.5 px for a wrong phrase.
const (
	confirmLabelSize   float32 = 12
	codeRadius         float32 = 4
	codePadH           float32 = 5
	codePadV           float32 = 1
	confirmFieldRadius float32 = 7
	mistypedWidth      float32 = 1.5
)

// showRedConfirm shows a red confirmation. The button stays disabled until amt.Confirm accepts
// the typed text (ConfirmPhrase: the host for these operations, case and surrounding spaces do
// not matter) and ready holds; a non-empty wrong text draws the field red.
func showRedConfirm(parent fyne.Window, s redSpec) *riskDialog {
	d := &riskDialog{}
	confirmation := func() (amt.Confirmation, error) {
		typed := ""
		if d.entry != nil {
			typed = d.entry.Text
		}
		return amt.Confirm(s.op, s.host, typed)
	}
	d.panel = showPanel(parent, panelSpec{
		danger: true, redButton: true, flush: true, title: s.title, action: s.button, cancel: lang.L("Cancel"),
		onAction: func() {
			if s.ready != nil && !s.ready() {
				return
			}
			if c, err := confirmation(); err == nil && s.onConfirm != nil {
				s.onConfirm(c)
			}
		},
		onCancel: s.onCancel,
	}, func(p *panel) []fyne.CanvasObject {
		d.panel = p
		var box fyne.CanvasObject
		box, d.warnBox = warningBox(true, s.warningBold, s.warningRest)
		body := []fyne.CanvasObject{box}
		if s.extra != nil {
			body = append(body, s.extra(d)...)
		}
		d.entry = p.newEntry(false)
		d.entry.OnChanged = func(string) { d.Revalidate() }
		d.border = &canvas.Rectangle{CornerRadius: confirmFieldRadius, StrokeWidth: 1}
		style.BindRect(d.border, style.ColorNameField, "")
		d.border.StrokeColor = style.Color(style.ColorNameControlBorder)
		d.alarm = style.BindRect(&canvas.Rectangle{CornerRadius: confirmFieldRadius, StrokeWidth: mistypedWidth, FillColor: color.Transparent},
			"", style.ColorNameDanger)
		d.alarm.Hide()
		d.field = container.NewStack(d.border, container.NewThemeOverride(d.entry, style.CardTheme{}), d.alarm)
		p.focus = d.entry
		return append(body, d.confirmLabel(amt.ConfirmPhrase(s.op, s.host)), d.field)
	})
	d.validate = func() {
		_, err := confirmation()
		mistyped := err != nil && strings.TrimSpace(d.entry.Text) != ""
		d.border.StrokeColor, d.border.StrokeWidth = style.Color(style.ColorNameControlBorder), 1
		if mistyped {
			d.border.StrokeColor, d.border.StrokeWidth = style.Color(style.ColorNameDanger), mistypedWidth
			d.alarm.Show()
		} else {
			d.alarm.Hide()
		}
		d.border.Refresh()
		d.setEnabled(err == nil && (s.ready == nil || s.ready()))
	}
	d.Revalidate()
	return d
}

// confirmLabel is "Type <phrase> to confirm" in one line, the phrase in code style.
func (d *riskDialog) confirmLabel(phrase string) fyne.CanvasObject {
	full := lang.L("Type {{.Phrase}} to confirm", map[string]any{"Phrase": confirmPhraseMark})
	before, after, _ := strings.Cut(full, confirmPhraseMark)
	d.prefix = designText(before, confirmLabelSize, style.ColorNameControlText, true)
	d.phrase = designText(phrase, confirmLabelSize, theme.ColorNameForeground, false)
	d.phrase.TextStyle = fyne.TextStyle{Monospace: true}
	chipBg := style.BindRect(&canvas.Rectangle{CornerRadius: codeRadius}, style.ColorNameCodeBackground, "")
	chip := container.NewStack(chipBg, container.New(layout.NewCustomPaddedLayout(codePadV, codePadV, codePadH, codePadH), d.phrase))
	return container.New(&rowLayout{gap: 0, fill: -1}, d.prefix, chip, designText(after, confirmLabelSize, style.ColorNameControlText, true))
}

// Geometry of the warning boxes (Danger.dc.html .warn): radius 10, 1 px border, 12 px padding,
// an 18 px triangle with stroke 2, 10 px from the text.
const (
	warnRadius   float32 = 10
	warnPad      float32 = 12
	warnGap      float32 = 10
	warnIconSize float32 = 18
	warnStroke   float32 = 2
)

// warningBox is the red (danger) or orange warning box: triangle and text, a semibold first
// sentence (bold, may be empty) and the rest. It returns the box and its background.
func warningBox(danger bool, bold, rest string) (fyne.CanvasObject, *canvas.Rectangle) {
	fill, border, text, variant := style.ColorNameWarningSurface, style.ColorNameWarningBorder, style.ColorNameWarningText, icons.WarningText
	if danger {
		fill, border, text, variant = style.ColorNameDangerSurface, style.ColorNameDangerBoxBorder, style.ColorNameDangerBoxText, icons.DangerText
	}
	var segs []*widget.TextSegment
	if bold != "" {
		segs = append(segs, textSeg(bold+" ", text, true))
	}
	segs = append(segs, textSeg(rest, text, false))
	icon := widgets.NewSymbol(icons.GetStroke(icons.Warning, variant, warnStroke), warnIconSize)
	// the rich text has the theme's inner padding around its text; the icon follows its first line
	pad := theme.InnerPadding()
	iconCol := container.New(layout.NewCustomPaddedLayout(pad-1, 0, 0, max(0, warnGap-pad)), container.NewVBox(icon))
	content := container.NewBorder(nil, nil, iconCol, nil, richLine(segs...))
	bg := style.BindRect(&canvas.Rectangle{CornerRadius: warnRadius, StrokeWidth: 1}, fill, border)
	inset := max(0, warnPad-pad)
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(inset, inset, warnPad, inset), content)), bg
}

// textSeg is an inline text segment in the color token c.
func textSeg(s string, c fyne.ThemeColorName, bold bool) *widget.TextSegment {
	return &widget.TextSegment{Text: s, Style: widget.RichTextStyle{
		Inline: true, ColorName: c, TextStyle: fyne.TextStyle{Bold: bold}, SizeName: theme.SizeNameText,
	}}
}

// richLine is a wrapping paragraph of inline segments.
func richLine(segs ...*widget.TextSegment) *widget.RichText {
	rs := make([]widget.RichTextSegment, len(segs))
	for i, s := range segs {
		rs[i] = s
	}
	rt := widget.NewRichText(rs...)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// dangerButtonTheme draws Fyne's danger button in the red of the device management design
// (ColorNameDanger) and, while disabled, light red with white text (ColorNameDangerDisabled).
type dangerButtonTheme struct{ fyne.Theme }

func (t dangerButtonTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameError:
		name = style.ColorNameDanger
	case theme.ColorNameDisabledButton:
		name = style.ColorNameDangerDisabled
	case theme.ColorNameDisabled:
		name = style.ColorNameWhite
	}
	return t.Theme.Color(name, v)
}
