package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of the choice field in the settings card (Settings.html): 26 high, radius 6,
// 10 px left and 8 px right padding, 6 px between the text and a 12 px chevron (stroke 2.2).
const (
	dropdownHeight  float32 = 26
	dropdownRadius  float32 = 6
	dropdownPadL    float32 = 10
	dropdownPadR    float32 = 8
	dropdownGap     float32 = 6
	dropdownChevron float32 = 12
	dropdownMenuGap float32 = 4
)

// Dropdown is the choice field of the settings dialog: the current choice and a chevron on a
// rounded field; a tap (or Space, Enter, Down) opens the options as the design's menu below
// it. widget.Select draws its own, taller field and cannot take the design's measures.
type Dropdown struct {
	widget.BaseWidget

	// Options are the choices; Selected is one of them.
	Options  []string
	Selected string
	// OnChanged is called after the user chose another option or SetSelected changed it.
	OnChanged func(string)
	// OnEscape, if set, runs on Esc while the field has the focus (a dialog closes).
	OnEscape func()

	hovered, focused bool
	menu             *MenuPopup
}

var (
	_ fyne.Tappable      = (*Dropdown)(nil)
	_ fyne.Focusable     = (*Dropdown)(nil)
	_ desktop.Hoverable  = (*Dropdown)(nil)
	_ desktop.Cursorable = (*Dropdown)(nil)
)

// NewDropdown returns a field with options and the first option selected.
func NewDropdown(options []string) *Dropdown {
	d := &Dropdown{Options: options}
	if len(options) > 0 {
		d.Selected = options[0]
	}
	d.ExtendBaseWidget(d)
	return d
}

// SelectedIndex returns the index of Selected in Options, -1 if it is none of them.
func (d *Dropdown) SelectedIndex() int {
	for i, o := range d.Options {
		if o == d.Selected {
			return i
		}
	}
	return -1
}

// SetSelected chooses an option; an unknown text is ignored.
func (d *Dropdown) SetSelected(s string) {
	for _, o := range d.Options {
		if o == s {
			if d.Selected != s {
				d.Selected = s
				d.Refresh()
				if d.OnChanged != nil {
					d.OnChanged(s)
				}
			}
			return
		}
	}
}

// SetSelectedIndex chooses the i-th option; an index out of range is ignored.
func (d *Dropdown) SetSelectedIndex(i int) {
	if i >= 0 && i < len(d.Options) {
		d.SetSelected(d.Options[i])
	}
}

// Open shows the options below the field; it does nothing while they are shown.
func (d *Dropdown) Open() {
	app := fyne.CurrentApp()
	if app == nil || (d.menu != nil && d.menu.Visible()) {
		return
	}
	c := app.Driver().CanvasForObject(d)
	if c == nil {
		return
	}
	m := fyne.NewMenu("")
	for _, o := range d.Options {
		item := fyne.NewMenuItem(o, func() { d.SetSelected(o) })
		item.Checked = o == d.Selected
		m.Items = append(m.Items, item)
	}
	d.menu = NewMenuPopup(m, c)
	abs := app.Driver().AbsolutePositionForObject(d)
	// Right edges aligned: the field sits at the right of its row.
	x := max(0, abs.X+d.Size().Width-menuWidth)
	d.menu.ShowAtPosition(fyne.NewPos(x, abs.Y+d.Size().Height+dropdownMenuGap))
}

// Menu returns the open options menu, nil if none was shown yet (for tests).
func (d *Dropdown) Menu() *MenuPopup { return d.menu }

func (d *Dropdown) Tapped(*fyne.PointEvent) {
	if app := fyne.CurrentApp(); app != nil {
		if c := app.Driver().CanvasForObject(d); c != nil {
			c.Focus(d)
		}
	}
	d.Open()
}

func (d *Dropdown) MouseIn(*desktop.MouseEvent)    { d.hovered = true; d.Refresh() }
func (d *Dropdown) MouseMoved(*desktop.MouseEvent) {}
func (d *Dropdown) MouseOut()                      { d.hovered = false; d.Refresh() }
func (d *Dropdown) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (d *Dropdown) FocusGained() { d.focused = true; d.Refresh() }
func (d *Dropdown) FocusLost()   { d.focused = false; d.Refresh() }
func (d *Dropdown) TypedRune(r rune) {
	if r == ' ' {
		d.Open()
	}
}
func (d *Dropdown) TypedKey(e *fyne.KeyEvent) {
	switch e.Name {
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeyDown:
		d.Open()
	case fyne.KeyEscape:
		if d.OnEscape != nil {
			d.OnEscape()
		}
	}
}

func (d *Dropdown) CreateRenderer() fyne.WidgetRenderer {
	r := &dropdownRenderer{d: d,
		bg:      &canvas.Rectangle{CornerRadius: dropdownRadius},
		over:    &canvas.Rectangle{CornerRadius: dropdownRadius},
		focus:   &canvas.Rectangle{CornerRadius: dropdownRadius, StrokeWidth: 2, FillColor: color.Transparent},
		text:    newText("", style.Size("text"), nil, false),
		chevron: NewSymbol(icons.GetStroke(icons.ChevronDown, icons.Secondary, 2.2), dropdownChevron),
	}
	r.Refresh()
	return r
}

type dropdownRenderer struct {
	d        *Dropdown
	bg, over *canvas.Rectangle
	focus    *canvas.Rectangle
	text     *canvas.Text
	chevron  *Symbol
}

// textWidth is the width of the widest option, so the field keeps its size when the choice
// changes.
func (r *dropdownRenderer) textWidth() float32 {
	w := fyne.MeasureText(r.d.Selected, r.text.TextSize, r.text.TextStyle).Width
	for _, o := range r.d.Options {
		w = max(w, fyne.MeasureText(o, r.text.TextSize, r.text.TextStyle).Width)
	}
	return w
}

func (r *dropdownRenderer) MinSize() fyne.Size {
	return fyne.NewSize(dropdownPadL+r.textWidth()+dropdownGap+dropdownChevron+dropdownPadR, dropdownHeight)
}

func (r *dropdownRenderer) Layout(size fyne.Size) {
	for _, o := range []*canvas.Rectangle{r.bg, r.over, r.focus} {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
	th := r.text.MinSize().Height
	r.text.Move(fyne.NewPos(dropdownPadL, (size.Height-th)/2))
	r.text.Resize(fyne.NewSize(size.Width-dropdownPadL-dropdownGap-dropdownChevron-dropdownPadR, th))
	r.chevron.Move(fyne.NewPos(size.Width-dropdownPadR-dropdownChevron, (size.Height-dropdownChevron)/2))
	r.chevron.Resize(fyne.NewSquareSize(dropdownChevron))
}

func (r *dropdownRenderer) Refresh() {
	d := r.d
	r.bg.FillColor = style.Color(style.ColorNameTile)
	r.bg.Refresh()
	r.over.FillColor = color.Transparent
	if d.hovered {
		r.over.FillColor = style.Color("hover")
	}
	r.over.Refresh()
	r.focus.StrokeColor = color.Transparent
	if d.focused {
		r.focus.StrokeColor = style.Color("focus")
	}
	r.focus.Refresh()
	r.text.Text = d.Selected
	r.text.Color = style.Color("foreground")
	r.text.Refresh()
	r.Layout(d.Size())
	canvas.Refresh(d)
}

func (r *dropdownRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.over, r.focus, r.text, r.chevron}
}
func (r *dropdownRenderer) Destroy() {}
