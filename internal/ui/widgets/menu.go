package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of a drop-down menu (SessionA2.html, Power menu): 236 wide, 5 padding, radius 10,
// 30 px rows 1 px apart with 10 px side padding, a 16 px symbol and a 10 px gap before the
// text, separators 1 px with a 4/8 px margin.
const (
	menuWidth      float32 = 236
	menuPad        float32 = 5
	menuRowGap     float32 = 1
	menuRowHeight  float32 = 30
	menuRowPadH    float32 = 10
	menuRowRadius  float32 = 6
	menuIconSize   float32 = 16
	menuIconGap    float32 = 10
	menuSepMarginV float32 = 4
	menuSepMarginH float32 = 8
	menuEdge       float32 = 8   // free space kept at the canvas edge when a menu scrolls
	menuMinScroll  float32 = 240 // least height a scrolling menu gets before it moves up
)

// MenuPopup shows a fyne.Menu as the design's drop-down: own rows instead of Fyne's menu
// widget, whose row height and symbol size follow theme sizes shared with other widgets.
// Item fields used: Label, Icon (16 px symbol, drawn as is), Disabled, Checked (a check mark in
// the symbol column), IsSeparator and Action. A tap outside or Escape dismisses it; Up, Down,
// Return and Space work as in Fyne's menu.
type MenuPopup struct {
	widget.BaseWidget

	// OnDismiss runs once the menu has closed, also after an item was chosen (before its
	// action).
	OnDismiss func()

	menu   *fyne.Menu
	canvas fyne.Canvas
	rows   []*menuRow // per menu item; nil for separators
	panel  fyne.CanvasObject
	scroll *container.Scroll // the rows; scrolls when the menu is taller than the canvas
	bg     *canvas.Rectangle // panel background, border and shadow
	pos    fyne.Position
	active int // keyboard row, -1 for none
	shown  bool
}

var (
	_ fyne.Tappable  = (*MenuPopup)(nil)
	_ fyne.Focusable = (*MenuPopup)(nil)
)

// NewMenuPopup returns a hidden menu for m on canvas c.
func NewMenuPopup(m *fyne.Menu, c fyne.Canvas) *MenuPopup {
	p := &MenuPopup{menu: m, canvas: c, active: -1}
	p.ExtendBaseWidget(p)
	var objs []fyne.CanvasObject
	for _, it := range m.Items {
		if it.IsSeparator {
			p.rows = append(p.rows, nil)
			objs = append(objs, newMenuSeparator())
			continue
		}
		r := newMenuRow(p, it)
		p.rows = append(p.rows, r)
		objs = append(objs, r)
	}
	bg := &canvas.Rectangle{CornerRadius: style.MenuRadius, StrokeWidth: 1,
		Shadow: canvas.Shadow{BlurRadius: 25, Offset: fyne.NewPos(0, 18), Variant: canvas.DropShadow}}
	p.bg = bg
	p.recolor()
	p.scroll = container.NewVScroll(&fyne.Container{Layout: menuRowsLayout{}, Objects: objs})
	p.panel = &fyne.Container{Layout: menuPanelLayout{}, Objects: []fyne.CanvasObject{bg, p.scroll}}
	return p
}

// ShowAtPosition shows the menu with its top left corner at pos (canvas coordinates) and
// takes the keyboard focus.
func (p *MenuPopup) ShowAtPosition(pos fyne.Position) {
	p.pos = pos
	if !p.shown {
		p.shown = true
		p.canvas.Overlays().Add(p)
	}
	p.Refresh()
	p.canvas.Focus(p)
}

// Dismiss closes the menu and calls OnDismiss; nothing happens if it is not shown.
func (p *MenuPopup) Dismiss() {
	if !p.shown {
		return
	}
	p.shown = false
	p.canvas.Overlays().Remove(p)
	if p.OnDismiss != nil {
		p.OnDismiss()
	}
}

// Visible reports whether the menu is shown.
func (p *MenuPopup) Visible() bool { return p.shown }

// Item returns the row of the i-th menu item (nil for a separator), for tests.
func (p *MenuPopup) Item(i int) fyne.Tappable {
	if r := p.rows[i]; r != nil {
		return r
	}
	return nil
}

// recolor reads the panel colors of the current theme variant.
func (p *MenuPopup) recolor() {
	p.bg.FillColor = style.Color(style.ColorNameMenuPanel)
	p.bg.StrokeColor = style.Color(style.ColorNameDialogBorder)
	p.bg.Shadow.Color = style.Color(style.ColorNameMenuShadow)
	p.bg.Refresh()
}

// Refresh redraws the rows from the current item fields (Disabled, Checked, Icon) and the
// panel in the colors of the current theme variant.
func (p *MenuPopup) Refresh() {
	p.recolor()
	for _, r := range p.rows {
		if r != nil {
			r.Refresh()
		}
	}
	p.BaseWidget.Refresh()
}

// Tapped outside the panel dismisses the menu; taps on rows go to the rows, taps on the
// panel's padding do nothing.
func (p *MenuPopup) Tapped(e *fyne.PointEvent) {
	pos, size := p.panel.Position(), p.panel.Size()
	if e != nil && e.Position.X >= pos.X && e.Position.Y >= pos.Y && e.Position.X < pos.X+size.Width && e.Position.Y < pos.Y+size.Height {
		return
	}
	p.Dismiss()
}

func (p *MenuPopup) FocusGained()   {}
func (p *MenuPopup) FocusLost()     {}
func (p *MenuPopup) TypedRune(rune) {}

func (p *MenuPopup) TypedKey(e *fyne.KeyEvent) {
	switch e.Name {
	case fyne.KeyDown:
		p.move(1)
	case fyne.KeyUp:
		p.move(-1)
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		if p.active >= 0 {
			p.rows[p.active].trigger()
		}
	case fyne.KeyEscape:
		p.Dismiss()
	}
}

// move steps the keyboard row by dir to the next enabled item.
func (p *MenuPopup) move(dir int) {
	n := len(p.rows)
	i := p.active
	for range n {
		i = (i + dir + n) % n
		if r := p.rows[i]; r != nil && !r.item.Disabled {
			p.setActive(i)
			return
		}
	}
}

func (p *MenuPopup) setActive(i int) {
	old := p.active
	p.active = i
	for _, j := range []int{old, i} {
		if j >= 0 && p.rows[j] != nil {
			p.rows[j].Refresh()
		}
	}
	if i >= 0 && p.rows[i] != nil {
		p.reveal(p.rows[i])
	}
}

// reveal scrolls a row into view when the menu is shorter than its rows.
func (p *MenuPopup) reveal(r *menuRow) {
	top, bottom := r.Position().Y, r.Position().Y+r.Size().Height
	off, view := p.scroll.Offset.Y, p.scroll.Size().Height
	switch {
	case top < off:
		p.scroll.Offset.Y = top
	case bottom > off+view:
		p.scroll.Offset.Y = bottom - view
	default:
		return
	}
	p.scroll.Refresh()
}

func (p *MenuPopup) index(r *menuRow) int {
	for i, x := range p.rows {
		if x == r {
			return i
		}
	}
	return -1
}

func (p *MenuPopup) CreateRenderer() fyne.WidgetRenderer {
	return &menuPopupRenderer{p: p}
}

type menuPopupRenderer struct{ p *MenuPopup }

func (r *menuPopupRenderer) Layout(size fyne.Size) {
	ps := r.p.panel.MinSize()
	pos := r.p.pos
	// A menu taller than the space below its button keeps its top and scrolls; it never
	// grows beyond the canvas.
	ps.Height = min(ps.Height, size.Height-2*menuEdge, max(size.Height-pos.Y-menuEdge, menuMinScroll))
	// Keep the panel inside the canvas, as Fyne's menu does.
	pos.X = max(0, min(pos.X, size.Width-ps.Width))
	pos.Y = max(0, min(pos.Y, size.Height-ps.Height))
	r.p.panel.Move(pos)
	r.p.panel.Resize(ps)
}

func (r *menuPopupRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *menuPopupRenderer) Refresh()                     { r.Layout(r.p.Size()); canvas.Refresh(r.p) }
func (r *menuPopupRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.p.panel} }
func (r *menuPopupRenderer) Destroy()                     {}

// menuPanelLayout draws objs[0] (background) under the whole panel and puts the scrolling
// rows (objs[1]) inside the padding.
type menuPanelLayout struct{}

func (menuPanelLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(size)
	objs[1].Move(fyne.NewPos(menuPad, menuPad))
	objs[1].Resize(fyne.NewSize(size.Width-2*menuPad, size.Height-2*menuPad))
}

func (menuPanelLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	m := objs[1].(*container.Scroll).Content.MinSize()
	return fyne.NewSize(max(menuWidth, m.Width+2*menuPad), m.Height+2*menuPad)
}

// menuRowsLayout stacks the rows and separators.
type menuRowsLayout struct{}

func (menuRowsLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for i, o := range objs {
		if i > 0 {
			y += menuRowGap
		}
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h
	}
}

func (menuRowsLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for i, o := range objs {
		if i > 0 {
			h += menuRowGap
		}
		m := o.MinSize()
		h += m.Height
		w = max(w, m.Width)
	}
	return fyne.NewSize(w, h)
}

// newMenuSeparator is the 1 px line between groups, with its margin.
func newMenuSeparator() fyne.CanvasObject {
	line := style.BindRect(canvas.NewRectangle(nil), style.ColorNameGroupDivider, "")
	return &fyne.Container{Layout: separatorLayout{}, Objects: []fyne.CanvasObject{line}}
}

type separatorLayout struct{}

func (separatorLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(menuSepMarginH, menuSepMarginV))
	objs[0].Resize(fyne.NewSize(size.Width-2*menuSepMarginH, 1))
}

func (separatorLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(2*menuSepMarginH, 1+2*menuSepMarginV)
}

// menuRow is one item: symbol (or check mark) and text; accent background on hover or when
// selected with the keyboard.
type menuRow struct {
	widget.BaseWidget
	p       *MenuPopup
	item    *fyne.MenuItem
	hovered bool
}

var (
	_ fyne.Tappable     = (*menuRow)(nil)
	_ desktop.Hoverable = (*menuRow)(nil)
)

func newMenuRow(p *MenuPopup, it *fyne.MenuItem) *menuRow {
	r := &menuRow{p: p, item: it}
	r.ExtendBaseWidget(r)
	return r
}

func (r *menuRow) Tapped(*fyne.PointEvent) { r.trigger() }

// trigger closes the menu and runs the item's action; disabled items do nothing.
func (r *menuRow) trigger() {
	if r.item.Disabled {
		return
	}
	r.p.Dismiss()
	if r.item.Action != nil {
		r.item.Action()
	}
}

func (r *menuRow) MouseIn(*desktop.MouseEvent) {
	r.hovered = true
	if i := r.p.index(r); i >= 0 && !r.item.Disabled {
		r.p.setActive(i)
	}
	r.Refresh()
}
func (r *menuRow) MouseMoved(*desktop.MouseEvent) {}
func (r *menuRow) MouseOut() {
	r.hovered = false
	if r.p.active == r.p.index(r) {
		r.p.setActive(-1)
	}
	r.Refresh()
}

func (r *menuRow) highlighted() bool {
	return !r.item.Disabled && (r.hovered || r.p.active == r.p.index(r))
}

func (r *menuRow) CreateRenderer() fyne.WidgetRenderer {
	rr := &menuRowRenderer{r: r, bg: &canvas.Rectangle{CornerRadius: menuRowRadius},
		icon: NewSymbol(nil, 0), text: newText("", style.Size(theme.SizeNameText), nil, false)}
	rr.Refresh()
	return rr
}

type menuRowRenderer struct {
	r    *menuRow
	bg   *canvas.Rectangle
	icon *Symbol
	text *canvas.Text
}

func (rr *menuRowRenderer) MinSize() fyne.Size {
	tw := fyne.MeasureText(rr.r.item.Label, rr.text.TextSize, rr.text.TextStyle).Width
	return fyne.NewSize(2*menuRowPadH+menuIconSize+menuIconGap+tw, menuRowHeight)
}

func (rr *menuRowRenderer) Layout(size fyne.Size) {
	rr.bg.Resize(size)
	rr.icon.Move(fyne.NewPos(menuRowPadH, (size.Height-menuIconSize)/2))
	rr.icon.Resize(fyne.NewSquareSize(menuIconSize))
	ts := rr.text.MinSize()
	x := menuRowPadH + menuIconSize + menuIconGap
	rr.text.Move(fyne.NewPos(x, (size.Height-ts.Height)/2))
	rr.text.Resize(fyne.NewSize(max(0, size.Width-x-menuRowPadH), ts.Height))
}

func (rr *menuRowRenderer) Refresh() {
	it := rr.r.item
	hi := rr.r.highlighted()
	rr.bg.FillColor = color.Transparent
	textColor := style.Color("foreground")
	switch {
	case it.Disabled:
		textColor = style.Color("disabled")
	case hi:
		rr.bg.FillColor = style.Color("primary")
		textColor = style.Color(style.ColorNameWhite)
	}
	rr.bg.Refresh()
	res := it.Icon
	if it.Checked && res == nil {
		res = theme.NewColoredResource(theme.ConfirmIcon(), style.ColorNameWhite)
		if !hi {
			res = theme.NewColoredResource(theme.ConfirmIcon(), theme.ColorNameForeground)
		}
	}
	rr.icon.SetResource(res)
	setVisible(rr.icon, res != nil)
	rr.text.Text = it.Label
	rr.text.Color = textColor
	rr.text.Refresh()
	rr.Layout(rr.r.Size())
}

func (rr *menuRowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{rr.bg, rr.icon, rr.text}
}
func (rr *menuRowRenderer) Destroy() {}
