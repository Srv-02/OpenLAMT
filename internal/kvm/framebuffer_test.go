package kvm

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"sync"
	"testing"
)

func TestFramebufferSnapshotIsCopy(t *testing.T) {
	fb := newFramebuffer(4, 2)
	fb.fill(0, 0, 4, 2, color.RGBA{255, 0, 0, 255})
	s := fb.Snapshot()
	fb.fill(0, 0, 4, 2, color.RGBA{0, 0, 255, 255})
	if s.RGBAAt(0, 0) != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("snapshot changed: %v", s.RGBAAt(0, 0))
	}
	if w, h := fb.Size(); w != 4 || h != 2 {
		t.Fatalf("size %dx%d", w, h)
	}
}

func TestFramebufferDrawClipsAtEdge(t *testing.T) {
	fb := newFramebuffer(8, 8)
	tl := image.NewRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(tl, tl.Rect, image.NewUniform(color.RGBA{0, 255, 0, 255}), image.Point{}, draw.Src)
	fb.draw(6, 6, tl)
	fb.draw(-2, -2, tl)
	fb.draw(100, 100, tl)
	s := fb.Snapshot()
	if s.RGBAAt(7, 7) != (color.RGBA{0, 255, 0, 255}) || s.RGBAAt(5, 5) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("clip wrong")
	}
	if s.RGBAAt(1, 1) != (color.RGBA{0, 255, 0, 255}) || s.RGBAAt(2, 2) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("negative origin clip wrong")
	}
}

func TestFramebufferFillClips(t *testing.T) {
	fb := newFramebuffer(4, 4)
	fb.fill(2, 2, 10, 10, color.RGBA{9, 9, 9, 255})
	fb.fill(-5, -5, 1, 1, color.RGBA{1, 1, 1, 255})
	fb.fill(0, 0, -1, 3, color.RGBA{1, 1, 1, 255})
	s := fb.Snapshot()
	if s.RGBAAt(3, 3) != (color.RGBA{9, 9, 9, 255}) || s.RGBAAt(0, 0) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("fill clip wrong")
	}
}

func TestFramebufferResizeClears(t *testing.T) {
	fb := newFramebuffer(4, 4)
	fb.fill(0, 0, 4, 4, color.RGBA{255, 255, 255, 255})
	fb.resize(2, 2)
	if w, h := fb.Size(); w != 2 || h != 2 {
		t.Fatalf("size %dx%d", w, h)
	}
	if fb.Snapshot().RGBAAt(0, 0) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("not black")
	}
}

func TestFramebufferConcurrent(t *testing.T) {
	fb := newFramebuffer(16, 16)
	tl := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				fb.draw(j%16, j%16, tl)
				fb.fill(0, 0, 3, 3, color.RGBA{1, 2, 3, 255})
				if j%50 == 0 {
					fb.resize(16, 16)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				fb.Snapshot()
				fb.Size()
			}
		}()
	}
	wg.Wait()
}

func TestSnapshotIntoReusesBufferOfSameSize(t *testing.T) {
	f := newFramebuffer(8, 4)
	f.fill(1, 1, 2, 2, color.RGBA{10, 20, 30, 255})
	first := f.SnapshotInto(nil)
	if first.Rect != f.img.Rect || !bytes.Equal(first.Pix, f.Snapshot().Pix) {
		t.Fatal("first snapshot differs from Snapshot")
	}
	f.fill(0, 0, 8, 4, color.RGBA{1, 2, 3, 255})
	second := f.SnapshotInto(first)
	if second != first {
		t.Fatal("buffer of the same size was not reused")
	}
	if got := second.RGBAAt(5, 3); got != (color.RGBA{1, 2, 3, 255}) {
		t.Fatalf("stale pixel %v", got)
	}
	f.resize(4, 4)
	third := f.SnapshotInto(second)
	if third == second || third.Rect.Dx() != 4 {
		t.Fatal("buffer of another size must be replaced")
	}
	if f.SnapshotInto(third) != third {
		t.Fatal("replacement buffer not reused")
	}
}
