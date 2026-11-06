package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The question dialogs (delete, power, quit, error, certificates, password, consent code) all
// come from showPanel, in the look of the delete dialog: an optional symbol tile at the left,
// a bold title, free content, and the buttons at the right. The order of the buttons is the
// macOS one: the main action at the far right, Cancel directly left of it, further buttons
// (Copy details) to the left of Cancel. A destructive action is red, takes no Enter and gives
// the focus to Cancel; every other dialog confirms with Enter. Esc always cancels.

// Geometry shared by all panel dialogs.
const (
	dialogWidth     float32 = 460 // the same for every panel dialog
	panelPadTop     float32 = 22
	panelPadBottom  float32 = 18
	panelPadH       float32 = 22
	panelGap        float32 = 14
	panelTileSize   float32 = 40
	panelTileRadius float32 = 10
	panelIconSize   float32 = 22
)

// panelSpec describes a panel dialog.
type panelSpec struct {
	icon     icons.Name // symbol in the red tile; "" for none
	danger   bool       // the action destroys something: red button, no Enter, Cancel has the focus
	noEnter  bool       // Enter never confirms and Cancel has the focus, without the red button (trusting a certificate)
	title    string
	action   string // text of the main button
	cancel   string // text of the Cancel button; "" for none
	onAction func() // runs after the dialog closed through the main button; may be nil
	onCancel func() // runs after it closed any other way; may be nil
	keep     bool   // the main button (and Enter) runs onAction without closing; onAction closes with p.close
}

// panel is a shown panel dialog.
type panel struct {
	spec    panelSpec
	canvas  fyne.Canvas
	prevKey func(*fyne.KeyEvent)
	pop     *widget.PopUp
	frame   *fyne.Container // the content of the dialog, dialogWidth wide
	action  *escButton
	cancel  *escButton
	extras  []fyne.CanvasObject // buttons left of Cancel; set by build
	focus   fyne.Focusable      // gets the focus when the dialog opens; set by build
	done    bool
}

// showPanel shows a panel dialog on parent. build runs before the dialog is shown: it returns
// the content below the title and may set p.extras and p.focus; the controls it creates with
// p.newEntry, p.newCheck and p.newButton close the dialog on Esc.
func showPanel(parent fyne.Window, spec panelSpec, build func(p *panel) []fyne.CanvasObject) *panel {
	c := parent.Canvas()
	p := &panel{spec: spec, canvas: c, prevKey: c.OnTypedKey()}
	var body []fyne.CanvasObject
	if build != nil {
		body = build(p)
	}
	p.action = p.newButton(spec.action, p.act)
	p.action.Importance = widget.HighImportance
	if spec.danger {
		p.action.Importance = widget.DangerImportance
	}
	buttons := []fyne.CanvasObject{layout.NewSpacer()}
	buttons = append(buttons, p.extras...)
	if spec.cancel != "" {
		p.cancel = newEscButton(spec.cancel, func() { p.finish(false) })
		buttons = append(buttons, p.cancel)
	}
	buttons = append(buttons, p.action)

	head := widget.NewLabelWithStyle(spec.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	head.SizeName = theme.SizeNameSubHeadingText
	head.Wrapping = fyne.TextWrapWord
	col := container.NewVBox(append([]fyne.CanvasObject{head}, body...)...)
	var content fyne.CanvasObject = col
	if spec.icon != "" {
		tileBg := style.BindRect(&canvas.Rectangle{CornerRadius: panelTileRadius}, style.ColorNameDangerTile, "")
		tileBg.SetMinSize(fyne.NewSquareSize(panelTileSize))
		img := widgets.NewSymbol(icons.GetStroke(spec.icon, icons.Danger, 2), panelIconSize)
		tile := container.NewVBox(container.NewStack(tileBg, container.NewCenter(img)))
		content = container.NewBorder(nil, nil, tile, nil, col)
	}
	scroll := scrollBody(dialogSpec{flex: -1}, content)
	p.frame = container.New(&panelLayout{scroll: scroll}, scroll, container.NewHBox(buttons...))
	p.pop = widget.NewModalPopUp(container.NewThemeOverride(p.frame, overrideTheme), c)
	c.SetOnTypedKey(func(e *fyne.KeyEvent) { // nothing has the focus
		if !p.key(e) && p.prevKey != nil {
			p.prevKey(e)
		}
	})
	p.pop.Show()
	switch {
	case p.focus != nil:
		c.Focus(p.focus)
	case (spec.danger || spec.noEnter) && p.cancel != nil:
		c.Focus(p.cancel)
	default:
		c.Unfocus() // Enter must reach the dialog, not a field of the window behind it
	}
	return p
}

// act is the main button: it closes the dialog and runs onAction, or with keep only runs it.
func (p *panel) act() {
	if !p.spec.keep {
		p.finish(true)
		return
	}
	if !p.done && p.spec.onAction != nil {
		p.spec.onAction()
	}
}

// close hides the dialog once without running a callback.
func (p *panel) close() bool {
	if p.done {
		return false
	}
	p.done = true
	p.canvas.SetOnTypedKey(p.prevKey)
	p.pop.Hide()
	return true
}

// finish closes the dialog once; ok runs the action, otherwise the cancel callback.
func (p *panel) finish(ok bool) {
	if !p.close() {
		return
	}
	switch {
	case ok && p.spec.onAction != nil:
		p.spec.onAction()
	case !ok && p.spec.onCancel != nil:
		p.spec.onCancel()
	}
}

// Dismiss closes the dialog as if Cancel was pressed.
func (p *panel) Dismiss() { p.finish(false) }

// submit is Enter: it confirms unless the dialog is destructive (or noEnter) or the action is not available.
func (p *panel) submit() {
	if p.spec.danger || p.spec.noEnter || p.action == nil || p.action.Disabled() {
		return
	}
	p.act()
}

// escKey handles Esc for a control that has the focus; it reports whether it did.
func (p *panel) escKey(e *fyne.KeyEvent) bool {
	if e.Name == fyne.KeyEscape {
		p.finish(false)
		return true
	}
	return false
}

// key handles Esc and Enter for the window when no control has the focus.
func (p *panel) key(e *fyne.KeyEvent) bool {
	if p.escKey(e) {
		return true
	}
	if e.Name == fyne.KeyReturn || e.Name == fyne.KeyEnter {
		p.submit()
		return true
	}
	return false
}

func (p *panel) newButton(text string, tapped func()) *escButton {
	b := newEscButton(text, tapped)
	b.onEscape = func() { p.finish(false) }
	return b
}

func (p *panel) newEntry(password bool) *dialogEntry {
	e := &dialogEntry{onKey: p.escKey}
	e.Password = password // adds the reveal button
	if password {
		e.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	}
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(string) { p.submit() }
	return e
}

func (p *panel) newCheck(text string, checked bool) *dialogCheck {
	c := &dialogCheck{onKey: p.key}
	c.Text = text
	c.Checked = checked
	c.ExtendBaseWidget(c)
	return c
}

// dialogEntry is an entry that closes its dialog on Esc; Enter submits as in any entry.
type dialogEntry struct {
	widget.Entry
	onKey func(*fyne.KeyEvent) bool
}

func (e *dialogEntry) TypedKey(k *fyne.KeyEvent) {
	if e.onKey != nil && e.onKey(k) {
		return
	}
	e.Entry.TypedKey(k)
}

// dialogCheck is a check box that closes its dialog on Esc and confirms it on Enter.
type dialogCheck struct {
	widget.Check
	onKey func(*fyne.KeyEvent) bool
}

func (c *dialogCheck) TypedKey(k *fyne.KeyEvent) {
	if c.onKey != nil && c.onKey(k) {
		return
	}
	c.Check.TypedKey(k)
}

// escButton is a button that is also tapped by Esc while it has the focus (the Cancel button),
// or runs onEscape when that is set (the other buttons of a dialog). Enter does nothing on a
// button, so a destructive action can only be started by a click or Space.
type escButton struct {
	widget.Button
	onEscape func()
}

func newEscButton(text string, tapped func()) *escButton {
	b := &escButton{}
	b.Text = text
	b.OnTapped = tapped
	b.ExtendBaseWidget(b)
	return b
}

func (b *escButton) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyEscape {
		switch {
		case b.onEscape != nil:
			b.onEscape()
		case b.OnTapped != nil:
			b.OnTapped()
		}
		return
	}
	b.Button.TypedKey(e)
}

// panelLayout is the frame of a panel dialog: the scroll area with the content above the
// button row. The frame is dialogWidth wide and as high as the content needs; in a window
// that is too small the scroll area gives way and the buttons stay visible.
type panelLayout struct{ scroll *container.Scroll }

// settle gives o width w and lets it wrap its texts at that width.
func settle(o fyne.CanvasObject, w float32) float32 {
	for range 3 {
		o.Resize(fyne.NewSize(w, o.MinSize().Height))
	}
	return o.MinSize().Height
}

func (l *panelLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	inner := dialogWidth - 2*panelPadH
	h := settle(l.scroll.Content, inner+2*scrollInset)
	return fyne.NewSize(dialogWidth, panelPadTop+h+panelGap+objs[1].MinSize().Height+panelPadBottom)
}

func (l *panelLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	btn := objs[1].MinSize()
	inner := max(0, size.Width-2*panelPadH)
	by := size.Height - panelPadBottom - btn.Height
	objs[1].Move(fyne.NewPos(panelPadH, by))
	objs[1].Resize(fyne.NewSize(inner, btn.Height))
	// The scroll area gives up scrollInset of the padding for the borders of its content.
	objs[0].Move(fyne.NewPos(panelPadH-scrollInset, panelPadTop))
	objs[0].Resize(fyne.NewSize(inner+2*scrollInset, max(0, by-panelGap-panelPadTop)))
	settle(l.scroll.Content, inner+2*scrollInset)
}
