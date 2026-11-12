package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Switch is the on/off toggle of the form cards (ManagerA.html): a 38 x 22 track with an
// 18 px white knob, accent when on. Tap or Space toggles it; it replaces widget.Check.
type Switch struct {
	widget.DisableableWidget

	// OnChanged is called after the state changed, by the user or by SetChecked.
	OnChanged func(bool)
	// OnEscape, if set, runs on Esc while the switch has the focus (a dialog closes).
	OnEscape func()
	// OnColor names the track color while on; "" is the accent ("primary").
	OnColor fyne.ThemeColorName

	checked bool
	focused bool
}

var (
	_ fyne.Tappable  = (*Switch)(nil)
	_ fyne.Focusable = (*Switch)(nil)
)

// NewSwitch returns a switch that is off.
func NewSwitch(changed func(bool)) *Switch {
	s := &Switch{OnChanged: changed}
	s.ExtendBaseWidget(s)
	return s
}

// Checked reports whether the switch is on.
func (s *Switch) Checked() bool { return s.checked }

// SetChecked sets the state and calls OnChanged if it changed.
func (s *Switch) SetChecked(on bool) {
	if s.checked == on {
		return
	}
	s.checked = on
	s.Refresh()
	if s.OnChanged != nil {
		s.OnChanged(on)
	}
}

// Toggle flips the state unless the switch is disabled.
func (s *Switch) Toggle() {
	if s.Disabled() {
		return
	}
	s.SetChecked(!s.checked)
}

func (s *Switch) Tapped(*fyne.PointEvent) {
	if s.Disabled() {
		return
	}
	if c := fyne.CurrentApp(); c != nil {
		if w := c.Driver().CanvasForObject(s); w != nil {
			w.Focus(s)
		}
	}
	s.Toggle()
}

func (s *Switch) FocusGained() { s.focused = true; s.Refresh() }
func (s *Switch) FocusLost()   { s.focused = false; s.Refresh() }

// TypedRune toggles on Space, like a checkbox.
func (s *Switch) TypedRune(r rune) {
	if r == ' ' {
		s.Toggle()
	}
}
func (s *Switch) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyEscape && s.OnEscape != nil {
		s.OnEscape()
	}
}

func (s *Switch) CreateRenderer() fyne.WidgetRenderer {
	r := &switchRenderer{
		s:     s,
		track: &canvas.Rectangle{CornerRadius: style.SwitchHeight / 2},
		focus: &canvas.Rectangle{CornerRadius: style.SwitchHeight/2 + 3, FillColor: color.Transparent, StrokeWidth: 3},
		knob:  &canvas.Circle{},
	}
	r.Refresh()
	return r
}

type switchRenderer struct {
	s            *Switch
	track, focus *canvas.Rectangle
	knob         *canvas.Circle
}

func (r *switchRenderer) MinSize() fyne.Size {
	return fyne.NewSize(style.SwitchWidth, style.SwitchHeight)
}

func (r *switchRenderer) Layout(size fyne.Size) {
	x := (size.Width - style.SwitchWidth) / 2
	y := (size.Height - style.SwitchHeight) / 2
	r.track.Move(fyne.NewPos(x, y))
	r.track.Resize(fyne.NewSize(style.SwitchWidth, style.SwitchHeight))
	r.focus.Move(fyne.NewPos(x-3, y-3))
	r.focus.Resize(fyne.NewSize(style.SwitchWidth+6, style.SwitchHeight+6))
	pad := (style.SwitchHeight - style.SwitchKnob) / 2
	kx := x + pad
	if r.s.checked {
		kx = x + style.SwitchWidth - pad - style.SwitchKnob
	}
	r.knob.Move(fyne.NewPos(kx, y+pad))
	r.knob.Resize(fyne.NewSquareSize(style.SwitchKnob))
}

func (r *switchRenderer) Refresh() {
	s := r.s
	track := style.Color(style.ColorNameSwitchOff)
	if s.checked {
		on := s.OnColor
		if on == "" {
			on = "primary"
		}
		track = style.Color(on)
	}
	knob := style.Color(style.ColorNameWhite)
	if s.Disabled() {
		// 35 % opacity like the disabled toolbar items; the card is the background.
		card := style.Color(style.ColorNameCard)
		track = style.Over(track, style.DisabledOpacity, card)
		knob = style.Over(knob, style.DisabledOpacity, card)
	}
	r.track.FillColor = track
	r.track.Refresh()
	r.knob.FillColor = knob
	r.knob.Refresh()
	r.focus.StrokeColor = color.Transparent
	if s.focused && !s.Disabled() {
		r.focus.StrokeColor = style.Color("focus")
	}
	r.focus.Refresh()
	r.Layout(s.Size())
}

func (r *switchRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.focus, r.track, r.knob}
}

func (r *switchRenderer) Destroy() {}
