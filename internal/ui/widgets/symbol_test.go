package widgets

import (
	"image"
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// capture draws o on black at the given screen scale.
func capture(o fyne.CanvasObject, size fyne.Size, scale float32) image.Image {
	c := software.NewCanvas()
	c.SetPadded(false)
	c.SetScale(scale)
	bg := canvas.NewRectangle(color.Black)
	c.SetContent(container.NewStack(bg, o))
	c.Resize(size)
	return c.Capture()
}

// barPixels measures the stroke of the "add" symbol's vertical bar in device pixels: the
// summed brightness of the rows between 7/24 and 10/24 of the symbol, which only the bar crosses.
func barPixels(img image.Image, side int) float64 {
	y0, y1 := side*7/24, side*10/24
	var sum float64
	for y := y0; y < y1; y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			sum += float64(r) / 0xffff
		}
	}
	return sum / float64(y1-y0)
}

// TestSymbolStrokeAtScales is the proof for #60: at screen scale 1 and 2 the stroke is the
// design value * size / 24 in layout units (device pixels / scale), within 10 %.
func TestSymbolStrokeAtScales(t *testing.T) {
	themedApp(t)
	cases := []struct {
		size   float32
		stroke float32
	}{{10, 2.4}, {12, 1.7}, {13, 1.7}, {15, 1.7}, {16, 1.7}, {16, 2}, {17, 1.7}, {18, 1.7}, {20, 1.7}, {32, 1.5}, {34, 1.4}, {22, 2}}
	for _, scale := range []float32{1, 2} {
		for _, c := range cases {
			s := NewSymbol(icons.GetStroke(icons.Add, icons.White, c.stroke), c.size)
			img := capture(container.NewCenter(s), fyne.NewSquareSize(c.size), scale)
			side := int(c.size * scale)
			want := float64(c.stroke) * float64(side) / 24
			got := barPixels(img, side)
			if math.Abs(got-want) > 0.1*want {
				t.Errorf("scale %g, %g px, stroke %g: %.2f device px, want %.2f +-10%%", scale, c.size, c.stroke, got, want)
			}
		}
	}
}

func TestSymbolFallsBackToImageForForeignResources(t *testing.T) {
	themedApp(t)
	s := NewSymbol(nil, 16)
	if s.MinSize() != fyne.NewSquareSize(16) {
		t.Errorf("MinSize %v", s.MinSize())
	}
	s.SetResource(theme.ConfirmIcon())
	r := test.WidgetRenderer(s).(*symbolRenderer)
	if !r.raster.Hidden || r.image.Hidden || r.image.Resource != theme.ConfirmIcon() {
		t.Error("foreign resource is not shown as an image")
	}
	s.SetResource(icons.Get(icons.Lock, icons.Secondary))
	if r.raster.Hidden || !r.image.Hidden || s.Resource() != icons.Get(icons.Lock, icons.Secondary) {
		t.Error("symbol is not shown as a raster")
	}
	s.SetResource(nil)
	if !r.raster.Hidden || !r.image.Hidden {
		t.Error("nil resource shows something")
	}
}

// TestSymbolRedrawsAfterThemeVariantChange: the raster is dropped when the theme variant changes,
// so the refresh after a theme switch paints the other stroke color.
func TestSymbolRedrawsAfterThemeVariantChange(t *testing.T) {
	themedApp(t)
	t.Cleanup(func() { style.SetVariant(theme.VariantDark) })
	style.SetVariant(theme.VariantDark)
	s := NewSymbol(icons.Get(icons.Heart, icons.HeartPink), 24)
	test.WidgetRenderer(s)
	dark := s.generate(24, 24).(*image.RGBA)
	if got := dark.RGBAAt(12, 12); got.R != 0xFF || got.G != 0x64 || got.B != 0x82 {
		t.Fatalf("dark center pixel %v", got)
	}
	if s.generate(24, 24) != image.Image(dark) {
		t.Error("same size and variant redrew")
	}
	style.SetVariant(theme.VariantLight)
	s.Refresh()
	light := s.generate(24, 24).(*image.RGBA)
	if got := light.RGBAAt(12, 12); got.R != 0xFF || got.G != 0x2D || got.B != 0x55 {
		t.Errorf("light center pixel %v", got)
	}
}
