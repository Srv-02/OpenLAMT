package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/session"
)

// Waits of a recording whose KVM session ended (a power action closed it, or AMT dropped it);
// variables for tests. The recording opens the session again itself after recReopenDelay, and
// after a power action only once it returned (AMT refuses off, cycle and shutdown while KVM is
// open). Failed reopens double the wait up to recReopenMax.
var (
	recReopenDelay = 3 * time.Second
	recReopenMax   = 30 * time.Second
)

const (
	maxRecordFPS     = 30
	maxCaptureName   = 100 // bytes of a name the agent chose, without the extension
	captureFolderDir = "OpenLAMT"
	maxGapWarnings   = 20 // warnings about a missing session per recording; more are counted
)

// Why the KVM session of a recording was missing, for its warnings.
const (
	whyAMT   = "AMT ended the session"
	whyPower = "power action"
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
	now                    func() time.Time // clock of the warnings

	mu       sync.Mutex
	gap      *recGap  // the session is missing since gap.from; nil while it runs
	warnings []string // one per past gap, at most maxGapWarnings
	moreGaps int      // gaps beyond maxGapWarnings
}

// recGap is a time without KVM session during a recording: the video repeats the last picture.
type recGap struct {
	from time.Time
	why  []string
}

// recordResult is what a finished recording leaves.
type recordResult struct {
	Path     string
	Duration time.Duration
	Bytes    int64
	Frames   int
	EndedBy  string
	Warnings []string
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

// lost notes that the session went away (why); a gap that is open already keeps its start.
func (r *recording) lost(why string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gap == nil {
		r.gap = &recGap{from: r.now()}
	}
	r.gap.add(why)
}

// because adds a reason to the open gap.
func (r *recording) because(why string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gap != nil {
		r.gap.add(why)
	}
}

// resumed closes the open gap: the session runs again.
func (r *recording) resumed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeGap(false)
}

// closeGap turns the open gap into a warning; the caller holds r.mu. atEnd: the recording ends
// while the session is missing.
func (r *recording) closeGap(atEnd bool) {
	if r.gap == nil {
		return
	}
	g := r.gap
	r.gap = nil
	if len(r.warnings) >= maxGapWarnings {
		r.moreGaps++
		return
	}
	const layout = "2006-01-02 15:04:05 UTC"
	from, to := g.from.UTC(), r.now().UTC()
	until := to.Format(layout)
	if until[:10] == from.Format(layout)[:10] {
		until = to.Format("15:04:05 UTC")
	}
	if atEnd {
		until = "until the recording ended at " + until
	} else {
		until = "to " + until
	}
	r.warnings = append(r.warnings, fmt.Sprintf("KVM was closed from %s %s (%s); the video shows the last picture meanwhile.",
		from.Format(layout), until, strings.Join(g.why, "; ")))
}

func (g *recGap) add(why string) {
	if !slices.Contains(g.why, why) {
		g.why = append(g.why, why)
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
		r.mu.Lock()
		r.closeGap(true)
		warnings := slices.Clone(r.warnings)
		if r.moreGaps > 0 {
			warnings = append(warnings, fmt.Sprintf("KVM was closed %d more times; the video shows the last picture meanwhile.", r.moreGaps))
		}
		r.mu.Unlock()
		d, size := r.recorder.Stats()
		r.res = recordResult{Path: r.recorder.Path(), Duration: d, Bytes: size, Frames: r.recorder.Frames(), EndedBy: why, Warnings: warnings}
		close(r.ended)
	})
	<-r.ended
	return r.res
}

// newRecording wraps r, which watches it end by itself (size limit, write error).
func newRecording(r *capture.Recorder, fps int) *recording {
	ctx, cancel := context.WithCancel(context.Background())
	rec := &recording{recorder: r, fps: fps, ctx: ctx, cancel: cancel, ended: make(chan struct{}),
		reopenDelay: recReopenDelay, reopenMax: recReopenMax, now: time.Now}
	go func() {
		select {
		case <-r.Done():
			rec.end("")
		case <-rec.ended:
		}
	}()
	return rec
}

// startRecording records into f from img on and feeds it from the client of e; fav is used to
// reopen the session. The caller holds e.mu.
func (m *kvmManager) startRecording(e *kvmEntry, f *os.File, img *image.RGBA, fav favorites.Favorite, fps, quality int) (*recording, error) {
	r, err := capture.StartRecorder(f, img, capture.RecorderOptions{FPS: fps, Quality: quality})
	if err != nil {
		return nil, err
	}
	if fps <= 0 {
		fps = capture.DefaultFPS
	}
	rec := newRecording(r, fps)
	rec.now = m.now
	go m.feed(rec, e, fav, e.client)
	return rec, nil
}

func (m *kvmManager) feedStep(step string) {
	if m.onFeed != nil {
		m.onFeed(step)
	}
}

// feed hands the newest frame of the session to the recorder until the recording ends. When the
// session ends, reopen brings it back; the recorder repeats the last frame meanwhile, and the
// recording notes the gap as a warning.
func (m *kvmManager) feed(rec *recording, e *kvmEntry, fav favorites.Favorite, c *kvm.Client) {
	var (
		img *image.RGBA
		seq uint64
	)
	for {
		n, err := c.WaitFrame(rec.ctx, seq)
		if rec.ctx.Err() != nil {
			return
		}
		if err != nil {
			why := whyAMT
			if m.powerRunning(e.key) {
				why = whyPower
			}
			rec.lost(why)
			m.feedStep("lost")
			if c = m.reopen(rec, e, fav); c == nil {
				return
			}
			rec.resumed()
			m.feedStep("resumed")
			seq = 0
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

// reopen waits until the session of e runs again and returns its client; nil when the recording
// ended. A tool call may open the session meanwhile (kvm_connect with a consent code, say), and
// the recording goes on with it. Otherwise the recording opens it itself after its wait, without
// holding e.mu, and only passively: never while a power action of the device runs (the wait
// starts again after it), never turning KVM on, never starting user consent. Network errors are
// tried again with a growing wait; consent, KVM off, a refused login or a changed certificate
// make it wait for a tool call.
func (m *kvmManager) reopen(rec *recording, e *kvmEntry, fav favorites.Favorite) *kvm.Client {
	delay, wait, passive := rec.reopenDelay, true, false
	for {
		e.mu.Lock()
		if e.removed {
			e.mu.Unlock()
			rec.end("the KVM session ended")
			return nil
		}
		if e.alive() {
			c := e.client
			e.mu.Unlock()
			return c
		}
		opened := e.awaitOpen()
		power := m.powerDone(e.key)
		var op *feedOpen
		if power == nil && !wait && !passive {
			op = e.startFeedOpen(rec.ctx)
		}
		e.mu.Unlock()

		switch {
		case power != nil:
			rec.because(whyPower)
			m.feedStep("power")
			select {
			case <-power:
				wait = true
			case <-opened:
			case <-rec.ctx.Done():
				return nil
			}
			continue
		case passive:
			m.feedStep("wait")
			select {
			case <-opened:
			case <-rec.ctx.Done():
				return nil
			}
			continue
		case wait:
			m.feedStep("delay")
			t := time.NewTimer(delay)
			select {
			case <-t.C:
				wait = false
			case <-opened:
			case <-rec.ctx.Done():
				t.Stop()
				return nil
			}
			t.Stop()
			continue
		}

		m.feedStep("open")
		c, warnings, err := m.open(op.ctx, fav, kvmOpen{passive: true})
		e.mu.Lock()
		installed := false
		if err == nil {
			if !e.removed && !e.alive() && !m.powerRunning(e.key) && rec.ctx.Err() == nil {
				e.install(c, warnings, fav.UseTLS)
				installed = true
			} else {
				_ = c.Close()
			}
		}
		e.endFeedOpen(op)
		e.mu.Unlock()
		switch {
		case rec.ctx.Err() != nil:
			return nil
		case installed:
			return c
		case err == nil: // a tool call opened the session, or a power action began: look again
			continue
		}
		if why, ok := waitReason(err); ok {
			rec.because(why)
			passive = true
		} else {
			wait, delay = true, min(delay*2, rec.reopenMax)
		}
	}
}

// waitReason says why a failed reopen of a recording is not tried again by itself: only a tool
// call can open the session then. ok is false for errors worth another try (network, device
// busy).
func waitReason(err error) (why string, ok bool) {
	var (
		off *amt.RedirectionOffError
		mm  *amt.FingerprintMismatchError
		re  *kvm.RedirectionError
		ref *refusal
	)
	switch {
	case errors.Is(err, amt.ErrConsentRequired), errors.Is(err, errConsentCodeNeeded):
		return "the device asked for user consent, so the recording waited for kvm_connect with the code", true
	case errors.As(err, &off) && off.Feature == "redirection":
		return "the redirection port is turned off on the device, and the recording never turns it on", true
	case errors.As(err, &off):
		return "KVM is turned off on the device, and the recording never turns it on", true
	case errors.As(err, &mm):
		return "the TLS certificate of the device changed, so the recording did not connect", true
	case errors.Is(err, session.ErrAuth), errors.As(err, &re) && re.Stage == "auth":
		return "the device rejected the stored login, so the recording stopped trying", true
	case errors.As(err, &ref):
		return "the password or the trusted certificate of the device was missing, so the recording waited for a tool call", true
	}
	return "", false
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

// reservedNames are the device names of Windows: a file named so (with any extension) is no
// file there. They are refused on every OS, so files stay portable.
var reservedNames = func() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}
	for _, d := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
		m["COM"+d], m["LPT"+d] = true, true
	}
	return m
}()

// captureName returns the file name for a new file: the base name the agent chose with ext (any
// extension of it is replaced, path parts are dropped, at most maxCaptureName bytes; a Windows
// device name gets the prefix openlamt-), or the app's name for the device.
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
	for len(n) > maxCaptureName { // cut at a character boundary
		_, size := utf8.DecodeLastRuneInString(n)
		n = n[:len(n)-size]
	}
	if n = strings.Trim(n, " .-"); n == "" {
		return "", refuse("name must hold a file name, for example \"step-1\".")
	}
	stem, _, _ := strings.Cut(n, ".")
	if reservedNames[strings.ToUpper(strings.TrimRight(stem, " "))] {
		n = "openlamt-" + n
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
		if old, ok := s.kvm.takeKept(e.key); ok { // a new recording drops it: say so once
			out.Warnings = append(out.Warnings, fmt.Sprintf("The last recording of %s ended by itself (%s) and was not fetched with kvm_record_stop: %s.",
				fav.DisplayName(), old.EndedBy, old.Path))
		}
		if c.Frame() == 0 { // the first picture, so the video does not start black
			wctx, cancel := context.WithTimeout(ctx, firstFrameWait)
			_, _ = c.WaitFrame(wctx, 0)
			cancel()
		}
		f, err := s.createCapture(dir, in.Name, fav.DisplayName(), "avi")
		if err != nil {
			return err
		}
		r, err := s.kvm.startRecording(e, f, c.SnapshotInto(nil), fav, in.FPS, in.Quality)
		if err != nil {
			return fmt.Errorf("start the recording: %w", err)
		}
		e.setRec(r)
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
	// Warnings name each time the KVM session was missing during the recording.
	Warnings []string `json:"warnings,omitempty" jsonschema:"each time the KVM session was closed during the recording (UTC), and why; the video shows the last picture meanwhile"`
}

func (r recordResult) output() *recordingResult {
	return &recordingResult{Path: r.Path, DurationSeconds: r.Duration.Round(100 * time.Millisecond).Seconds(), Bytes: r.Bytes, Frames: r.Frames,
		EndedBy: r.EndedBy, Warnings: r.Warnings}
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
	res, ok, err := s.kvm.stopRecording(fav)
	switch {
	case err != nil:
		return nil, recordStopOutput{}, err
	case !ok:
		return nil, recordStopOutput{}, refuse("No recording of %s is running.", fav.DisplayName())
	}
	return nil, recordStopOutput{Device: fav.DisplayName(), recordingResult: *res.output()}, nil
}
