package mcpserver

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// alwaysHarness starts a harness with capture.record_always on, one device NUC (128x64 red
// screen) and capture.folder in a temp dir; no session is open yet. It returns the folder.
func alwaysHarness(t *testing.T, o kvmtest.Options) (*harness, *kvmtest.Server, string) {
	t.Helper()
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, o)
	dir := t.TempDir()
	h.cfg.KVMIdleTimeout = time.Hour // the real timer never fires during a test
	h.cfg.RecordAlways, h.cfg.CaptureFolder = true, dir
	h.start()
	home := t.TempDir()
	h.srv.s.home = func() (string, error) { return home, nil }
	return h, srv, dir
}

func redScreen() kvmtest.Options {
	return kvmtest.Options{Width: 128, Height: 64, UpdatesPerConn: true, Updates: [][]byte{solidUpdate(128, 64, red565)}}
}

// aviFiles lists the AVI files in dir.
func aviFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.avi"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// recordedFrames returns the frames written so far into the current file of the recording of NUC.
func recordedFrames(h *harness) int {
	e := h.kvmEntry("NUC")
	if e == nil {
		return 0
	}
	if r := e.live.Load(); r != nil {
		return r.current().Frames()
	}
	return 0
}

// TestRecordAlwaysRecordsEverySession: with capture.record_always, kvm_connect starts the
// recording into capture.folder under the default name and says so; the agent can neither stop
// it nor start another one; kvm_disconnect ends it and returns the finished file (#205).
func TestRecordAlwaysRecordsEverySession(t *testing.T) {
	h, _, dir := alwaysHarness(t, redScreen())
	out := h.mustOK("kvm_connect", dev("NUC"))
	p, _ := out["recording"].(string)
	if filepath.Dir(p) != dir || !strings.HasPrefix(filepath.Base(p), "openlamt-NUC-") || !strings.HasSuffix(p, ".avi") {
		t.Fatalf("recording %q in %v", p, out)
	}
	if w := warningsOf(out); !strings.Contains(w, "recorded by the server configuration (capture.record_always)") || !strings.Contains(w, p) {
		t.Fatalf("warnings %q", w)
	}
	again := h.mustOK("kvm_connect", dev("NUC"))
	if again["already_open"] != true || again["recording"] != p {
		t.Fatalf("second connect %v", again)
	}
	h.mustFail("kvm_record_stop", dev("NUC"),
		"This session is recorded by the server configuration (capture.record_always); the recording ends with the session: "+
			"with kvm_disconnect, after the idle timeout, or when this server stops.")
	h.mustFail("kvm_record_start", dev("NUC"), "recorded already, by the server configuration")
	h.mustFail("kvm_record_start", dev("NUC", "folder", t.TempDir(), "name", "mine"), "recorded already")
	eventually(t, "frames", func() bool { return recordedFrames(h) >= 3 })
	h.mustOK("kvm_screenshot", dev("NUC")) // the session works as without the recording
	dis := h.mustOK("kvm_disconnect", dev("NUC"))
	rec, _ := dis["recording"].(map[string]any)
	if dis["closed"] != true || rec["path"] != p || rec["mandatory"] != true || !strings.Contains(rec["ended_by"].(string), "kvm_disconnect") {
		t.Fatalf("%v", dis)
	}
	if _, ok := rec["earlier_files"]; ok {
		t.Fatalf("earlier files without a size limit: %v", rec)
	}
	frames, size := readAVI(t, p)
	if frames < 3 || size != (image.Point{128, 64}) {
		t.Fatalf("%d frames of %v", frames, size)
	}
	if n := len(aviFiles(t, dir)); n != 1 {
		t.Fatalf("%d files", n)
	}
	// the next session gets its own file
	next := h.mustOK("kvm_connect", dev("NUC"))
	if np, _ := next["recording"].(string); np == "" || np == p {
		t.Fatalf("new session recording %q", np)
	}
}

// TestRecordAlwaysDefaultFolder: without capture.folder the recording goes to OpenLAMT in the
// home folder, as the agent's recordings do.
func TestRecordAlwaysDefaultFolder(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, redScreen())
	h.cfg.KVMIdleTimeout, h.cfg.RecordAlways = time.Hour, true
	h.start()
	home := t.TempDir()
	h.srv.s.home = func() (string, error) { return home, nil }
	out := h.mustOK("kvm_connect", dev("NUC"))
	if p, _ := out["recording"].(string); filepath.Dir(p) != filepath.Join(home, "OpenLAMT") {
		t.Fatalf("recording %q", p)
	}
}

// TestRecordAlwaysRefusesConnectWithoutRecording: no KVM session without its recording. A
// folder that cannot be made refuses before KVM opens; a missing first picture or a recorder that
// cannot start closes the session it just opened again (#205, fail closed).
func TestRecordAlwaysRefusesConnectWithoutRecording(t *testing.T) {
	t.Run("folder", func(t *testing.T) {
		h, srv, dir := alwaysHarness(t, redScreen())
		file := filepath.Join(dir, "a-file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		h.srv.s.cfg.CaptureFolder = filepath.Join(file, "videos") // under a file: cannot be created
		h.mustFail("kvm_connect", dev("NUC"), "capture.record_always), and the recording could not start")
		text := h.mustFail("kvm_connect", dev("NUC"), "No KVM session was opened.")
		if !strings.Contains(text, "videos") {
			t.Fatalf("no folder in %q", text)
		}
		if n := srv.Connections(); n != 0 || h.kvmEntry("NUC") != nil {
			t.Fatalf("%d KVM connections, entry %v", n, h.kvmEntry("NUC"))
		}
		h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
	})
	t.Run("no first picture", func(t *testing.T) {
		old := firstFrameWait
		firstFrameWait = 50 * time.Millisecond
		t.Cleanup(func() { firstFrameWait = old })
		h, srv, dir := alwaysHarness(t, kvmtest.Options{Width: 128, Height: 64}) // sends no picture
		h.mustFail("kvm_connect", dev("NUC"), "the device sent no picture")
		h.mustFail("kvm_connect", dev("NUC"), "The KVM session was closed again.")
		if n := srv.Connections(); n != 2 || h.kvmEntry("NUC") != nil {
			t.Fatalf("%d KVM connections, entry %v", n, h.kvmEntry("NUC"))
		}
		if m := aviFiles(t, dir); len(m) != 0 {
			t.Fatalf("files %v", m)
		}
		h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
	})
	t.Run("recorder", func(t *testing.T) {
		h, srv, dir := alwaysHarness(t, redScreen())
		var mu sync.Mutex
		var clients []*os.File
		h.srv.s.kvm.startRec = func(f *os.File, _ *image.RGBA, _ capture.RecorderOptions) (*capture.Recorder, error) {
			mu.Lock()
			clients = append(clients, f)
			mu.Unlock()
			f.Close()
			os.Remove(f.Name())
			return nil, errors.New("no space left on device")
		}
		h.mustFail("kvm_connect", dev("NUC"), "no space left on device")
		if h.kvmEntry("NUC") != nil {
			t.Fatal("the session stays without its recording")
		}
		if srv.Connections() != 1 || len(clients) != 1 || len(aviFiles(t, dir)) != 0 {
			t.Fatalf("connections %d, recorders %d, files %v", srv.Connections(), len(clients), aviFiles(t, dir))
		}
		h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{"Enter"}), "call kvm_connect first")
		h.srv.s.kvm.startRec = capture.StartRecorder
		out := h.mustOK("kvm_connect", dev("NUC"))
		if out["recording"] == "" || out["already_open"] != false {
			t.Fatalf("%v", out)
		}
	})
}

// TestRecordAlwaysRollsOverAtTheLimit: at the size limit the mandatory recording goes on in a
// new file at once, the session stays open, and kvm_disconnect lists every file, each one a
// complete AVI (#205).
func TestRecordAlwaysRollsOverAtTheLimit(t *testing.T) {
	h, _, dir := alwaysHarness(t, redScreen())
	h.srv.s.kvm.startRec = func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error) {
		o.FPS, o.Limit = 100, 3000 // a few hundred milliseconds per file
		return capture.StartRecorder(f, first, o)
	}
	out := h.mustOK("kvm_connect", dev("NUC"))
	first := out["recording"].(string)
	e := h.kvmEntry("NUC")
	rec := e.live.Load()
	eventually(t, "three files", func() bool { return rec.files() >= 3 })
	if h.kvmEntry("NUC") != e || !rec.running() {
		t.Fatal("the session closed at the size limit")
	}
	h.mustOK("kvm_screenshot", dev("NUC"))
	dis := h.mustOK("kvm_disconnect", dev("NUC"))
	res, _ := dis["recording"].(map[string]any)
	earlier, _ := res["earlier_files"].([]any)
	if len(earlier) < 2 || !strings.Contains(res["ended_by"].(string), "kvm_disconnect") {
		t.Fatalf("%v", dis)
	}
	paths := []string{}
	for _, f := range earlier {
		m := f.(map[string]any)
		if m["bytes"].(float64) <= 0 || m["bytes"].(float64) > 3000 || m["frames"].(float64) < 1 {
			t.Fatalf("earlier file %v", m)
		}
		paths = append(paths, m["path"].(string))
	}
	paths = append(paths, res["path"].(string))
	if paths[0] != first {
		t.Fatalf("first file %s, connect said %s", paths[0], first)
	}
	files := aviFiles(t, dir)
	if len(files) != len(paths) {
		t.Fatalf("%d files in the folder, the result lists %v", len(files), paths)
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] || filepath.Dir(p) != dir {
			t.Fatalf("paths %v", paths)
		}
		seen[p] = true
		if _, size := readAVI(t, p); size != (image.Point{128, 64}) {
			t.Fatalf("%s: first frame %v", p, size)
		}
	}
}

// TestRecordAlwaysWriteErrorClosesSession: a mandatory recording that fails during the session
// closes it; the KVM tools say why until kvm_connect opens a new session with a new recording
// (#205, fail closed).
func TestRecordAlwaysWriteErrorClosesSession(t *testing.T) {
	h, _, _ := alwaysHarness(t, redScreen())
	files := make(chan *os.File, 4)
	h.srv.s.kvm.startRec = func(f *os.File, first *image.RGBA, o capture.RecorderOptions) (*capture.Recorder, error) {
		files <- f
		return capture.StartRecorder(f, first, o)
	}
	out := h.mustOK("kvm_connect", dev("NUC"))
	p := out["recording"].(string)
	e := h.kvmEntry("NUC")
	c := e.client
	(<-files).Close() // the disk went away: the next write fails
	eventually(t, "session closed", func() bool { return h.kvmEntry("NUC") == nil })
	select {
	case <-c.Done():
	default:
		t.Fatal("the KVM client still runs")
	}
	for _, tool := range []string{"kvm_screenshot", "kvm_send_keys", "kvm_mouse"} {
		args := dev("NUC")
		switch tool {
		case "kvm_send_keys":
			args = dev("NUC", "keys", []string{"Enter"})
		case "kvm_mouse":
			args = dev("NUC", "action", "move", "x", 1, "y", 1)
		}
		text := h.mustFail(tool, args, "was closed because its recording failed")
		if !strings.Contains(text, "a write error") || !strings.Contains(text, p) || !strings.Contains(text, "Call kvm_connect") {
			t.Fatalf("%s: %q", tool, text)
		}
	}
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
	dis := h.mustOK("kvm_disconnect", dev("NUC"))
	rec, _ := dis["recording"].(map[string]any)
	if dis["closed"] != false || rec["path"] != p || !strings.Contains(rec["ended_by"].(string), "write error") {
		t.Fatalf("%v", dis)
	}
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
	again := h.mustOK("kvm_connect", dev("NUC"))
	if np, _ := again["recording"].(string); np == "" || np == p {
		t.Fatalf("new recording %q", np)
	}
	h.mustOK("kvm_screenshot", dev("NUC"))
}

// TestRecordAlwaysIdleTimeoutClosesSession: a mandatory recording does not keep the session
// open; the idle timeout closes it and finishes the file (#205).
func TestRecordAlwaysIdleTimeoutClosesSession(t *testing.T) {
	h, _, _ := alwaysHarness(t, redScreen())
	clock, skew := skewClock()
	h.srv.s.kvm.now = clock
	out := h.mustOK("kvm_connect", dev("NUC"))
	e := h.kvmEntry("NUC")
	skew.Store(int64(3 * time.Hour))
	h.srv.s.kvm.expire(e)
	if h.kvmEntry("NUC") != nil {
		t.Fatal("a mandatory recording kept the idle session open")
	}
	readAVI(t, out["recording"].(string))
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
}

// TestKVMIdleTimeoutClosesWaitingRecording: a recording the agent started keeps the session
// open only while it records frames (TestKVMIdleTimeoutKeepsRecordingSession). While it waits
// for a tool call (here: the device asks for consent again) the idle timeout closes the session;
// kvm_record_stop still returns the result.
func TestKVMIdleTimeoutClosesWaitingRecording(t *testing.T) {
	fastReopen(t)
	h, _, _ := recHarness(t)
	clock, skew := skewClock()
	h.srv.s.kvm.now = clock
	f := h.devices["192.0.2.10"]
	p := probeFeed(h)
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
	e := h.kvmEntry("NUC")
	skew.Store(int64(3 * time.Hour))
	h.srv.s.kvm.expire(e) // records frames: stays
	if h.kvmEntry("NUC") != e {
		t.Fatal("idle timeout closed a session that records")
	}
	release := p.hold("delay")
	dropKVM(h)
	p.wait("delay")
	f.Consent, f.ConsentCode = true, "123456"
	release()
	p.wait("wait")
	skew.Store(int64(6 * time.Hour))
	h.srv.s.kvm.expire(e)
	if h.kvmEntry("NUC") != nil {
		t.Fatal("a recording that waits for kvm_connect keeps the idle session open")
	}
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	if out["path"] != start["path"] || !strings.Contains(out["ended_by"].(string), "idle timeout") {
		t.Fatalf("%v", out)
	}
	readAVI(t, start["path"].(string))
}

// TestKVMToolCallWaitsForPowerAction: a tool call that would open KVM again waits until the
// power action of the device returned, so it never opens KVM while AMT runs off or cycle
// (side finding of #207). Without the wait the call opens a second KVM connection early.
func TestKVMToolCallWaitsForPowerAction(t *testing.T) {
	for _, tool := range []string{"kvm_screenshot", "kvm_connect"} {
		t.Run(tool, func(t *testing.T) {
			h, srv, _ := recHarness(t)
			f := h.devices["192.0.2.10"]
			waits := make(chan string, 16)
			h.srv.s.kvm.onCall = func(step string) { waits <- step }
			gate := make(chan struct{})
			endPower := sync.OnceFunc(func() { close(gate) })
			t.Cleanup(endPower)
			f.PowerGate = gate
			power := make(chan string, 1)
			go func() {
				text, _, _ := h.call("power_action", dev("NUC", "action", "cycle"))
				power <- text
			}()
			eventually(t, "power command", f.PowerStarted) // KVM is closed now
			call := make(chan bool, 1)
			go func() {
				_, _, isErr := h.call(tool, dev("NUC"))
				call <- isErr
			}()
			select {
			case step := <-waits:
				if step != "power" {
					t.Fatalf("step %q", step)
				}
			case isErr := <-call:
				t.Fatalf("%s returned (error %v) while the power action runs", tool, isErr)
			case <-time.After(5 * time.Second):
				t.Fatalf("%s never waited for the power action", tool)
			}
			if n := srv.Connections(); n != 1 {
				t.Fatalf("%d KVM connections while the power action runs", n)
			}
			endPower()
			if text := <-power; !strings.Contains(text, "accepted") {
				t.Fatalf("power_action: %s", text)
			}
			select {
			case isErr := <-call:
				if isErr {
					t.Fatalf("%s failed after the power action", tool)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s still waits after the power action", tool)
			}
			if n := srv.Connections(); n != 2 {
				t.Fatalf("connections %d", n)
			}
		})
	}
}

// TestRecordAlwaysOffChangesNothing: without capture.record_always kvm_connect records nothing
// and names no recording.
func TestRecordAlwaysOffChangesNothing(t *testing.T) {
	h, _, home := recHarness(t)
	out := h.mustOK("kvm_connect", dev("NUC"))
	if _, ok := out["recording"]; ok || strings.Contains(warningsOf(out), "record_always") {
		t.Fatalf("%v", out)
	}
	if e := h.kvmEntry("NUC"); e.live.Load() != nil {
		t.Fatal("a recording runs")
	}
	if m, _ := filepath.Glob(filepath.Join(home, "OpenLAMT", "*")); len(m) != 0 {
		t.Fatalf("files %v", m)
	}
}
