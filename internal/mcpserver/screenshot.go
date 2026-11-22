package mcpserver

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

// fitSize returns the size of a w x h image scaled to fit into maxW x maxH with its aspect
// kept. It never upscales and never returns a side below 1.
func fitSize(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	scale := min(1, float64(maxW)/float64(w), float64(maxH)/float64(h))
	if scale >= 1 {
		return w, h
	}
	return max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
}

// encodeScreenshot scales img to fit sc (Catmull-Rom, which keeps small text readable) and
// encodes it as PNG or JPEG. It returns the bytes, the image size and the MIME type.
func encodeScreenshot(img *image.RGBA, sc ScreenshotConfig) ([]byte, int, int, string, error) {
	b := img.Bounds()
	w, h := fitSize(b.Dx(), b.Dy(), sc.MaxWidth, sc.MaxHeight)
	var out image.Image = img
	if w != b.Dx() || h != b.Dy() {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		out = dst
	}
	var buf bytes.Buffer
	if sc.Format == "png" {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, out); err != nil {
			return nil, 0, 0, "", err
		}
		return buf.Bytes(), w, h, "image/png", nil
	}
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: sc.JPEGQuality}); err != nil {
		return nil, 0, 0, "", err
	}
	return buf.Bytes(), w, h, "image/jpeg", nil
}

// screenPoint maps (x, y) in an imgW x imgH screenshot to the centre of the matching area of a
// scrW x scrH screen, clamped to the screen. Without a screenshot (imgW or imgH 0) x and y are
// screen pixels already.
func screenPoint(x, y, imgW, imgH, scrW, scrH int) (int, int) {
	if imgW > 0 && imgH > 0 {
		x = (2*x + 1) * scrW / (2 * imgW)
		y = (2*y + 1) * scrH / (2 * imgH)
	}
	return max(0, min(x, scrW-1)), max(0, min(y, scrH-1))
}
