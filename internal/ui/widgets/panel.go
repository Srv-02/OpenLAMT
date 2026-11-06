package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// NewPanel returns content on a rounded rectangle with a 1 px border and the given padding
// (top, right, bottom, left): the cards and frames of the About and settings dialogs. fill and
// border name color tokens (style.ColorName...), so the panel follows the theme variant.
func NewPanel(fill, border fyne.ThemeColorName, radius float32, content fyne.CanvasObject, top, right, bottom, left float32) fyne.CanvasObject {
	bg := style.BindRect(&canvas.Rectangle{StrokeWidth: 1, CornerRadius: radius}, fill, border)
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(top, bottom, left, right), content))
}
