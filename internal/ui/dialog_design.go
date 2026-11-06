package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The About and settings dialogs follow HANDOFF.md 5.4 and 5.5: a framed panel of fixed size
// without Fyne's title row, so they are a modal pop-up with their own layout (dialog.NewCustom
// always adds a title bar) and the Esc handling of showPowerConfirm.

// dialogSpec is the size and spacing of a design dialog.
type dialogSpec struct {
	width, height                     float32
	padTop, padRight, padBottom, padL float32
	gap                               float32
	flex                              int // index of the body object that takes the free height
}

// designDialog is a shown design dialog.
type designDialog struct {
	pop     *widget.PopUp
	canvas  fyne.Canvas
	prevKey func(*fyne.KeyEvent)
	closed  bool
}

// Close hides the dialog; Esc and the dialog's own buttons call it. It is safe to call twice.
func (d *designDialog) Close() {
	if d.closed {
		return
	}
	d.closed = true
	d.canvas.SetOnTypedKey(d.prevKey)
	d.pop.Hide()
}

// showDesignDialog shows body (stacked top to bottom, full width) in a framed panel on parent.
// Esc closes it while no control has the focus; controls that take the focus call the returned
// dialog's Close from their OnEscape. build gets the dialog before it is shown, to wire the
// buttons.
func showDesignDialog(parent fyne.Window, spec dialogSpec, build func(d *designDialog) []fyne.CanvasObject) *designDialog {
	c := parent.Canvas()
	d := &designDialog{canvas: c, prevKey: c.OnTypedKey()}
	body := build(d)
	content := container.New(&stackLayout{spec: spec}, body...)
	frame := style.BindRect(&canvas.Rectangle{StrokeWidth: 1, CornerRadius: style.DialogRadius}, "overlayBackground", style.ColorNameDialogBorder)
	framed := container.New(&fixedSizeLayout{size: fyne.NewSize(spec.width, spec.height)}, frame, content)
	d.pop = widget.NewModalPopUp(container.NewThemeOverride(framed, overrideTheme), c)
	c.SetOnTypedKey(func(e *fyne.KeyEvent) {
		if e.Name == fyne.KeyEscape {
			d.Close()
		} else if d.prevKey != nil {
			d.prevKey(e)
		}
	})
	d.pop.Show()
	return d
}

// scrollBody stacks objs like a dialog body (spec.flex takes the free height) in a vertical
// scroll area, which is the flex object of the dialog around it: the buttons below it stay
// visible whatever the window size. The area may shrink to minScrollHeight.
//
// The body keeps scrollInset free at the left and right: borders of cards are drawn centered on
// the edge, and half of one on the edge of the clip would be cut off. The dialog around it
// gives up the same scrollInset of its own padding, so the content does not move.
func scrollBody(spec dialogSpec, objs ...fyne.CanvasObject) *container.Scroll {
	spec.padL, spec.padRight = scrollInset, scrollInset
	s := container.NewVScroll(container.New(&stackLayout{spec: spec}, objs...))
	s.SetMinSize(fyne.NewSize(0, minScrollHeight))
	return s
}

const (
	minScrollHeight float32 = 40
	scrollInset     float32 = 2
)

// fixedSizeLayout gives all objects the full size, which is also its minimum.
type fixedSizeLayout struct{ size fyne.Size }

func (l *fixedSizeLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}
func (l *fixedSizeLayout) MinSize([]fyne.CanvasObject) fyne.Size { return l.size }

// stackLayout stacks its objects top to bottom at their minimum height with spec.gap between,
// inside the padding; the object spec.flex gets the height that is left.
type stackLayout struct{ spec dialogSpec }

func (l *stackLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	s := l.spec
	w := size.Width - s.padL - s.padRight
	free := size.Height - s.padTop - s.padBottom - s.gap*float32(max(0, len(objs)-1))
	for i, o := range objs {
		if i != s.flex {
			free -= o.MinSize().Height
		}
	}
	y := s.padTop
	for i, o := range objs {
		h := o.MinSize().Height
		if i == s.flex {
			h = max(h, free)
		}
		o.Move(fyne.NewPos(s.padL, y))
		o.Resize(fyne.NewSize(w, h))
		y += h + s.gap
	}
}

func (l *stackLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	s := l.spec
	var w, h float32
	for _, o := range objs {
		w = max(w, o.MinSize().Width)
		h += o.MinSize().Height
	}
	h += s.gap * float32(max(0, len(objs)-1))
	return fyne.NewSize(w+s.padL+s.padRight, h+s.padTop+s.padBottom)
}

// rowLayout puts objects left to right, centered vertically, gap apart; the object with index
// fill (-1 for none) takes the width that is left.
type rowLayout struct {
	gap  float32
	fill int
}

func (l *rowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var used float32
	for i, o := range objs {
		if i != l.fill {
			used += o.MinSize().Width
		}
	}
	rest := size.Width - used - l.gap*float32(max(0, len(objs)-1))
	x := float32(0)
	for i, o := range objs {
		ms := o.MinSize()
		w := ms.Width
		if i == l.fill {
			w = max(0, rest)
		}
		o.Move(fyne.NewPos(x, (size.Height-ms.Height)/2))
		o.Resize(fyne.NewSize(w, ms.Height))
		x += w + l.gap
	}
}

func (l *rowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		w += o.MinSize().Width
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(w+l.gap*float32(max(0, len(objs)-1)), h)
}

// baselineLayout puts canvas.Text objects left to right on one baseline (align-items:
// baseline): Inter's descent is 0.24 of the text size, so a smaller text sits lower.
type baselineLayout struct{ gap float32 }

func descent(o fyne.CanvasObject) float32 {
	if t, ok := o.(*canvas.Text); ok {
		return 0.24 * t.TextSize
	}
	return 0
}

func (l *baselineLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var maxDescent float32
	for _, o := range objs {
		maxDescent = max(maxDescent, descent(o))
	}
	x := float32(0)
	for _, o := range objs {
		ms := o.MinSize()
		o.Move(fyne.NewPos(x, size.Height-ms.Height-maxDescent+descent(o)))
		o.Resize(ms)
		x += ms.Width + l.gap
	}
}

func (l *baselineLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h, maxDescent float32
	for _, o := range objs {
		maxDescent = max(maxDescent, descent(o))
	}
	for _, o := range objs {
		w += o.MinSize().Width
		h = max(h, o.MinSize().Height+maxDescent-descent(o))
	}
	return fyne.NewSize(w+l.gap*float32(max(0, len(objs)-1)), h)
}

// designText is a single-line text in a design size and the color token c; bold uses the SemiBold face.
func designText(s string, size float32, c fyne.ThemeColorName, bold bool) *canvas.Text {
	t := style.BindText(canvas.NewText(s, nil), c)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Bold: bold}
	return t
}

// dialogButton is a 32 px dialog button (Cancel, Save, Close): radius 8, 13 px semibold.
// High is the accent button, otherwise white 12 %.
func dialogButton(text string, high bool, padH float32, tapped func()) *widgets.PrimaryButton {
	b := widgets.NewPrimaryButton(text, nil, tapped)
	b.Metrics = &widgets.ButtonMetrics{Height: 32, Radius: 8, PaddingH: padH, TextSize: 13}
	if !high {
		b.Importance = widget.MediumImportance
	}
	return b
}

// spacer is an empty object that only takes room, like CSS flex: 1.
func spacer() fyne.CanvasObject {
	return canvas.NewRectangle(color.Transparent)
}
