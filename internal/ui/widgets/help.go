package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of the in-app help (overall design, #8 and V14): a 14 px grey info symbol behind a
// row label; a click opens a 330 px popover with a 10 px radius and 12/14 px padding.
const (
	helpIconSize    float32 = 14
	helpHitSize     float32 = 22 // the tappable area around the symbol
	helpPopoverW    float32 = 330
	helpPopoverPadV float32 = 12
	helpPopoverPadH float32 = 14
	helpGap         float32 = 4 // between the symbol and the popover
	helpEdge        float32 = 8 // free space kept at the canvas edge
)

// HelpContent is the text of one help popover: a title (the setting) and up to four parts. What
// and When are always shown; Wrong and Recover only when the setting carries a risk (a part left
// empty is omitted). The caller passes translated texts, one translation key per part, ideally in
// the translation files of its feature.
type HelpContent struct {
	Title   string
	What    string // "What it does"
	When    string // "When you need it"
	Wrong   string // "What can go wrong"
	Recover string // "How to recover"
}

// parts returns the labelled parts in order, skipping empty ones.
func (c HelpContent) parts() [][2]string {
	var out [][2]string
	for _, p := range [][2]string{
		{lang.L("What it does"), c.What},
		{lang.L("When you need it"), c.When},
		{lang.L("What can go wrong"), c.Wrong},
		{lang.L("How to recover"), c.Recover},
	} {
		if p[1] != "" {
			out = append(out, p)
		}
	}
	return out
}

// InfoButton is the info symbol behind a row label: a tap (or Space and Enter with the keyboard
// focus) opens a HelpPopover next to it. Hovering shows nothing, so that it works the same on
// every platform.
type InfoButton struct {
	widget.BaseWidget

	Content HelpContent

	// OnShown runs after the popover opened (for tests and callers that track it).
	OnShown func(*HelpPopover)

	sym     *Symbol
	focused bool
}

var (
	_ fyne.Tappable      = (*InfoButton)(nil)
	_ fyne.Focusable     = (*InfoButton)(nil)
	_ desktop.Cursorable = (*InfoButton)(nil)
)

// NewInfoButton returns an info symbol that opens the popover for c.
func NewInfoButton(c HelpContent) *InfoButton {
	b := &InfoButton{Content: c}
	b.sym = NewSymbol(icons.Get(icons.Info, icons.Secondary), helpIconSize)
	b.ExtendBaseWidget(b)
	return b
}

// Tapped opens the popover; with the canvas gone (a button that is not shown) it does nothing.
func (b *InfoButton) Tapped(*fyne.PointEvent) {
	drv := fyne.CurrentApp().Driver()
	c := drv.CanvasForObject(b)
	if c == nil {
		return
	}
	pos := drv.AbsolutePositionForObject(b)
	p := ShowHelp(c, pos.Add(fyne.NewPos(0, b.Size().Height+helpGap)), b.Content)
	if b.OnShown != nil {
		b.OnShown(p)
	}
}

func (b *InfoButton) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (b *InfoButton) FocusGained()   { b.focused = true; b.Refresh() }
func (b *InfoButton) FocusLost()     { b.focused = false; b.Refresh() }
func (b *InfoButton) TypedRune(rune) {}
func (b *InfoButton) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeySpace || e.Name == fyne.KeyReturn || e.Name == fyne.KeyEnter {
		b.Tapped(nil)
	}
}

func (b *InfoButton) CreateRenderer() fyne.WidgetRenderer {
	// hidden and colored from the start: before the first Refresh a plain circle would show its
	// default stroke (the focus ring would appear without focus)
	ring := &canvas.Circle{StrokeWidth: 1.5, Hidden: !b.focused, StrokeColor: style.Color(style.ColorNameAccentText)}
	return &infoButtonRenderer{b: b, ring: ring}
}

type infoButtonRenderer struct {
	b    *InfoButton
	ring *canvas.Circle
}

func (r *infoButtonRenderer) Layout(size fyne.Size) {
	r.b.sym.Resize(fyne.NewSquareSize(helpIconSize))
	r.b.sym.Move(fyne.NewPos((size.Width-helpIconSize)/2, (size.Height-helpIconSize)/2))
	r.ring.Resize(size)
}
func (r *infoButtonRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(helpHitSize) }
func (r *infoButtonRenderer) Refresh() {
	r.ring.Hidden = !r.b.focused
	r.ring.StrokeColor = style.Color(style.ColorNameAccentText)
	r.b.sym.SetResource(icons.Get(icons.Info, icons.Secondary)) // follows the theme variant
	r.Layout(r.b.Size())
	canvas.Refresh(r.b)
}
func (r *infoButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.ring, r.b.sym}
}
func (r *infoButtonRenderer) Destroy() {}

// HelpPopover is the shown popover: a panel of 330 px over a transparent layer that catches a
// tap outside it. A tap outside and Esc close it; a tap inside does nothing.
type HelpPopover struct {
	widget.BaseWidget

	// OnDismiss runs once the popover has closed.
	OnDismiss func()

	canvas fyne.Canvas
	pos    fyne.Position
	panel  fyne.CanvasObject
	bg     *canvas.Rectangle
	shown  bool
	prev   fyne.Focusable
}

var (
	_ fyne.Tappable  = (*HelpPopover)(nil)
	_ fyne.Focusable = (*HelpPopover)(nil)
)

// ShowHelp shows the popover for c with its top left corner at pos (canvas coordinates; kept
// inside the canvas) and takes the keyboard focus; closing it gives the focus back.
func ShowHelp(cv fyne.Canvas, pos fyne.Position, c HelpContent) *HelpPopover {
	p := &HelpPopover{canvas: cv, pos: pos, shown: true}
	p.ExtendBaseWidget(p)
	p.bg = &canvas.Rectangle{CornerRadius: style.MenuRadius, StrokeWidth: 1,
		Shadow: canvas.Shadow{BlurRadius: 25, Offset: fyne.NewPos(0, 18), Variant: canvas.DropShadow}}
	items := []fyne.CanvasObject{}
	if c.Title != "" {
		title := widget.NewRichText(&widget.TextSegment{Text: c.Title,
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}, SizeName: "text"}})
		items = append(items, title)
	}
	for _, part := range c.parts() {
		rt := widget.NewRichText(
			&widget.TextSegment{Text: part[0] + ":", Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}, Inline: true}},
			&widget.TextSegment{Text: " " + part[1], Style: widget.RichTextStyle{Inline: true}},
		)
		rt.Wrapping = fyne.TextWrapWord
		items = append(items, rt)
	}
	p.panel = container.New(&helpPanelLayout{}, p.bg, container.NewVBox(items...))
	p.recolor()
	p.prev = nil
	if f, ok := cv.Focused().(fyne.Focusable); ok {
		p.prev = f
	}
	cv.Overlays().Add(p)
	cv.Focus(p)
	return p
}

func (p *HelpPopover) recolor() {
	p.bg.FillColor = style.Color(style.ColorNameMenuPanel)
	p.bg.StrokeColor = style.Color(style.ColorNameDialogBorder)
	p.bg.Shadow.Color = style.Color(style.ColorNameMenuShadow)
	p.bg.Refresh()
}

// Refresh redraws the panel in the colors of the current theme variant.
func (p *HelpPopover) Refresh() { p.recolor(); p.BaseWidget.Refresh() }

// Visible reports whether the popover is shown.
func (p *HelpPopover) Visible() bool { return p.shown }

// PanelSize returns the size of the panel, 330 px wide.
func (p *HelpPopover) PanelSize() fyne.Size { return p.panel.Size() }

// Dismiss closes the popover and calls OnDismiss; nothing happens if it is closed already.
func (p *HelpPopover) Dismiss() {
	if !p.shown {
		return
	}
	p.shown = false
	removeOverlay(p.canvas, p)
	if p.prev != nil && p.canvas.Focused() == nil {
		p.canvas.Focus(p.prev)
	}
	if p.OnDismiss != nil {
		p.OnDismiss()
	}
}

// Tapped outside the panel closes the popover.
func (p *HelpPopover) Tapped(e *fyne.PointEvent) {
	pos, size := p.panel.Position(), p.panel.Size()
	if e != nil && e.Position.X >= pos.X && e.Position.Y >= pos.Y && e.Position.X < pos.X+size.Width && e.Position.Y < pos.Y+size.Height {
		return
	}
	p.Dismiss()
}

func (p *HelpPopover) FocusGained()   {}
func (p *HelpPopover) FocusLost()     {}
func (p *HelpPopover) TypedRune(rune) {}
func (p *HelpPopover) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyEscape {
		p.Dismiss()
	}
}

func (p *HelpPopover) CreateRenderer() fyne.WidgetRenderer { return &helpPopoverRenderer{p: p} }

type helpPopoverRenderer struct{ p *HelpPopover }

func (r *helpPopoverRenderer) Layout(size fyne.Size) {
	h := r.p.panel.MinSize().Height
	ps := fyne.NewSize(helpPopoverW, min(h, size.Height-2*helpEdge))
	pos := r.p.pos
	pos.X = max(0, min(pos.X, size.Width-ps.Width))
	pos.Y = max(0, min(pos.Y, size.Height-ps.Height))
	r.p.panel.Move(pos)
	r.p.panel.Resize(ps)
}
func (r *helpPopoverRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *helpPopoverRenderer) Refresh()                     { r.Layout(r.p.Size()); canvas.Refresh(r.p) }
func (r *helpPopoverRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.p.panel} }
func (r *helpPopoverRenderer) Destroy()                     {}

// helpPanelLayout draws objs[0] (background) under the whole panel and puts the text (objs[1])
// inside the padding. The text is laid out at the panel width, so its height follows from it.
type helpPanelLayout struct{}

func (*helpPanelLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(size)
	objs[1].Move(fyne.NewPos(helpPopoverPadH, helpPopoverPadV))
	objs[1].Resize(fyne.NewSize(size.Width-2*helpPopoverPadH, size.Height-2*helpPopoverPadV))
}

// MinSize is 330 px wide; the height is the text's at that width.
func (*helpPanelLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	inner := helpPopoverW - 2*helpPopoverPadH
	objs[1].Resize(fyne.NewSize(inner, objs[1].MinSize().Height))
	return fyne.NewSize(helpPopoverW, objs[1].MinSize().Height+2*helpPopoverPadV)
}
