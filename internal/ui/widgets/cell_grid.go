package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Cell geometry of the credits grid (About.html): 14 px side padding, 8 px between name and
// license badge, badges 20 high with radius 5 and 7 px side padding.
const (
	cellPadH      float32 = 14
	cellGap       float32 = 8
	badgeHeight   float32 = 20
	badgeRadius   float32 = 5
	badgePadH     float32 = 7
	badgeTextSize float32 = 11
)

// NewCell returns one cell of a CellGrid: lead at the left, trail (nil for none) at the right,
// both centered vertically.
func NewCell(lead, trail fyne.CanvasObject) fyne.CanvasObject {
	c := &cell{lead: lead, trail: trail}
	c.ExtendBaseWidget(c)
	return c
}

type cell struct {
	widget.BaseWidget
	lead, trail fyne.CanvasObject
}

func (c *cell) CreateRenderer() fyne.WidgetRenderer { return &cellRenderer{c: c} }

type cellRenderer struct{ c *cell }

func (r *cellRenderer) MinSize() fyne.Size {
	w, h := 2*cellPadH+r.c.lead.MinSize().Width, r.c.lead.MinSize().Height
	if r.c.trail != nil {
		w += cellGap + r.c.trail.MinSize().Width
		h = max(h, r.c.trail.MinSize().Height)
	}
	return fyne.NewSize(w, h)
}

func (r *cellRenderer) Layout(size fyne.Size) {
	right := size.Width - cellPadH
	if t := r.c.trail; t != nil {
		ts := t.MinSize()
		t.Move(fyne.NewPos(right-ts.Width, (size.Height-ts.Height)/2))
		t.Resize(ts)
		right -= ts.Width + cellGap
	}
	ls := r.c.lead.MinSize()
	r.c.lead.Move(fyne.NewPos(cellPadH, (size.Height-ls.Height)/2))
	r.c.lead.Resize(fyne.NewSize(min(ls.Width, right-cellPadH), ls.Height))
}

func (r *cellRenderer) Refresh() {
	for _, o := range r.Objects() {
		o.Refresh()
	}
	r.Layout(r.c.Size())
}

func (r *cellRenderer) Objects() []fyne.CanvasObject {
	if r.c.trail == nil {
		return []fyne.CanvasObject{r.c.lead}
	}
	return []fyne.CanvasObject{r.c.lead, r.c.trail}
}
func (r *cellRenderer) Destroy() {}

// NewBadge returns the small monospace license badge of the credits.
func NewBadge(text string) fyne.CanvasObject {
	b := &Badge{Text: text}
	b.ExtendBaseWidget(b)
	return b
}

// Badge is a short monospace text on a small rounded tile.
type Badge struct {
	widget.BaseWidget
	Text string
}

func (b *Badge) CreateRenderer() fyne.WidgetRenderer {
	r := &badgeRenderer{b: b,
		bg:   &canvas.Rectangle{CornerRadius: badgeRadius},
		text: newText("", badgeTextSize, style.Color(style.ColorNameDialogText), false)}
	r.text.TextStyle = fyne.TextStyle{Monospace: true}
	r.Refresh()
	return r
}

type badgeRenderer struct {
	b    *Badge
	bg   *canvas.Rectangle
	text *canvas.Text
}

func (r *badgeRenderer) MinSize() fyne.Size {
	return fyne.NewSize(2*badgePadH+fyne.MeasureText(r.b.Text, badgeTextSize, r.text.TextStyle).Width, badgeHeight)
}

func (r *badgeRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	ts := fyne.MeasureText(r.b.Text, badgeTextSize, r.text.TextStyle)
	r.text.Move(fyne.NewPos((size.Width-ts.Width)/2, (size.Height-ts.Height)/2))
	r.text.Resize(ts)
}

func (r *badgeRenderer) Refresh() {
	r.bg.FillColor = style.Color(style.ColorNameTile)
	r.bg.Refresh()
	r.text.Color = style.Color(style.ColorNameDialogText)
	r.text.Text = r.b.Text
	r.text.Refresh()
	r.Layout(r.b.Size())
}

func (r *badgeRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.text} }
func (r *badgeRenderer) Destroy()                     {}

// CellGrid lays cells out in columns of equal width and rows of RowHeight, in a rounded card
// (radius 10, #232326, hairline border) with hairlines between the cells.
type CellGrid struct {
	widget.BaseWidget

	Cols      int
	RowHeight float32
	cells     []fyne.CanvasObject
}

// NewCellGrid returns a grid of cols columns for cells.
func NewCellGrid(cols int, rowHeight float32, cells ...fyne.CanvasObject) *CellGrid {
	g := &CellGrid{Cols: cols, RowHeight: rowHeight, cells: cells}
	g.ExtendBaseWidget(g)
	return g
}

func (g *CellGrid) rows() int { return (len(g.cells) + g.Cols - 1) / g.Cols }

func (g *CellGrid) CreateRenderer() fyne.WidgetRenderer {
	r := &gridRenderer{g: g, bg: &canvas.Rectangle{CornerRadius: style.CardRadius,
		FillColor: style.Color(style.ColorNameDialogCard), StrokeColor: style.Color(style.ColorNameCardBorder), StrokeWidth: 1}}
	for i := 0; i < g.rows()-1+g.Cols-1; i++ {
		r.lines = append(r.lines, &canvas.Rectangle{FillColor: style.Color(style.ColorNameCardBorder)})
	}
	return r
}

type gridRenderer struct {
	g     *CellGrid
	bg    *canvas.Rectangle
	lines []*canvas.Rectangle // row separators, then column separators
}

// colWidths returns the width of every column for the given total width. The columns are
// equal, unless a column's widest cell needs more than its share (Inter is wider than the
// system font of the mockup): that column then gets what it needs and the others share the
// rest.
func (r *gridRenderer) colWidths(total float32) []float32 {
	g := r.g
	mins := make([]float32, g.Cols)
	for i, c := range g.cells {
		mins[i%g.Cols] = max(mins[i%g.Cols], c.MinSize().Width)
	}
	avail := total - 2 - float32(g.Cols-1)
	widths := make([]float32, g.Cols)
	for i := range widths {
		widths[i] = avail / float32(g.Cols)
	}
	for i := range widths {
		if mins[i] > widths[i] {
			var others float32
			for j := range widths {
				if j != i {
					others += widths[j]
				}
			}
			if g.Cols > 1 {
				give := min(mins[i]-widths[i], others)
				widths[i] += give
				for j := range widths {
					if j != i {
						widths[j] -= give / float32(g.Cols-1)
					}
				}
			}
		}
	}
	return widths
}

func (r *gridRenderer) MinSize() fyne.Size {
	// The cells may shrink to the equal share or less: only their sum matters, so the dialog
	// decides the width. A single minimal width keeps the grid from forcing the dialog wider.
	rows := float32(r.g.rows())
	return fyne.NewSize(float32(r.g.Cols)*60+float32(r.g.Cols-1)+2, rows*r.g.RowHeight+rows-1+2)
}

func (r *gridRenderer) Layout(size fyne.Size) {
	g := r.g
	r.bg.Resize(size)
	widths := r.colWidths(size.Width)
	xs := make([]float32, g.Cols) // left edge of every column
	x := float32(1)
	for i, w := range widths {
		xs[i] = x
		x += w + 1
	}
	li := 0
	for i := 1; i < g.rows(); i++ {
		y := 1 + float32(i)*g.RowHeight + float32(i-1)
		r.lines[li].Move(fyne.NewPos(1, y))
		r.lines[li].Resize(fyne.NewSize(size.Width-2, 1))
		li++
	}
	for i := 1; i < g.Cols; i++ {
		r.lines[li].Move(fyne.NewPos(xs[i]-1, 1))
		r.lines[li].Resize(fyne.NewSize(1, size.Height-2))
		li++
	}
	for i, c := range g.cells {
		col, row := float32(i%g.Cols), float32(i/g.Cols)
		c.Move(fyne.NewPos(xs[int(col)], 1+row*(g.RowHeight+1)))
		c.Resize(fyne.NewSize(widths[int(col)], g.RowHeight))
	}
}

func (r *gridRenderer) Refresh() {
	r.bg.FillColor = style.Color(style.ColorNameDialogCard)
	r.bg.StrokeColor = style.Color(style.ColorNameCardBorder)
	for _, l := range r.lines {
		l.FillColor = style.Color(style.ColorNameCardBorder)
	}
	for _, c := range r.g.cells {
		c.Refresh()
	}
	r.Layout(r.g.Size())
	canvas.Refresh(r.g)
}

func (r *gridRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.bg}
	for _, l := range r.lines {
		objs = append(objs, l)
	}
	return append(objs, r.g.cells...)
}
func (r *gridRenderer) Destroy() {}
