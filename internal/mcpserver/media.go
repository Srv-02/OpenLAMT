package mcpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/mkiso"
)

// iderOpener mounts img on the favorite device with the consent code (may be empty). On error
// the caller still owns img.
type iderOpener func(ctx context.Context, device string, img ider.Image, code consentCode) (*ider.Client, []string, error)

var (
	// buildImage builds the ISO of a folder in the image cache; tests count and hold builds.
	buildImage = mkiso.BuildTemp
	// pendingKeep is how long an image waits for the media_mount call with the consent code.
	pendingKeep = 10 * time.Minute
	// beforeImage runs between the path check and opening the image; tests swap files there.
	beforeImage func()
)

// clearBootTimeout bounds clearing the IDE-R boot option after an unmount or at Close.
const clearBootTimeout = 15 * time.Second

// mediaManager keeps the IDE-R mounts of this server, one per device, like the app's Media
// menu: media_mount opens the session, media_unmount, the device or Close end it. An image
// built or opened for a call that needs user consent is kept for the retry with the code, so a
// large folder is not built twice.
//
// Lock order: an entry's mu may be held while taking m.mu, never the other way round.
type mediaManager struct {
	mu       sync.Mutex
	entries  map[string]*mediaEntry   // mounted or mounting, by favorite name
	pending  map[string]*pendingImage // image kept for the consent retry, by favorite name
	closed   bool
	inFlight sync.WaitGroup // mounts between reserve and their end; Add only under mu while !closed
	watchers sync.WaitGroup // one per mounted session, until it ended

	base context.Context // ends with closeAll; a mount ends with it or with its request
	stop context.CancelFunc

	open      iderOpener
	clearBoot func(device string) error
	logger    *slog.Logger
	now       func() time.Time // clock of the mount time and the read rate; tests replace it
	mounts    uint64           // mounts so far, under mu; numbers the entries (gen)
}

// mediaEntry is the mount of one device. Its mutex serializes the calls on it; it is not held
// while the image is prepared and mounted (mounting is set then and other calls refuse).
type mediaEntry struct {
	mu        sync.Mutex
	device    string
	mounting  bool
	removed   bool         // unmounted, ended or shut down: the entry is no longer in the manager
	client    *ider.Client // nil while mounting
	warnings  []string     // of the mount, e.g. no TLS
	path      string       // cleaned absolute path as given
	name      string       // base name
	folder    bool
	size      int64
	info      *mkiso.Info // folder only
	mountedAt time.Time
	gen       uint64 // number of the mount in this server, so a confirmed action finds the mount it was asked for
	bootNext  bool   // media_boot_next set the IDE-R boot option (or may have, after a failure)

	sampleAt    time.Time // previous media_status sample of the read rate
	sampleBytes int64
}

// mediaSource is what media_mount was asked to mount, after mediaPath resolved and checked it.
type mediaSource struct {
	path    string // resolved, clean and absolute
	folder  bool
	checked os.FileInfo // what mediaPath checked; the image must be the same file or folder
}

// pendingImage is an image kept for the media_mount call with the consent code.
type pendingImage struct {
	src   mediaSource
	img   ider.Image
	info  *mkiso.Info
	timer *time.Timer
}

func newMediaManager(open iderOpener, clearBoot func(string) error, logger *slog.Logger) *mediaManager {
	base, stop := context.WithCancel(context.Background())
	return &mediaManager{entries: map[string]*mediaEntry{}, pending: map[string]*pendingImage{}, base: base, stop: stop,
		open: open, clearBoot: clearBoot, logger: logger, now: time.Now}
}

func notMounted(device string) error {
	return refuse("Nothing is mounted on %q by this server: call media_mount first.", device)
}

func mountInProgress(device string) error {
	return refuse("A mount on %q is in progress: wait for that media_mount call to return.", device)
}

// alive reports whether the session runs; the caller holds e.mu.
func (e *mediaEntry) alive() bool {
	if e.client == nil {
		return false
	}
	select {
	case <-e.client.Done():
		return false
	default:
		return true
	}
}

// busyError refuses a second mount on the device of e.
func (e *mediaEntry) busyError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mounting {
		return mountInProgress(e.device)
	}
	return refuse("%q has «%s» mounted by this server already: call media_unmount first.", e.device, visible(e.name))
}

// free refuses when device has a mount or one is in progress.
func (m *mediaManager) free(device string) error {
	m.mu.Lock()
	closed, e := m.closed, m.entries[device]
	m.mu.Unlock()
	switch {
	case closed:
		return errShuttingDown
	case e != nil:
		return e.busyError()
	}
	return nil
}

// reserve adds a mounting entry for device and counts the mount as in flight.
func (m *mediaManager) reserve(device string) (*mediaEntry, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errShuttingDown
	}
	if old := m.entries[device]; old != nil {
		m.mu.Unlock()
		return nil, old.busyError()
	}
	e := &mediaEntry{device: device, mounting: true}
	m.entries[device] = e
	m.inFlight.Add(1)
	m.mu.Unlock()
	return e, nil
}

// remove takes e out of the manager; the caller holds e.mu.
func (m *mediaManager) remove(e *mediaEntry) {
	e.removed = true
	m.mu.Lock()
	if m.entries[e.device] == e {
		delete(m.entries, e.device)
	}
	m.mu.Unlock()
}

// takePending removes the kept image of device from the manager; the caller owns it then.
func (m *mediaManager) takePending(device string) *pendingImage {
	m.mu.Lock()
	p := m.pending[device]
	delete(m.pending, device)
	m.mu.Unlock()
	if p != nil {
		p.timer.Stop() // its callback finds another or no image and does nothing
	}
	return p
}

// keep stores img for the consent retry on device; after Close it closes img instead.
func (m *mediaManager) keep(device string, p *pendingImage) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = p.img.Close()
		return
	}
	old := m.pending[device]
	m.pending[device] = p
	p.timer = time.AfterFunc(pendingKeep, func() { m.dropPending(device, p) })
	m.mu.Unlock()
	if old != nil {
		old.timer.Stop()
		_ = old.img.Close()
	}
}

// dropPending closes p when it is still the image kept for device (its expiry).
func (m *mediaManager) dropPending(device string, p *pendingImage) {
	m.mu.Lock()
	if m.pending[device] != p {
		m.mu.Unlock()
		return
	}
	delete(m.pending, device)
	m.mu.Unlock()
	_ = p.img.Close()
}

// image returns the image kept for src, or opens the ISO or builds the folder image. What it
// opens must still be what mediaPath checked: the path resolves to the same place and names the
// same file or folder (os.SameFile), so a link or file swapped after the check is refused.
func (m *mediaManager) image(ctx context.Context, device string, src mediaSource) (ider.Image, *mkiso.Info, error) {
	if p := m.takePending(device); p != nil {
		if p.src.path == src.path && p.src.folder == src.folder && os.SameFile(p.src.checked, src.checked) {
			return p.img, p.info, nil
		}
		_ = p.img.Close()
	}
	if r, err := filepath.EvalSymlinks(src.path); err != nil || r != src.path {
		return nil, nil, changedSource(src)
	}
	if !src.folder {
		f, err := os.Open(src.path)
		if err != nil {
			return nil, nil, err
		}
		fi, err := f.Stat() // the opened file itself, not the path
		if err != nil || !fi.Mode().IsRegular() || !os.SameFile(fi, src.checked) {
			f.Close()
			return nil, nil, changedSource(src)
		}
		if fi.Size() == 0 {
			f.Close()
			return nil, nil, refuse("%s is empty.", visible(src.path))
		}
		return &isoFile{File: f, size: fi.Size()}, nil, nil
	}
	// The build reads the folder through an os.Root: no open leaves the checked folder, also when
	// an entry becomes a link meanwhile. Links and special files are skipped (Info.Skipped).
	root, err := os.OpenRoot(src.path)
	if err != nil {
		return nil, nil, changedSource(src)
	}
	defer root.Close()
	if fi, err := root.Stat("."); err != nil || !fi.IsDir() || !os.SameFile(fi, src.checked) {
		return nil, nil, changedSource(src)
	}
	t, info, err := buildImage(ctx, src.path, mkiso.Options{Root: root})
	if err != nil {
		return nil, nil, err
	}
	return t, &info, nil
}

func changedSource(src mediaSource) error {
	return refuse("%s changed after it was checked (a link or the file was replaced). Nothing was mounted.", visible(src.path))
}

// isoFile is an opened ISO file as an IDE-R image (ider.OpenISO opens by path; here the file is
// opened first and checked through its handle).
type isoFile struct {
	*os.File
	size int64
}

func (f *isoFile) Size() int64 { return f.size }

// mount prepares the image of src and mounts it on device, then runs fn with the entry locked.
// It runs until ctx or the manager's base context ends (Close); Close waits for it.
func (m *mediaManager) mount(ctx context.Context, device string, src mediaSource, code consentCode, fn func(e *mediaEntry)) error {
	e, err := m.reserve(device)
	if err != nil {
		return err
	}
	defer m.inFlight.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(m.base, cancel)()

	fail := func(err error) error {
		e.mu.Lock()
		m.remove(e)
		e.mu.Unlock()
		if m.base.Err() != nil {
			return errShuttingDown
		}
		return err
	}
	if beforeImage != nil {
		beforeImage()
	}
	img, info, err := m.image(ctx, device, src)
	if err != nil {
		return fail(err)
	}
	c, warnings, err := m.open(ctx, device, img, code)
	var ce *amt.ConsentError
	switch {
	case errors.Is(err, errConsentCodeNeeded) || errors.As(err, &ce):
		// The device shows a code now, or did not accept it: keep the image for the next call.
		m.keep(device, &pendingImage{src: src, img: img, info: info})
		return fail(err)
	case err != nil:
		_ = img.Close() // a folder image is deleted
		return fail(err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	m.mu.Lock()
	closed := m.closed
	m.mounts++
	e.gen = m.mounts
	m.mu.Unlock()
	if closed { // Close came while the session opened: it is ours to end
		m.remove(e)
		_ = c.Close()
		return errShuttingDown
	}
	now := m.now()
	e.mounting, e.client, e.warnings = false, c, warnings
	e.path, e.name, e.folder, e.size, e.info = src.path, filepath.Base(src.path), src.folder, img.Size(), info
	e.mountedAt, e.sampleAt = now, now
	m.watchers.Add(1)
	go m.watch(e, c)
	fn(e)
	return nil
}

// watch waits for the end of the session; an end by the device or the network removes the
// mount, is logged and clears the boot option if media_boot_next set it.
func (m *mediaManager) watch(e *mediaEntry, c *ider.Client) {
	defer m.watchers.Done()
	<-c.Done()
	e.mu.Lock()
	if e.removed { // unmounted or shut down by us
		e.mu.Unlock()
		return
	}
	m.remove(e)
	boot := e.bootNext
	e.mu.Unlock()
	m.logger.Warn("the device ended the media (IDE-R) session", "device", e.device, "image", e.name, "reason", mediaEndReason(c.Err()))
	if boot {
		if err := m.clearBoot(e.device); err != nil {
			m.logger.Warn("clearing the IDE-R boot option", "device", e.device, "err", err)
		}
	}
}

// mediaEndReason is why a session ended, for the agent and the log.
func mediaEndReason(err error) string {
	switch {
	case err == nil, errors.Is(err, ider.ErrClosed):
		return "The media session was closed."
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "The connection to the device was closed."
	}
	return explain(err)
}

// mounted runs fn with the entry of device locked, when this server has a running mount there.
func (m *mediaManager) mounted(device string, fn func(e *mediaEntry) error) error {
	m.mu.Lock()
	closed, e := m.closed, m.entries[device]
	m.mu.Unlock()
	switch {
	case closed:
		return errShuttingDown
	case e == nil:
		return notMounted(device)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.removed:
		return notMounted(device)
	case e.mounting:
		return mountInProgress(device)
	case !e.alive(): // the device ended it; the watcher removes it
		return notMounted(device)
	}
	return fn(e)
}

// inspect runs fn with the entry of device locked (nil without one) and whether an image is
// kept for the consent retry.
func (m *mediaManager) inspect(device string, fn func(e *mediaEntry, pending bool)) error {
	m.mu.Lock()
	closed, e, pending := m.closed, m.entries[device], m.pending[device] != nil
	m.mu.Unlock()
	if closed {
		return errShuttingDown
	}
	if e == nil {
		fn(nil, pending)
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		e = nil
	}
	fn(e, pending)
	return nil
}

// unmountResult is what media_unmount did.
type unmountResult struct {
	unmounted, cleared bool
	warning            string
}

// unmount closes the mount of device and a kept image, and clears the boot option if
// media_boot_next set it.
func (m *mediaManager) unmount(device string) (unmountResult, error) {
	m.mu.Lock()
	closed, e := m.closed, m.entries[device]
	m.mu.Unlock()
	if closed {
		return unmountResult{}, errShuttingDown
	}
	if p := m.takePending(device); p != nil {
		_ = p.img.Close()
	}
	if e == nil {
		return unmountResult{}, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.removed:
		return unmountResult{}, nil
	case e.mounting:
		return unmountResult{}, mountInProgress(device)
	}
	m.remove(e)
	_ = e.client.Close()
	r := unmountResult{unmounted: true}
	if e.bootNext {
		if err := m.clearBoot(device); err != nil {
			r.warning = "The IDE-R boot option may still be set: " + explain(err)
		} else {
			r.cleared = true
		}
	}
	return r, nil
}

// closeAll ends every mount and kept image, clears the boot options media_boot_next set (in
// parallel, each bounded) and refuses new calls. A mount in flight is cancelled and waited for.
func (m *mediaManager) closeAll() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stop()
	m.inFlight.Wait()
	m.mu.Lock()
	entries := slices.Collect(maps.Values(m.entries))
	pending := slices.Collect(maps.Values(m.pending))
	clear(m.entries)
	clear(m.pending)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range pending {
		p.timer.Stop()
		wg.Go(func() { _ = p.img.Close() })
	}
	for _, e := range entries {
		wg.Go(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.removed || e.client == nil {
				return
			}
			e.removed = true
			_ = e.client.Close()
			if e.bootNext {
				if err := m.clearBoot(e.device); err != nil {
					m.logger.Warn("clearing the IDE-R boot option", "device", e.device, "err", err)
				}
			}
		})
	}
	wg.Wait()
	m.watchers.Wait()
}
