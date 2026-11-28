package mcpserver

import (
	"context"
	"errors"
	"image"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// Regression tests for the gate of capture.record_always (kvm_gate.go): no KVM input reaches
// the device and no screen is returned while the mandatory recording does not run or has no
// picture of the client. Each test counts the events that reach the fake device after the
// recording stopped showing the session.

// wakeKeys counts the F15 presses (the wake key of kvm_connect) the fake received.
func wakeKeys(srv *kvmtest.Server) int {
	n := 0
	for _, m := range srv.ClientMessages() {
		if m[0] == 4 && m[1] == 1 && m[4] == 0 && m[5] == 0 && m[6] == 0xff && m[7] == 0xcc {
			n++
		}
	}
	return n
}

// awaitInputClosed waits until kvm_disconnect, started on another goroutine, has closed the
// input of r and waits for the entry. It runs in a tool call, not on the test goroutine.
func awaitInputClosed(t *testing.T, r *recording) {
	deadline := time.Now().Add(3 * time.Second)
	for !r.inputClosed() {
		if time.Now().After(deadline) {
			t.Error("kvm_disconnect never closed the input")
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRecordAlwaysRecordingFailsWhileACallReopens (B1): the mandatory recording fails (a write
// error) while a tool call opens the KVM session again that AMT ended. The call sends nothing
// and returns no screen; before the gate it typed 60 key events, clicked 3 pointer events, sent
// 4 key events and returned the screen, all unrecorded.
func TestRecordAlwaysRecordingFailsWhileACallReopens(t *testing.T) {
	for _, tool := range []string{"kvm_type_text", "kvm_screenshot", "kvm_mouse", "kvm_send_keys"} {
		t.Run(tool, func(t *testing.T) {
			old := recReopenDelay
			recReopenDelay = 20 * time.Second // only the tool call opens the session again
			t.Cleanup(func() { recReopenDelay = old })
			h, srv, _ := alwaysHarness(t, redScreen())
			files := make(chan *os.File, 4)
			h.srv.s.kvm.startRec = func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error) {
				files <- f
				return capture.StartRecorder(f, first, o)
			}
			h.mustOK("kvm_connect", dev("NUC"))
			r := h.kvmEntry("NUC").live.Load()
			f := <-files
			orig := h.srv.s.kvm.open
			var opens atomic.Int32
			h.srv.s.kvm.open = func(ctx context.Context, fav favorites.Favorite, o kvmOpen) (*kvm.Client, []string, error) {
				if !o.passive && opens.Add(1) == 1 {
					f.Close() // the disk goes away while the call opens KVM again
					select {
					case <-r.ended:
					case <-time.After(3 * time.Second):
						t.Error("the recording did not end")
					}
				}
				return orig(ctx, fav, o)
			}
			p := probeFeed(h)
			dropKVM(h)
			p.wait("lost")
			before := len(inputEvents(srv))
			args := map[string]map[string]any{
				"kvm_type_text":  dev("NUC", "text", strings.Repeat("z", 30)),
				"kvm_screenshot": dev("NUC"),
				"kvm_mouse":      dev("NUC", "action", "click", "x", 5, "y", 5),
				"kvm_send_keys":  dev("NUC", "keys", []string{"Enter", "Enter"}),
			}[tool]
			text, _, isErr := h.call(tool, args)
			time.Sleep(300 * time.Millisecond) // the fake reads what was sent on its own
			if n := len(inputEvents(srv)) - before; n != 0 {
				t.Errorf("%d input events reached the device after the recording failed", n)
			}
			if !isErr || !strings.Contains(text, "was closed because its recording failed") {
				t.Fatalf("%s after the recording failed: error %v, %.200q", tool, isErr, text)
			}
		})
	}
}

// TestRecordAlwaysDisconnectWhileACallReopens (B2): kvm_disconnect while a tool call opens the
// session again. Nothing of the call reaches the device: before the gate 1 or 2 key events did
// in 40 of 40 runs, after the recording's feed was cancelled.
func TestRecordAlwaysDisconnectWhileACallReopens(t *testing.T) {
	old := recReopenDelay
	recReopenDelay = 20 * time.Second
	t.Cleanup(func() { recReopenDelay = old })
	const runs = 10
	leaked := 0
	for i := range runs {
		func() {
			h, srv, _ := alwaysHarness(t, redScreen())
			defer h.srv.Close()
			h.mustOK("kvm_connect", dev("NUC"))
			r := h.kvmEntry("NUC").live.Load()
			orig := h.srv.s.kvm.open
			var opens atomic.Int32
			disc := make(chan struct{})
			h.srv.s.kvm.open = func(ctx context.Context, fav favorites.Favorite, o kvmOpen) (*kvm.Client, []string, error) {
				if o.passive || opens.Add(1) != 1 {
					return orig(ctx, fav, o)
				}
				go func() { h.call("kvm_disconnect", dev("NUC")); close(disc) }()
				awaitInputClosed(t, r)
				c, w, err := orig(ctx, fav, o)
				if err == nil {
					_, _ = c.WaitFrame(ctx, 0) // the new client has its first picture
				}
				return c, w, err
			}
			p := probeFeed(h)
			dropKVM(h)
			p.wait("lost")
			before := len(inputEvents(srv))
			text, _, isErr := h.call("kvm_type_text", dev("NUC", "text", strings.Repeat("w", 20)))
			<-disc
			time.Sleep(100 * time.Millisecond)
			if n := len(inputEvents(srv)) - before; n > 0 {
				leaked++
				t.Errorf("run %d: %d input events reached the device after kvm_disconnect closed the input", i, n)
			}
			if !isErr || !strings.Contains(text, "call kvm_connect first") {
				t.Fatalf("run %d: kvm_type_text: error %v, %.200q", i, isErr, text)
			}
		}()
	}
	t.Logf("%d of %d runs sent input after kvm_disconnect", leaked, runs)
}

// TestRecordAlwaysDisconnectWhileKVMConnectReopens (B2, wake keys): kvm_disconnect while
// kvm_connect opens the session again; the wake keys of kvm_connect never reach the device.
func TestRecordAlwaysDisconnectWhileKVMConnectReopens(t *testing.T) {
	old := recReopenDelay
	recReopenDelay = 20 * time.Second
	t.Cleanup(func() { recReopenDelay = old })
	const runs = 5
	for i := range runs {
		func() {
			h, srv, _ := alwaysHarness(t, redScreen())
			defer h.srv.Close()
			h.mustOK("kvm_connect", dev("NUC"))
			r := h.kvmEntry("NUC").live.Load()
			orig := h.srv.s.kvm.open
			var opens atomic.Int32
			disc := make(chan struct{})
			h.srv.s.kvm.open = func(ctx context.Context, fav favorites.Favorite, o kvmOpen) (*kvm.Client, []string, error) {
				if o.passive || opens.Add(1) != 1 {
					return orig(ctx, fav, o)
				}
				go func() { h.call("kvm_disconnect", dev("NUC")); close(disc) }()
				awaitInputClosed(t, r)
				c, w, err := orig(ctx, fav, o)
				if err == nil {
					_, _ = c.WaitFrame(ctx, 0)
				}
				return c, w, err
			}
			p := probeFeed(h)
			eventually(t, "the wake keys of the first connect", func() bool { return wakeKeys(srv) >= 2 })
			dropKVM(h)
			p.wait("lost")
			before := wakeKeys(srv)
			h.call("kvm_connect", dev("NUC"))
			<-disc
			time.Sleep(wakeRepeat + 200*time.Millisecond) // both wake keys would be out by now
			if n := wakeKeys(srv) - before; n != 0 {
				t.Errorf("run %d: %d wake keys reached the device after kvm_disconnect closed the input", i, n)
			}
		}()
	}
}

// TestRecordAlwaysInputWaitsForTheNextFile (B4): at the size limit the mandatory recording
// starts its next file; input waits meanwhile, so no key falls between the files (before the
// gate 40 key events did, without a warning). If the next file cannot start, nothing more is
// sent (before: 38 key events that are in no file at all).
func TestRecordAlwaysInputWaitsForTheNextFile(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "slow next file", true: "next file fails"}[fail], func(t *testing.T) {
			h, srv, _ := alwaysHarness(t, redScreen())
			var (
				files     atomic.Int32
				during    atomic.Int32
				atFailure atomic.Int32
			)
			h.srv.s.kvm.startRec = func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error) {
				if files.Add(1) == 2 {
					time.Sleep(100 * time.Millisecond) // keys sent before the switch reach the fake
					k0 := len(keyEvents(srv))
					time.Sleep(500 * time.Millisecond)
					k1 := len(keyEvents(srv))
					during.Store(int32(k1 - k0))
					if fail {
						atFailure.Store(int32(k1))
						f.Close()
						os.Remove(f.Name())
						return nil, errors.New("disk full")
					}
				}
				o.FPS, o.Limit = 100, 12000
				return capture.StartRecorder(f, first, o)
			}
			h.mustOK("kvm_connect", dev("NUC"))
			text := strings.Repeat("q", 200)
			out, _, isErr := h.call("kvm_type_text", dev("NUC", "text", text))
			if files.Load() < 2 {
				t.Fatalf("the recording never reached its size limit (%d files)", files.Load())
			}
			if n := during.Load(); n != 0 {
				t.Errorf("%d key events reached the device while the recording started its next file", n)
			}
			if fail {
				time.Sleep(300 * time.Millisecond)
				if n := len(keyEvents(srv)) - int(atFailure.Load()); n != 0 {
					t.Errorf("%d key events reached the device after the next file failed", n)
				}
				if !isErr || !strings.Contains(out, "was closed because its recording failed") || !strings.Contains(out, "disk full") {
					t.Fatalf("kvm_type_text: error %v, %.300q", isErr, out)
				}
				return
			}
			if isErr {
				t.Fatalf("kvm_type_text: %s", out)
			}
			eventually(t, "all key events", func() bool { return len(keyEvents(srv)) >= 2*len(text) })
			res, _ := h.mustOK("kvm_disconnect", dev("NUC"))["recording"].(map[string]any)
			if w := warningsOf(res); !strings.Contains(w, "started its next file at the size limit") || !strings.Contains(w, "no KVM input was sent meanwhile") {
				t.Fatalf("warnings %q", w)
			}
			if earlier, _ := res["earlier_files"].([]any); len(earlier) == 0 {
				t.Fatalf("no earlier files: %v", res)
			}
		})
	}
}
