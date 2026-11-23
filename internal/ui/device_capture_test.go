package ui

import (
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// captureAt is the fixed time of the file names in these tests.
var captureAt = time.Date(2026, 11, 23, 10, 14, 12, 0, time.Local)

// fakeSave records the save dialog and answers with path ("" cancels).
type fakeSave struct {
	kinds []captureKind
	names []string
	path  string
}

func (f *fakeSave) pickers() filePickers {
	return filePickers{save: func(_ fyne.Window, kind captureKind, name string, chosen func(string), _ func(error)) {
		f.kinds = append(f.kinds, kind)
		f.names = append(f.names, name)
		if f.path != "" {
			chosen(f.path)
		}
	}}
}

// captureDevice is a connected KVM device window (with its first frame unless noFrame) whose
// screenshots and recordings go to folder; saved receives every reported result.
func captureDevice(t *testing.T, folder string, noFrame bool) (*kvmHarness, *fakeSave, chan struct{}) {
	t.Helper()
	opts := kvmtest.Options{}
	if noFrame {
		opts.Updates = [][]byte{} // the server never sends a frame
	}
	h := newKVMDevice(t, &sessionFake{}, opts)
	h.d.settings.SetCaptureFolder(folder)
	save := &fakeSave{}
	saved := make(chan struct{}, 8)
	h.d.capture.pickers = save.pickers()
	h.d.capture.now = func() time.Time { return captureAt }
	h.d.capture.checkSync = true
	h.d.capture.onSaved = func() { saved <- struct{}{} }
	h.connect(t, !noFrame)
	return h, save, saved
}

// TestScreenshotNeedsSessionWithFrame: Screenshot and Record are enabled only while a KVM
// session runs and has shown a frame, in the toolbar and the full screen bar, and not in the
// Console view.
func TestScreenshotNeedsSessionWithFrame(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{Updates: [][]byte{}})
	all := []fyne.Disableable{h.d.shotBtn, h.d.recBtn, h.d.barShot, h.d.barRec}
	disabled := func(want bool) {
		t.Helper()
		for i, b := range all {
			if b.Disabled() != want {
				t.Fatalf("button %d disabled=%v, want %v", i, b.Disabled(), want)
			}
		}
	}
	disabled(true)
	h.connect(t, false)
	disabled(true) // connected, but no frame yet
	if h.d.shotBtn.Caption() != "Screenshot" || h.d.recBtn.Caption() != "Record" {
		t.Fatalf("captions %q %q", h.d.shotBtn.Caption(), h.d.recBtn.Caption())
	}
	h.disconnect(t)

	h2, _, _ := captureDevice(t, t.TempDir(), false)
	all = []fyne.Disableable{h2.d.shotBtn, h2.d.recBtn, h2.d.barShot, h2.d.barRec}
	h = h2
	disabled(false)
	h.d.setConsoleView(true)
	disabled(true)
	h.d.setConsoleView(false)
	disabled(false)
	h.disconnect(t)
	disabled(true)
}

// TestScreenshotSavesPNGInFolder: with a folder set the screenshot goes there without a dialog,
// at the native resolution, named openlamt-<device>-<time>.png; a second one in the same second
// gets its own name. The status bar names the file.
func TestScreenshotSavesPNGInFolder(t *testing.T) {
	dir := t.TempDir()
	h, save, saved := captureDevice(t, dir, false)
	test.Tap(h.d.shotBtn)
	recv(t, saved)
	if len(save.names) != 0 {
		t.Fatal("save dialog shown although a folder is set")
	}
	name := "openlamt-Büro-2026-11-23-101412.png"
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 128 || b.Dy() != 64 {
		t.Fatalf("screenshot %v, want the framebuffer 128x64", b)
	}
	if r, g, _, _ := img.At(3, 3).RGBA(); r>>8 < 240 || g>>8 > 10 {
		t.Fatalf("pixel %v, want the red frame", img.At(3, 3))
	}
	if got := h.d.capture.line.Text; got != "Screenshot saved: "+name || !h.d.capture.line.Visible() {
		t.Fatalf("status %q", got)
	}
	test.Tap(h.d.barShot) // the full screen bar's button does the same
	recv(t, saved)
	if _, err := os.Stat(filepath.Join(dir, "openlamt-Büro-2026-11-23-101412-2.png")); err != nil {
		t.Fatalf("second screenshot: %v", err)
	}
	if h.d.win.Canvas().Focused() != h.d.view {
		t.Fatal("the view must keep the focus")
	}
}

// TestScreenshotFolderFallback: without a folder every click opens the save dialog with the
// proposed name; a folder that is gone or read-only falls back to the dialog and the status bar
// says why; a cancelled dialog saves nothing.
func TestScreenshotFolderFallback(t *testing.T) {
	out := filepath.Join(t.TempDir(), "chosen.png")
	h, save, saved := captureDevice(t, "", false)
	save.path = out
	test.Tap(h.d.shotBtn)
	recv(t, saved)
	if len(save.names) != 1 || save.names[0] != "openlamt-Büro-2026-11-23-101412.png" || save.kinds[0].ext != "png" {
		t.Fatalf("dialog %v %v", save.names, save.kinds)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	if h.d.capture.line.Text != "Screenshot saved: chosen.png" {
		t.Fatalf("status %q", h.d.capture.line.Text)
	}

	gone := filepath.Join(t.TempDir(), "gone")
	h.d.settings.SetCaptureFolder(gone)
	save.path = "" // cancel
	test.Tap(h.d.shotBtn)
	if len(save.names) != 2 {
		t.Fatal("no save dialog for a missing folder")
	}
	if want := "The folder " + gone + " does not exist. Choose where to save the file."; h.d.capture.line.Text != want {
		t.Fatalf("status %q", h.d.capture.line.Text)
	}

	if os.Getuid() != 0 && runtime.GOOS != "windows" {
		ro := t.TempDir()
		if err := os.Chmod(ro, 0o500); err == nil {
			t.Cleanup(func() { os.Chmod(ro, 0o700) })
			if f, err := os.CreateTemp(ro, "x"); err == nil { // file systems that ignore the mode
				f.Close()
			} else {
				h.d.settings.SetCaptureFolder(ro)
				test.Tap(h.d.shotBtn)
				if !strings.Contains(h.d.capture.line.Text, "is not writable") || len(save.names) != 3 {
					t.Fatalf("read-only: %q, %d dialogs", h.d.capture.line.Text, len(save.names))
				}
			}
		}
	}
}

// aviFrames checks the RIFF/AVI header of path and returns the frame count of the main header.
func aviFrames(t *testing.T, path string) uint32 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 64 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "AVI " || int(binary.LittleEndian.Uint32(b[4:]))+8 != len(b) {
		t.Fatalf("not a complete AVI: %d bytes, %q", len(b), b[:min(len(b), 12)])
	}
	return binary.LittleEndian.Uint32(b[48:])
}

// TestRecordStartStop: Record starts a recording into the folder, turns into a red "Stop" in the
// toolbar and the full screen bar and shows REC time and size in the status bar; Stop completes
// the file and the status bar names it.
func TestRecordStartStop(t *testing.T) {
	dir := t.TempDir()
	h, _, saved := captureDevice(t, dir, false)
	test.Tap(h.d.recBtn)
	if h.d.capture.recorder == nil {
		t.Fatalf("not recording: %q", h.d.capture.line.Text)
	}
	for _, b := range []*widgets.ToolbarButton{h.d.recBtn, h.d.barRec} {
		if b.Caption() != "Stop" || !b.Danger() || b.Disabled() {
			t.Fatalf("record button while recording: %q danger=%v disabled=%v", b.Caption(), b.Danger(), b.Disabled())
		}
	}
	if !h.d.capture.recItem.Visible() || !strings.HasPrefix(h.d.capture.recLine.Text, "REC 00:00 · ") || !strings.HasSuffix(h.d.capture.recLine.Text, " MB") {
		t.Fatalf("REC line %q visible=%v", h.d.capture.recLine.Text, h.d.capture.recItem.Visible())
	}
	if h.d.shotBtn.Disabled() {
		t.Fatal("Screenshot must stay available while recording")
	}
	h.d.setConsoleView(true) // Stop stays available in the Console view
	if h.d.recBtn.Disabled() || !h.d.shotBtn.Disabled() {
		t.Fatal("Console view: Stop must stay enabled, Screenshot not")
	}
	h.d.setConsoleView(false)
	test.Tap(h.d.barRec)
	if h.d.capture.recorder != nil || h.d.recBtn.Caption() != "Record" || h.d.recBtn.Danger() || h.d.capture.recItem.Visible() {
		t.Fatal("recording state not cleared by Stop")
	}
	recv(t, saved)
	name := "openlamt-Büro-2026-11-23-101412.avi"
	if h.d.capture.line.Text != "Recording saved: "+name {
		t.Fatalf("status %q", h.d.capture.line.Text)
	}
	if n := aviFrames(t, filepath.Join(dir, name)); n < 1 {
		t.Fatalf("%d frames", n)
	}
}

// TestRecordStopsWithSessionAndWindow: the recording ends and its file is complete when the KVM
// session ends, and when the window closes.
func TestRecordStopsWithSessionAndWindow(t *testing.T) {
	dir := t.TempDir()
	h, _, saved := captureDevice(t, dir, false)
	test.Tap(h.d.recBtn)
	if h.d.capture.recorder == nil {
		t.Fatal("not recording")
	}
	h.disconnect(t)
	recv(t, saved)
	if h.d.capture.recorder != nil || !h.d.recBtn.Disabled() || h.d.recBtn.Caption() != "Record" {
		t.Fatal("recording not ended with the session")
	}
	aviFrames(t, filepath.Join(dir, "openlamt-Büro-2026-11-23-101412.avi"))
	if !strings.HasPrefix(h.d.capture.line.Text, "Recording saved: ") {
		t.Fatalf("status %q", h.d.capture.line.Text)
	}

	dir2 := t.TempDir()
	h2, _, saved2 := captureDevice(t, dir2, false)
	test.Tap(h2.d.recBtn)
	if h2.d.capture.recorder == nil {
		t.Fatal("not recording")
	}
	h2.d.win.Close()
	recv(t, saved2)
	aviFrames(t, filepath.Join(dir2, "openlamt-Büro-2026-11-23-101412.avi"))
}

// TestRecordWithSaveDialog: without a folder Record asks for the file first; a cancelled dialog
// starts nothing.
func TestRecordWithSaveDialog(t *testing.T) {
	h, save, saved := captureDevice(t, "", false)
	test.Tap(h.d.recBtn)
	if len(save.names) != 1 || save.kinds[0].ext != "avi" || save.names[0] != "openlamt-Büro-2026-11-23-101412.avi" || h.d.capture.recorder != nil {
		t.Fatalf("cancelled dialog: %v recording=%v", save.names, h.d.capture.recorder != nil)
	}
	save.path = filepath.Join(t.TempDir(), "session.avi")
	test.Tap(h.d.recBtn)
	if h.d.capture.recorder == nil {
		t.Fatalf("not recording: %q", h.d.capture.line.Text)
	}
	test.Tap(h.d.recBtn)
	recv(t, saved)
	if h.d.capture.line.Text != "Recording saved: session.avi" {
		t.Fatalf("status %q", h.d.capture.line.Text)
	}
	aviFrames(t, save.path)
}

func TestRecTimeAndSize(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "00:00", 134 * time.Second: "02:14", 3725 * time.Second: "1:02:05"} {
		if got := recTime(d); got != want {
			t.Errorf("recTime(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int64]string{400_000: "0.4 MB", 18_400_000: "18 MB"} {
		if got := recSize(n); got != want {
			t.Errorf("recSize(%d) = %q, want %q", n, got, want)
		}
	}
}
