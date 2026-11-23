package ui

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
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
	panelFormGap    float32 = 12 // between the parts of a flush panel (Forms.dc.html .dlg gap)
)

// tileTone is the color of a panel's symbol tile.
type tileTone int

const (
	toneDanger  tileTone = iota // red tile, red symbol (delete, power, quit)
	toneInfo                    // blue tile, accent symbol (Dialogs.dc.html: enable IDE-R or SOL, restart from ISO)
	toneWarning                 // orange tile, orange symbol (Dialogs.dc.html: an ISO is still mounted)
)

// Tile of the device management questions (Dialogs.dc.html .ico): 36 px, radius 9, a 20 px
// symbol with stroke 1.8, above the title.
const (
	questionTileSize   float32 = 36
	questionTileRadius float32 = 9
	questionIconSize   float32 = 20
	questionIconStroke float32 = 1.8
)

// panelSpec describes a panel dialog.
type panelSpec struct {
	icon icons.Name // symbol in the tile; "" for none
	tone tileTone   // colors of the tile
	// tileAbove puts a 36 px tile above the title (Dialogs.dc.html) instead of a 40 px tile left of it.
	tileAbove bool
	// redButton draws the main button in the red of the device management design (Danger.dc.html):
	// #C2001A, light red while disabled.
	redButton bool
	danger    bool // the action destroys something: red button, no Enter, Cancel has the focus
	noEnter   bool // Enter never confirms and Cancel has the focus, without the red button (trusting a certificate)
	title     string
	action    string // text of the main button
	cancel    string // text of the Cancel button; "" for none
	onAction  func() // runs after the dialog closed through the main button; may be nil
	onCancel  func() // runs after it closed any other way; may be nil
	keep      bool   // the main button (and Enter) runs onAction without closing; onAction closes with p.close
	// flush lines the title up with content that has no inner padding of its own (warning boxes,
	// fields: the device management dialogs of Danger.dc.html and Forms.dc.html) and spaces the
	// parts panelFormGap apart. Texts with an inner padding go through flushText.
	flush bool
	// width replaces dialogWidth (the certificate details: 560); 0 keeps it.
	width float32
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
	leading []fyne.CanvasObject // buttons at the left end of the row (a delete action); set by build
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
	buttons := append(append([]fyne.CanvasObject{}, p.leading...), layout.NewSpacer())
	buttons = append(buttons, p.extras...)
	if spec.cancel != "" {
		p.cancel = newEscButton(spec.cancel, func() { p.finish(false) })
		buttons = append(buttons, p.cancel)
	}
	var action fyne.CanvasObject = p.action
	if spec.redButton {
		action = container.NewThemeOverride(p.action, dangerButtonTheme{overrideTheme})
	}
	buttons = append(buttons, action)

	head := widget.NewLabelWithStyle(spec.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	head.SizeName = theme.SizeNameSubHeadingText
	head.Wrapping = fyne.TextWrapWord
	col := container.NewVBox(append([]fyne.CanvasObject{head}, body...)...)
	if spec.flush {
		col = container.New(layout.NewCustomPaddedVBoxLayout(panelFormGap), append([]fyne.CanvasObject{flushText(head)}, body...)...)
	}
	var content fyne.CanvasObject = col
	if spec.icon != "" {
		size, radius, iconSize, stroke := panelTileSize, panelTileRadius, panelIconSize, float32(2)
		if spec.tileAbove {
			size, radius, iconSize, stroke = questionTileSize, questionTileRadius, questionIconSize, questionIconStroke
		}
		fill, variant := style.ColorNameDangerTile, icons.Danger
		switch spec.tone {
		case toneInfo:
			fill, variant = style.ColorNameAccentTint, icons.AccentText
		case toneWarning:
			fill, variant = style.ColorNameWarningSurface, icons.WarningIcon
		}
		tileBg := style.BindRect(&canvas.Rectangle{CornerRadius: radius}, fill, "")
		tileBg.SetMinSize(fyne.NewSquareSize(size))
		img := widgets.NewSymbol(icons.GetStroke(spec.icon, variant, stroke), iconSize)
		tile := container.NewStack(tileBg, container.NewCenter(img))
		if spec.tileAbove {
			// the tile starts where the text of the labels below starts (they have an inner padding)
			inset := container.New(layout.NewCustomPaddedLayout(0, 0, theme.InnerPadding(), 0), tile)
			content = container.NewVBox(append([]fyne.CanvasObject{container.NewHBox(inset), head}, body...)...)
		} else {
			content = container.NewBorder(nil, nil, container.NewVBox(tile), nil, col)
		}
	}
	scroll := scrollBody(dialogSpec{flex: -1}, content)
	width := dialogWidth
	if spec.width > 0 {
		width = spec.width
	}
	p.frame = container.New(&panelLayout{scroll: scroll, width: width}, scroll, container.NewHBox(buttons...))
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

// flushText takes the inner padding of a label or rich text away on the left, top and bottom,
// so its text lines up with the unpadded content of a flush panel.
func flushText(o fyne.CanvasObject) fyne.CanvasObject {
	pad := theme.InnerPadding()
	return container.New(layout.NewCustomPaddedLayout(-pad, -pad, -pad, 0), o)
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

// fit gives the shown dialog the height of its content again after a part was shown or hidden
// (a modal pop-up only grows by itself) and centers it.
func (p *panel) fit() {
	if p.done || p.pop == nil {
		return
	}
	p.pop.Resize(p.pop.MinSize())
	if o := p.canvas.Overlays().Top(); o != nil {
		o.Refresh()
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

// newWrapCheck is newCheck with a text that wraps at the width of the dialog instead of being
// cut off: the checkbox "I understand the risk for <device>", where a long device name or host
// must stay readable. A text of one line looks exactly like newCheck.
func (p *panel) newWrapCheck(text string, checked bool) *dialogCheck {
	c := p.newCheck(text, checked)
	c.wrap = true
	return c
}

// dialogCheck is a check box that closes its dialog on Esc and confirms it on Enter.
type dialogCheck struct {
	widget.Check
	onKey func(*fyne.KeyEvent) bool
	wrap  bool // the text wraps (newWrapCheck)
}

// CreateRenderer draws a wrapping check (newWrapCheck) with wrapCheckRenderer, any other with
// Fyne's renderer.
func (c *dialogCheck) CreateRenderer() fyne.WidgetRenderer {
	r := c.Check.CreateRenderer()
	if !c.wrap {
		return r
	}
	return newWrapCheckRenderer(c, r)
}

// widget.Check takes taps and hover only within its minimum size, which is narrow for a
// wrapping text. A wrapping check takes them on its whole text instead, as widget.Check does on
// its one line; the position is handed on as one inside (or outside) its minimum size.

func (c *dialogCheck) Tapped(e *fyne.PointEvent) {
	if c.wrap {
		if !c.onText(e.Position) {
			return
		}
		e = &fyne.PointEvent{Position: fyne.NewPos(0, c.Size().Height/2)}
	}
	c.Check.Tapped(e)
}

func (c *dialogCheck) MouseIn(e *desktop.MouseEvent) { c.MouseMoved(e) }

func (c *dialogCheck) MouseMoved(e *desktop.MouseEvent) {
	if c.wrap {
		m := *e
		m.Position = fyne.NewPos(0, 0)
		if !c.onText(e.Position) {
			m.Position = fyne.NewPos(math.MaxFloat32, math.MaxFloat32)
		}
		e = &m
	}
	c.Check.MouseMoved(e)
}

// onText reports whether pos lies on the box or the text of a wrapping check, not right of a
// short text. Like widget.Check, a check that was never laid out (test code) takes any position.
func (c *dialogCheck) onText(pos fyne.Position) bool {
	if c.Size().IsZero() {
		return true
	}
	th := c.Theme()
	pad := th.Size(theme.SizeNameInnerPadding)
	end := wrapCheckTextX(th) + fyne.MeasureText(c.Text, th.Size(theme.SizeNameText), fyne.TextStyle{}).Width + 2*pad
	return pos.X >= 0 && pos.Y >= 0 && pos.X <= min(end, c.Size().Width) && pos.Y <= c.Size().Height
}

// wrapCheckTextX is where the wrapping text of a check starts: its text (inside the inner
// padding of the rich text) then begins where widget.Check draws its one line.
func wrapCheckTextX(th fyne.Theme) float32 {
	return th.Size(theme.SizeNameInlineIcon) + 2*th.Size(theme.SizeNameInputBorder)
}

// wrapCheckRenderer draws a wrapping check: widget.Check's box (with its focus and hover circle)
// centered on the first line, and the text as a wrapping rich text in place of Fyne's single
// line, which is left out.
type wrapCheckRenderer struct {
	check *dialogCheck
	base  fyne.WidgetRenderer // widget.Check's renderer
	text  *widget.RichText
	objs  []fyne.CanvasObject
}

func newWrapCheckRenderer(c *dialogCheck, base fyne.WidgetRenderer) *wrapCheckRenderer {
	r := &wrapCheckRenderer{check: c, base: base, text: richLine(textSeg("", theme.ColorNameForeground, false))}
	for _, o := range base.Objects() {
		if _, line := o.(*canvas.Text); !line {
			r.objs = append(r.objs, o)
		}
	}
	r.objs = append(r.objs, r.text)
	r.update()
	return r
}

// update takes the text and the disabled color over from the check.
func (r *wrapCheckRenderer) update() {
	seg := r.text.Segments[0].(*widget.TextSegment)
	seg.Text = r.check.Text
	seg.Style.ColorName = theme.ColorNameForeground
	if r.check.Disabled() {
		seg.Style.ColorName = theme.ColorNameDisabled
	}
	r.text.Refresh()
}

// firstLine is the height of widget.Check with one line of text.
func (r *wrapCheckRenderer) firstLine() float32 { return r.base.MinSize().Height }

func (r *wrapCheckRenderer) MinSize() fyne.Size {
	t := r.text.MinSize()
	return fyne.NewSize(wrapCheckTextX(r.check.Theme())+t.Width, max(r.firstLine(), t.Height))
}

func (r *wrapCheckRenderer) Layout(size fyne.Size) {
	r.base.Layout(fyne.NewSize(size.Width, r.firstLine()))
	x := wrapCheckTextX(r.check.Theme())
	r.text.Move(fyne.NewPos(x, 0))
	r.text.Resize(fyne.NewSize(max(0, size.Width-x), size.Height))
}

func (r *wrapCheckRenderer) Refresh() {
	r.base.Refresh()
	r.update()
}

func (r *wrapCheckRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *wrapCheckRenderer) Destroy()                     { r.base.Destroy() }

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
type panelLayout struct {
	scroll *container.Scroll
	width  float32 // dialogWidth, or the width of panelSpec
}

// settle gives o width w and lets it wrap its texts at that width.
func settle(o fyne.CanvasObject, w float32) float32 {
	for range 3 {
		o.Resize(fyne.NewSize(w, o.MinSize().Height))
	}
	return o.MinSize().Height
}

func (l *panelLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	inner := l.width - 2*panelPadH
	h := settle(l.scroll.Content, inner+2*scrollInset)
	return fyne.NewSize(l.width, panelPadTop+h+panelGap+objs[1].MinSize().Height+panelPadBottom)
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
