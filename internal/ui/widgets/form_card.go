package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Row geometry inside a card (ManagerA.html): 44 high, 14 px left and right padding, a 136 px
// label column with 16 px gap, or a 17 px symbol with 12 px gap.
const (
	rowPadH     float32 = 14
	rowLabelGap float32 = 16
	rowIconGap  float32 = 12
	rowIconSize float32 = 17
)

// FormCard is a rounded card (radius 10, #2A2A2D, hairline border) that stacks its rows with
// hairline separators between them. Rows are at least 44 high; use NewFormRow and NewIconRow
// for the two row kinds of the design, or pass any object. The rows use style.CardTheme, so
// entries inside a card have no border until focused.
type FormCard struct {
	widget.BaseWidget

	// Background names the token that replaces the card color (style.ColorNameCard); the About
	// and settings dialogs sit on style.ColorNameDialogCard instead.
	Background fyne.ThemeColorName

	rows []fyne.CanvasObject
}

// NewFormCard returns a card with rows.
func NewFormCard(rows ...fyne.CanvasObject) *FormCard {
	c := &FormCard{rows: rows}
	c.ExtendBaseWidget(c)
	return c
}

func (c *FormCard) background() color.Color {
	if c.Background != "" {
		return style.Color(c.Background)
	}
	return style.Color(style.ColorNameCard)
}

// Rows returns the rows.
func (c *FormCard) Rows() []fyne.CanvasObject { return c.rows }

// SetRows replaces the rows.
func (c *FormCard) SetRows(rows ...fyne.CanvasObject) {
	c.rows = rows
	c.Refresh()
}

func (c *FormCard) CreateRenderer() fyne.WidgetRenderer {
	r := &formCardRenderer{c: c, bg: &canvas.Rectangle{
		CornerRadius: style.CardRadius,
		FillColor:    c.background(),
		StrokeColor:  style.Color(style.ColorNameCardBorder),
		StrokeWidth:  1,
	}}
	r.inner = container.NewWithoutLayout()
	r.box = container.NewThemeOverride(r.inner, style.CardTheme{})
	r.Refresh()
	return r
}

type formCardRenderer struct {
	c     *FormCard
	bg    *canvas.Rectangle
	seps  []*canvas.Rectangle
	inner *fyne.Container // holds the rows, under style.CardTheme
	box   *container.ThemeOverride
	objs  []fyne.CanvasObject
}

func rowHeight(o fyne.CanvasObject) float32 {
	if !o.Visible() {
		return 0
	}
	return max(style.FormRowHeight, o.MinSize().Height)
}

func (r *formCardRenderer) visibleRows() []fyne.CanvasObject {
	var out []fyne.CanvasObject
	for _, o := range r.c.rows {
		if o.Visible() {
			out = append(out, o)
		}
	}
	return out
}

func (r *formCardRenderer) MinSize() fyne.Size {
	var w, h float32
	rows := r.visibleRows()
	for _, o := range rows {
		w = max(w, o.MinSize().Width)
		h += rowHeight(o)
	}
	if n := len(rows); n > 1 {
		h += float32(n - 1) // separators
	}
	return fyne.NewSize(w+2, h+2) // + border
}

func (r *formCardRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.box.Resize(size)
	r.inner.Resize(size)
	y := float32(1)
	si := 0
	for i, o := range r.visibleRows() {
		if i > 0 {
			s := r.seps[si]
			s.Show()
			s.Move(fyne.NewPos(1, y))
			s.Resize(fyne.NewSize(size.Width-2, 1))
			si++
			y++
		}
		h := rowHeight(o)
		o.Move(fyne.NewPos(1, y))
		o.Resize(fyne.NewSize(size.Width-2, h))
		y += h
	}
	for ; si < len(r.seps); si++ {
		r.seps[si].Hide()
	}
}

func (r *formCardRenderer) Refresh() {
	for len(r.seps) < max(0, len(r.c.rows)-1) {
		r.seps = append(r.seps, &canvas.Rectangle{})
	}
	r.bg.FillColor = r.c.background()
	r.bg.StrokeColor = style.Color(style.ColorNameCardBorder)
	r.objs = append(r.objs[:0], r.bg)
	for _, s := range r.seps {
		s.FillColor = style.Color(style.ColorNameCardSeparator)
		r.objs = append(r.objs, s)
	}
	r.objs = append(r.objs, r.box)
	r.inner.Objects = r.c.rows
	r.Layout(r.c.Size())
	r.box.Refresh()
	canvas.Refresh(r.c)
}

func (r *formCardRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *formCardRenderer) Destroy()                     {}

// NewFormRow returns a card row with label (136 px column, secondary color) and content
// filling the rest, e.g. an entry or a value label.
func NewFormRow(label string, content fyne.CanvasObject) fyne.CanvasObject {
	l := newText(label, style.Size("text"), style.Color(style.ColorNameTextSecondary), false)
	return &rowContainer{lead: l, leadToken: style.ColorNameTextSecondary, leadWidth: style.FormLabelWidth, gap: rowLabelGap, content: content, leadText: label}
}

// NewIconRow returns a card row with a 17 px symbol (secondary color), text filling the rest
// and an optional trailing object such as a Switch (nil for none).
func NewIconRow(icon icons.Name, text string, trailing fyne.CanvasObject) fyne.CanvasObject {
	img := NewSymbol(icons.Get(icon, icons.Secondary), 0)
	t := newText(text, style.Size("text"), style.Color("foreground"), false)
	return &rowContainer{lead: img, leadWidth: rowIconSize, gap: rowIconGap, content: t, contentToken: "foreground", contentText: text, trailing: trailing}
}

// rowContainer lays out one card row: lead (label or symbol), content, optional trailing.
type rowContainer struct {
	widget.BaseWidget
	lead      fyne.CanvasObject
	leadWidth float32
	gap       float32
	content   fyne.CanvasObject
	trailing  fyne.CanvasObject
	// full texts of lead and content when they are *canvas.Text, for ellipsizing
	leadText, contentText string
	// color tokens of lead and content when they are *canvas.Text, read on every refresh
	leadToken, contentToken fyne.ThemeColorName
}

func (c *rowContainer) CreateRenderer() fyne.WidgetRenderer {
	c.ExtendBaseWidget(c)
	return &rowRenderer{c: c}
}

type rowRenderer struct{ c *rowContainer }

func (r *rowRenderer) MinSize() fyne.Size {
	c := r.c
	w := rowPadH + c.leadWidth + c.gap + c.content.MinSize().Width + rowPadH
	h := max(style.FormRowHeight, c.content.MinSize().Height)
	if c.trailing != nil {
		w += c.gap + c.trailing.MinSize().Width
		h = max(h, c.trailing.MinSize().Height)
	}
	return fyne.NewSize(w, h)
}

func (r *rowRenderer) Layout(size fyne.Size) {
	c := r.c
	x := rowPadH
	if t, ok := c.lead.(*canvas.Text); ok {
		fitText(t, c.leadText, c.leadWidth)
		ts := t.MinSize()
		t.Move(fyne.NewPos(x, (size.Height-ts.Height)/2))
		t.Resize(fyne.NewSize(c.leadWidth, ts.Height))
	} else {
		c.lead.Move(fyne.NewPos(x, (size.Height-c.leadWidth)/2))
		c.lead.Resize(fyne.NewSquareSize(c.leadWidth))
	}
	x += c.leadWidth + c.gap
	right := size.Width - rowPadH
	if c.trailing != nil {
		ts := c.trailing.MinSize()
		c.trailing.Move(fyne.NewPos(right-ts.Width, (size.Height-ts.Height)/2))
		c.trailing.Resize(ts)
		right -= ts.Width + c.gap
	}
	cs := c.content.MinSize()
	if t, ok := c.content.(*canvas.Text); ok {
		fitText(t, c.contentText, right-x)
		cs = t.MinSize()
	}
	c.content.Move(fyne.NewPos(x, (size.Height-cs.Height)/2))
	c.content.Resize(fyne.NewSize(right-x, cs.Height))
}

func (r *rowRenderer) Refresh() {
	// Children refresh too, so a theme set by the card's override reaches them.
	if t, ok := r.c.lead.(*canvas.Text); ok && r.c.leadToken != "" {
		t.Color = style.Color(r.c.leadToken)
	}
	if t, ok := r.c.content.(*canvas.Text); ok && r.c.contentToken != "" {
		t.Color = style.Color(r.c.contentToken)
	}
	for _, o := range r.Objects() {
		o.Refresh()
	}
	r.Layout(r.c.Size())
}

func (r *rowRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.c.lead, r.c.content}
	if r.c.trailing != nil {
		objs = append(objs, r.c.trailing)
	}
	return objs
}

func (r *rowRenderer) Destroy() {}
