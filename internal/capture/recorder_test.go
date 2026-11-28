package capture

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeClock drives a Recorder: tick advances the time and delivers one tick, then waits until
// the recorder has handled it.
type fakeClock struct {
	t       time.Time
	ticks   chan time.Time
	stepped chan struct{}
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC), ticks: make(chan time.Time), stepped: make(chan struct{})}
}

func (c *fakeClock) options(fps int, limit int64) RecorderOptions {
	return RecorderOptions{FPS: fps, Limit: limit, now: func() time.Time { return c.t }, ticker: func(time.Duration) (<-chan time.Time, func()) {
		return c.ticks, func() {}
	}, stepped: c.stepped}
}

// tick moves the clock by d, hands the recorder one tick and waits until it has handled it.
// The clock is only read by the recorder while it handles a tick or stops.
func (c *fakeClock) tick(_ *Recorder, d time.Duration) {
	c.t = c.t.Add(d)
	c.ticks <- c.t
	<-c.stepped
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

// moviFrames parses the AVI at path and returns its movi chunks.
func moviFrames(t *testing.T, path string) ([]riffChunk, uint32) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	top := parseRIFF(t, b, 0, len(b))
	hdrl := find(t, top[0].children, "LIST", "hdrl")
	avih := find(t, hdrl.children, "avih", "")
	return find(t, top[0].children, "LIST", "movi").children, binary.LittleEndian.Uint32(avih.data[16:])
}

func decodeAt(t *testing.T, data []byte, x, y int) color.RGBA {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
}

// TestRecorderKeepsTheTimeline: the first frame is written at the start, every frame slot after
// it gets the newest offered frame or a repeat, and slots missed by a slow tick are filled with
// repeats, so the video runs in real time.
func TestRecorderKeepsTheTimeline(t *testing.T) {
	clk := newFakeClock()
	path := filepath.Join(t.TempDir(), "rec.avi")
	r, err := StartRecorder(create(t, path), solid(32, 16, color.RGBA{R: 255}), clk.options(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	clk.tick(r, 100*time.Millisecond) // nothing new: repeat
	r.Offer(solid(32, 16, color.RGBA{B: 255}))
	clk.tick(r, 300*time.Millisecond) // two missed slots, then the blue frame
	if el, size := r.Stats(); el != 400*time.Millisecond || size <= 0 {
		t.Fatalf("stats %v %d", el, size)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(); err != nil { // a second Stop is harmless
		t.Fatal(err)
	}
	frames, total := moviFrames(t, path)
	if len(frames) != 5 || total != 5 {
		t.Fatalf("%d chunks, header says %d; want 5", len(frames), total)
	}
	sizes := []bool{true, false, false, false, true} // real frame or repeat
	for i, real := range sizes {
		if (len(frames[i].data) > 0) != real {
			t.Fatalf("chunk %d: %d bytes", i, len(frames[i].data))
		}
	}
	if c := decodeAt(t, frames[0].data, 5, 5); c.R < 200 || c.B > 50 {
		t.Fatalf("first frame %v", c)
	}
	if c := decodeAt(t, frames[4].data, 5, 5); c.B < 200 || c.R > 50 {
		t.Fatalf("last frame %v", c)
	}
	if r.Frames() != len(frames) {
		t.Fatalf("Frames() %d, file has %d", r.Frames(), len(frames))
	}
	select {
	case <-r.Done():
	default:
		t.Fatal("Done not closed after Stop")
	}
}

// TestRecorderFitsNewResolution: a frame of another size (the remote switched its resolution) is
// scaled into the size of the recording, letterboxed on black.
func TestRecorderFitsNewResolution(t *testing.T) {
	clk := newFakeClock()
	path := filepath.Join(t.TempDir(), "rec.avi")
	r, err := StartRecorder(create(t, path), solid(64, 32, color.RGBA{R: 255}), clk.options(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	r.Offer(solid(32, 32, color.RGBA{G: 255})) // square: pillarboxed into 64x32
	clk.tick(r, 100*time.Millisecond)
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	frames, _ := moviFrames(t, path)
	img, err := jpeg.Decode(bytes.NewReader(frames[1].data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 32 {
		t.Fatalf("frame size %v", img.Bounds())
	}
	if c := decodeAt(t, frames[1].data, 32, 16); c.G < 200 {
		t.Fatalf("centre %v", c)
	}
	if c := decodeAt(t, frames[1].data, 2, 16); c.G > 40 || c.R > 40 {
		t.Fatalf("border %v, want black", c)
	}
}

// TestRecorderStopsAtTheLimit: a recording that would grow past the limit stops by itself with
// ErrLimit and leaves a valid file.
func TestRecorderStopsAtTheLimit(t *testing.T) {
	clk := newFakeClock()
	path := filepath.Join(t.TempDir(), "rec.avi")
	first := solid(32, 16, color.RGBA{R: 255})
	r, err := StartRecorder(create(t, path), first, clk.options(10, 4096))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		select {
		case <-r.Done():
		default:
			r.Offer(solid(32, 16, color.RGBA{G: uint8(i * 40)}))
			clk.tick(r, 100*time.Millisecond)
			if i > 100 {
				t.Fatal("limit not reached")
			}
			continue
		}
		break
	}
	if err := r.Stop(); !errors.Is(err, ErrLimit) {
		t.Fatalf("Stop: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() > 4096 {
		t.Fatalf("file %v: %d bytes", err, fi.Size())
	}
	frames, total := moviFrames(t, path)
	if int(total) != len(frames) || len(frames) < 1 {
		t.Fatalf("%d frames, header %d", len(frames), total)
	}
}

// create opens path for a recording as the save dialog path does.
func create(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := CreateChosen(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestRecorderRemovesFileOnStartError: a recording that cannot start (empty frame) closes and
// removes its file.
func TestRecorderRemovesFileOnStartError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.avi")
	if _, err := StartRecorder(create(t, path), image.NewRGBA(image.Rect(0, 0, 0, 0)), RecorderOptions{}); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file left: %v", err)
	}
}
