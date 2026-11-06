package widgets

import (
	"image"
	"image/draw"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// AppIcon shows the OpenLAMT app icon at any size and screen scale, drawn from the vector
// artwork for the pixel size Fyne asks for (the full icon in the About dialog, the simplified
// one in the settings footer).
type AppIcon struct {
	widget.BaseWidget

	Variant icons.AppVariant
	size    float32

	mu   sync.Mutex
	last *image.RGBA
}

// NewAppIcon returns the icon at size x size.
func NewAppIcon(v icons.AppVariant, size float32) *AppIcon {
	a := &AppIcon{Variant: v, size: size}
	a.ExtendBaseWidget(a)
	return a
}

func (a *AppIcon) generate(w, h int) image.Image {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last != nil && a.last.Bounds().Dx() == w && a.last.Bounds().Dy() == h {
		return a.last
	}
	side := min(w, h)
	img := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	if src := icons.RenderApp(a.Variant, side); src != nil {
		off := image.Pt((w-side)/2, (h-side)/2)
		draw.Draw(img, src.Bounds().Add(off), src, image.Point{}, draw.Src)
	}
	a.last = img
	return img
}

func (a *AppIcon) CreateRenderer() fyne.WidgetRenderer {
	return &appIconRenderer{a: a, raster: canvas.NewRaster(a.generate)}
}

type appIconRenderer struct {
	a      *AppIcon
	raster *canvas.Raster
}

func (r *appIconRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(r.a.size) }
func (r *appIconRenderer) Layout(size fyne.Size) {
	r.raster.Move(fyne.NewPos(0, 0))
	r.raster.Resize(size)
}
func (r *appIconRenderer) Refresh() {
	r.a.mu.Lock()
	r.a.last = nil
	r.a.mu.Unlock()
	r.raster.Refresh()
}
func (r *appIconRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.raster} }
func (r *appIconRenderer) Destroy()                     {}
