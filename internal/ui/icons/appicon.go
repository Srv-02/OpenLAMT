package icons

import (
	"embed"
	"fmt"
	"image"
	"strings"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

// AppVariant selects the artwork of the app icon.
type AppVariant string

const (
	// AppFull is the icon with pins and LED glow (the About dialog, 84 px).
	AppFull AppVariant = "full"
	// AppSimple is the simplified icon for small sizes: no pins, thicker strokes, a plain LED
	// (the footer of the settings dialog, 36 px; Settings.html).
	AppSimple AppVariant = "simple"
)

//go:embed app/*.svg
var appFiles embed.FS

// RenderApp draws the app icon into a side x side image. As in Render, the stroke widths of
// the 1024 unit artwork are scaled to the pixel size. It returns nil for an empty size.
func RenderApp(v AppVariant, side int) *image.RGBA {
	if side <= 0 {
		return nil
	}
	src, err := appFiles.ReadFile("app/" + string(v) + ".svg")
	if err != nil {
		panic(fmt.Sprintf("icons: unknown app icon %q", v))
	}
	icon, err := oksvg.ReadIconStream(strings.NewReader(string(src)), oksvg.StrictErrorMode)
	if err != nil {
		panic(fmt.Sprintf("icons: app icon %q: %v", v, err))
	}
	for i := range icon.SVGPaths {
		icon.SVGPaths[i].LineWidth *= float64(side) / 1024
	}
	icon.SetTarget(0, 0, float64(side), float64(side))
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	scanner := rasterx.NewScannerGV(side, side, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(side, side, scanner), 1)
	return img
}
