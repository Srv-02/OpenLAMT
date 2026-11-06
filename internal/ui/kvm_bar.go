package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of the full screen bar: toolbar items on the toolbar color, rounded at the bottom
// (the top edge touches the screen edge), with a hairline border like the menus.
const (
	barPadV   float32 = 2
	barPadH   float32 = 6
	barGap    float32 = 6
	barRadius float32 = 10
)

// dragBar is the narrow tool bar shown over the KVM image in full screen, in the style of the
// window toolbar. It sits on the top edge of its layer, centred until it is dragged
// horizontally, and is clamped to the layer's width. The layer (barLayout) places it, because SetFullScreen resizes the window
// asynchronously (window_desktop.go:126-135) and only the layout sees the final width.
type dragBar struct {
	widget.BaseWidget
	content fyne.CanvasObject
	width   float32 // available width, set by barLayout
	x       float32 // left edge once dragged
	moved   bool
}

var _ fyne.Draggable = (*dragBar)(nil)

func newDragBar(items ...fyne.CanvasObject) *dragBar {
	row := container.New(layout.NewCustomPaddedHBoxLayout(barGap), items...)
	b := &dragBar{content: container.New(layout.NewCustomPaddedLayout(barPadV, barPadV, barPadH, barPadH), row)}
	b.ExtendBaseWidget(b)
	return b
}

func (b *dragBar) CreateRenderer() fyne.WidgetRenderer {
	bg := style.BindRect(&canvas.Rectangle{StrokeWidth: 1, CornerRadius: barRadius}, style.ColorNameToolbar, style.ColorNameGroupDivider)
	// The rectangle reaches above the top edge, so only the lower corners show as rounded.
	top := container.New(layout.NewCustomPaddedLayout(-barRadius, 0, 0, 0), bg)
	return widget.NewSimpleRenderer(container.NewStack(top, b.content))
}

// layer returns the container that holds the bar at the top edge.
func (b *dragBar) layer() *fyne.Container { return container.New(barLayout{b}, b) }

// recentre forgets the dragged position; the next place centres the bar again.
func (b *dragBar) recentre() { b.moved = false; b.place() }

// place sizes the bar to its minimum and puts it on the top edge, centred or at the dragged
// position, clamped to the available width.
func (b *dragBar) place() {
	b.Resize(b.MinSize())
	if !b.moved {
		b.x = (b.width - b.Size().Width) / 2
	}
	b.x = max(0, min(b.x, b.width-b.Size().Width))
	b.Move(fyne.NewPos(b.x, 0))
}

func (b *dragBar) Dragged(e *fyne.DragEvent) {
	b.moved = true
	b.x = b.Position().X + e.Dragged.DX
	b.place()
}

func (b *dragBar) DragEnd() {}

// barLayout is the layout of the bar's layer: it records the available width and places the bar.
type barLayout struct{ bar *dragBar }

func (l barLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	l.bar.width = size.Width
	l.bar.place()
}

func (l barLayout) MinSize([]fyne.CanvasObject) fyne.Size { return l.bar.MinSize() }
