package mcpserver

import (
	"context"
	"errors"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
)

// kvmOpen says how a KVM session is opened.
type kvmOpen struct {
	code   consentCode // the consent code of the call; may be empty
	agreed bool        // the person in the MCP client agreed to turn KVM on (see server.mayEnable)
	// passive: a recording opens the session again by itself. It never turns KVM on and never
	// starts user consent, so the device shows no code that nobody asked for.
	passive bool
}

// kvmOpener opens a KVM session to the host and user of fav as o says.
type kvmOpener func(ctx context.Context, fav favorites.Favorite, o kvmOpen) (*kvm.Client, []string, error)

// kvmManager keeps one KVM session per device across tool calls. AMT allows one KVM session per
// device, so the manager owns it: kvm_connect opens it, the other KVM tools use it, a session
// that AMT ended or a power action closed is opened again by the next call, an idle session is
// closed after the idle timeout, and Close ends all of them when the client goes away.
//
// Lock order: an entry's mu may be held while taking m.mu, never the other way round.
type kvmManager struct {
	mu      sync.Mutex
	entries map[string]*kvmEntry // by favKey
	closed  bool
	idle    time.Duration // 0: never
	open    kvmOpener
	now     func() time.Time // clock of the idle timeout and of recording warnings; tests replace it
	// powers are the power actions that run now, by favKey (see beginPower).
	powers map[string]*powerRun
	// kept is the result of a recording that ended by itself and whose session closed before
	// kvm_record_stop fetched it, one per favKey.
	kept map[string]recordResult
	// failed is the result of a mandatory recording that failed and closed its session, one
	// per favKey, until kvm_connect or kvm_disconnect: the KVM tools name it instead of "no
	// session".
	failed map[string]recordResult
	// startRec starts the recorder of a file; tests replace it (size limit, write errors).
	startRec func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error)
	onFeed   func(step string) // tests: called at each step of a recording's reopen
	onCall   func(step string) // tests: called when a tool call waits (step "power")
}

// powerRun counts the power actions of one device that run now; done is closed when the last
// one ended.
type powerRun struct {
	n    int
	done chan struct{}
}

// kvmEntry is the KVM session of one device. Its mutex serializes the tool calls on it.
type kvmEntry struct {
	mu       sync.Mutex
	key      string      // favKey of the device
	client   *kvm.Client // nil after a power action closed it; opened again on use
	warnings []string    // of the last open, e.g. no TLS
	tls      bool        // the open session uses TLS (the favorite may have changed since)
	removed  bool        // disconnected, idle or shut down: the entry is no longer in the manager
	lastUsed time.Time
	timer    *time.Timer
	rec      *recording // the recording of this device, running or ended and not yet reported by kvm_record_stop
	// live is rec, readable without mu: stop, disconnect and Close cancel the recording's feed
	// before they wait for mu.
	live atomic.Pointer[recording]
	// feedOpen is the reopen of the recording that runs now, without mu held; nil otherwise.
	feedOpen *feedOpen
	// opened is closed when a session is opened (install); a recording waits on it.
	opened chan struct{}

	imgW, imgH   int        // size of the last screenshot; 0 before the first one
	buttons      kvm.Button // mouse buttons held by kvm_mouse down
	lastX, lastY int        // last pointer position on the screen
}

// feedOpen is a reopen of a recording in progress (see kvmManager.reopen).
type feedOpen struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the new client is installed or closed again
}

func newKVMManager(idle time.Duration, open kvmOpener) *kvmManager {
	return &kvmManager{entries: map[string]*kvmEntry{}, idle: idle, open: open, now: time.Now,
		powers: map[string]*powerRun{}, kept: map[string]recordResult{}, failed: map[string]recordResult{}, startRec: capture.StartRecorder}
}

var errShuttingDown = refuse("The MCP server is shutting down.")

func noSession(fav favorites.Favorite) error {
	return refuse("No KVM session is open for %q: call kvm_connect first.", fav.DisplayName())
}

// missing is the error of a KVM tool call without a session: noSession, or, after a mandatory
// recording failed and closed the session, why.
func (m *kvmManager) missing(fav favorites.Favorite) error {
	m.mu.Lock()
	res, failed := m.failed[favKey(fav)]
	m.mu.Unlock()
	if failed {
		return errRecordingFailed(fav, res)
	}
	return noSession(fav)
}

func errRecordingFailed(fav favorites.Favorite, res recordResult) error {
	return refuse("The KVM session of %q was closed because its recording failed (%s); the server configuration (capture.record_always) "+
		"allows no KVM session without a recording. The video so far: %s. Call kvm_connect to open a new session with a new recording.",
		fav.DisplayName(), res.EndedBy, res.Path)
}

func (m *kvmManager) callStep(step string) {
	if m.onCall != nil {
		m.onCall(step)
	}
}

// recordingFailed closes the session of e after its mandatory recording rec ended by itself
// (a write error, or the file after the size limit could not start): no session runs without
// its recording. The result stays for the next KVM call and kvm_disconnect (see failed).
func (m *kvmManager) recordingFailed(e *kvmEntry, rec *recording) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed || e.rec != rec { // ended from outside, or replaced
		return
	}
	// The entry leaves the map and the failure is kept in one step under m.mu, so a call never
	// finds neither (it would say "no session" instead of why). A call that already holds the
	// entry waits for e.mu and then sees removed and the failure.
	e.removed = true
	res, _ := e.shut("")
	m.mu.Lock()
	if m.entries[e.key] == e {
		delete(m.entries, e.key)
	}
	m.failed[e.key] = res
	m.mu.Unlock()
}

// takeFailed returns the failed result of the device with key once (see kvmManager.failed).
func (m *kvmManager) takeFailed(key string) (recordResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.failed[key]
	delete(m.failed, key)
	return res, ok
}

// alive reports whether the client runs.
func (e *kvmEntry) alive() bool {
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

// release lets go of held mouse buttons and keys, so nothing stays pressed on the host.
func (e *kvmEntry) release() {
	if !e.alive() {
		return
	}
	if e.buttons != 0 {
		_ = e.client.PointerEvent(e.lastX, e.lastY, 0)
	}
	e.buttons = 0
	_ = e.client.ReleaseAll()
}

// install makes c the client of the session; the caller holds e.mu. A recording that waits for
// the session goes on with it.
func (e *kvmEntry) install(c *kvm.Client, warnings []string, tls bool) {
	e.client, e.warnings, e.buttons, e.tls = c, warnings, 0, tls
	if e.opened != nil {
		close(e.opened)
		e.opened = nil
	}
}

// awaitOpen returns a channel that is closed at the next install; the caller holds e.mu.
func (e *kvmEntry) awaitOpen() <-chan struct{} {
	if e.opened == nil {
		e.opened = make(chan struct{})
	}
	return e.opened
}

// setRec makes r the recording of the entry (nil: none); the caller holds e.mu.
func (e *kvmEntry) setRec(r *recording) {
	e.rec = r
	e.live.Store(r)
}

// cancelFeed stops the feed of a running recording, without e.mu: a reopen of the feed then
// gives up. The recording itself ends later, under e.mu.
func (e *kvmEntry) cancelFeed() {
	if r := e.live.Load(); r != nil {
		r.cancel()
	}
}

// startFeedOpen registers a reopen of the recording; the caller holds e.mu.
func (e *kvmEntry) startFeedOpen(ctx context.Context) *feedOpen {
	octx, cancel := context.WithCancel(ctx)
	op := &feedOpen{ctx: octx, cancel: cancel, done: make(chan struct{})}
	e.feedOpen = op
	return op
}

// endFeedOpen ends the reopen op; the caller holds e.mu.
func (e *kvmEntry) endFeedOpen(op *feedOpen) {
	op.cancel()
	if e.feedOpen == op {
		e.feedOpen = nil
	}
	close(op.done)
}

// awaitReady waits until neither a reopen of the recording (awaitFeedOpen) nor a power action
// of the device runs, before a tool call opens the session: AMT refuses off, cycle and shutdown
// while KVM is open, so a call never opens it while the power action runs. The caller holds
// e.mu; it is unlocked while waiting and held again on return, also on a ctx error.
func (m *kvmManager) awaitReady(ctx context.Context, e *kvmEntry) error {
	for {
		if err := e.awaitFeedOpen(ctx); err != nil {
			return err
		}
		done := m.powerDone(e.key)
		if done == nil {
			return nil
		}
		e.mu.Unlock()
		m.callStep("power")
		select {
		case <-done:
		case <-ctx.Done():
			e.mu.Lock()
			return ctx.Err()
		}
		e.mu.Lock()
	}
}

// awaitFeedOpen waits until a reopen of the recording that runs now has ended, so nobody opens a
// second session next to it. The caller holds e.mu; it is unlocked while waiting and held again
// on return, also on a ctx error.
func (e *kvmEntry) awaitFeedOpen(ctx context.Context) error {
	for e.feedOpen != nil {
		done := e.feedOpen.done
		e.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			e.mu.Lock()
			return ctx.Err()
		}
		e.mu.Lock()
	}
	return nil
}

// shut ends a recording (why says why), releases input and closes the client; the caller holds
// e.mu. It returns the result of the recording, if there was one.
func (e *kvmEntry) shut(why string) (recordResult, bool) {
	if e.timer != nil {
		e.timer.Stop()
	}
	var (
		res recordResult
		had = e.rec != nil
	)
	if had {
		res = e.rec.end(why)
		e.setRec(nil)
	}
	e.release()
	if e.client != nil {
		_ = e.client.Close()
		e.client = nil
	}
	return res, had
}

// has reports whether the device of fav has a session entry (open, or to be opened again on use).
func (m *kvmManager) has(fav favorites.Favorite) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[favKey(fav)] != nil
}

// sessionTLS reports whether the running session of the device of fav uses TLS; ok is false
// without a running session.
func (m *kvmManager) sessionTLS(fav favorites.Favorite) (tls, ok bool) {
	m.mu.Lock()
	e := m.entries[favKey(fav)]
	m.mu.Unlock()
	if e == nil {
		return false, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tls, !e.removed && e.alive()
}

// connect opens the session of the device of fav, or keeps the running one (already true), and
// runs fn with the entry locked. A session that ended is opened again with code; agreed goes to
// the opener.
func (m *kvmManager) connect(ctx context.Context, fav favorites.Favorite, code consentCode, agreed bool, fn func(e *kvmEntry, already bool) error) error {
	key := favKey(fav)
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return errShuttingDown
		}
		e, created := m.entries[key], false
		if e == nil {
			e, created = &kvmEntry{key: key}, true
			m.entries[key] = e
		}
		m.mu.Unlock()

		e.mu.Lock()
		if !e.removed && !e.alive() {
			if err := m.awaitReady(ctx, e); err != nil {
				if created && !e.removed && !e.alive() {
					m.remove(e)
				}
				e.mu.Unlock()
				return err
			}
		}
		if e.removed { // closed meanwhile: start over
			e.mu.Unlock()
			continue
		}
		already := e.alive()
		if !already {
			c, warnings, err := m.open(ctx, fav, kvmOpen{code: code, agreed: agreed})
			if err != nil {
				if created {
					m.remove(e)
				}
				e.mu.Unlock()
				return err
			}
			e.install(c, warnings, fav.UseTLS)
			if e.timer == nil && m.idle > 0 {
				e.timer = time.AfterFunc(m.idle, func() { m.expire(e) })
			}
			m.mu.Lock()
			delete(m.failed, key) // a new session: the failure was reported or is past
			m.mu.Unlock()
		}
		e.lastUsed = m.now()
		err := fn(e, already)
		e.lastUsed = m.now()
		e.mu.Unlock()
		return err
	}
}

// reopenError: the session had ended and opening it again failed.
type reopenError struct{ err error }

func (e *reopenError) Error() string { return "reopen the KVM session: " + e.err.Error() }
func (e *reopenError) Unwrap() error { return e.err }

// use runs fn on the open session of the device of fav with the entry locked. A session that
// ended is opened again first, without a consent code; if consent is needed then, the error asks
// for kvm_connect with the code.
func (m *kvmManager) use(ctx context.Context, fav favorites.Favorite, fn func(e *kvmEntry) error) error {
	m.mu.Lock()
	closed, e := m.closed, m.entries[favKey(fav)]
	m.mu.Unlock()
	switch {
	case closed:
		return errShuttingDown
	case e == nil:
		return m.missing(fav)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.removed && !e.alive() {
		if err := m.awaitReady(ctx, e); err != nil {
			return err
		}
	}
	if e.removed {
		return m.missing(fav)
	}
	if r := e.rec; r != nil && r.mandatory && !r.running() { // failed; recordingFailed closes the session next
		return errRecordingFailed(fav, r.end(""))
	}
	if !e.alive() {
		e.release()
		c, warnings, err := m.open(ctx, fav, kvmOpen{})
		switch {
		case errors.Is(err, errConsentCodeNeeded):
			return &consentNeededError{tool: "kvm_connect", reopen: true}
		case err != nil:
			return &reopenError{err: err}
		}
		e.install(c, warnings, fav.UseTLS)
	}
	e.lastUsed = m.now()
	err := fn(e)
	e.lastUsed = m.now()
	return err
}

// remove takes e out of the manager; the caller holds e.mu.
func (m *kvmManager) remove(e *kvmEntry) {
	e.removed = true
	m.mu.Lock()
	if m.entries[e.key] == e {
		delete(m.entries, e.key)
	}
	m.mu.Unlock()
}

// expire closes e once it was not used for the idle timeout; a call in progress delays it. A
// recording the agent started keeps the session open while it records frames; one that waits
// for a tool call (consent, KVM off, refused login, see reopen) does not, and a mandatory one
// never does, so a session the agent forgot still closes.
func (m *kvmManager) expire(e *kvmEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return
	}
	if e.rec.running() && !e.rec.mandatory && e.alive() {
		e.lastUsed = m.now()
		e.timer.Reset(m.idle)
		return
	}
	if left := m.idle - m.now().Sub(e.lastUsed); left > 0 {
		e.timer.Reset(left)
		return
	}
	m.remove(e)
	// Keep the result of a recording the agent started for kvm_record_stop. A mandatory one
	// is the person's: its files stay in the capture folder.
	if res, ok := e.shut("the KVM session was closed after the idle timeout"); ok && !res.Mandatory {
		m.mu.Lock()
		m.kept[e.key] = res
		m.mu.Unlock()
	}
}

// takeKept returns the kept result of the device with key once (see kvmManager.kept).
func (m *kvmManager) takeKept(key string) (recordResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.kept[key]
	delete(m.kept, key)
	return res, ok
}

// disconnect closes and removes the session of the device of fav; why ends a running recording.
// It reports whether there was a session, and the result of the recording on it, if any.
// A mandatory recording that failed and closed the session before is returned once, too.
func (m *kvmManager) disconnect(fav favorites.Favorite, why string) (bool, *recordResult) {
	m.mu.Lock()
	e := m.entries[favKey(fav)]
	m.mu.Unlock()
	if e == nil {
		if res, ok := m.takeFailed(favKey(fav)); ok {
			return false, &res
		}
		return false, nil
	}
	e.cancelFeed()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return false, nil
	}
	m.remove(e)
	if res, ok := e.shut(why); ok {
		return true, &res
	}
	return true, nil
}

// stopRecording ends the recording of the device of fav and returns its result: a running one,
// one that ended by itself on the session, or the kept result of one whose session closed
// since. ok is false if there is none.
func (m *kvmManager) stopRecording(fav favorites.Favorite) (res recordResult, ok bool, err error) {
	key := favKey(fav)
	m.mu.Lock()
	closed, e := m.closed, m.entries[key]
	m.mu.Unlock()
	if closed {
		return recordResult{}, false, errShuttingDown
	}
	if e != nil {
		if r := e.live.Load(); r != nil && r.mandatory {
			return recordResult{}, false, errMandatoryStop
		}
		e.cancelFeed()
		e.mu.Lock()
		if !e.removed && e.rec != nil && !e.rec.mandatory {
			res = e.rec.end("stopped")
			e.setRec(nil)
			e.lastUsed = m.now()
			e.mu.Unlock()
			return res, true, nil
		}
		e.mu.Unlock()
	}
	res, ok = m.takeKept(key)
	return res, ok, nil
}

var errMandatoryStop = refuse("This session is recorded by the server configuration (capture.record_always); the recording ends with kvm_disconnect.")

// powerRunning reports whether a power action of the device with key runs now.
func (m *kvmManager) powerRunning(key string) bool { return m.powerDone(key) != nil }

// powerDone returns a channel that is closed when no power action of the device with key runs
// any more; nil if none runs now.
func (m *kvmManager) powerDone(key string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.powers[key]; p != nil {
		return p.done
	}
	return nil
}

// beginPower prepares a power action that AMT refuses while KVM is open. It marks the device,
// so a recording does not open KVM again until end is called (after the action returned); it
// stops a reopen of the recording that runs now and closes a running session. The entry stays,
// so the next KVM call opens the session again. closed reports whether a running session was
// closed. On an error (ctx ended) nothing is marked.
func (m *kvmManager) beginPower(ctx context.Context, fav favorites.Favorite) (closed bool, end func(), err error) {
	key := favKey(fav)
	m.mu.Lock()
	p := m.powers[key]
	if p == nil {
		p = &powerRun{done: make(chan struct{})}
		m.powers[key] = p
	}
	p.n++
	e := m.entries[key]
	m.mu.Unlock()
	end = sync.OnceFunc(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if p.n--; p.n == 0 {
			delete(m.powers, key)
			close(p.done)
		}
	})
	if e == nil {
		return false, end, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op := e.feedOpen; op != nil {
		op.cancel()
	}
	if err := e.awaitFeedOpen(ctx); err != nil {
		end()
		return false, nil, err
	}
	if e.removed || !e.alive() {
		return false, end, nil
	}
	e.release()
	_ = e.client.Close()
	e.client = nil
	return true, end, nil
}

// closeAll ends every session and refuses new ones (the client went away).
func (m *kvmManager) closeAll() {
	m.mu.Lock()
	m.closed = true
	entries := make([]*kvmEntry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	clear(m.entries)
	clear(m.kept)
	clear(m.failed)
	m.mu.Unlock()
	for _, e := range entries {
		e.cancelFeed()
	}
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Go(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.removed = true
			e.shut("the MCP server shut down")
		})
	}
	wg.Wait()
}
