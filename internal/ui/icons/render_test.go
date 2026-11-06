package icons

import (
	"image"
	"math"
	"testing"
)

// barWidth measures the stroke of the vertical bar of the "add" symbol: the summed alpha of
// the pixel rows between 7/24 and 10/24 of the height, which only the bar crosses.
func barWidth(img *image.RGBA, side int) float64 {
	y0, y1 := side*7/24, side*10/24
	var sum float64
	for y := y0; y < y1; y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			sum += float64(img.RGBAAt(x, y).A) / 255
		}
	}
	return sum / float64(y1-y0)
}

// TestRenderScalesStroke is the proof for #60: the stroke is the design value times size / 24
// in pixels, whatever the pixel size (Fyne itself would draw 1.7 pixels at every size).
func TestRenderScalesStroke(t *testing.T) {
	for _, stroke := range []float32{1.4, 1.7, 2, 2.4} {
		for _, side := range []int{10, 13, 16, 20, 26, 32, 34, 40, 64, 68} {
			want := float64(stroke) * float64(side) / 24
			got := barWidth(Render(Spec{Add, White, stroke}, side, side), side)
			if math.Abs(got-want) > 0.1*want {
				t.Errorf("stroke %g at %d px: %.2f px, want %.2f +-10%%", stroke, side, got, want)
			}
		}
	}
}

func TestRenderCentersInNonSquareImage(t *testing.T) {
	img := Render(Spec{Add, White, DefaultStroke}, 40, 24)
	if img.Bounds().Dx() != 40 || img.Bounds().Dy() != 24 {
		t.Fatalf("bounds %v", img.Bounds())
	}
	if img.RGBAAt(2, 12).A != 0 || img.RGBAAt(37, 12).A != 0 {
		t.Error("symbol leaves the centered 24 px square")
	}
	if Render(Spec{Add, White, DefaultStroke}, 0, 10) != nil {
		t.Error("empty size gives an image")
	}
}

func TestLookup(t *testing.T) {
	r := GetStroke(Server, Accent, 1.5)
	s, ok := Lookup(r)
	if !ok || s != (Spec{Server, Accent, 1.5}) {
		t.Fatalf("Lookup = %v %v", s, ok)
	}
	if Get(Lock, Secondary) != Get(Lock, Secondary) {
		t.Error("resources are not cached")
	}
	if _, ok := Lookup(nil); ok {
		t.Error("nil is known")
	}
}

func TestRenderAppIcon(t *testing.T) {
	for _, v := range []AppVariant{AppFull, AppSimple} {
		img := RenderApp(v, 84)
		if img == nil || img.Bounds().Dx() != 84 {
			t.Fatalf("%s: %v", v, img)
		}
		if img.RGBAAt(20, 42).A != 255 || img.RGBAAt(1, 1).A != 0 {
			t.Errorf("%s: plate %v, corner %v", v, img.RGBAAt(20, 42), img.RGBAAt(1, 1))
		}
		// The green LED sits at (661, 363)/1024 in the full and (666, 358)/1024 in the simple artwork.
		led := img.RGBAAt(84*663/1024, 84*361/1024)
		if led.G < 150 || led.R > 100 {
			t.Errorf("%s: LED pixel %v is not green", v, led)
		}
	}
	if RenderApp(AppFull, 0) != nil {
		t.Error("empty size gives an image")
	}
}
