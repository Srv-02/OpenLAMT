package mcpserver

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// eventAudit checks each event and screen read that passed the gate, at the moment it was
// sent, against the state of the recording, read on its own: a mandatory recording that runs,
// whose feed was not stopped, whose file did not end, and that has a picture of this client.
type eventAudit struct {
	mu         sync.Mutex
	sent       int
	unrecorded []string
}

// auditEvents installs the audit on h; call it before kvm_connect (new entries take the hook).
func auditEvents(h *harness) *eventAudit {
	a := &eventAudit{}
	h.srv.s.kvm.onSent = func(k *kvmInput) {
		why := unrecordedWhy(k)
		a.mu.Lock()
		defer a.mu.Unlock()
		a.sent++
		if why != "" && len(a.unrecorded) < 20 {
			a.unrecorded = append(a.unrecorded, why)
		}
	}
	return a
}

func (a *eventAudit) counts() (sent int, unrecorded []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sent, a.unrecorded
}

// unrecordedWhy says why an event that passed the gate was not recorded; "" if it was.
func unrecordedWhy(k *kvmInput) string {
	r := k.r
	switch {
	case r == nil || !r.mandatory:
		return "no mandatory recording"
	case !r.running():
		return "the recording ended"
	case r.ctx.Err() != nil:
		return "the feed of the recording was stopped"
	case k.h == nil || k.h.c != k.c || !k.h.isFed():
		return "the recording had no picture of this client"
	}
	select {
	case <-r.current().Halted():
		return "the file of the recording takes no more frames"
	default:
	}
	return ""
}

// TestReleaseIsRecordedBeforeTheEnd (B3): when a session ends, held keys and buttons are
// released while its recording still runs, then the recording ends, then the client closes:
// the end of a drag is in the video. Before, the recording ended first and the release reached
// the device unrecorded. A mandatory recording that failed sends nothing more, not even the
// release.
func TestReleaseIsRecordedBeforeTheEnd(t *testing.T) {
	for _, how := range []string{"kvm_disconnect", "idle timeout", "server close", "record_start/kvm_disconnect", "recording failed"} {
		t.Run(how, func(t *testing.T) {
			var (
				h                  *harness
				srv                *kvmtest.Server
				files              = make(chan *os.File, 4)
				released, recorded atomic.Int32
			)
			onSent := func(k *kvmInput) {
				if k.release {
					released.Add(1)
					if r := k.r; r != nil && r.running() && r.ctx.Err() == nil { // mandatory or not
						recorded.Add(1)
					}
				}
			}
			clock, skew := skewClock()
			if how == "record_start/kvm_disconnect" {
				h, srv, _ = recHarness(t)
				e := h.kvmEntry("NUC")
				e.mu.Lock()
				e.onSent = onSent
				e.mu.Unlock()
				h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
			} else {
				h, srv, _ = alwaysHarness(t, redScreen())
				h.srv.s.kvm.onSent = onSent
				h.srv.s.kvm.now = clock
				h.srv.s.kvm.startRec = func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error) {
					files <- f
					return capture.StartRecorder(f, first, o)
				}
				h.mustOK("kvm_connect", dev("NUC"))
			}
			r := h.kvmEntry("NUC").live.Load()
			h.mustOK("kvm_mouse", dev("NUC", "action", "down", "x", 10, "y", 10))
			h.mustOK("kvm_mouse", dev("NUC", "action", "move", "x", 60, "y", 30))
			eventually(t, "the drag", func() bool { return len(pointerEvents(srv)) >= 3 })
			switch how {
			case "kvm_disconnect", "record_start/kvm_disconnect":
				h.mustOK("kvm_disconnect", dev("NUC"))
			case "idle timeout":
				skew.Store(int64(2 * time.Hour))
				h.srv.s.kvm.expire(h.kvmEntry("NUC"))
			case "server close":
				h.srv.Close()
			case "recording failed":
				(<-files).Close() // the disk went away: the next write fails
				eventually(t, "session closed", func() bool { return h.kvmEntry("NUC") == nil })
			}
			if r.running() {
				t.Fatal("the recording still runs")
			}
			if how == "recording failed" {
				time.Sleep(200 * time.Millisecond)
				if n := len(pointerEvents(srv)); n != 3 || released.Load() != 0 {
					t.Fatalf("%d pointer events, %d released through the gate: the release went out after the recording failed", n, released.Load())
				}
				return
			}
			if n, m := released.Load(), recorded.Load(); n == 0 || m != n {
				t.Fatalf("%d of %d release events were sent while the recording ran", m, n)
			}
			eventually(t, "the release of the button", func() bool {
				pe := pointerEvents(srv)
				last := pe[len(pe)-1]
				return len(pe) == 4 && last.mask == 0 && last.x == 60 && last.y == 30
			})
		})
	}
}

// TestRecordAlwaysChaosSendsNothingUnrecorded: kvm_connect, kvm_disconnect, power actions,
// sessions that AMT ends, typing, keys, clicks and screenshots, all at once for a few seconds.
// No event passes the gate that the recording does not show.
func TestRecordAlwaysChaosSendsNothingUnrecorded(t *testing.T) {
	oldD, oldM := recReopenDelay, recReopenMax
	recReopenDelay, recReopenMax = 20*time.Millisecond, 80*time.Millisecond
	t.Cleanup(func() { recReopenDelay, recReopenMax = oldD, oldM })
	h, _, _ := alwaysHarness(t, redScreen())
	a := auditEvents(h)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	seed := uint64(0)
	worker := func(f func(rng *rand.Rand)) {
		seed++
		rng := rand.New(rand.NewPCG(205, seed))
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				f(rng)
			}
		})
	}
	pause := func(rng *rand.Rand, ms int) { time.Sleep(time.Duration(rng.IntN(ms)) * time.Millisecond) }
	worker(func(*rand.Rand) { h.call("kvm_connect", dev("NUC")) })
	worker(func(rng *rand.Rand) { pause(rng, 200); h.call("kvm_disconnect", dev("NUC")) })
	worker(func(rng *rand.Rand) { pause(rng, 150); h.call("power_action", dev("NUC", "action", "cycle")) })
	worker(func(rng *rand.Rand) {
		pause(rng, 80)
		if e := h.kvmEntry("NUC"); e != nil { // AMT ends the session
			e.mu.Lock()
			c := e.client
			e.mu.Unlock()
			if c != nil {
				_ = c.Close()
			}
		}
	})
	worker(func(*rand.Rand) { h.call("kvm_type_text", dev("NUC", "text", "abcd")) })
	worker(func(*rand.Rand) { h.call("kvm_send_keys", dev("NUC", "keys", []string{"Tab"})) })
	worker(func(rng *rand.Rand) {
		h.call("kvm_mouse", dev("NUC", "action", "click", "x", rng.IntN(128), "y", rng.IntN(64)))
	})
	worker(func(*rand.Rand) { h.call("kvm_screenshot", dev("NUC")) })
	time.Sleep(3 * time.Second)
	close(stop)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		buf := make([]byte, 1<<20)
		t.Fatalf("the calls did not return\n%s", buf[:runtime.Stack(buf, true)])
	}
	h.call("kvm_disconnect", dev("NUC"))
	sent, unrecorded := a.counts()
	if len(unrecorded) > 0 {
		t.Fatalf("%d events or screen reads passed the gate unrecorded (of %d), e.g. %q", len(unrecorded), sent, unrecorded)
	}
	if sent == 0 {
		t.Fatal("no event passed the gate")
	}
	t.Logf("%d events and screen reads passed the gate, all recorded", sent)
}

// TestKVMInputOnlyThroughTheGate: in the package code, input and screen reads of a KVM client
// go only through kvmInput (kvm_gate.go). A call of an input or screen method elsewhere is on a
// variable declared as *kvmInput; the recording itself takes its frames with SnapshotInto.
func TestKVMInputOnlyThroughTheGate(t *testing.T) {
	gated := map[string]bool{"KeyEvent": true, "SendKey": true, "PointerEvent": true, "ReleaseAll": true,
		"WakeScreen": true, "CtrlAltDel": true, "Snapshot": true, "SnapshotInto": true}
	recordingFrames := map[string]bool{"feed": true, "kvmRecordStart": true, "startMandatory": true}
	isInput := func(x ast.Expr) bool {
		id, ok := x.(*ast.Ident)
		if !ok || id.Obj == nil {
			return false
		}
		f, ok := id.Obj.Decl.(*ast.Field)
		if !ok {
			return false
		}
		star, ok := f.Type.(*ast.StarExpr)
		if !ok {
			return false
		}
		name, ok := star.X.(*ast.Ident)
		return ok && name.Name == "kvmInput"
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "kvm_gate.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "typing" && sel.Sel.Name == "Type" {
					checked++
					if len(call.Args) < 2 || !isInput(call.Args[1]) {
						t.Errorf("%s: typing.Type in %s types on something other than a *kvmInput", fset.Position(call.Pos()), fd.Name.Name)
					}
					return true
				}
				if !gated[sel.Sel.Name] {
					return true
				}
				checked++
				switch {
				case isInput(sel.X):
				case sel.Sel.Name == "SnapshotInto" && recordingFrames[fd.Name.Name]:
				default:
					t.Errorf("%s: %s in %s does not go through the gate (kvmInput)", fset.Position(call.Pos()), sel.Sel.Name, fd.Name.Name)
				}
				return true
			})
		}
	}
	if checked < 8 {
		t.Fatalf("only %d calls checked: the test does not see the KVM tools", checked)
	}
}

// BenchmarkGatePass measures what the gate adds to an event: the check under the read lock.
func BenchmarkGatePass(b *testing.B) {
	f, err := capture.CreateUnique(b.TempDir(), "bench.avi")
	if err != nil {
		b.Fatal(err)
	}
	rc, err := capture.StartRecorder(f, image.NewRGBA(image.Rect(0, 0, 64, 64)), capture.RecorderOptions{})
	if err != nil {
		b.Fatal(err)
	}
	r := newRecording(rc, capture.DefaultFPS)
	r.mandatory = true
	defer r.end("")
	h := newHandoff(nil)
	h.markFed()
	k := &kvmInput{ctx: context.Background(), strict: true, r: r, h: h}
	send := func() error { return nil }
	b.Run("one", func(b *testing.B) {
		for b.Loop() {
			_ = k.pass(send)
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = k.pass(send)
			}
		})
	})
}
