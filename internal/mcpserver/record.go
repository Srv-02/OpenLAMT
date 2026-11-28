package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
)

// Waits of a recording whose KVM session ended (a power action closed it, or AMT dropped it);
// variables for tests. The first wait gives a power command that closed the session time to
// finish: AMT refuses off, cycle and shutdown while KVM is open, so a reopen right away would
// block it. Failed reopens double the wait up to recReopenMax.
var (
	recReopenDelay = 3 * time.Second
	recReopenMax   = 30 * time.Second
)

const (
	maxRecordFPS     = 30
	maxCaptureName   = 100 // runes of a name the agent chose
	captureFolderDir = "OpenLAMT"
)

// recording is a screen recording of one device: a capture.Recorder, fed with the frames of the
// KVM session by a goroutine. It lives on the device's kvmEntry. end finalizes the file, once,
// whoever ends the recording first.
type recording struct {
	recorder *capture.Recorder
	fps      int
	ctx      context.Context // ends with the recording; stops the feed
	cancel   context.CancelFunc
	once     sync.Once
	ended    chan struct{} // closed when the file is finalized and res is set
	res      recordResult
	// The waits before a reopen, read when the recording starts.
	reopenDelay, reopenMax time.Duration
}

// recordResult is what a finished recording leaves.
type recordResult struct {
	Path     string
	Duration time.Duration
	Bytes    int64
	Frames   int
	EndedBy  string
}

// running reports whether r is a recording that has not ended; false for nil.
func (r *recording) running() bool {
	if r == nil {
		return false
	}
	select {
	case <-r.ended:
		return false
	default:
		return true
	}
}

// end stops the feed, finalizes the file and returns the result. why says what ended it; a limit
// or write error of the recorder wins over it. Later calls return the first result.
func (r *recording) end(why string) recordResult {
	r.once.Do(func() {
		r.cancel()
		err := r.recorder.Stop()
		switch {
		case errors.Is(err, capture.ErrLimit):
			why = fmt.Sprintf("the recording reached the size limit (%d GB)", capture.DefaultLimit/1_000_000_000)
		case err != nil:
			why = "a write error: " + err.Error()
		case why == "":
			why = "stopped"
		}
		d, size := r.recorder.Stats()
		r.res = recordResult{Path: r.recorder.Path(), Duration: d, Bytes: size, Frames: r.recorder.Frames(), EndedBy: why}
		close(r.ended)
	})
	<-r.ended
	return r.res
}

// newRecording wraps r, which watches it end by itself (size limit, write error).
func newRecording(r *capture.Recorder, fps int) *recording {
	ctx, cancel := context.WithCancel(context.Background())
	rec := &recording{recorder: r, fps: fps, ctx: ctx, cancel: cancel, ended: make(chan struct{}), reopenDelay: recReopenDelay, reopenMax: recReopenMax}
	go func() {
		select {
		case <-r.Done():
			rec.end("")
		case <-rec.ended:
		}
	}()
	return rec
}

// startRecording records into f from img on and feeds it from c; fav is used to reopen the
// session. The caller holds the entry lock.
func (m *kvmManager) startRecording(f *os.File, img *image.RGBA, c *kvm.Client, fav favorites.Favorite, fps, quality int) (*recording, error) {
	r, err := capture.StartRecorder(f, img, capture.RecorderOptions{FPS: fps, Quality: quality})
	if err != nil {
		return nil, err
	}
	if fps <= 0 {
		fps = capture.DefaultFPS
	}
	rec := newRecording(r, fps)
	go m.feed(rec, fav, c)
	return rec, nil
}

// feed hands the newest frame of the session to the recorder until the recording ends. When the
// session ends it waits, opens it again like a tool call would (kvmManager.use) and goes on; the
// recorder repeats the last frame meanwhile. A failed reopen is tried again with a growing wait.
func (m *kvmManager) feed(rec *recording, fav favorites.Favorite, c *kvm.Client) {
	var (
		img   *image.RGBA
		seq   uint64
		delay = rec.reopenDelay
	)
	for rec.ctx.Err() == nil {
		if c == nil {
			if sleepCtx(rec.ctx, delay) != nil {
				return
			}
			var nc *kvm.Client
			err := m.use(rec.ctx, fav, func(e *kvmEntry) error { nc = e.client; return nil })
			var re *reopenError
			var cn *consentNeededError
			switch {
			case err == nil:
				c, seq, delay = nc, 0, rec.reopenDelay
			case rec.ctx.Err() != nil:
				return
			case errors.As(err, &re) || errors.As(err, &cn):
				delay = min(delay*2, rec.reopenMax)
			default: // the entry is gone or the server shuts down
				rec.end("the KVM session ended")
				return
			}
			continue
		}
		n, err := c.WaitFrame(rec.ctx, seq)
		if err != nil {
			c, delay = nil, rec.reopenDelay
			continue
		}
		seq = n
		img = c.SnapshotInto(img)
		rec.recorder.Offer(img)
		// At most two snapshots per frame slot: the recorder takes one frame per slot anyway.
		if sleepCtx(rec.ctx, time.Second/time.Duration(2*rec.fps)) != nil {
			return
		}
	}
}

// --- where files go

// captureDir returns the folder for a new file: folder if the agent gave one (absolute, must
// exist), else capture.folder of the config, else OpenLAMT in the home folder. The two defaults
// are created (mode 0700) if missing.
func (s *server) captureDir(folder string) (string, error) {
	var dir string
	switch folder = strings.TrimSpace(folder); {
	case folder != "":
		if !filepath.IsAbs(folder) {
			return "", refuse("folder must be an absolute path, not %q.", folder)
		}
		dir = filepath.Clean(folder)
	case s.cfg.CaptureFolder != "":
		dir = s.cfg.CaptureFolder
	default:
		home, err := s.home()
		if err != nil {
			return "", refuse("The home folder is unknown, so there is no default folder. Pass folder, an absolute path.")
		}
		dir = filepath.Join(home, captureFolderDir)
	}
	if folder == "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create the folder %s: %w", dir, err)
		}
	}
	switch err := capture.CheckFolder(dir); {
	case errors.Is(err, capture.ErrFolderMissing):
		return "", refuse("The folder %s does not exist. Create it first or choose another folder.", dir)
	case errors.Is(err, capture.ErrFolderReadOnly):
		return "", refuse("The folder %s is not writable.", dir)
	case err != nil:
		return "", err
	}
	return dir, nil
}

// captureName returns the file name for a new file: the base name the agent chose with ext (any
// extension of it is replaced, path parts are dropped), or the app's name for the device.
func captureName(name, device string, now time.Time, ext string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return capture.FileName(device, now, ext), nil
	}
	n := path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	n = strings.TrimSuffix(n, path.Ext(n))
	n = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"|?*/\`, r) || unicode.IsControl(r) {
			return '-'
		}
		return r
	}, n)
	if r := []rune(n); len(r) > maxCaptureName {
		n = string(r[:maxCaptureName])
	}
	if n = strings.Trim(n, " .-"); n == "" {
		return "", refuse("name must hold a file name, for example \"step-1\".")
	}
	return n + "." + ext, nil
}

// createCapture makes the new file of a recording or screenshot; it never overwrites a file.
func (s *server) createCapture(dir, name, device, ext string) (*os.File, error) {
	fn, err := captureName(name, device, time.Now(), ext)
	if err != nil {
		return nil, err
	}
	return capture.CreateUnique(dir, fn)
}

// --- kvm_record_start and kvm_record_stop

type recordStartInput struct {
	Device  string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Folder  string `json:"folder,omitempty" jsonschema:"folder of the video: absolute path of an existing folder; default: capture.folder of the server config, else the folder OpenLAMT in the home folder"`
	Name    string `json:"name,omitempty" jsonschema:"base name of the video file; .avi is set by the tool; default: openlamt-<device>-<date>-<time>. An existing file is never overwritten: -2, -3 is added"`
	FPS     int    `json:"fps,omitempty" jsonschema:"frames per second, 1 to 30 (default 10)"`
	Quality int    `json:"quality,omitempty" jsonschema:"JPEG quality of the frames, 1 to 100 (default 75)"`
}

type recordStartOutput struct {
	Device       string   `json:"device"`
	Path         string   `json:"path" jsonschema:"the video file; it is complete only after kvm_record_stop"`
	ScreenWidth  int      `json:"screen_width"`
	ScreenHeight int      `json:"screen_height"`
	FPS          int      `json:"fps"`
	Warnings     []string `json:"warnings,omitempty"`
}

func (s *server) kvmRecordStart(ctx context.Context, _ *mcp.CallToolRequest, in recordStartInput, rec *record) (*mcp.CallToolResult, recordStartOutput, error) {
	rec.device = in.Device
	if in.FPS < 0 || in.FPS > maxRecordFPS {
		return nil, recordStartOutput{}, refuse("fps must be between 1 and %d.", maxRecordFPS)
	}
	if in.Quality < 0 || in.Quality > 100 {
		return nil, recordStartOutput{}, refuse("quality must be between 1 and 100.")
	}
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, recordStartOutput{}, err
	}
	dir, err := s.captureDir(in.Folder)
	if err != nil {
		return nil, recordStartOutput{}, err
	}
	var out recordStartOutput
	err = s.kvm.use(ctx, fav, func(e *kvmEntry) error {
		switch {
		case e.rec.running():
			return refuse("%s is being recorded already (%s). Call kvm_record_stop first.", fav.DisplayName(), e.rec.recorder.Path())
		case e.rec != nil:
			return refuse("The last recording of %s ended by itself (%s). Call kvm_record_stop to get its result.", fav.DisplayName(), e.rec.end("").EndedBy)
		}
		c := e.client
		out.Warnings = append(out.Warnings, e.warnings...)
		if c.Frame() == 0 { // the first picture, so the video does not start black
			wctx, cancel := context.WithTimeout(ctx, firstFrameWait)
			_, _ = c.WaitFrame(wctx, 0)
			cancel()
		}
		f, err := s.createCapture(dir, in.Name, fav.DisplayName(), "avi")
		if err != nil {
			return err
		}
		r, err := s.kvm.startRecording(f, c.SnapshotInto(nil), c, fav, in.FPS, in.Quality)
		if err != nil {
			return fmt.Errorf("start the recording: %w", err)
		}
		e.rec = r
		w, h := c.Size()
		out = recordStartOutput{Device: fav.DisplayName(), Path: r.recorder.Path(), ScreenWidth: w, ScreenHeight: h, FPS: r.fps, Warnings: out.Warnings}
		return nil
	})
	if err != nil {
		return nil, recordStartOutput{}, err
	}
	return nil, out, nil
}

type recordingResult struct {
	Path            string  `json:"path" jsonschema:"the finished video file"`
	DurationSeconds float64 `json:"duration_seconds"`
	Bytes           int64   `json:"bytes"`
	Frames          int     `json:"frames"`
	EndedBy         string  `json:"ended_by" jsonschema:"why the recording ended: stopped, or the reason it ended by itself (size limit, write error)"`
}

func (r recordResult) output() *recordingResult {
	return &recordingResult{Path: r.Path, DurationSeconds: r.Duration.Round(100 * time.Millisecond).Seconds(), Bytes: r.Bytes, Frames: r.Frames, EndedBy: r.EndedBy}
}

type recordStopOutput struct {
	Device string `json:"device"`
	recordingResult
}

func (s *server) kvmRecordStop(_ context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, recordStopOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, recordStopOutput{}, err
	}
	if !s.kvm.has(fav) {
		return nil, recordStopOutput{}, refuse("No recording of %s is running.", fav.DisplayName())
	}
	var res recordResult
	err = s.kvm.inspect(fav, func(e *kvmEntry) error {
		if e.rec == nil {
			return refuse("No recording of %s is running.", fav.DisplayName())
		}
		res, e.rec = e.rec.end("stopped"), nil
		return nil
	})
	if err != nil {
		return nil, recordStopOutput{}, err
	}
	return nil, recordStopOutput{Device: fav.DisplayName(), recordingResult: *res.output()}, nil
}
