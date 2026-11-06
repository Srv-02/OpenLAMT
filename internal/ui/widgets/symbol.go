package widgets

import (
	"image"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Symbol shows one of the icons package's symbols with the design's stroke at any size and
// screen scale. Fyne draws SVG strokes in device pixels without scaling them, so a plain
// canvas.Image gets the stroke wrong (issue #60); the raster here is drawn by icons.Render for
// the pixel size Fyne asks for, which also follows a window moving to another monitor.
//
// Any other resource (a themed check mark) is shown as a canvas.Image.
type Symbol struct {
	widget.BaseWidget

	// Translucency makes the symbol see-through, from 0 (opaque) to 1; Refresh applies it.
	Translucency float64

	min fyne.Size

	mu   sync.Mutex
	res  fyne.Resource
	spec icons.Spec
	own  bool // res is a symbol of package icons
	last *image.RGBA
	// lastLight is the theme variant last was drawn for: a theme change drops the raster, so the
	// refresh after it draws the symbol in the new colors.
	lastLight bool
}

// NewSymbol returns a symbol for res (nil for none) with a minimum size of size x size.
func NewSymbol(res fyne.Resource, size float32) *Symbol {
	s := &Symbol{min: fyne.NewSquareSize(size)}
	s.ExtendBaseWidget(s)
	s.setResource(res)
	return s
}

// Resource returns the shown resource.
func (s *Symbol) Resource() fyne.Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.res
}

// SetResource changes the shown resource.
func (s *Symbol) SetResource(res fyne.Resource) {
	s.setResource(res)
	s.Refresh()
}

func (s *Symbol) setResource(res fyne.Resource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res = res
	s.spec, s.own = icons.Lookup(res)
	s.last = nil
}

func (s *Symbol) generate(w, h int) image.Image {
	s.mu.Lock()
	defer s.mu.Unlock()
	light := style.IsLight()
	if s.own && (s.last == nil || s.last.Bounds().Dx() != w || s.last.Bounds().Dy() != h || s.lastLight != light) {
		s.last = icons.Render(s.spec, w, h)
		s.lastLight = light
	}
	if !s.own || s.last == nil {
		return image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	}
	return s.last
}

func (s *Symbol) CreateRenderer() fyne.WidgetRenderer {
	r := &symbolRenderer{s: s, raster: canvas.NewRaster(s.generate),
		image: &canvas.Image{FillMode: canvas.ImageFillContain}}
	r.Refresh()
	return r
}

type symbolRenderer struct {
	s      *Symbol
	raster *canvas.Raster
	image  *canvas.Image
}

func (r *symbolRenderer) MinSize() fyne.Size { return r.s.min }

func (r *symbolRenderer) Layout(size fyne.Size) {
	r.raster.Move(fyne.NewPos(0, 0))
	r.raster.Resize(size)
	r.image.Move(fyne.NewPos(0, 0))
	r.image.Resize(size)
}

func (r *symbolRenderer) Refresh() {
	r.s.mu.Lock()
	res, own := r.s.res, r.s.own
	r.s.mu.Unlock()
	r.raster.Hidden = !own || res == nil
	r.image.Hidden = own || res == nil
	r.image.Resource = res
	r.raster.Translucency = r.s.Translucency
	r.image.Translucency = r.s.Translucency
	r.raster.Refresh()
	r.image.Refresh()
}

func (r *symbolRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.raster, r.image}
}
func (r *symbolRenderer) Destroy() {}

// setVisible shows or hides o without a refresh when nothing changes.
func setVisible(o fyne.CanvasObject, on bool) {
	if o.Visible() == on {
		return
	}
	if on {
		o.Show()
	} else {
		o.Hide()
	}
}
