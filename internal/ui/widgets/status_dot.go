// Package widgets holds the custom widgets of the OpenLAMT design (issue #44) shared by the
// manager and the device window: ToolbarButton, Switch, FormCard, SidebarRow and StatusDot.
// Colors and sizes come from package style, symbols from package icons.
package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// dotRing is the width of the optional ring around a dot.
const dotRing float32 = 1.5

// selectedRing is the width of the white ring of a dot on a selected row.
const selectedRing float32 = 1

// outlineWidth is the width of the outline of a filled dot in light (style.StatusOutline).
const outlineWidth float32 = 1

// DotState is what a status dot shows.
type DotState int

const (
	DotNone    DotState = iota // nothing drawn; the space stays reserved
	DotOn                      // powered on: green
	DotOff                     // powered off: red
	DotUnknown                 // state not known (no password, error, unknown certificate): gray ring
	DotAsleep                  // standby or hibernation: orange
)

// Status is the state of the palette (style.StatusColor) a dot state is drawn in; DotNone and
// unknown values answer style.StatusUnknown.
func (s DotState) Status() style.Status {
	switch s {
	case DotOn:
		return style.StatusOn
	case DotOff:
		return style.StatusOff
	case DotAsleep:
		return style.StatusStandby
	}
	return style.StatusUnknown
}

// StatusDot is the round power-state indicator (7 to 8 px) of the sidebar, the Power toolbar
// button, the status bar and the status row of the detail card. Its colors come from
// style.StatusColor for the current variant and color blind mode, read on every Refresh.
type StatusDot struct {
	widget.BaseWidget

	state      DotState
	size       float32
	selected   bool        // on a selected (accent) row the dot keeps its color and gets a white ring
	border     color.Color // optional ring, e.g. on the Power button; nil for none
	colorBlind bool        // always the color-blind palette (the preview in the settings)
}

// NewStatusDot returns a dot of diameter size (style.StatusDotSize or StatusDotSizeSmall).
func NewStatusDot(state DotState, size float32) *StatusDot {
	d := &StatusDot{state: state, size: size}
	d.ExtendBaseWidget(d)
	return d
}

// NewColorBlindPreviewDot returns a dot that always draws the color-blind palette of the current
// variant, whatever the setting says: the preview of the setting "Color blind mode".
func NewColorBlindPreviewDot(state DotState, size float32) *StatusDot {
	d := NewStatusDot(state, size)
	d.colorBlind = true
	return d
}

// State returns the shown state.
func (d *StatusDot) State() DotState { return d.state }

// SetState changes the shown state.
func (d *StatusDot) SetState(s DotState) {
	if d.state == s {
		return
	}
	d.state = s
	d.Refresh()
}

// SetSelected draws a white ring around the dot, for rows on the accent background.
func (d *StatusDot) SetSelected(sel bool) {
	if d.selected == sel {
		return
	}
	d.selected = sel
	d.Refresh()
}

// SetBorder draws a 1.5 px ring in c around the dot (nil removes it).
func (d *StatusDot) SetBorder(c color.Color) {
	d.border = c
	d.Refresh()
}

// color is the palette color of the shown state.
func (d *StatusDot) color() color.Color {
	return style.StatusColor(d.state.Status(), style.Variant(), d.colorBlind || style.ColorBlind())
}

// filled reports whether the state is drawn as a filled dot.
func (d *StatusDot) filled() bool {
	return d.state == DotOn || d.state == DotOff || d.state == DotAsleep
}

// colors returns fill and stroke for the current state.
func (d *StatusDot) colors() (fill, stroke color.Color, width float32) {
	switch d.state {
	case DotOn, DotOff, DotAsleep:
		fill = d.color()
		if d.border != nil {
			return fill, d.border, dotRing
		}
		if d.selected { // real color with a thin white ring inside the dot, so the row keeps its size
			return fill, style.Color(style.ColorNameWhite), selectedRing
		}
		return fill, color.Transparent, 0
	case DotUnknown:
		c := d.color()
		if d.selected {
			c = style.Color(style.ColorNameSelectedText2)
		}
		if d.border != nil { // the ring sits inside the dot, on a disk in the border color
			return d.border, color.Transparent, 0
		}
		return color.Transparent, c, dotRing
	}
	return color.Transparent, color.Transparent, 0
}

// innerRing returns the circle drawn over the dot at its plain size: the gray ring of an unknown
// state with a border (Power button), or in light the outline of a filled dot, which the white
// ring of a selected row replaces. hidden when there is none.
func (d *StatusDot) innerRing() (stroke color.Color, width float32, hidden bool) {
	switch {
	case d.state == DotUnknown && d.border != nil:
		return d.color(), dotRing, false
	case d.filled() && style.IsLight() && !d.selected:
		return style.StatusOutline(style.Variant()), outlineWidth, false
	}
	return color.Transparent, 0, true
}

func (d *StatusDot) CreateRenderer() fyne.WidgetRenderer {
	r := &dotRenderer{d: d, circle: &canvas.Circle{}, inner: &canvas.Circle{FillColor: color.Transparent, StrokeWidth: dotRing}}
	r.Refresh()
	return r
}

// dotRenderer draws circle; inner is drawn over it at the plain dot size (innerRing): the ring
// of an unknown state with a border, which like the known states covers the symbol under it with
// the border color instead of letting it show through a hollow ring, or the light outline.
type dotRenderer struct {
	d      *StatusDot
	circle *canvas.Circle
	inner  *canvas.Circle
}

// outer is the diameter including the ring: CSS adds the 1.5 px border outside the dot.
func (r *dotRenderer) outer() float32 {
	if r.d.border != nil || r.d.state == DotUnknown {
		return r.d.size + 2*dotRing
	}
	return r.d.size
}

func (r *dotRenderer) Layout(size fyne.Size) {
	s := r.outer()
	r.circle.Resize(fyne.NewSquareSize(s))
	r.circle.Move(fyne.NewPos((size.Width-s)/2, (size.Height-s)/2))
	r.inner.Resize(fyne.NewSquareSize(r.d.size))
	r.inner.Move(fyne.NewPos((size.Width-r.d.size)/2, (size.Height-r.d.size)/2))
}

func (r *dotRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(r.outer()) }

func (r *dotRenderer) Refresh() {
	r.circle.FillColor, r.circle.StrokeColor, r.circle.StrokeWidth = r.d.colors()
	r.inner.StrokeColor, r.inner.StrokeWidth, r.inner.Hidden = r.d.innerRing()
	r.Layout(r.d.Size())
	r.circle.Refresh()
	r.inner.Refresh()
}

func (r *dotRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.circle, r.inner} }
func (r *dotRenderer) Destroy()                     {}
