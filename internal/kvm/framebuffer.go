package kvm

import (
	"image"
	"image/color"
	"image/draw"
	"sync"
)

// Framebuffer is the remote screen; reads and writes may come from different goroutines.
type Framebuffer struct {
	mu  sync.RWMutex
	img *image.RGBA
}

func blackImage(w, h int) *image.RGBA {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
	return img
}

func newFramebuffer(w, h int) *Framebuffer {
	return &Framebuffer{img: blackImage(w, h)}
}

// Size returns the current screen size.
func (f *Framebuffer) Size() (w, h int) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	b := f.img.Bounds()
	return b.Dx(), b.Dy()
}

// Snapshot returns a deep copy of the screen.
func (f *Framebuffer) Snapshot() *image.RGBA {
	f.mu.RLock()
	defer f.mu.RUnlock()
	cp := image.NewRGBA(f.img.Rect)
	copy(cp.Pix, f.img.Pix)
	return cp
}

// SnapshotInto copies the screen into dst and returns it; dst is reused when it has the same
// bounds, otherwise (or when nil) a new image is allocated. Reusing the buffer avoids one
// allocation of width*height*4 bytes per frame.
func (f *Framebuffer) SnapshotInto(dst *image.RGBA) *image.RGBA {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if dst == nil || dst.Rect != f.img.Rect || dst.Stride != f.img.Stride || len(dst.Pix) != len(f.img.Pix) {
		dst = image.NewRGBA(f.img.Rect)
	}
	copy(dst.Pix, f.img.Pix)
	return dst
}

// resize replaces the screen with a fresh black image.
func (f *Framebuffer) resize(w, h int) {
	img := blackImage(w, h)
	f.mu.Lock()
	f.img = img
	f.mu.Unlock()
}

// draw copies tile to (x, y), clipped to the screen.
func (f *Framebuffer) draw(x, y int, tile *image.RGBA) {
	if tile == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r := image.Rectangle{Min: image.Pt(x, y), Max: image.Pt(x, y).Add(tile.Bounds().Size())}
	draw.Draw(f.img, r, tile, tile.Bounds().Min, draw.Src)
}

// fill paints a rectangle, clipped to the screen.
func (f *Framebuffer) fill(x, y, w, h int, c color.RGBA) {
	if w <= 0 || h <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	draw.Draw(f.img, image.Rect(x, y, x+w, y+h), image.NewUniform(c), image.Point{}, draw.Src)
}
