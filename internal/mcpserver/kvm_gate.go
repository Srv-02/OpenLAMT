package mcpserver

import (
	"context"
	"image"
	"sync"
	"time"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
)

// The gate of capture.record_always. Every key, pointer and wake event of the KVM tools and
// every screen image they return passes kvmInput.pass at the moment it is sent: the session's
// mandatory recording runs, does not end, does not switch files, and has a picture of this very
// client. The changes of that state (closeInput, halt, a switch of files) take the gate's lock
// for writing, while an event holds it for reading across its check and its send, so no event
// slips through between the two. The tools never use the raw *kvm.Client for input or for the
// screen (TestKVMInputOnlyThroughTheGate checks that).

// recSwitchWait bounds how long input waits while a mandatory recording starts its next file at
// the size limit; a variable for tests.
var recSwitchWait = 5 * time.Second

// recGate is the part of a recording that decides whether input passes; see kvmInput.pass.
type recGate struct {
	mu        sync.RWMutex
	inputOff  bool          // closeInput: tool calls send nothing more; the release before the end still passes
	halted    bool          // the recording ends or ended (halt): nothing passes
	failed    bool          // with halted: the recording ended by itself (a write error, no next file)
	switching bool          // the recording starts its next file at the size limit (rollOver)
	changed   chan struct{} // closed and replaced when a switch ends or the recording halts
	// inputCtx ends at closeInput and at halt: tool calls that wait for the screen stop then.
	inputCtx    context.Context
	inputCancel context.CancelFunc
}

func (g *recGate) init() {
	g.changed = make(chan struct{})
	g.inputCtx, g.inputCancel = context.WithCancel(context.Background())
}

// signal wakes the events that wait for a change; the caller holds g.mu for writing.
func (g *recGate) signal() {
	close(g.changed)
	g.changed = make(chan struct{})
}

// closeInput lets no input of tool calls pass any more (kvm_disconnect, the server's Close),
// without e.mu: a call that types now stops at its next key. The recording goes on until the
// session ends, so the release of held keys and buttons is still sent and recorded.
func (r *recording) closeInput() {
	r.gate.mu.Lock()
	r.gate.inputOff = true
	r.gate.mu.Unlock()
	r.gate.inputCancel()
}

// halt closes the gate for good and stops the feed: the first step of end. failed: the
// recording ended by itself. It waits for an event in flight.
func (r *recording) halt(failed bool) {
	g := &r.gate
	g.mu.Lock()
	if !g.halted {
		g.halted, g.failed = true, failed
		g.signal()
	}
	g.mu.Unlock()
	g.inputCancel()
	r.cancel()
}

// inputClosed reports whether the gate lets no input of tool calls pass any more.
func (r *recording) inputClosed() bool {
	r.gate.mu.RLock()
	defer r.gate.mu.RUnlock()
	return r.gate.inputOff || r.gate.halted
}

// failedNow reports whether the recording ended by itself, also while end still runs.
func (r *recording) failedNow() bool {
	r.gate.mu.RLock()
	defer r.gate.mu.RUnlock()
	return r.gate.halted && r.gate.failed
}

// beginSwitch notes that the recording starts its next file; false if it ends already.
func (r *recording) beginSwitch() bool {
	r.gate.mu.Lock()
	defer r.gate.mu.Unlock()
	if r.gate.halted {
		return false
	}
	r.gate.switching = true
	return true
}

// endSwitch ends the switch that beginSwitch began at from: the recording goes on in nr after
// cur, or, with err, it ends (watch halts it next). Waiting input goes on. A switch that took a
// frame slot or more leaves a warning: the video has no picture of that time.
func (r *recording) endSwitch(cur, nr *capture.Recorder, err error, from time.Time) bool {
	r.gate.mu.Lock()
	defer r.gate.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gate.switching = false
	r.gate.signal()
	if err != nil {
		r.nextErr = err
		return false
	}
	r.earlier = append(r.earlier, fileResult(cur))
	r.recorder = nr
	if to := r.now(); to.Sub(from) >= time.Second/time.Duration(r.fps) {
		r.switchGap(from, to)
	}
	return true
}

// awaitSwitch waits until a switch of files that runs now has ended; end calls it after halt,
// so the file it finishes is the last one.
func (r *recording) awaitSwitch() {
	r.gate.mu.RLock()
	switching, changed := r.gate.switching, r.gate.changed
	r.gate.mu.RUnlock()
	if switching {
		<-changed
	}
}

type gateVerdict int

const (
	gatePass   gateVerdict = iota
	gateClosed             // the session ends: disconnect, idle timeout, Close
	gateFailed             // the recording ended by itself
	gateNotFed             // the recording has no picture of this client
	gateWait               // the recording starts its next file
)

// verdict says whether an event on client c (handoff h) passes now; the caller holds r.gate.mu.
// release: the release of held input before the session ends, which passes after closeInput.
func (r *recording) verdict(c *kvm.Client, h *kvmHandoff, release bool) gateVerdict {
	g := &r.gate
	switch {
	case g.halted && g.failed:
		return gateFailed
	case g.halted, g.inputOff && !release:
		return gateClosed
	case g.switching:
		return gateWait
	}
	select {
	case <-r.recorder.Halted(): // the file takes no more frames: the next one starts, or the recording fails (watch)
		return gateWait
	default:
	}
	if h == nil || h.c != c || !h.isFed() {
		return gateNotFed
	}
	return gatePass
}

// kvmInput is the client of a session as the KVM tools use it for input and for the screen.
// Under capture.record_always each event passes the gate of the recording r that ran on the
// session when the input was made (see pass); otherwise it goes straight to the client.
type kvmInput struct {
	c       *kvm.Client
	ctx     context.Context // of the tool call: ends a wait for a switch of files
	fav     favorites.Favorite
	strict  bool        // capture.record_always
	r       *recording  // nil: none
	h       *kvmHandoff // the handoff of c
	release bool        // the release of held input before the session ends (see verdict)
	onSent  func(k *kvmInput)
}

// input returns the client of e for input and screen reads of a tool call; the caller holds
// e.mu.
func (e *kvmEntry) input(ctx context.Context, fav favorites.Favorite) *kvmInput {
	return &kvmInput{c: e.client, ctx: ctx, fav: fav, strict: e.strict, r: e.rec, h: e.handoff.Load(), onSent: e.onSent}
}

// pass runs send, one event or screen read, if the gate lets it pass. Under
// capture.record_always it refuses without a running mandatory recording, after closeInput
// (but for a release), after halt, and without a picture of this client in the recording; while
// the recording starts its next file it waits up to recSwitchWait. The check and send run under
// the gate's read lock: no change of the recording falls between them.
func (k *kvmInput) pass(send func() error) error {
	if !k.strict {
		err := send()
		if err == nil && k.onSent != nil {
			k.onSent(k)
		}
		return err
	}
	r := k.r
	if r == nil || !r.mandatory {
		return errNotRecorded(k.fav)
	}
	var timeout <-chan time.Time
	for {
		r.gate.mu.RLock()
		v := r.verdict(k.c, k.h, k.release)
		if v == gatePass {
			err := send()
			if err == nil && k.onSent != nil {
				k.onSent(k)
			}
			r.gate.mu.RUnlock()
			return err
		}
		changed := r.gate.changed
		r.gate.mu.RUnlock()
		switch v {
		case gateClosed:
			return noSession(k.fav)
		case gateFailed:
			return errRecordingFailed(k.fav, r.end(""))
		case gateNotFed:
			return errNotFed(k.fav)
		}
		if timeout == nil {
			t := time.NewTimer(recSwitchWait)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case <-changed:
		case <-timeout:
			return errSwitchSlow(k.fav)
		case <-k.ctx.Done():
			return k.ctx.Err()
		}
	}
}

func errSwitchSlow(fav favorites.Favorite) error {
	return refuse("The recording of the KVM session of %q (capture.record_always) is starting its next file at the size limit and took longer than %v. "+
		"No KVM input runs unrecorded, so the rest of this call was not sent. Try again in a moment.", fav.DisplayName(), recSwitchWait)
}

func (k *kvmInput) KeyEvent(key kvm.Key, down bool) error {
	return k.pass(func() error { return k.c.KeyEvent(key, down) })
}

// SendKey makes kvmInput a typing.KeySender.
func (k *kvmInput) SendKey(sym uint32, down bool) error {
	return k.pass(func() error { return k.c.SendKey(sym, down) })
}

func (k *kvmInput) ReleaseAll() error { return k.pass(k.c.ReleaseAll) }

func (k *kvmInput) PointerEvent(x, y int, b kvm.Button) error {
	return k.pass(func() error { return k.c.PointerEvent(x, y, b) })
}

func (k *kvmInput) WakeScreen() error { return k.pass(k.c.WakeScreen) }

// Snapshot returns the screen, if the gate lets it pass.
func (k *kvmInput) Snapshot() (*image.RGBA, error) {
	var img *image.RGBA
	err := k.pass(func() error { img = k.c.Snapshot(); return nil })
	return img, err
}

// WaitFrame is kvm.Client.WaitFrame that ends early, under capture.record_always, when the gate
// lets no input of tool calls pass any more.
func (k *kvmInput) WaitFrame(ctx context.Context, after uint64) (uint64, error) {
	if k.strict && k.r != nil {
		wctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(k.r.gate.inputCtx, cancel)()
		ctx = wctx
	}
	return k.c.WaitFrame(ctx, after)
}

func (k *kvmInput) Frame() uint64         { return k.c.Frame() }
func (k *kvmInput) Size() (w, h int)      { return k.c.Size() }
func (k *kvmInput) Done() <-chan struct{} { return k.c.Done() }

// release lets go of held mouse buttons and keys, so nothing stays pressed on the host; the
// caller holds e.mu. It runs before the session ends, while its recording still runs: under
// capture.record_always it passes the gate also after closeInput, never after the recording
// ended.
func (e *kvmEntry) release() {
	if !e.alive() {
		return
	}
	in := e.input(context.Background(), favorites.Favorite{})
	in.release = true
	if e.buttons != 0 {
		_ = in.PointerEvent(e.lastX, e.lastY, 0)
	}
	e.buttons = 0
	_ = in.ReleaseAll()
}
