package mcpserver

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
)

// fastReopen makes a recording reopen its session after 1 ms instead of seconds.
func fastReopen(t *testing.T) {
	t.Helper()
	oldDelay, oldMax := recReopenDelay, recReopenMax
	recReopenDelay, recReopenMax = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { recReopenDelay, recReopenMax = oldDelay, oldMax })
}

// feedProbe sees the steps of a recording's feed (kvmManager.onFeed) and can hold it at one.
type feedProbe struct {
	t      *testing.T
	events chan string
	mu     sync.Mutex
	holds  map[string]chan struct{}
}

// probeFeed installs a probe on the server of h; call it before kvm_record_start.
func probeFeed(h *harness) *feedProbe {
	p := &feedProbe{t: h.t, events: make(chan string, 4096), holds: map[string]chan struct{}{}}
	h.srv.s.kvm.onFeed = func(step string) {
		p.mu.Lock()
		hold := p.holds[step]
		p.mu.Unlock()
		select {
		case p.events <- step:
		default:
		}
		if hold != nil {
			<-hold
		}
	}
	return p
}

// hold stops the feed at step until release is called; later steps of that name pass.
func (p *feedProbe) hold(step string) (release func()) {
	ch := make(chan struct{})
	p.mu.Lock()
	p.holds[step] = ch
	p.mu.Unlock()
	var once sync.Once
	release = func() {
		once.Do(func() {
			p.mu.Lock()
			delete(p.holds, step)
			p.mu.Unlock()
			close(ch)
		})
	}
	p.t.Cleanup(release)
	return release
}

// wait waits up to 5 s for step and returns the steps seen before it.
func (p *feedProbe) wait(step string) []string {
	p.t.Helper()
	var seen []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-p.events:
			if s == step {
				return seen
			}
			seen = append(seen, s)
		case <-deadline:
			p.t.Fatalf("the recording never reached %q (steps: %v)", step, seen)
		}
	}
}

func count(steps []string, step string) int {
	n := 0
	for _, s := range steps {
		if s == step {
			n++
		}
	}
	return n
}

func warningsOf(out map[string]any) string {
	var all []string
	ws, _ := out["warnings"].([]any)
	for _, w := range ws {
		all = append(all, w.(string))
	}
	return strings.Join(all, "\n")
}

// dropKVM ends the KVM session of NUC as AMT does: the client closes, the entry stays.
func dropKVM(h *harness) {
	e := h.kvmEntry("NUC")
	e.mu.Lock()
	c := e.client
	e.mu.Unlock()
	_ = c.Close()
}

// returnsWithin runs f and fails the test if it does not return within 5 s.
func returnsWithin(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocks while the recording reopens the KVM session", what)
	}
}

// startHangingReopen starts a recording on NUC whose reopen hangs in OpenKVM until the test ends.
func startHangingReopen(t *testing.T) (*harness, map[string]any) {
	fastReopen(t)
	h, _, _ := recHarness(t)
	p := probeFeed(h)
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
	gate := make(chan struct{})
	h.devices["192.0.2.10"].KVMGate = gate
	t.Cleanup(func() { close(gate) }) // runs before the server's Close
	dropKVM(h)
	p.wait("open")
	return h, start
}

// TestKVMRecordStopReturnsWhileReopenHangs: a reopen of the recording that hangs at the device
// never blocks kvm_record_stop (defect 1 of #204).
func TestKVMRecordStopReturnsWhileReopenHangs(t *testing.T) {
	h, start := startHangingReopen(t)
	var out map[string]any
	returnsWithin(t, "kvm_record_stop", func() { out = h.mustOK("kvm_record_stop", dev("NUC")) })
	if out["path"] != start["path"] || out["ended_by"] != "stopped" {
		t.Fatalf("%v", out)
	}
	readAVI(t, start["path"].(string))
	returnsWithin(t, "kvm_disconnect", func() { h.mustOK("kvm_disconnect", dev("NUC")) })
}

// TestKVMServerCloseReturnsWhileReopenHangs: the same for the server's Close.
func TestKVMServerCloseReturnsWhileReopenHangs(t *testing.T) {
	h, start := startHangingReopen(t)
	returnsWithin(t, "Close", h.srv.Close)
	readAVI(t, start["path"].(string))
}

// TestKVMRecordingWaitsForPowerAction: while a power action runs the recording never opens
// KVM again, whether the action closed the session or AMT had ended it before (defect 2).
func TestKVMRecordingWaitsForPowerAction(t *testing.T) {
	for _, ended := range []bool{false, true} {
		name := map[bool]string{false: "session open", true: "session ended before"}[ended]
		t.Run(name, func(t *testing.T) {
			fastReopen(t)
			h, srv, _ := recHarness(t)
			f := h.devices["192.0.2.10"]
			p := probeFeed(h)
			start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
			release := func() {}
			if ended {
				release = p.hold("delay")
				dropKVM(h)
				p.wait("delay")
			}
			gate := make(chan struct{})
			endPower := sync.OnceFunc(func() { close(gate) })
			t.Cleanup(endPower)
			f.PowerGate = gate
			done := make(chan string, 1)
			go func() {
				text, _, _ := h.call("power_action", dev("NUC", "action", "cycle"))
				done <- text
			}()
			eventually(t, "power command", f.PowerStarted)
			release()
			p.wait("power")
			if n := srv.Connections(); n != 1 {
				t.Fatalf("%d KVM connections while the power action runs", n)
			}
			endPower()
			if text := <-done; !strings.Contains(text, "accepted") {
				t.Fatalf("power_action: %s", text)
			}
			p.wait("resumed")
			if n := srv.Connections(); n != 2 {
				t.Fatalf("connections %d", n)
			}
			out := h.mustOK("kvm_record_stop", dev("NUC"))
			if w := warningsOf(out); !strings.Contains(w, "power action") || !strings.Contains(w, "the video shows the last picture") {
				t.Fatalf("warnings %q", w)
			}
			readAVI(t, start["path"].(string))
		})
	}
}

// TestKVMRecordingNeverTurnsKVMOn: KVM that the person turned off stays off; the recording
// waits instead (defect 3). The defaults would let a tool call turn it on (group settings on,
// require_client_confirmation off).
func TestKVMRecordingNeverTurnsKVMOn(t *testing.T) {
	fastReopen(t)
	h, _, _ := recHarness(t)
	f := h.devices["192.0.2.10"]
	p := probeFeed(h)
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
	release := p.hold("delay")
	dropKVM(h)
	p.wait("delay")
	f.Redirection().KVMOff = true // as after set_remote_features with kvm false
	release()
	p.wait("wait")
	if on := f.RedirectionEnabled(); len(on) != 0 {
		t.Fatalf("the recording turned on %v", on)
	}
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	w := warningsOf(out)
	if !strings.Contains(w, "KVM is turned off on the device") || !strings.Contains(w, "until the recording ended") {
		t.Fatalf("warnings %q", w)
	}
	readAVI(t, start["path"].(string))
}

// TestKVMRecordingNeverStartsConsent: a reopen that needs user consent makes the device show no
// code; the recording waits until kvm_connect with the code opened the session, then goes on
// with it (defect 4).
func TestKVMRecordingNeverStartsConsent(t *testing.T) {
	fastReopen(t)
	h, srv, _ := recHarness(t)
	f := h.devices["192.0.2.10"]
	p := probeFeed(h)
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
	release := p.hold("delay")
	dropKVM(h)
	p.wait("delay")
	f.Consent, f.ConsentCode = true, "123456"
	release()
	seen := p.wait("wait")
	if n := f.ConsentPrompts(); n != 0 {
		t.Fatalf("the recording started user consent %d times", n)
	}
	if n := count(seen, "open"); n != 1 {
		t.Fatalf("%d opens before waiting (steps %v)", n, seen)
	}
	h.mustOK("kvm_connect", dev("NUC", "consent_code", "123456"))
	p.wait("resumed")
	if n := f.ConsentPrompts(); n != 1 {
		t.Fatalf("consent prompts %d, want the one of kvm_connect", n)
	}
	if n := srv.Connections(); n != 2 {
		t.Fatalf("connections %d", n)
	}
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	w := warningsOf(out)
	if !strings.Contains(w, "user consent") || !strings.Contains(w, " to ") || strings.Contains(w, "until the recording ended") {
		t.Fatalf("warnings %q", w)
	}
	readAVI(t, start["path"].(string))
}

// limitRecording puts a recording on the entry of NUC that ends by itself at its size limit.
func limitRecording(t *testing.T, h *harness) *capture.Recorder {
	t.Helper()
	f, err := capture.CreateUnique(t.TempDir(), "limit.avi")
	if err != nil {
		t.Fatal(err)
	}
	noise := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range noise.Pix {
		noise.Pix[i] = byte(i * 7)
	}
	r, err := capture.StartRecorder(f, noise, capture.RecorderOptions{FPS: 30, Limit: 4000})
	if err != nil {
		t.Fatal(err)
	}
	e := h.kvmEntry("NUC")
	e.mu.Lock()
	rec := newRecording(r, 30)
	e.rec = rec
	e.mu.Unlock()
	deadline := time.After(5 * time.Second)
	for {
		r.Offer(noise)
		select {
		case <-rec.ended: // the recording saw its limit and finished the file
			return r
		case <-deadline:
			t.Fatal("the recording never reached its limit")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestKVMRecordResultSurvivesIdleTimeout: a recording that ended at its limit no longer keeps
// the session; after the idle timeout removed it, kvm_record_stop still returns the result,
// once (defect 5a).
func TestKVMRecordResultSurvivesIdleTimeout(t *testing.T) {
	h, _, _ := recHarness(t)
	clock, skew := skewClock()
	h.srv.s.kvm.now = clock
	r := limitRecording(t, h)
	e := h.kvmEntry("NUC")
	skew.Store(int64(3 * time.Hour))
	h.srv.s.kvm.expire(e)
	if h.kvmEntry("NUC") != nil {
		t.Fatal("the session of a finished recording stays open after the idle timeout")
	}
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	if out["path"] != r.Path() || !strings.Contains(out["ended_by"].(string), "size limit") {
		t.Fatalf("%v", out)
	}
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
}

// TestKVMRecordResultAfterFavoriteChange: update_favorite and delete_favorite end the session
// and return the finished recording (defect 5b).
func TestKVMRecordResultAfterFavoriteChange(t *testing.T) {
	for _, tool := range []string{"update_favorite", "delete_favorite"} {
		t.Run(tool, func(t *testing.T) {
			h := newFavHarness(t)
			h.saved("NUC", "192.0.2.10", false)
			f := &session.Fake{FP: testFP, St: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}}
			h.devices["192.0.2.10"] = f
			addKVM(t, f, kvmtest.Options{Width: 128, Height: 64, Updates: [][]byte{solidUpdate(128, 64, red565)}})
			h.elicit = func(*mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
			}
			h.cfg.KVMIdleTimeout = time.Hour
			h.start()
			h.mustOK("kvm_connect", dev("NUC"))
			start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
			args := dev("NUC")
			if tool == "update_favorite" {
				args = dev("NUC", "host", "192.0.2.11")
			}
			out := h.mustOK(tool, args)
			rec, _ := out["recording"].(map[string]any)
			if rec["path"] != start["path"] || !strings.Contains(rec["ended_by"].(string), "favorite") {
				t.Fatalf("%v", out)
			}
			readAVI(t, start["path"].(string))
		})
	}
}

// TestCaptureNameLimitsBytes: a long name of multi-byte characters is cut to at most
// maxCaptureName bytes at a character boundary, so the file can be created (defect 6).
func TestCaptureNameLimitsBytes(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 11, 12, 0, time.UTC)
	long := strings.Repeat("日", 100)
	got, err := captureName(long, "NUC", now, "png")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(got, ".png")
	if len(base) > maxCaptureName || !utf8.ValidString(got) || !strings.HasPrefix(long, base) || len(base) < maxCaptureName-3 {
		t.Fatalf("%q (%d bytes)", got, len(base))
	}
	h, _, _ := recHarness(t)
	dir := t.TempDir()
	_, _, out := h.screenshot(dev("NUC", "save", true, "folder", dir, "name", long))
	if p, _ := out["saved_path"].(string); filepath.Dir(p) != dir {
		t.Fatalf("%v", out)
	}
	if _, err := os.Stat(out["saved_path"].(string)); err != nil {
		t.Fatal(err)
	}
}

// TestCaptureNameAvoidsReservedNames: Windows device names get a prefix on every OS, so a file
// never goes to a device and stays portable (defect 7).
func TestCaptureNameAvoidsReservedNames(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 11, 12, 0, time.UTC)
	for _, tc := range []struct{ name, want string }{
		{"NUL", "openlamt-NUL.avi"},
		{"nul.txt", "openlamt-nul.avi"},
		{"con", "openlamt-con.avi"},
		{"Prn", "openlamt-Prn.avi"},
		{"AUX ", "openlamt-AUX.avi"},
		{"COM1", "openlamt-COM1.avi"},
		{"lpt9.avi", "openlamt-lpt9.avi"},
		{"CON.tar.gz", "openlamt-CON.tar.avi"},
		{"com¹", "openlamt-com¹.avi"},
		{"console", "console.avi"},
		{"COM10", "COM10.avi"},
		{"nullzwo", "nullzwo.avi"},
	} {
		got, err := captureName(tc.name, "NUC", now, "avi")
		if err != nil || got != tc.want {
			t.Errorf("%q: %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

// TestRecordingGapWarnings: each time without session is one warning with UTC times; the list
// is capped at maxGapWarnings plus a count, and a gap open at the end says so.
func TestRecordingGapWarnings(t *testing.T) {
	f, err := capture.CreateUnique(t.TempDir(), "gaps.avi")
	if err != nil {
		t.Fatal(err)
	}
	r, err := capture.StartRecorder(f, image.NewRGBA(image.Rect(0, 0, 16, 16)), capture.RecorderOptions{FPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecording(r, 1)
	clock := time.Date(2026, 11, 28, 14, 2, 10, 0, time.FixedZone("CET", 3600))
	rec.now = func() time.Time { return clock }
	rec.lost(whyPower)
	clock = clock.Add(90 * time.Second)
	rec.resumed()
	for range maxGapWarnings + 4 {
		rec.lost(whyAMT)
		rec.because("the device asked for user consent")
		rec.resumed()
	}
	rec.lost(whyAMT) // open at the end: the 26th gap
	res := rec.end("stopped")
	if len(res.Warnings) != maxGapWarnings+1 {
		t.Fatalf("%d warnings: %q", len(res.Warnings), res.Warnings)
	}
	want := "KVM was closed from 2026-11-28 13:02:10 UTC to 13:03:40 UTC (power action); the video shows the last picture meanwhile."
	if res.Warnings[0] != want {
		t.Fatalf("first warning %q, want %q", res.Warnings[0], want)
	}
	if w := res.Warnings[1]; !strings.Contains(w, "(AMT ended the session; the device asked for user consent)") {
		t.Fatalf("second warning %q", w)
	}
	if last := res.Warnings[maxGapWarnings]; last != "KVM was closed 6 more times; the video shows the last picture meanwhile." {
		t.Fatalf("last warning %q", last)
	}
}
