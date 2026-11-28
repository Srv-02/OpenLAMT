package mcpserver

import (
	"context"
	"errors"
	"image"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// inputAudit checks every KVM tool call that runs on the session of NUC (step "input" of
// kvmManager.use, with the entry locked): whether a mandatory recording runs on the entry, and
// whether the recording still misses the session (its gap is open: the video shows the old
// picture while the call sends input).
type inputAudit struct {
	mu         sync.Mutex
	calls      int
	unrecorded int // calls without a running mandatory recording
	inGap      int // calls while the recording had no picture of the session
}

// auditInput installs the audit on h; onPower gets the other steps (the "power" waits).
func auditInput(h *harness, onPower func(step string)) *inputAudit {
	a := &inputAudit{}
	h.srv.s.kvm.onCall = func(step string) {
		if step != "input" {
			if onPower != nil {
				onPower(step)
			}
			return
		}
		e := h.kvmEntry("NUC") // the caller holds e.mu: e.rec is safe to read
		a.mu.Lock()
		defer a.mu.Unlock()
		a.calls++
		if e == nil {
			a.unrecorded++
			return
		}
		r := e.rec
		if r == nil || !r.mandatory || !r.running() {
			a.unrecorded++
		}
		if r != nil && !a.fed(e, r) {
			a.inGap++
		}
	}
	return a
}

// fed reports whether the recording r of e had a picture of the session's client before the
// call: no gap is open, and its feed offered a frame of this very client to the recorder.
func (a *inputAudit) fed(e *kvmEntry, r *recording) bool {
	r.mu.Lock()
	gap := r.gap != nil
	r.mu.Unlock()
	h := e.handoff.Load()
	return !gap && h != nil && h.c == e.client && h.isFed()
}

func (a *inputAudit) counts() (calls, unrecorded, inGap int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, a.unrecorded, a.inGap
}

// TestRecordAlwaysRefusesInputBeforeTheRecordingStarts: with capture.record_always, a KVM tool
// call never runs on a session without its recording. kvm_connect waits for a power action with
// its entry in the manager, a kvm_type_text waits too; whoever gets the entry first after the
// power action, the text is typed only on a recorded session, or the call is refused (F1 of the
// review of #205: without the check the tool call opened KVM itself and typed unrecorded).
func TestRecordAlwaysRefusesInputBeforeTheRecordingStarts(t *testing.T) {
	const runs = 20
	refused, typedUnrecorded := 0, 0
	for i := range runs {
		func() {
			h, srv, _ := alwaysHarness(t, redScreen())
			defer h.srv.Close()
			f := h.devices["192.0.2.10"]
			waits := make(chan string, 16)
			a := auditInput(h, func(step string) { waits <- step })
			gate := make(chan struct{})
			f.PowerGate = gate
			endPower := sync.OnceFunc(func() { close(gate) })
			defer endPower()
			power := make(chan string, 1)
			go func() { text, _, _ := h.call("power_action", dev("NUC", "action", "cycle")); power <- text }()
			eventually(t, "power command", f.PowerStarted)
			conn := make(chan bool, 1)
			go func() { _, _, isErr := h.call("kvm_connect", dev("NUC")); conn <- isErr }()
			<-waits // kvm_connect waits for the power action, its entry in the manager
			type result struct {
				text  string
				isErr bool
			}
			typed := make(chan result, 1)
			go func() {
				text, _, isErr := h.call("kvm_type_text", dev("NUC", "text", strings.Repeat("y", 20)))
				typed <- result{text, isErr}
			}()
			select { // the tool call waits too, or is refused at once
			case <-waits:
			case r := <-typed:
				typed <- r
			}
			endPower()
			<-power
			r := <-typed
			if isErr := <-conn; isErr {
				t.Fatalf("run %d: kvm_connect failed", i)
			}
			calls, unrecorded, _ := a.counts()
			if unrecorded > 0 {
				typedUnrecorded++
				t.Errorf("run %d: %d of %d tool calls ran without a recording; %d key events reached the device", i, unrecorded, calls, len(keyEvents(srv)))
			}
			switch {
			case r.isErr && !strings.Contains(r.text, "has no recording yet"):
				t.Fatalf("run %d: kvm_type_text: %s", i, r.text)
			case r.isErr:
				refused++
			}
		}()
	}
	t.Logf("%d of %d runs: kvm_type_text refused (no recording yet), %d typed without a recording", refused, runs, typedUnrecorded)
}

// TestRecordAlwaysToolCallDuringFirstConnect: tool calls that race the first kvm_connect of a
// device never run on its session before the recording runs (the window between the new entry
// in the manager and its lock).
func TestRecordAlwaysToolCallDuringFirstConnect(t *testing.T) {
	h, _, _ := alwaysHarness(t, redScreen())
	fav, err := h.srv.s.lookup("NUC")
	if err != nil {
		t.Fatal(err)
	}
	var unrecorded atomic.Int32
	const connects = 100
	for range connects {
		var wg sync.WaitGroup
		wg.Go(func() { h.call("kvm_connect", dev("NUC")) })
		for range 4 {
			wg.Go(func() {
				for range 50 {
					_ = h.srv.s.kvm.use(t.Context(), fav, func(e *kvmEntry) error {
						if r := e.rec; r == nil || !r.mandatory || !r.running() {
							unrecorded.Add(1)
						}
						return nil
					})
				}
			})
		}
		wg.Wait()
		h.mustOK("kvm_disconnect", dev("NUC"))
	}
	if n := unrecorded.Load(); n > 0 {
		t.Fatalf("%d calls ran on a session without its recording in %d connects", n, connects)
	}
}

// TestRecordingHasThePictureBeforeInputAfterAReopen: when a KVM tool call opens a session again
// that AMT ended or a power action closed, the recording feeds from the new session before the
// call sends input, so the video shows what the input does (F2 of the review of #205). It holds
// for the recording of the server configuration and for one the agent started. The gap warning
// names the power action as the reason, however fast the action returned (F6).
func TestRecordingHasThePictureBeforeInputAfterAReopen(t *testing.T) {
	old := recReopenDelay
	recReopenDelay = 20 * time.Second // the recording never opens the session itself here
	t.Cleanup(func() { recReopenDelay = old })
	for _, mandatory := range []bool{true, false} {
		for _, cause := range []string{"AMT", "power"} {
			name := map[bool]string{true: "record_always", false: "kvm_record_start"}[mandatory] + "/" + cause
			t.Run(name, func(t *testing.T) {
				var (
					h   *harness
					srv *kvmtest.Server
					p   *feedProbe
					a   *inputAudit
				)
				if mandatory {
					h, srv, _ = alwaysHarness(t, redScreen())
					p, a = probeFeed(h), auditInput(h, nil)
					h.mustOK("kvm_connect", dev("NUC"))
				} else {
					h, srv, _ = recHarness(t)
					p, a = probeFeed(h), auditInput(h, nil)
					h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
				}
				r := h.kvmEntry("NUC").live.Load()
				eventually(t, "frames", func() bool { return r.current().Frames() >= 2 })
				if cause == "AMT" {
					dropKVM(h)
				} else {
					h.mustOK("power_action", dev("NUC", "action", "cycle"))
				}
				p.wait("lost")
				before := len(keyEvents(srv))
				text := strings.Repeat("abcdefghij", 10)
				h.mustOK("kvm_type_text", dev("NUC", "text", text))
				calls, _, inGap := a.counts()
				if inGap > 0 {
					t.Errorf("%d of %d tool calls sent input while the recording still showed the old picture", inGap, calls)
				}
				if n := len(keyEvents(srv)) - before; n != 2*len(text) {
					t.Fatalf("%d key events, want %d", n, 2*len(text))
				}
				var res map[string]any
				if mandatory {
					res, _ = h.mustOK("kvm_disconnect", dev("NUC"))["recording"].(map[string]any)
				} else {
					res = h.mustOK("kvm_record_stop", dev("NUC"))
				}
				w := warningsOf(res)
				want := map[string]string{"AMT": "(" + whyAMT + ")", "power": "(" + whyPower + ")"}[cause]
				if !strings.Contains(w, want) || strings.Contains(w, "until the recording ended") {
					t.Fatalf("warnings %q, want %q", w, want)
				}
				readAVI(t, res["path"].(string))
			})
		}
	}
}

// TestKVMIdleTickKeepsRecordingDuringReconnect: a recording the agent started keeps its session
// across a short reconnect (AMT ended the session, the recording tries again by itself): a
// regular idle tick during that retry does not close it (F3 of the review of #205).
func TestKVMIdleTickKeepsRecordingDuringReconnect(t *testing.T) {
	fastReopen(t)
	h, _, _ := recHarness(t)
	clock, skew := skewClock()
	h.srv.s.kvm.now = clock
	p := probeFeed(h)
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
	e := h.kvmEntry("NUC")
	skew.Store(int64(3 * time.Hour))
	h.srv.s.kvm.expire(e) // records frames: the session stays
	release := p.hold("delay")
	dropKVM(h)
	p.wait("delay") // the recording waits to try again: not for a tool call
	skew.Store(int64(4 * time.Hour))
	h.srv.s.kvm.expire(e) // the next tick, one idle timeout later
	if h.kvmEntry("NUC") != e {
		t.Fatal("the idle tick closed the session of a recording that reconnects by itself")
	}
	release()
	p.wait("resumed")
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	if out["path"] != start["path"] || out["ended_by"] != "stopped" {
		t.Fatalf("%v", out)
	}
}

// TestKVMRecordStopNeverCancelsTheMandatoryFeed: kvm_record_stop calls that race kvm_connect
// never stop the feed of the recording of the server configuration (F4).
func TestKVMRecordStopNeverCancelsTheMandatoryFeed(t *testing.T) {
	h, _, _ := alwaysHarness(t, redScreen())
	fav, err := h.srv.s.lookup("NUC")
	if err != nil {
		t.Fatal(err)
	}
	const connects = 100
	hits := 0
	for range connects {
		var stop atomic.Bool
		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() {
				for !stop.Load() {
					_, _, _ = h.srv.s.kvm.stopRecording(fav)
				}
			})
		}
		h.mustOK("kvm_connect", dev("NUC"))
		stop.Store(true)
		wg.Wait()
		if r := h.kvmEntry("NUC").live.Load(); r != nil && r.running() && r.ctx.Err() != nil {
			hits++
		}
		h.mustOK("kvm_disconnect", dev("NUC"))
	}
	if hits > 0 {
		t.Fatalf("the feed of a running mandatory recording was stopped in %d of %d connects", hits, connects)
	}
}

// TestRecordAlwaysRollOverFailureClosesSession: at the size limit the next file cannot start:
// the session closes, the KVM tools say why, kvm_disconnect returns the result once (#205, fail
// closed).
func TestRecordAlwaysRollOverFailureClosesSession(t *testing.T) {
	h, _, _ := alwaysHarness(t, redScreen())
	var n atomic.Int32
	h.srv.s.kvm.startRec = func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error) {
		if n.Add(1) > 1 {
			f.Close()
			os.Remove(f.Name())
			return nil, errors.New("disk full")
		}
		o.FPS, o.Limit = 100, 3000
		return capture.StartRecorder(f, first, o)
	}
	out := h.mustOK("kvm_connect", dev("NUC"))
	p := out["recording"].(string)
	e := h.kvmEntry("NUC")
	e.mu.Lock()
	c := e.client
	e.mu.Unlock()
	eventually(t, "session closed", func() bool { return h.kvmEntry("NUC") == nil })
	select {
	case <-c.Done():
	default:
		t.Fatal("the KVM client still runs")
	}
	text := h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{"Enter"}), "was closed because its recording failed")
	if !strings.Contains(text, "next file could not be started: disk full") || !strings.Contains(text, p) {
		t.Fatalf("%q", text)
	}
	dis := h.mustOK("kvm_disconnect", dev("NUC"))
	rec, _ := dis["recording"].(map[string]any)
	if dis["closed"] != false || rec["path"] != p || !strings.Contains(rec["ended_by"].(string), "next file could not be started") {
		t.Fatalf("%v", dis)
	}
	if _, ok := rec["earlier_files"]; ok {
		t.Fatalf("earlier files without a second file: %v", rec)
	}
	readAVI(t, p)
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
}

// TestRecordAlwaysUseRefusesAfterTheRecordingEnded: a mandatory recording that ended before its
// session was closed (recordingFailed runs next) refuses every KVM call and names the result.
func TestRecordAlwaysUseRefusesAfterTheRecordingEnded(t *testing.T) {
	h, srv, _ := alwaysHarness(t, redScreen())
	out := h.mustOK("kvm_connect", dev("NUC"))
	e := h.kvmEntry("NUC")
	e.mu.Lock()
	r := e.rec
	r.end("a write error: test") // as watch does, without closing the session yet
	e.mu.Unlock()
	before := len(keyEvents(srv))
	text := h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{"Enter"}), "was closed because its recording failed")
	if !strings.Contains(text, "a write error: test") || !strings.Contains(text, out["recording"].(string)) {
		t.Fatalf("%q", text)
	}
	h.mustFail("kvm_screenshot", dev("NUC"), "was closed because its recording failed")
	if n := len(keyEvents(srv)) - before; n != 0 {
		t.Fatalf("%d key events after the recording ended", n)
	}
}

// TestKVMConnectGivesUpWhileAPowerActionRuns: kvm_connect that waits for a power action and
// whose call ends meanwhile opens nothing; a new entry leaves the manager, an existing one stays
// for the next call.
func TestKVMConnectGivesUpWhileAPowerActionRuns(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new entry", true: "existing entry"}[existing], func(t *testing.T) {
			h, srv, _ := alwaysHarness(t, redScreen())
			f := h.devices["192.0.2.10"]
			if existing {
				h.mustOK("kvm_connect", dev("NUC"))
			}
			waits := make(chan string, 16)
			h.srv.s.kvm.onCall = func(step string) {
				if step == "power" {
					waits <- step
				}
			}
			gate := make(chan struct{})
			f.PowerGate = gate
			endPower := sync.OnceFunc(func() { close(gate) })
			t.Cleanup(endPower)
			go h.call("power_action", dev("NUC", "action", "cycle"))
			eventually(t, "power command", f.PowerStarted)
			conns := srv.Connections()
			fav, err := h.srv.s.lookup("NUC")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			ran := false
			go func() {
				done <- h.srv.s.kvm.connect(ctx, fav, "", false, func(*kvmEntry, bool) error { ran = true; return nil })
			}()
			<-waits
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) || ran {
				t.Fatalf("connect: %v, fn ran %v", err, ran)
			}
			if n := srv.Connections(); n != conns {
				t.Fatalf("%d KVM connections, want %d", n, conns)
			}
			if e := h.kvmEntry("NUC"); (e != nil) != existing {
				t.Fatalf("entry %v", e)
			}
			endPower()
		})
	}
}

// TestRecordingWithoutPictureOfTheReopenedSession: a session opened again that sends no picture
// within firstFrameWait. The recording of the server configuration refuses the call and nothing
// is sent (fail closed); one the agent started lets the call go on and its result says so.
func TestRecordingWithoutPictureOfTheReopenedSession(t *testing.T) {
	old, oldDelay := firstFrameWait, recReopenDelay
	firstFrameWait, recReopenDelay = 100*time.Millisecond, 20*time.Second
	t.Cleanup(func() { firstFrameWait, recReopenDelay = old, oldDelay })
	once := kvmtest.Options{Width: 128, Height: 64, Updates: [][]byte{solidUpdate(128, 64, red565)}} // the first connection only
	for _, mandatory := range []bool{true, false} {
		t.Run(map[bool]string{true: "record_always", false: "kvm_record_start"}[mandatory], func(t *testing.T) {
			var (
				h   *harness
				srv *kvmtest.Server
			)
			if mandatory {
				h, srv, _ = alwaysHarness(t, once)
				h.mustOK("kvm_connect", dev("NUC"))
			} else {
				h = newHarness(t)
				f := h.addDevice("NUC", "192.0.2.10", false)
				srv = addKVM(t, f, once)
				h.cfg.KVMIdleTimeout = time.Hour
				h.start()
				h.mustOK("kvm_connect", dev("NUC"))
				h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
			}
			dropKVM(h)
			before := len(keyEvents(srv))
			if mandatory {
				h.mustFail("kvm_type_text", dev("NUC", "text", "abc"), "had no picture of it within 100ms")
				if n := len(keyEvents(srv)) - before; n != 0 {
					t.Fatalf("%d key events without a picture in the recording", n)
				}
				h.mustFail("kvm_screenshot", dev("NUC"), "nothing was sent and no screen was read")
				if n := srv.Connections(); n != 2 {
					t.Fatalf("%d KVM connections, want 2 (the reopened session is kept)", n)
				}
				res, _ := h.mustOK("kvm_disconnect", dev("NUC"))["recording"].(map[string]any)
				readAVI(t, res["path"].(string))
				return
			}
			h.mustOK("kvm_type_text", dev("NUC", "text", "abc"))
			if n := len(keyEvents(srv)) - before; n != 6 {
				t.Fatalf("%d key events, want 6", n)
			}
			h.mustOK("kvm_type_text", dev("NUC", "text", "d"))
			out := h.mustOK("kvm_record_stop", dev("NUC"))
			if w := warningsOf(out); !strings.Contains(w, "2 KVM calls ran after KVM was opened again, before the recording had a picture of the new session") {
				t.Fatalf("warnings %q", w)
			}
		})
	}
}

// TestRecordAlwaysFailureStopsRunningInput: a mandatory recording that fails while a call types
// stops the typing at once: no input goes on after the recording ended.
func TestRecordAlwaysFailureStopsRunningInput(t *testing.T) {
	h, srv, _ := alwaysHarness(t, redScreen())
	files := make(chan *os.File, 4)
	h.srv.s.kvm.startRec = func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error) {
		files <- f
		return capture.StartRecorder(f, first, o)
	}
	h.mustOK("kvm_connect", dev("NUC"))
	r := h.kvmEntry("NUC").live.Load()
	text := strings.Repeat("k", 200) // about 6 s at the typing delay
	done := make(chan string, 1)
	go func() {
		text, _, _ := h.call("kvm_type_text", dev("NUC", "text", text))
		done <- text
	}()
	eventually(t, "typing", func() bool { return len(keyEvents(srv)) >= 10 })
	(<-files).Close() // the disk went away: the next write fails
	select {
	case <-r.ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the recording did not end")
	}
	atEnd := len(keyEvents(srv))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("kvm_type_text goes on after the recording ended")
	}
	if n := len(keyEvents(srv)); n >= 2*len(text) || n > atEnd+2 {
		t.Fatalf("%d key events, %d when the recording ended, of %d", n, atEnd, 2*len(text))
	}
	eventually(t, "session closed", func() bool { return h.kvmEntry("NUC") == nil })
}
