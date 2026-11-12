package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of a filter chip (WindowB.dc.html .chip): 1 px border, fully rounded, 4 x 10 px
// padding, 12 px text.
const (
	chipPadH float32 = 10
	chipPadV float32 = 4
)

// Chip is a filter chip of the log pages ("All", "Warning", "Critical"): a rounded label with a
// border, filled dark with white text while active. A tap calls OnTapped; the caller decides
// which chip is active (SetActive).
type Chip struct {
	widget.DisableableWidget

	OnTapped func()

	text    string
	active  bool
	hovered bool
	focused bool
}

var (
	_ fyne.Tappable      = (*Chip)(nil)
	_ fyne.Focusable     = (*Chip)(nil)
	_ desktop.Hoverable  = (*Chip)(nil)
	_ desktop.Cursorable = (*Chip)(nil)
)

// NewChip returns an inactive chip.
func NewChip(text string, tapped func()) *Chip {
	c := &Chip{text: text, OnTapped: tapped}
	c.ExtendBaseWidget(c)
	return c
}

// SetText changes the caption.
func (c *Chip) SetText(s string) { c.text = s; c.Refresh() }

// Text returns the caption.
func (c *Chip) Text() string { return c.text }

// SetActive marks the chip selected.
func (c *Chip) SetActive(on bool) { c.active = on; c.Refresh() }

// Active reports whether the chip is selected.
func (c *Chip) Active() bool { return c.active }

func (c *Chip) Tapped(*fyne.PointEvent) {
	if c.Disabled() || c.OnTapped == nil {
		return
	}
	c.OnTapped()
}

func (c *Chip) MouseIn(*desktop.MouseEvent)    { c.hovered = true; c.Refresh() }
func (c *Chip) MouseMoved(*desktop.MouseEvent) {}
func (c *Chip) MouseOut()                      { c.hovered = false; c.Refresh() }
func (c *Chip) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (c *Chip) FocusGained()                   { c.focused = true; c.Refresh() }
func (c *Chip) FocusLost()                     { c.focused = false; c.Refresh() }
func (c *Chip) TypedRune(r rune) {
	if r == ' ' {
		c.Tapped(nil)
	}
}
func (c *Chip) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyReturn || e.Name == fyne.KeyEnter {
		c.Tapped(nil)
	}
}

func (c *Chip) CreateRenderer() fyne.WidgetRenderer {
	r := &chipRenderer{c: c,
		bg:    &canvas.Rectangle{StrokeWidth: 1},
		over:  &canvas.Rectangle{},
		focus: &canvas.Rectangle{StrokeWidth: 2, FillColor: color.Transparent},
		text:  newText("", style.Size(style.SizeNameSmallText), nil, false),
	}
	r.Refresh()
	return r
}

type chipRenderer struct {
	c               *Chip
	bg, over, focus *canvas.Rectangle
	text            *canvas.Text
}

func (r *chipRenderer) MinSize() fyne.Size {
	ts := fyne.MeasureText(r.c.text, r.text.TextSize, r.text.TextStyle)
	return fyne.NewSize(ts.Width+2*chipPadH+2, ts.Height+2*chipPadV+2)
}

func (r *chipRenderer) Layout(size fyne.Size) {
	h := r.MinSize().Height
	top := (size.Height - h) / 2
	for _, o := range []*canvas.Rectangle{r.bg, r.over, r.focus} {
		o.CornerRadius = h / 2
		o.Move(fyne.NewPos(0, top))
		o.Resize(fyne.NewSize(size.Width, h))
	}
	ts := r.text.MinSize()
	r.text.Move(fyne.NewPos((size.Width-ts.Width)/2, top+(h-ts.Height)/2))
	r.text.Resize(ts)
}

func (r *chipRenderer) Refresh() {
	c := r.c
	fill, border, fg := style.Color(style.ColorNameField), style.Color(style.ColorNameControlBorder), style.Color(style.ColorNameControlText)
	if c.active {
		fill, border, fg = style.Color(style.ColorNameChipActive), style.Color(style.ColorNameChipActive), style.Color(style.ColorNameChipActiveText)
	}
	if c.Disabled() {
		fg = style.Color("disabled")
	}
	r.bg.FillColor, r.bg.StrokeColor = fill, border
	r.bg.Refresh()
	r.over.FillColor = color.Transparent
	if c.hovered && !c.active && !c.Disabled() {
		r.over.FillColor = style.Color("hover")
	}
	r.over.Refresh()
	r.focus.StrokeColor = color.Transparent
	if c.focused && !c.Disabled() {
		r.focus.StrokeColor = style.Color("focus")
	}
	r.focus.Refresh()
	r.text.Text = c.text
	r.text.Color = fg
	r.text.Refresh()
	r.Layout(c.Size())
	canvas.Refresh(c)
}

func (r *chipRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.over, r.focus, r.text}
}
func (r *chipRenderer) Destroy() {}
