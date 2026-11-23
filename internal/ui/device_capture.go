package ui

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"

	"github.com/Srv-02/openlamt/internal/capture"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Screenshot (issue #4) and Record (issue #26) of the device window (gesamtdesign, device
// window): both save the remote screen at its native resolution, never the toolbar or the full
// screen bar, into the folder of the settings or, without one, where the save dialog says. The
// status bar reports the saved file or the error.

// captureState is what the device window keeps for screenshots and recordings; Fyne thread only.
type captureState struct {
	recorder  *capture.Recorder // running recording or nil
	stopTick  func()            // stops the one-second refresh of the REC line; nil while not recording
	recLine   *ellipsisText     // "REC 02:14 · 18 MB", red; hidden while not recording
	recItem   fyne.CanvasObject // the REC symbol and recLine in the status bar
	line      *ellipsisText     // result of the last screenshot or recording; hidden while empty
	pickers   filePickers       // save dialog; replaced in tests
	now       func() time.Time  // time in the file name; replaced in tests
	onSaved   func()            // test hook: a screenshot or recording was written (or failed)
	options   capture.RecorderOptions
	checkSync bool // tests: check the folder on the Fyne thread instead of a goroutine
}

// buildCapture creates the Screenshot and Record buttons of the toolbar and the full screen bar
// and the status bar items. Called from buildKVM before the bar is built.
func (d *DeviceWindow) buildCapture() {
	d.shotBtn = widgets.NewToolbarButton(icons.Camera, lang.L("Screenshot"), d.takeScreenshot)
	d.recBtn = widgets.NewToolbarButton(icons.Record, lang.L("Record"), d.toggleRecording)
	d.barShot = widgets.NewToolbarButton(icons.Camera, lang.L("Screenshot"), d.takeScreenshot)
	d.barRec = widgets.NewToolbarButton(icons.Record, lang.L("Record"), d.toggleRecording)
	d.capture.pickers = defaultPickers()
	d.capture.now = time.Now

	size := style.Size(style.SizeNameSecondaryText)
	d.capture.line = newEllipsisText("", size, style.ColorNameTextSecondary, false)
	d.capture.line.Hide()
	d.capture.recLine = newEllipsisText("", size, style.ColorNameErrorText, true)
	dot := widgets.NewSymbol(icons.Get(icons.Record, icons.Danger), statusLockSize)
	d.capture.recItem = container.New(layout.NewCustomPaddedHBoxLayout(statusLockGap), container.NewCenter(dot), d.capture.recLine)
	d.capture.recItem.Hide()
}

// captureAllowed reports whether Screenshot and Record may start: a KVM session with at least
// one frame, in the KVM view (like Ctrl+Alt+Del there).
func (d *DeviceWindow) captureAllowed() bool {
	return (d.kvmClient != nil || d.kvmReconnecting) && d.view.hasFrame && !d.consoleView && !d.aborted && !d.closed
}

// syncCaptureButtons enables Screenshot and Record in the toolbar and the full screen bar; while
// a recording runs, Record reads "Stop" in red and stays enabled in every view.
func (d *DeviceWindow) syncCaptureButtons() {
	on := d.captureAllowed()
	recording := d.capture.recorder != nil
	for _, b := range []*widgets.ToolbarButton{d.shotBtn, d.barShot} {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
	for _, b := range []*widgets.ToolbarButton{d.recBtn, d.barRec} {
		if recording {
			b.SetIcon(icons.Stop)
			b.SetCaption(lang.L("Stop"))
		} else {
			b.SetIcon(icons.Record)
			b.SetCaption(lang.L("Record"))
		}
		b.SetDanger(recording)
		if on || recording {
			b.Enable()
		} else {
			b.Disable()
		}
	}
}

// setCaptureStatus shows text in the status bar until the next screenshot or recording.
func (d *DeviceWindow) setCaptureStatus(text string) {
	d.capture.line.SetText(text)
	if text == "" {
		d.capture.line.Hide()
	} else {
		d.capture.line.Show()
	}
	d.relayoutStatus()
}

// captureDest finds the file for a new screenshot or recording of kind: a free name in the
// folder of the settings, or the save dialog with the proposed name when no folder is set or the
// folder cannot take the file (the status bar says why). chosen runs on the Fyne thread.
func (d *DeviceWindow) captureDest(kind captureKind, chosen func(path string)) {
	name := capture.FileName(d.name, d.capture.now(), kind.ext)
	dir := d.settings.CaptureFolder()
	decide := func(err error) {
		if d.closed {
			return
		}
		switch {
		case err == nil:
			chosen(capture.UniquePath(dir, name))
			return
		case errors.Is(err, capture.ErrFolderMissing):
			d.setCaptureStatus(lang.L("The folder {{.Folder}} does not exist. Choose where to save the file.", map[string]any{"Folder": dir}))
		case errors.Is(err, capture.ErrFolderReadOnly):
			d.setCaptureStatus(lang.L("The folder {{.Folder}} is not writable. Choose where to save the file.", map[string]any{"Folder": dir}))
		}
		d.releaseKeys()
		d.capture.pickers.save(d.win, kind, name, chosen, func(err error) {
			d.setCaptureStatus(lang.L("Could not save the file: {{.Err}}", map[string]any{"Err": describe(err)}))
		})
	}
	if dir == "" || d.capture.checkSync {
		decide(capture.CheckFolder(dir))
		return
	}
	go func() { // a folder on a network share may take a while to answer
		err := capture.CheckFolder(dir)
		fyne.Do(func() { decide(err) })
	}()
}

// takeScreenshot saves the image shown at the moment of the click as PNG.
func (d *DeviceWindow) takeScreenshot() {
	if !d.captureAllowed() {
		return
	}
	img := d.view.frame()
	if img == nil {
		return
	}
	d.touchIdle() // local input, like Ctrl+Alt+Del
	d.focusView()
	d.captureDest(screenshotKind(), func(path string) { d.saveScreenshot(path, img) })
}

func (d *DeviceWindow) saveScreenshot(path string, img *image.RGBA) {
	done := d.exitWork.add()
	go func() {
		err := capture.SavePNG(path, img)
		done()
		fyne.Do(func() {
			if !d.closed {
				if err != nil {
					d.setCaptureStatus(lang.L("Could not save the screenshot: {{.Err}}", map[string]any{"Err": describe(err)}))
				} else {
					d.setCaptureStatus(lang.L("Screenshot saved: {{.Name}}", map[string]any{"Name": filepath.Base(path)}))
				}
			}
			if d.capture.onSaved != nil {
				d.capture.onSaved()
			}
		})
	}()
}

// toggleRecording starts a recording or stops the running one.
func (d *DeviceWindow) toggleRecording() {
	if d.capture.recorder != nil {
		d.stopRecording()
		return
	}
	if !d.captureAllowed() {
		return
	}
	d.touchIdle()
	d.focusView()
	d.captureDest(recordingKind(), d.startRecording)
}

// startRecording records into path from the frame shown now on; the session may have ended
// while the save dialog was open.
func (d *DeviceWindow) startRecording(path string) {
	if d.capture.recorder != nil || !d.captureAllowed() {
		return
	}
	img := d.view.frame()
	if img == nil {
		return
	}
	r, err := capture.StartRecorder(path, img, d.capture.options)
	if err != nil {
		d.setCaptureStatus(lang.L("Could not start the recording: {{.Err}}", map[string]any{"Err": describe(err)}))
		return
	}
	d.capture.recorder = r
	d.view.rec.Store(r)
	d.setCaptureStatus("")
	d.refreshRecLine()
	d.capture.recItem.Show()
	d.capture.stopTick = secondTicker(d.refreshRecLine)
	d.syncCaptureButtons()
	d.relayoutStatus()
	done := d.exitWork.add() // quitting waits until the file is complete
	go func() {
		<-r.Done() // Stop, a write error or the size limit
		err := r.Stop()
		done()
		fyne.Do(func() { d.recordingEnded(r, err) })
	}()
}

// stopRecording ends the running recording: the button and the status bar switch back at once,
// the file is completed on a goroutine and recordingEnded reports it. Safe to call at any time.
func (d *DeviceWindow) stopRecording() {
	r := d.capture.recorder
	if r == nil {
		return
	}
	d.dropRecorder()
	go r.Stop()
}

// dropRecorder forgets the running recording in the window.
func (d *DeviceWindow) dropRecorder() {
	d.capture.recorder = nil
	d.view.rec.Store(nil)
	if d.capture.stopTick != nil {
		d.capture.stopTick()
		d.capture.stopTick = nil
	}
	if d.closed {
		return
	}
	d.capture.recItem.Hide()
	d.syncCaptureButtons()
	d.relayoutStatus()
}

// recordingEnded reports a recording whose file is complete (or failed).
func (d *DeviceWindow) recordingEnded(r *capture.Recorder, err error) {
	if d.capture.recorder == r { // ended by itself: write error or size limit
		d.dropRecorder()
	}
	if !d.closed {
		name := filepath.Base(r.Path())
		switch {
		case err == nil:
			d.setCaptureStatus(lang.L("Recording saved: {{.Name}}", map[string]any{"Name": name}))
		case errors.Is(err, capture.ErrLimit):
			d.setCaptureStatus(lang.L("Recording stopped at the 2 GB limit and saved: {{.Name}}", map[string]any{"Name": name}))
		default:
			d.setCaptureStatus(lang.L("Could not save the recording: {{.Err}}", map[string]any{"Err": describe(err)}))
		}
	}
	if d.capture.onSaved != nil {
		d.capture.onSaved()
	}
}

// refreshRecLine shows the recorded time and the file size.
func (d *DeviceWindow) refreshRecLine() {
	r := d.capture.recorder
	if r == nil {
		return
	}
	el, size := r.Stats()
	d.capture.recLine.SetText(lang.L("REC {{.Time}} · {{.Size}}", map[string]any{"Time": recTime(el), "Size": recSize(size)}))
	d.relayoutStatus()
}

// recTime is m:ss, or h:mm:ss from an hour on.
func recTime(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// recSize is the file size in MB: one decimal below 10 MB, whole numbers above.
func recSize(n int64) string {
	mb := float64(n) / 1e6
	if mb < 10 {
		return fmt.Sprintf("%.1f MB", mb)
	}
	return fmt.Sprintf("%d MB", int64(mb))
}
