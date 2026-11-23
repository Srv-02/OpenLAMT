package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of the progress bar of a dialog (overall design, update dialog .prog): 6 px high,
// radius 3.
const (
	progressBarHeight float32 = 6
	progressBarRadius float32 = 3
)

// ProgressBar is a thin bar in the accent color on a grey track, without text: the dialog says
// the numbers in a line of its own.
type ProgressBar struct {
	widget.BaseWidget
	value float32
}

// NewProgressBar returns an empty bar.
func NewProgressBar() *ProgressBar {
	b := &ProgressBar{}
	b.ExtendBaseWidget(b)
	return b
}

// Value is the share filled, 0 to 1.
func (b *ProgressBar) Value() float32 { return b.value }

// SetValue fills the bar to v (clamped to 0 to 1).
func (b *ProgressBar) SetValue(v float32) {
	v = min(max(v, 0), 1)
	if b.value != v {
		b.value = v
		b.Refresh()
	}
}

func (b *ProgressBar) CreateRenderer() fyne.WidgetRenderer {
	r := &progressBarRenderer{b: b,
		track: &canvas.Rectangle{CornerRadius: progressBarRadius},
		fill:  &canvas.Rectangle{CornerRadius: progressBarRadius},
	}
	r.Refresh()
	return r
}

type progressBarRenderer struct {
	b           *ProgressBar
	track, fill *canvas.Rectangle
}

func (r *progressBarRenderer) MinSize() fyne.Size { return fyne.NewSize(0, progressBarHeight) }

func (r *progressBarRenderer) Layout(size fyne.Size) {
	y := (size.Height - progressBarHeight) / 2
	r.track.Move(fyne.NewPos(0, y))
	r.track.Resize(fyne.NewSize(size.Width, progressBarHeight))
	r.fill.Move(fyne.NewPos(0, y))
	r.fill.Resize(fyne.NewSize(size.Width*r.b.value, progressBarHeight))
}

func (r *progressBarRenderer) Refresh() {
	r.track.FillColor = style.Color(style.ColorNameGroupDivider)
	r.fill.FillColor = style.Color(theme.ColorNamePrimary)
	r.Layout(r.b.Size())
	r.track.Refresh()
	r.fill.Refresh()
}

func (r *progressBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill}
}

func (r *progressBarRenderer) Destroy() {}
