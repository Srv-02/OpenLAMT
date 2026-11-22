package mcpserver

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// TestFitSize: the longest side fits the limits, the aspect stays, nothing is upscaled.
func TestFitSize(t *testing.T) {
	for _, c := range []struct{ w, h, maxW, maxH, wantW, wantH int }{
		{1920, 1080, 1280, 1280, 1280, 720},
		{1024, 768, 1280, 1280, 1024, 768},
		{1280, 1024, 1280, 1280, 1280, 1024},
		{2560, 1600, 1280, 1280, 1280, 800},
		{1080, 1920, 1280, 1280, 720, 1280},
		{1920, 1080, 800, 300, 533, 300},
		{640, 480, 100, 100, 100, 75},
		{3, 1000, 16, 16, 1, 16},
	} {
		if w, h := fitSize(c.w, c.h, c.maxW, c.maxH); w != c.wantW || h != c.wantH {
			t.Errorf("%dx%d in %dx%d: %dx%d, want %dx%d", c.w, c.h, c.maxW, c.maxH, w, h, c.wantW, c.wantH)
		}
	}
}

// TestEncodeScreenshotJPEGQuality: a higher quality gives a larger file of the fitted size.
func TestEncodeScreenshotJPEGQuality(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 7), uint8(y * 13), uint8(x ^ y), 255})
		}
	}
	low, w, h, mime, err := encodeScreenshot(img, ScreenshotConfig{MaxWidth: 1280, MaxHeight: 1280, Format: "jpeg", JPEGQuality: 30})
	if err != nil || w != 1280 || h != 720 || mime != "image/jpeg" {
		t.Fatalf("%v %dx%d %s", err, w, h, mime)
	}
	high, _, _, _, err := encodeScreenshot(img, ScreenshotConfig{MaxWidth: 1280, MaxHeight: 1280, Format: "jpeg", JPEGQuality: 95})
	if err != nil || len(high) <= len(low) {
		t.Fatalf("%v sizes %d %d", err, len(low), len(high))
	}
	dec, err := jpeg.Decode(bytes.NewReader(high))
	if err != nil || dec.Bounds().Dx() != 1280 || dec.Bounds().Dy() != 720 {
		t.Fatalf("%v %v", err, dec.Bounds())
	}
}

// TestScreenPoint: the centre of a screenshot pixel, clamped to the screen.
func TestScreenPoint(t *testing.T) {
	for _, c := range []struct{ x, y, imgW, imgH, scrW, scrH, wantX, wantY int }{
		{0, 0, 1280, 720, 1920, 1080, 0, 0},
		{640, 360, 1280, 720, 1920, 1080, 960, 540},
		{1279, 719, 1280, 720, 1920, 1080, 1919, 1079},
		{5000, 5000, 1280, 720, 1920, 1080, 1919, 1079},
		{10, 20, 0, 0, 1920, 1080, 10, 20}, // no screenshot yet: screen pixels
		{50, 25, 100, 50, 200, 100, 101, 51},
	} {
		if x, y := screenPoint(c.x, c.y, c.imgW, c.imgH, c.scrW, c.scrH); x != c.wantX || y != c.wantY {
			t.Errorf("%+v: %d,%d", c, x, y)
		}
	}
}
