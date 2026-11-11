package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of the primary button (ManagerA.html "Connect", SessionA2.html "Connect KVM"):
// 36 high, 18 px side padding, radius 9, a 16 px symbol 8 px from the text, text 13.5 semibold.
const (
	pbIconSize float32 = 16
	pbGap      float32 = 8
)

// ButtonMetrics replaces the measures of a PrimaryButton for the smaller buttons of the About
// and settings dialogs (30 px pills, 32 px dialog buttons). A zero field keeps the default of
// the 36 px button above.
type ButtonMetrics struct {
	Height, Radius, PaddingH, IconSize, Gap, TextSize float32
	// Neutral names the fill token of a button that is not HighImportance; the default is
	// the theme's "button" color (white 12 %). NeutralBorder names the token of its 1 px
	// border (none by default). Tokens, so the button follows the theme variant.
	Neutral, NeutralBorder fyne.ThemeColorName
	// NeutralText names the token of the text of a button that is not HighImportance or
	// DangerImportance; the default is "foreground" (the light red Edit of the signed-in user).
	NeutralText fyne.ThemeColorName
}

// PrimaryButton is the accent button with text and an optional symbol, in the design's
// measurements. Importance is HighImportance (accent), DangerImportance (the full red button of
// Danger.dc.html and the danger zone: white semibold text, light red while disabled) or
// anything else (neutral, e.g. the Cancel state of the Connect KVM button). The symbol is a resource of package icons, drawn
// by Symbol with the stroke it was requested with (the design uses 2).
type PrimaryButton struct {
	widget.DisableableWidget

	Text       string
	Importance widget.Importance
	// IconTrailing puts the symbol after the text (Connect); otherwise before it.
	IconTrailing bool
	OnTapped     func()
	// OnEscape, if set, runs on Esc while the button has the focus (a dialog closes).
	OnEscape func()
	// Metrics changes the measures; nil keeps the 36 px button of the manager and the toolbar.
	Metrics *ButtonMetrics

	icon    fyne.Resource
	hovered bool
	focused bool
}

var (
	_ fyne.Tappable      = (*PrimaryButton)(nil)
	_ fyne.Focusable     = (*PrimaryButton)(nil)
	_ desktop.Hoverable  = (*PrimaryButton)(nil)
	_ desktop.Cursorable = (*PrimaryButton)(nil)
)

// NewPrimaryButton returns an accent button; icon may be nil.
func NewPrimaryButton(text string, icon fyne.Resource, tapped func()) *PrimaryButton {
	b := &PrimaryButton{Text: text, icon: icon, OnTapped: tapped, Importance: widget.HighImportance}
	b.ExtendBaseWidget(b)
	return b
}

// SetText changes the text.
func (b *PrimaryButton) SetText(s string) { b.Text = s; b.Refresh() }

// SetIcon changes the symbol; nil removes it.
func (b *PrimaryButton) SetIcon(res fyne.Resource) { b.icon = res; b.Refresh() }

// Icon returns the symbol.
func (b *PrimaryButton) Icon() fyne.Resource { return b.icon }

// metrics returns Metrics with the defaults filled in.
func (b *PrimaryButton) metrics() ButtonMetrics {
	m := ButtonMetrics{Height: style.PrimaryButtonHeight, Radius: style.PrimaryButtonRadius,
		PaddingH: style.PrimaryButtonPadding, IconSize: pbIconSize, Gap: pbGap,
		TextSize: style.Size(style.SizeNameButtonText), Neutral: "button"}
	if o := b.Metrics; o != nil {
		for _, f := range []struct {
			dst *float32
			v   float32
		}{{&m.Height, o.Height}, {&m.Radius, o.Radius},
			{&m.PaddingH, o.PaddingH}, {&m.IconSize, o.IconSize}, {&m.Gap, o.Gap}, {&m.TextSize, o.TextSize}} {
			if f.v != 0 {
				*f.dst = f.v
			}
		}
		if o.Neutral != "" {
			m.Neutral = o.Neutral
		}
		m.NeutralBorder = o.NeutralBorder
		m.NeutralText = o.NeutralText
	}
	return m
}

func (b *PrimaryButton) Tapped(*fyne.PointEvent) {
	if b.Disabled() || b.OnTapped == nil {
		return
	}
	b.OnTapped()
}

func (b *PrimaryButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.Refresh() }
func (b *PrimaryButton) MouseMoved(*desktop.MouseEvent) {}
func (b *PrimaryButton) MouseOut()                      { b.hovered = false; b.Refresh() }
func (b *PrimaryButton) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (b *PrimaryButton) FocusGained() { b.focused = true; b.Refresh() }
func (b *PrimaryButton) FocusLost()   { b.focused = false; b.Refresh() }
func (b *PrimaryButton) TypedRune(r rune) {
	if r == ' ' {
		b.Tapped(nil)
	}
}
func (b *PrimaryButton) TypedKey(e *fyne.KeyEvent) {
	switch e.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		b.Tapped(nil)
	case fyne.KeyEscape:
		if b.OnEscape != nil {
			b.OnEscape()
		}
	}
}

func (b *PrimaryButton) CreateRenderer() fyne.WidgetRenderer {
	r := &primaryButtonRenderer{b: b,
		bg:    &canvas.Rectangle{CornerRadius: style.PrimaryButtonRadius},
		over:  &canvas.Rectangle{CornerRadius: style.PrimaryButtonRadius},
		focus: &canvas.Rectangle{CornerRadius: style.PrimaryButtonRadius, StrokeWidth: 2, FillColor: color.Transparent},
		icon:  NewSymbol(nil, pbIconSize),
		text:  newText("", style.Size(style.SizeNameButtonText), nil, true),
	}
	r.Refresh()
	return r
}

type primaryButtonRenderer struct {
	b     *PrimaryButton
	bg    *canvas.Rectangle
	over  *canvas.Rectangle // hover tint
	focus *canvas.Rectangle
	icon  *Symbol
	text  *canvas.Text
}

func (r *primaryButtonRenderer) hasIcon() bool { return r.b.icon != nil }

func (r *primaryButtonRenderer) MinSize() fyne.Size {
	m := r.b.metrics()
	w := 2*m.PaddingH + fyne.MeasureText(r.b.Text, m.TextSize, r.text.TextStyle).Width
	if r.hasIcon() {
		w += m.Gap + m.IconSize
	}
	return fyne.NewSize(w, m.Height)
}

func (r *primaryButtonRenderer) Layout(size fyne.Size) {
	for _, o := range []*canvas.Rectangle{r.bg, r.over, r.focus} {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
	m := r.b.metrics()
	ts := fyne.MeasureText(r.b.Text, m.TextSize, r.text.TextStyle)
	content := ts.Width
	if r.hasIcon() {
		content += m.Gap + m.IconSize
	}
	x := (size.Width - content) / 2
	textPos := fyne.NewPos(x, (size.Height-r.text.MinSize().Height)/2)
	iconPos := fyne.NewPos(x, (size.Height-m.IconSize)/2)
	if r.hasIcon() {
		if r.b.IconTrailing {
			iconPos.X = x + ts.Width + m.Gap
		} else {
			textPos.X = x + m.IconSize + m.Gap
		}
	}
	r.text.Move(textPos)
	r.text.Resize(fyne.NewSize(ts.Width, r.text.MinSize().Height))
	r.icon.Move(iconPos)
	r.icon.Resize(fyne.NewSquareSize(m.IconSize))
}

func (r *primaryButtonRenderer) Refresh() {
	b := r.b
	m := b.metrics()
	high := b.Importance == widget.HighImportance
	fill, fg := style.Color(m.Neutral), style.Color("foreground")
	if m.NeutralText != "" {
		fg = style.Color(m.NeutralText)
	}
	if high {
		fill, fg = style.Color("primary"), style.Color(style.ColorNameWhite)
	}
	danger := b.Importance == widget.DangerImportance
	if danger {
		fill, fg = style.Color(style.ColorNameDanger), style.Color(style.ColorNameWhite)
	}
	switch {
	case b.Disabled() && danger:
		fill = style.Color(style.ColorNameDangerDisabled)
	case b.Disabled():
		fill, fg = style.Color("disabledButton"), style.Color("disabled")
	}
	for _, o := range []*canvas.Rectangle{r.bg, r.over, r.focus} {
		o.CornerRadius = m.Radius
	}
	r.bg.FillColor = fill
	r.bg.StrokeColor, r.bg.StrokeWidth = color.Transparent, 0
	if !high && !danger && m.NeutralBorder != "" && !b.Disabled() {
		r.bg.StrokeColor, r.bg.StrokeWidth = style.Color(m.NeutralBorder), 1
	}
	r.bg.Refresh()
	r.over.FillColor = color.Transparent
	if (b.hovered || b.focused) && !b.Disabled() {
		r.over.FillColor = style.Color("hover")
	}
	r.over.Refresh()
	r.focus.StrokeColor = style.Color("focus")
	r.focus.Hidden = !b.focused || b.Disabled()
	r.focus.Refresh()
	r.text.Text = b.Text
	r.text.TextSize = m.TextSize
	r.text.Color = fg
	r.text.Refresh()
	r.icon.Translucency = 0
	if b.Disabled() {
		r.icon.Translucency = 1 - style.DisabledOpacity
	}
	r.icon.min = fyne.NewSquareSize(m.IconSize)
	r.icon.SetResource(b.icon)
	setVisible(r.icon, b.icon != nil)
	r.Layout(b.Size())
	canvas.Refresh(b)
}

func (r *primaryButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.over, r.focus, r.icon, r.text}
}
func (r *primaryButtonRenderer) Destroy() {}
