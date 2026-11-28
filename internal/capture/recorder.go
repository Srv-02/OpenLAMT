package capture

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"sync"
	"time"
)

// Defaults of a recording: 10 frame slots per second (a KVM session rarely delivers more), JPEG
// quality 75, and a stop shortly below 2 GB, the size every AVI 1.0 reader accepts (the RIFF
// and index fields are 32 bit, many readers treat them as signed).
const (
	DefaultFPS     = 10
	DefaultQuality = 75
	DefaultLimit   = 2_000_000_000
)

// ErrLimit: the recording reached its size limit and stopped by itself; the file is complete.
var ErrLimit = errors.New("capture: recording reached the size limit")

// RecorderOptions tune a Recorder; zero values take the defaults.
type RecorderOptions struct {
	FPS     int
	Quality int
	Limit   int64 // bytes of the finished file

	// Tests replace the clock and the ticker and learn when a tick was handled.
	now     func() time.Time
	ticker  func(time.Duration) (<-chan time.Time, func())
	stepped chan struct{}
}

// Recorder writes the remote screen into an MJPEG AVI in real time. Offer hands it the newest
// frame (from the frame loop of the view); every frame slot gets the newest frame or, if none
// came, a repeat, so the video keeps the pace of the session also while the screen does not
// change or a reconnect runs. A frame of another size is fitted into the size of the first.
type Recorder struct {
	path   string
	file   *os.File
	avi    *AVIWriter
	opts   RecorderOptions
	slot   time.Duration
	start  time.Time
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	jpeg   bytes.Buffer
	width  int
	height int

	mu      sync.Mutex
	pending *image.RGBA // newest offered frame, at the recording size
	work    *image.RGBA // the frame being encoded; swapped with pending
	dirty   bool        // pending holds a frame that was not written yet
	elapsed time.Duration
	size    int64
	frames  int
	err     error
}

// StartRecorder records into f (from CreateUnique or CreateChosen), which it closes at the end:
// it writes first as the first frame and starts the clock. On failure f is closed and removed.
func StartRecorder(f *os.File, first *image.RGBA, opts RecorderOptions) (*Recorder, error) {
	if opts.FPS <= 0 {
		opts.FPS = DefaultFPS
	}
	if opts.Quality <= 0 {
		opts.Quality = DefaultQuality
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.ticker == nil {
		opts.ticker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	path := f.Name()
	b := first.Bounds()
	if b.Empty() {
		f.Close()
		os.Remove(path)
		return nil, errors.New("capture: empty frame")
	}
	a, err := NewAVIWriter(f, b.Dx(), b.Dy(), opts.FPS)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	r := &Recorder{path: path, file: f, avi: a, opts: opts, slot: time.Second / time.Duration(opts.FPS),
		stop: make(chan struct{}), done: make(chan struct{}), width: b.Dx(), height: b.Dy(),
		pending: image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy())), work: image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))}
	fitInto(r.work, first)
	if err := r.encode(r.work); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	r.size, r.frames = a.Size(), a.Frames()
	r.start = opts.now()
	tick, stopTick := opts.ticker(r.slot)
	go r.loop(tick, stopTick)
	return r, nil
}

// Path is the file being written.
func (r *Recorder) Path() string { return r.path }

// Done is closed once the recording has ended: after Stop, a write error or the size limit.
func (r *Recorder) Done() <-chan struct{} { return r.done }

// Stats returns the recorded time and the file size so far.
func (r *Recorder) Stats() (time.Duration, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.elapsed, r.size
}

// Frames returns the number of frame slots written so far (repeats included).
func (r *Recorder) Frames() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frames
}

// Offer makes img the newest frame. It copies the pixels, so the caller may reuse img; safe
// from any goroutine.
func (r *Recorder) Offer(img *image.RGBA) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fitInto(r.pending, img)
	r.dirty = true
}

// Stop ends the recording, completes the file and returns why it ended: nil, ErrLimit (the file
// is complete) or the write error. It waits for the file to be closed; safe to call twice.
func (r *Recorder) Stop() error {
	r.once.Do(func() { close(r.stop) })
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *Recorder) loop(tick <-chan time.Time, stopTick func()) {
	defer stopTick()
	for {
		select {
		case <-r.stop:
			r.finish(r.step(r.opts.now()))
			close(r.done)
			return
		case <-tick:
			err := r.step(r.opts.now())
			if err != nil {
				r.finish(err)
				close(r.done)
			}
			if r.opts.stepped != nil {
				r.opts.stepped <- struct{}{}
			}
			if err != nil {
				return
			}
		}
	}
}

// step writes the slots that are due at now: repeats for the slots that passed without a tick,
// then the newest frame (or a repeat) in the current one.
func (r *Recorder) step(now time.Time) error {
	due := int(now.Sub(r.start)/r.slot) + 1
	for r.avi.Frames() < due-1 {
		if err := r.write(nil); err != nil {
			return err
		}
	}
	if r.avi.Frames() < due {
		r.mu.Lock()
		dirty := r.dirty
		if dirty {
			r.pending, r.work = r.work, r.pending
			r.dirty = false
		}
		r.mu.Unlock()
		var data []byte
		if dirty {
			r.jpeg.Reset()
			if err := jpeg.Encode(&r.jpeg, r.work, &jpeg.Options{Quality: r.opts.Quality}); err != nil {
				return err
			}
			data = r.jpeg.Bytes()
		}
		if err := r.write(data); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.elapsed = time.Duration(r.avi.Frames()-1) * r.slot
	r.mu.Unlock()
	return nil
}

// encode writes img as the next frame.
func (r *Recorder) encode(img *image.RGBA) error {
	r.jpeg.Reset()
	if err := jpeg.Encode(&r.jpeg, img, &jpeg.Options{Quality: r.opts.Quality}); err != nil {
		return err
	}
	return r.write(r.jpeg.Bytes())
}

// write appends one chunk (nil: a repeat) unless the finished file would pass the limit.
func (r *Recorder) write(data []byte) error {
	if r.avi.Projected(len(data)) > r.opts.Limit {
		return ErrLimit
	}
	var err error
	if data == nil {
		err = r.avi.WriteRepeat()
	} else {
		err = r.avi.WriteFrame(data)
	}
	r.mu.Lock()
	r.size, r.frames = r.avi.Size(), r.avi.Frames()
	r.mu.Unlock()
	return err
}

// finish writes the index and closes the file; err is why the recording ended. A close error
// replaces nil and ErrLimit: the file is not complete then.
func (r *Recorder) finish(err error) {
	if cerr := r.avi.Close(); cerr != nil && (err == nil || errors.Is(err, ErrLimit)) {
		err = cerr
	}
	if cerr := r.file.Close(); cerr != nil && (err == nil || errors.Is(err, ErrLimit)) {
		err = cerr
	}
	r.mu.Lock()
	r.size = r.avi.Size()
	r.err = err
	r.mu.Unlock()
}

// fitInto draws src scaled to fit dst, centred on black (nearest neighbour). Equal sizes are a
// plain copy.
func fitInto(dst, src *image.RGBA) {
	db, sb := dst.Bounds(), src.Bounds()
	if db.Size() == sb.Size() {
		for y := 0; y < db.Dy(); y++ {
			copy(dst.Pix[y*dst.Stride:y*dst.Stride+db.Dx()*4], src.Pix[(y+sb.Min.Y-src.Rect.Min.Y)*src.Stride+(sb.Min.X-src.Rect.Min.X)*4:])
		}
		return
	}
	clear(dst.Pix)
	for i := 3; i < len(dst.Pix); i += 4 {
		dst.Pix[i] = 0xFF
	}
	dw, dh, sw, sh := db.Dx(), db.Dy(), sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return
	}
	// scale = min(dw/sw, dh/sh), kept as a fraction to stay exact
	w, h := dw, sh*dw/sw
	if h > dh {
		w, h = sw*dh/sh, dh
	}
	if w <= 0 || h <= 0 {
		return
	}
	x0, y0 := (dw-w)/2, (dh-h)/2
	for y := 0; y < h; y++ {
		sy := y * sh / h
		srow := src.Pix[(sy+sb.Min.Y-src.Rect.Min.Y)*src.Stride:]
		drow := dst.Pix[(y0+y)*dst.Stride:]
		for x := 0; x < w; x++ {
			sx := (x*sw/w + sb.Min.X - src.Rect.Min.X) * 4
			d := (x0 + x) * 4
			copy(drow[d:d+4], srow[sx:sx+4])
		}
	}
}
