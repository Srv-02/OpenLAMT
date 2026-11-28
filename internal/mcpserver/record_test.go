package mcpserver

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// readAVI checks that path is an AVI and returns its number of video chunks and the size of the
// first frame, decoded as JPEG.
func readAVI(t *testing.T, path string) (frames int, first image.Point) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "AVI " {
		t.Fatalf("%s is not an AVI", path)
	}
	i := bytes.Index(b, []byte("movi"))
	if i < 0 {
		t.Fatal("no movi list")
	}
	for p := i + 4; p+8 <= len(b) && string(b[p:p+4]) == "00dc"; {
		n := int(binary.LittleEndian.Uint32(b[p+4:]))
		if n > 0 && frames == 0 {
			img, err := jpeg.Decode(bytes.NewReader(b[p+8 : p+8+n]))
			if err != nil {
				t.Fatalf("first frame: %v", err)
			}
			first = img.Bounds().Size()
		}
		frames++
		p += 8 + n + n%2
	}
	if !bytes.Contains(b, []byte("idx1")) {
		t.Fatal("the AVI has no index: it was not finalized")
	}
	return frames, first
}

// recHarness starts a harness with one device NUC (128x64 red screen) and an open KVM session,
// and a home folder in a temp dir.
func recHarness(t *testing.T) (*harness, *kvmtest.Server, string) {
	t.Helper()
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{Width: 128, Height: 64, UpdatesPerConn: true, Updates: [][]byte{solidUpdate(128, 64, red565)}})
	h.cfg.KVMIdleTimeout = time.Hour
	h.start()
	home := t.TempDir()
	h.srv.s.home = func() (string, error) { return home, nil }
	h.mustOK("kvm_connect", dev("NUC"))
	return h, srv, home
}

func TestKVMRecordStartStop(t *testing.T) {
	h, _, _ := recHarness(t)
	dir := t.TempDir()
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", dir, "name", "demo.mp4", "fps", 20))
	want := filepath.Join(dir, "demo.avi")
	if start["path"] != want || start["fps"] != float64(20) || start["screen_width"] != float64(128) {
		t.Fatalf("%v", start)
	}
	time.Sleep(400 * time.Millisecond)
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	if out["path"] != want || out["ended_by"] != "stopped" || out["bytes"].(float64) <= 0 || out["frames"].(float64) < 3 || out["duration_seconds"].(float64) <= 0 {
		t.Fatalf("%v", out)
	}
	frames, size := readAVI(t, want)
	if frames != int(out["frames"].(float64)) || size != (image.Point{128, 64}) {
		t.Fatalf("AVI has %d frames of %v; stop said %v", frames, size, out)
	}
	if fi, _ := os.Stat(want); fi.Size() != int64(out["bytes"].(float64)) {
		t.Fatalf("size %d, stop said %v", fi.Size(), out["bytes"])
	}
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
	// the action log carries the path
	var logged bool
	for _, e := range h.logEntries() {
		if e["tool"] == "kvm_record_stop" && e["path"] == want {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("path not in the action log: %v", h.logEntries())
	}
}

func TestKVMRecordRefusals(t *testing.T) {
	h, _, _ := recHarness(t)
	dir := t.TempDir()
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
	h.mustFail("kvm_record_stop", dev("Ghost"), "No favorite named")
	h.mustFail("kvm_record_start", dev("NUC", "folder", "relative/dir"), "absolute path")
	h.mustFail("kvm_record_start", dev("NUC", "folder", filepath.Join(dir, "missing")), "does not exist")
	h.mustFail("kvm_record_start", dev("NUC", "folder", dir, "name", ".."), "file name")
	h.mustFail("kvm_record_start", dev("NUC", "folder", dir, "fps", 99), "fps")
	h.mustFail("kvm_record_start", dev("NUC", "folder", dir, "quality", 101), "quality")
	if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
		t.Fatalf("files left behind: %v", m)
	}
	h.mustOK("kvm_record_start", dev("NUC", "folder", dir))
	h.mustFail("kvm_record_start", dev("NUC", "folder", dir), "already")
	h.mustOK("kvm_record_stop", dev("NUC"))
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
	// no session: start needs kvm_connect
	h.mustOK("kvm_disconnect", dev("NUC"))
	h.mustFail("kvm_record_start", dev("NUC", "folder", dir), "call kvm_connect first")
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
}

func TestKVMRecordNeverOverwritesAndForcesExtension(t *testing.T) {
	h, _, _ := recHarness(t)
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"run.gif", "run.mp3", "../../run"} {
		out := h.mustOK("kvm_record_start", dev("NUC", "folder", dir, "name", name))
		paths = append(paths, out["path"].(string))
		h.mustOK("kvm_record_stop", dev("NUC"))
	}
	want := []string{"run.avi", "run-2.avi", "run-3.avi"}
	for i, p := range paths {
		if filepath.Dir(p) != dir || filepath.Base(p) != want[i] {
			t.Fatalf("paths %v, want %v in %s", paths, want, dir)
		}
		readAVI(t, p)
	}
}

func TestKVMRecordDefaultFolders(t *testing.T) {
	h, _, home := recHarness(t)
	// no folder, no config: OpenLAMT in the home folder, created private
	out := h.mustOK("kvm_record_start", dev("NUC"))
	p := out["path"].(string)
	if filepath.Dir(p) != filepath.Join(home, "OpenLAMT") || !strings.HasPrefix(filepath.Base(p), "openlamt-NUC-") || !strings.HasSuffix(p, ".avi") {
		t.Fatalf("path %s", p)
	}
	if fi, err := os.Stat(filepath.Dir(p)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder %v %v", fi, err)
	}
	h.mustOK("kvm_record_stop", dev("NUC"))
	// capture.folder of the config wins over the home folder
	h.srv.s.cfg.CaptureFolder = filepath.Join(t.TempDir(), "docs")
	out = h.mustOK("kvm_record_start", dev("NUC"))
	if filepath.Dir(out["path"].(string)) != h.srv.s.cfg.CaptureFolder {
		t.Fatalf("path %v", out["path"])
	}
	h.mustOK("kvm_record_stop", dev("NUC"))
}

func TestKVMDisconnectFinalizesRecording(t *testing.T) {
	h, _, _ := recHarness(t)
	dir := t.TempDir()
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", dir))
	time.Sleep(250 * time.Millisecond)
	out := h.mustOK("kvm_disconnect", dev("NUC"))
	rec, _ := out["recording"].(map[string]any)
	if out["closed"] != true || rec["path"] != start["path"] || rec["frames"].(float64) < 2 || !strings.Contains(rec["ended_by"].(string), "kvm_disconnect") {
		t.Fatalf("%v", out)
	}
	readAVI(t, start["path"].(string))
}

func TestKVMServerCloseFinalizesRecording(t *testing.T) {
	h, _, _ := recHarness(t)
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
	time.Sleep(150 * time.Millisecond)
	h.srv.Close()
	readAVI(t, start["path"].(string))
}

// TestKVMIdleTimeoutKeepsRecordingSession: a running recording is not idle; after it ends the
// idle timeout counts again.
func TestKVMIdleTimeoutKeepsRecordingSession(t *testing.T) {
	h, _, _ := recHarness(t)
	clock, skew := skewClock()
	h.srv.s.kvm.now = clock
	h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir()))
	e := h.kvmEntry("NUC")
	skew.Store(int64(3 * time.Hour))
	h.srv.s.kvm.expire(e)
	if h.kvmEntry("NUC") != e || !e.alive() {
		t.Fatal("idle timeout closed a session that is recording")
	}
	h.mustOK("kvm_record_stop", dev("NUC"))
	skew.Store(int64(6 * time.Hour))
	h.srv.s.kvm.expire(e)
	if h.kvmEntry("NUC") != nil {
		t.Fatal("idle session without a recording stays open")
	}
}

// TestKVMRecordingContinuesAcrossPowerAction: a power action closes the client; the feed opens
// the session again and the recording goes on.
func TestKVMRecordingContinuesAcrossPowerAction(t *testing.T) {
	old := recReopenDelay
	recReopenDelay = 30 * time.Millisecond
	t.Cleanup(func() { recReopenDelay = old })
	h, srv, _ := recHarness(t)
	start := h.mustOK("kvm_record_start", dev("NUC", "folder", t.TempDir(), "fps", 20))
	c := h.kvmEntry("NUC").client
	h.mustOK("power_action", dev("NUC", "action", "cycle"))
	select {
	case <-c.Done():
	default:
		t.Fatal("power cycle left KVM open")
	}
	eventually(t, "session reopened by the recording", func() bool {
		e := h.kvmEntry("NUC")
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.alive()
	})
	if srv.Connections() != 2 {
		t.Fatalf("connections %d", srv.Connections())
	}
	time.Sleep(150 * time.Millisecond)
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	if out["ended_by"] != "stopped" {
		t.Fatalf("%v", out)
	}
	readAVI(t, start["path"].(string))
}

// TestKVMRecordEndedByItself: a recording that stopped by itself (limit) keeps its result for
// kvm_record_stop, with the reason; a new start is refused until then.
func TestKVMRecordEndedByItself(t *testing.T) {
	h, _, _ := recHarness(t)
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
	for i := 0; i < 50; i++ {
		r.Offer(noise)
		time.Sleep(10 * time.Millisecond)
		select {
		case <-r.Done():
			i = 50
		default:
		}
	}
	<-r.Done()
	h.mustFail("kvm_record_start", dev("NUC", "folder", t.TempDir()), "ended by itself")
	out := h.mustOK("kvm_record_stop", dev("NUC"))
	if !strings.Contains(out["ended_by"].(string), "size limit") || out["path"] != r.Path() || out["frames"].(float64) < 1 {
		t.Fatalf("%v", out)
	}
	readAVI(t, r.Path())
	h.mustFail("kvm_record_stop", dev("NUC"), "No recording")
}

func TestKVMScreenshotSavesNativePNG(t *testing.T) {
	h, _, _ := recHarness(t)
	dir := t.TempDir()
	// the image for the agent is scaled down; the file is not
	img, _, out := h.screenshot(dev("NUC", "max_width", 32, "max_height", 32, "save", true, "folder", dir, "name", "step1.jpg"))
	if img.Bounds().Dx() != 32 {
		t.Fatalf("agent image %v", img.Bounds())
	}
	want := filepath.Join(dir, "step1.png")
	if out["saved_path"] != want {
		t.Fatalf("%v", out)
	}
	f, err := os.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	saved, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Bounds().Size() != (image.Point{128, 64}) {
		t.Fatalf("saved size %v", saved.Bounds())
	}
	if r, g, b, _ := saved.At(5, 5).RGBA(); r>>8 < 0xf0 || g>>8 > 0x10 || b>>8 > 0x10 {
		t.Fatalf("pixel %d %d %d is not red", r>>8, g>>8, b>>8)
	}
	// never overwrites
	_, _, out = h.screenshot(dev("NUC", "save", true, "folder", dir, "name", "step1"))
	if out["saved_path"] != filepath.Join(dir, "step1-2.png") {
		t.Fatalf("%v", out)
	}
	// the path is in the action log
	var logged bool
	for _, e := range h.logEntries() {
		if e["tool"] == "kvm_screenshot" && e["path"] == filepath.Join(dir, "step1-2.png") {
			logged = true
		}
	}
	if !logged {
		t.Fatal("saved path not in the action log")
	}
	// without save nothing is written and no path comes back
	_, _, out = h.screenshot(dev("NUC"))
	if _, ok := out["saved_path"]; ok {
		t.Fatalf("%v", out)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 2 {
		t.Fatalf("files %v", m)
	}
}

func TestKVMScreenshotSaveRefusals(t *testing.T) {
	h, _, home := recHarness(t)
	h.mustFail("kvm_screenshot", dev("NUC", "save", true, "folder", "rel"), "absolute path")
	h.mustFail("kvm_screenshot", dev("NUC", "save", true, "folder", filepath.Join(home, "nope")), "does not exist")
	h.mustFail("kvm_screenshot", dev("NUC", "folder", home), "only apply with save")
	h.mustFail("kvm_screenshot", dev("NUC", "name", "x"), "only apply with save")
	// default folder
	_, _, out := h.screenshot(dev("NUC", "save", true))
	if filepath.Dir(out["saved_path"].(string)) != filepath.Join(home, "OpenLAMT") {
		t.Fatalf("%v", out)
	}
}

func TestCaptureName(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 11, 12, 0, time.UTC)
	for _, tc := range []struct{ name, want string }{
		{"", "openlamt-NUC-2026-11-28-101112.avi"},
		{"  ", "openlamt-NUC-2026-11-28-101112.avi"},
		{"demo", "demo.avi"},
		{"demo.avi", "demo.avi"},
		{"demo.png", "demo.avi"},
		{"a/b/c.mov", "c.avi"},
		{`C:\x\y.z`, "y.avi"},
		{"boot: step?", "boot- step.avi"},
		{"/etc/passwd", "passwd.avi"},
		{"x\x00y", "x-y.avi"},
	} {
		got, err := captureName(tc.name, "NUC", now, "avi")
		if err != nil || got != tc.want {
			t.Errorf("%q: %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	for _, bad := range []string{"..", ".", "/", ".avi", "---"} {
		if _, err := captureName(bad, "NUC", now, "avi"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestKVMRecordToolsNeedReadGroup(t *testing.T) {
	h := newHarness(t)
	h.cfg.Groups[GroupRead] = false
	h.addDevice("NUC", "192.0.2.10", false)
	h.start()
	for _, n := range h.toolNames() {
		if n == "kvm_record_start" || n == "kvm_record_stop" || n == "kvm_screenshot" {
			t.Fatalf("%s offered without the read group", n)
		}
	}
	h2 := newHarness(t)
	h2.addDevice("NUC", "192.0.2.10", false)
	h2.start()
	names := strings.Join(h2.toolNames(), " ")
	if !strings.Contains(names, "kvm_record_start") || !strings.Contains(names, "kvm_record_stop") {
		t.Fatalf("tools %s", names)
	}
}
