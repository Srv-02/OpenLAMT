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
//   - showOrangeConfirm (Danger.dc.html, right): an orange warning box (or a plain text,
//     Extras.dc.html), an optional "old → new" line and the checkbox "I understand the risk for
//     <device>"; the red button stays disabled until it is ticked and hands amt.ConfirmChecked's
//     confirmation to onConfirm.
//   - showRedConfirm (Danger.dc.html, left and middle; RedCheck.dc.html): a red warning box,
//     optional fields and the same checkbox; the red button stays disabled until it is ticked and
//     the extra condition holds.
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
	check   *dialogCheck      // "I understand the risk for <device>"

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
	// op is the operation amt.ConfirmChecked confirms. Empty: the change needs no
	// amt.Confirmation (deleting a user); the checkbox alone enables the button and onConfirm gets
	// the zero Confirmation.
	op             amt.Operation
	host           string // Session.Config().Host
	device         string // the device in the checkbox text (deviceLabel); empty: host
	title, warning string
	// text is a plain paragraph in place of the warning box (Extras.dc.html); used when warning
	// is empty.
	text               string
	oldValue, newValue string // optional line "old → new" with the new value in semibold; both empty for none
	// danger draws the red warning box with the semibold warningBold before warning (Forms.dc.html
	// "Change TLS certificate"); otherwise the box is orange and warningBold is ignored.
	danger      bool
	warningBold string
	// lead builds content shown above the warning (the field of "Change host name",
	// HostEnv.dc.html); it calls d.Revalidate when it changes. May be nil.
	lead func(d *riskDialog) []fyne.CanvasObject
	// extra builds content shown between the warning and the checkbox; it calls d.Revalidate
	// when it changes. May be nil.
	extra func(d *riskDialog) []fyne.CanvasObject
	// ready is the extra condition for the button; nil means none.
	ready     func() bool
	button    string
	onConfirm func(amt.Confirmation) // runs after the dialog closed through the button
	onCancel  func()                 // may be nil
}

// showOrangeConfirm shows an orange confirmation (or, with danger, the red one of
// showRedConfirm). The button stays disabled until the checkbox is ticked and
// amt.ConfirmChecked accepts op.
func showOrangeConfirm(parent fyne.Window, s orangeSpec) *riskDialog {
	d := &riskDialog{}
	confirmation := func() (amt.Confirmation, error) {
		checked := d.check != nil && d.check.Checked
		if s.op == "" {
			if !checked {
				return amt.Confirmation{}, amt.ErrConfirmationMismatch
			}
			return amt.Confirmation{}, nil
		}
		return amt.ConfirmChecked(s.op, s.host, checked)
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
		var body []fyne.CanvasObject
		if s.lead != nil {
			body = append(body, s.lead(d)...)
		}
		if s.warning == "" && s.text != "" {
			body = append(body, flushText(richLine(textSeg(s.text, style.ColorNameControlText, false))))
		} else {
			bold := ""
			if s.danger {
				bold = s.warningBold
			}
			var box fyne.CanvasObject
			box, d.warnBox = warningBox(s.danger, bold, s.warning)
			body = append(body, box)
		}
		if s.oldValue != "" || s.newValue != "" {
			body = append(body, flushText(richLine(
				textSeg(s.oldValue+" → ", style.ColorNameControlText, false),
				textSeg(s.newValue, theme.ColorNameForeground, true))))
		}
		if s.extra != nil {
			body = append(body, s.extra(d)...)
		}
		d.check = p.newCheck(riskCheckText(s.device, s.host), false)
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
	device                   string // the device in the checkbox text (deviceLabel); empty: host
	title                    string
	warningBold, warningRest string // the warning: a semibold first sentence and the rest
	// extra builds fields shown between the warning and the checkbox (the admin account
	// dialog); they call d.Revalidate when they change. May be nil.
	extra func(d *riskDialog) []fyne.CanvasObject
	// ready is the extra condition for the button (fields valid); nil means none.
	ready     func() bool
	button    string
	onConfirm func(amt.Confirmation) // runs after the dialog closed through the button
	onCancel  func()                 // may be nil
}

// showRedConfirm shows a red confirmation: the orange dialog with the red warning box. The
// checkbox names the device, so the confirmation still says which device it hits; the button
// stays disabled until it is ticked and ready holds.
func showRedConfirm(parent fyne.Window, s redSpec) *riskDialog {
	return showOrangeConfirm(parent, orangeSpec{
		op: s.op, host: s.host, device: s.device, title: s.title,
		danger: true, warningBold: s.warningBold, warning: s.warningRest,
		extra: s.extra, ready: s.ready, button: s.button, onConfirm: s.onConfirm, onCancel: s.onCancel,
	})
}

// riskCheckText is the checkbox of the orange and red confirmations, naming the device: its
// label (deviceLabel), else the host, else none.
func riskCheckText(device, host string) string {
	if device = strings.TrimSpace(device); device == "" {
		device = strings.TrimSpace(host)
	}
	if device == "" {
		return lang.L("I understand the risk")
	}
	return lang.L("I understand the risk for {{.Device}}", map[string]any{"Device": device})
}

// deviceLabel names a device in a confirmation: "name (host)", or one of them when the other
// is empty or both are the same.
func deviceLabel(name, host string) string {
	name, host = strings.TrimSpace(name), strings.TrimSpace(host)
	switch {
	case name == "" || strings.EqualFold(name, host):
		return host
	case host == "":
		return name
	}
	return name + " (" + host + ")"
}

// confirmFieldRadius is the corner radius of the framed fields in the confirmations.
const confirmFieldRadius float32 = 7

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
