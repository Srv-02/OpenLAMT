package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/mkiso"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Media of the device window (Media.dc.html, Dialogs.dc.html): the Media button opens a popover
// with three states. Nothing mounted offers "Mount ISO file…" and "Mount folder as ISO…";
// preparing shows the folder ISO being built (and, for every image, the mount until it runs);
// mounted shows the image, its read statistics, the next-start boot checkbox, Unmount and
// "Restart from ISO…". The image is mounted over IDE-R as a read-only CD drive. While it is
// mounted the status bar reads "ISO mounted · x MB/s" and the Power menu offers "Boot from ISO…".
// Closing the window with an image mounted (or being prepared) asks first.

// Geometry of the popover (Media.dc.html .pop): white card, radius 14, 16 padding, 12 between
// rows; 340 wide, 400 while mounted.
const (
	mediaPopRadius       float32 = 14
	mediaPopPad          float32 = 16
	mediaPopGap          float32 = 12
	mediaPopWidth        float32 = 340
	mediaPopWidthMounted float32 = 400
	mediaTitleSize       float32 = 14
	mediaSizeTextSize    float32 = 12
	mediaBarHeight       float32 = 6
	mediaBarRadius       float32 = 3
	mediaDotSize         float32 = 8
	mediaButtonHeight    float32 = 34
	mediaButtonIcon      float32 = 18
	mediaButtonGap       float32 = 10
	mediaButtonPad       float32 = 12
	mediaButtonRadius    float32 = 8
	mediaButtonText      float32 = 13
)

// mediaDialogSize is the size of the file and folder dialogs.
var mediaDialogSize = fyne.NewSize(800, 540)

// mediaRateWindow is the span of the read rate (MB/s); mediaProgressEvery throttles the build
// progress posted to the Fyne thread.
const (
	mediaRateWindow    = 5 * time.Second
	mediaProgressEvery = 100 * time.Millisecond
)

type mediaPhase int

const (
	mediaIdle      mediaPhase = iota // nothing mounted
	mediaPreparing                   // a folder ISO is built, or an image is being mounted
	mediaMounted                     // an IDE-R session runs
)

// mediaMount is one running IDE-R session. detached is set before the window closes the client
// itself (Unmount, window closed, abort), so the watcher does not report that end.
type mediaMount struct {
	c        *ider.Client
	detached atomic.Bool
}

// mediaPanel holds the media state of one device window; only touched on the Fyne thread.
type mediaPanel struct {
	d      *DeviceWindow
	phase  mediaPhase
	name   string // file name of the ISO, or the name of the folder
	folder bool   // the image is built from a folder

	ctx    context.Context // the attempt while preparing; nil otherwise
	cancel context.CancelFunc

	done, total int64       // folder build progress in bytes
	info        *mkiso.Info // the folder ISO is built

	mount    *mediaMount // the running session while mounted
	size     int64       // image size in bytes
	bootNext bool        // "Boot from this ISO on the next start" is ticked
	bootBusy bool        // NextBootFromISO or ClearISOBoot of the checkbox runs
	rate     rateMeter
	stopTick func()

	askIDER  *panel // "Turn on media redirection?" while shown; it owns the image then
	closeAsk *panel // "An ISO is still mounted" while shown

	pop     *mediaPopover // open popover or nil
	ui      mediaControls // controls of the shown state; zero while the popover is closed
	syncing bool          // the checkbox is set from code: no OnChanged action

	// Hooks, replaced in tests.
	pickISO, pickFolder func(chosen func(path string))  // Fyne file and folder dialogs
	ticker              func(each func()) (stop func()) // 1 s statistics while mounted
	now                 func() time.Time                // clock of the read rate
	onBuildProgress     func(done, total int64)         // called on the build goroutine; nil in production
	onEvent             func(string)                    // end of an asynchronous step; nil in production
	beforeBoot          func()                          // on the goroutine of the checkbox, before the session call; nil in production
}

// mediaControls are the controls of the popover state that is shown.
type mediaControls struct {
	title                                 *ellipsisText
	iso, folder, cancel, unmount, restart *widgets.PrimaryButton
	hint, progress, stats                 *widget.RichText
	bar                                   *mediaBar
	size                                  *ellipsisText
	check                                 *widget.Check
}

func newMediaPanel(d *DeviceWindow) *mediaPanel {
	m := &mediaPanel{d: d, ticker: secondTicker, now: time.Now}
	m.pickISO, m.pickFolder = m.dialogISO, m.dialogFolder
	return m
}

// secondTicker runs each on the Fyne thread every second until stop is called.
func secondTicker(each func()) (stop func()) {
	t := time.NewTicker(time.Second)
	done := make(chan struct{})
	go func() {
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fyne.Do(each)
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

func (m *mediaPanel) event(e string) {
	if m.onEvent != nil {
		m.onEvent(e)
	}
}

// mounted reports whether an image is mounted; nil-safe.
func (m *mediaPanel) mounted() bool { return m != nil && m.phase == mediaMounted }

// busy reports whether an image is mounted or being prepared; nil-safe.
func (m *mediaPanel) busy() bool { return m != nil && m.phase != mediaIdle }

// current reports whether ctx is the attempt that is still being prepared.
func (m *mediaPanel) current(ctx context.Context) bool {
	return ctx != nil && m.ctx == ctx && ctx.Err() == nil
}

// report shows an error through the window's error path: a certificate mismatch aborts the
// window, a cancelled consent or attempt stays silent, nothing is shown after the window closed.
func (m *mediaPanel) report(err error) {
	switch {
	case err == nil, errors.Is(err, errConsentCancelled), errors.Is(err, context.Canceled):
	case isMismatch(err):
		m.d.abort(err)
	case !m.d.closed:
		m.d.onError(err)
	}
}

// reset returns to "nothing mounted": a pending attempt is cancelled (its goroutine closes the
// image), the statistics stop, the status bar and the Power menu follow. A mounted client is
// left to the caller.
func (m *mediaPanel) reset() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.stopTick != nil {
		m.stopTick()
	}
	m.phase, m.ctx, m.cancel, m.stopTick = mediaIdle, nil, nil, nil
	m.mount, m.info, m.size = nil, nil, 0
	m.done, m.total = 0, 0
	m.bootNext, m.bootBusy = false, false
	m.rate = rateMeter{}
	m.d.setMediaStatus("")
	m.d.updatePowerItems()
	m.render()
}

// --- Mounting ---

func (m *mediaPanel) chooseISO() {
	m.dismiss()
	if m.phase != mediaIdle {
		return
	}
	m.d.releaseKeys()
	m.pickISO(func(path string) { m.start(path, false) })
}

func (m *mediaPanel) chooseFolder() {
	m.dismiss()
	if m.phase != mediaIdle {
		return
	}
	m.d.releaseKeys()
	m.pickFolder(func(path string) { m.start(path, true) })
}

// dialogISO is the Fyne file dialog for an .iso file.
func (m *mediaPanel) dialogISO(chosen func(path string)) {
	dlg := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			m.report(err)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		r.Close()
		chosen(path)
	}, m.d.win)
	dlg.SetFilter(storage.NewExtensionFileFilter([]string{".iso"}))
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}

// dialogFolder is the Fyne folder dialog.
func (m *mediaPanel) dialogFolder(chosen func(path string)) {
	dlg := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil {
			m.report(err)
			return
		}
		if u == nil {
			return
		}
		chosen(u.Path())
	}, m.d.win)
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}

// start opens the ISO file at path, or builds an ISO from the folder at path, and then mounts
// it (prepared, settingsRead, open, opened). The popover shows the progress.
func (m *mediaPanel) start(path string, folder bool) {
	if m.phase != mediaIdle || m.d.aborted || m.d.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.phase, m.ctx, m.cancel = mediaPreparing, ctx, cancel
	m.name, m.folder = filepath.Base(path), folder
	m.done, m.total, m.info = 0, 0, nil
	m.show()
	progress := m.progressFunc(ctx)
	work := m.d.exitWork
	done := work.add() // a cancelled build removes its partial file before it returns
	go func() {
		defer done()
		var img ider.Image
		var info *mkiso.Info
		var err error
		if folder {
			t, inf, e := mkiso.BuildTemp(ctx, path, mkiso.Options{Progress: progress})
			if e == nil {
				img, info = t, &inf
			}
			err = e
		} else {
			i, e := ider.OpenISO(path)
			if e == nil {
				img = i
			}
			err = e
		}
		img = work.track(img) // until closed: a folder ISO is deleted then
		fyne.Do(func() { m.prepared(ctx, img, info, err) })
	}()
}

// progressFunc is mkiso's Progress for the attempt ctx: it posts at most every
// mediaProgressEvery, and always the last call, to the Fyne thread.
func (m *mediaPanel) progressFunc(ctx context.Context) func(done, total int64) {
	hook := m.onBuildProgress
	var last time.Time
	return func(done, total int64) {
		if hook != nil {
			hook(done, total)
		}
		now := time.Now()
		if done < total && now.Sub(last) < mediaProgressEvery {
			return
		}
		last = now
		fyne.Do(func() {
			if m.current(ctx) {
				m.done, m.total = done, total
				m.refreshProgress()
			}
			m.event("progress")
		})
	}
}

// prepared runs when the image is open or built; it reads whether IDE-R is turned on.
func (m *mediaPanel) prepared(ctx context.Context, img ider.Image, info *mkiso.Info, err error) {
	defer m.event("prepared")
	if !m.current(ctx) { // cancelled: BuildTemp removed its partial file itself
		if img != nil {
			img.Close()
		}
		return
	}
	if err != nil {
		m.reset()
		m.report(err)
		return
	}
	if info != nil {
		m.info = info
		m.done = m.total
		m.refreshProgress()
	}
	sess := m.d.sess
	go func() {
		st, err := sess.Settings()
		fyne.Do(func() { m.settingsRead(ctx, img, st.Features.IDER, err) })
	}()
}

// settingsRead asks to turn media redirection on if it is off (Dialogs.dc.html); Cancel closes
// the image (a built one is deleted) and returns to "nothing mounted".
func (m *mediaPanel) settingsRead(ctx context.Context, img ider.Image, on bool, err error) {
	defer m.event("settings")
	switch {
	case !m.current(ctx):
		img.Close()
		return
	case err != nil:
		img.Close()
		m.reset()
		m.report(err)
		return
	case on:
		m.open(ctx, img)
		return
	}
	m.d.releaseKeys()
	args := map[string]any{"Name": m.d.name}
	p := showQuestion(m.d.win, icons.Media, toneInfo, lang.L("Turn on media redirection?"),
		lang.L("Media redirection (IDE-R) is turned off on “{{.Name}}”. Turn it on to mount ISO files and folders on this device? The setting stays on until you turn it off in Settings.", args),
		lang.L("Cancel"), lang.L("Turn On"), func() {
			m.askIDER = nil
			m.open(ctx, img)
		})
	p.spec.onCancel = func() {
		m.askIDER = nil
		img.Close()
		if m.current(ctx) {
			m.reset()
		}
	}
	m.askIDER = p
}

// open mounts img over IDE-R (OpenIDER turns IDE-R on and asks for user consent like KVM).
// The client owns the image on success; on error the image is still the caller's and is closed
// here (ider.Dial, amt.OpenIDER).
func (m *mediaPanel) open(ctx context.Context, img ider.Image) {
	if !m.current(ctx) {
		img.Close()
		return
	}
	prompt := &consentPrompt{ctx: ctx, ask: m.d.askConsent}
	sess := m.d.sess
	go func() {
		c, err := sess.OpenIDER(ctx, img, ider.StartNow, prompt)
		if err != nil {
			img.Close()
		}
		fyne.Do(func() { m.opened(ctx, c, err) })
	}()
}

// opened shows the mounted image, starts the statistics and watches the session.
func (m *mediaPanel) opened(ctx context.Context, c *ider.Client, err error) {
	defer m.event("opened")
	if !m.current(ctx) {
		if c != nil {
			go c.Close()
		}
		return
	}
	m.cancel() // the context only bounded the mount (ider.Dial keeps no reference)
	m.ctx, m.cancel = nil, nil
	if err != nil {
		m.reset()
		m.report(err)
		return
	}
	mt := &mediaMount{c: c}
	m.phase, m.mount, m.size = mediaMounted, mt, c.Stats().ImageSize
	m.rate = rateMeter{}
	m.stopTick = m.ticker(m.tick)
	go func() {
		<-c.Done()
		if !mt.detached.Load() {
			fyne.Do(func() { m.ended(mt) })
		}
	}()
	m.d.updatePowerItems()
	m.render()
	m.tick()
}

// cancelPrepare is Cancel of the preparing state: the build or mount stops, a built image is
// deleted.
func (m *mediaPanel) cancelPrepare() {
	if m.phase == mediaPreparing {
		m.reset()
	}
}

// tick reads the statistics once a second while mounted.
func (m *mediaPanel) tick() {
	if m.phase != mediaMounted {
		return
	}
	s := m.mount.c.Stats()
	now := m.now()
	m.rate.add(now, s.BytesRead)
	r := m.rate.rate()
	m.d.setMediaStatus(lang.L("ISO mounted") + " · " + formatRate(r))
	if m.ui.stats != nil {
		setMuted(m.ui.stats, statsLine(s, r, now))
	}
}

// ended runs when the device or the network ended the session: back to "nothing mounted", the
// boot option is cleared if it was set, and the cause is reported.
func (m *mediaPanel) ended(mt *mediaMount) {
	defer m.event("ended")
	if m.mount != mt {
		return
	}
	err, boot := mt.c.Err(), m.bootNext
	m.reset()
	if boot {
		m.clearBoot()
	}
	if !errors.Is(err, ider.ErrClosed) {
		m.report(err)
	}
}

// unmount closes the session off the Fyne thread and clears the next-start boot option if it
// was set.
func (m *mediaPanel) unmount() {
	if m.phase != mediaMounted {
		return
	}
	mt, boot := m.mount, m.bootNext
	mt.detached.Store(true)
	m.reset()
	sess := m.d.sess
	done := m.d.exitWork.add()
	go func() {
		defer done()
		mt.c.Close()
		var err error
		if boot {
			err = sess.ClearISOBoot()
		}
		fyne.Do(func() {
			m.report(err)
			m.event("unmounted")
		})
	}()
}

// release ends media for a window that closes or is aborted: the popover and an open
// redirection question close, a pending attempt is cancelled and a mounted session detached.
// It returns the blocking rest (closing the session, clearing the boot option if clearBoot and
// it was set), which shutdown runs before it releases the session. nil-safe.
func (m *mediaPanel) release(clearBoot bool) func() {
	if m == nil || m.phase == mediaIdle && m.pop == nil && m.askIDER == nil {
		return func() {} // nothing to end: the closing window is left alone
	}
	m.dismiss()
	if q := m.askIDER; q != nil {
		m.askIDER = nil
		q.Dismiss() // closes the image
	}
	mt, boot := m.mount, m.bootNext && clearBoot
	if mt != nil {
		mt.detached.Store(true)
	}
	m.reset()
	if mt == nil {
		return func() {}
	}
	sess := m.d.sess
	return func() {
		mt.c.Close()
		if boot {
			_ = sess.ClearISOBoot()
		}
	}
}

// --- Boot ---

// setBootNext is the checkbox: on sets the IDE-R boot option for the next start, off clears it.
// A failure puts the checkbox back and is reported.
func (m *mediaPanel) setBootNext(on bool) {
	if m.syncing || m.phase != mediaMounted || m.bootBusy || on == m.bootNext {
		return
	}
	m.bootNext, m.bootBusy = on, true
	m.syncCheck()
	mt, sess, hook := m.mount, m.d.sess, m.beforeBoot
	go func() {
		if hook != nil {
			hook()
		}
		var err error
		if on {
			err = sess.NextBootFromISO()
		} else {
			err = sess.ClearISOBoot()
		}
		fyne.Do(func() { m.bootSet(mt, on, err) })
	}()
}

func (m *mediaPanel) bootSet(mt *mediaMount, on bool, err error) {
	defer m.event("boot")
	if m.mount != mt { // unmounted meanwhile: the image is gone, the option must not stay set
		if on && err == nil && !m.d.closed {
			m.clearBoot()
		}
		m.report(err)
		return
	}
	m.bootBusy = false
	if err != nil {
		m.bootNext = !on
	}
	m.syncCheck()
	m.report(err)
}

// clearBoot clears the IDE-R boot option off the Fyne thread.
func (m *mediaPanel) clearBoot() {
	sess := m.d.sess
	done := m.d.exitWork.add()
	go func() {
		err := sess.ClearISOBoot()
		done()
		fyne.Do(func() {
			m.report(err)
			m.event("cleared")
		})
	}()
}

// syncCheck shows bootNext in the checkbox, which is locked while a change runs.
func (m *mediaPanel) syncCheck() {
	c := m.ui.check
	if c == nil {
		return
	}
	m.syncing = true
	c.SetChecked(m.bootNext)
	m.syncing = false
	if m.bootBusy {
		c.Disable()
	} else {
		c.Enable()
	}
}

// askBoot asks once before "Restart from ISO…" or "Boot from ISO…" and then runs the power
// action BootToISO (reset, or power on a device that is off); the general power confirmation is
// not shown in addition.
func (m *mediaPanel) askBoot() {
	if m.phase != mediaMounted {
		return
	}
	m.dismiss()
	m.d.releaseKeys()
	args := map[string]any{"Name": m.d.name, "ISO": m.name}
	showQuestion(m.d.win, icons.Power, toneInfo, lang.L("Restart “{{.Name}}” from the ISO?", args),
		lang.L("The device restarts now and boots once from “{{.ISO}}”. Unsaved work on the device is lost. After that, it boots from its own drive again.", args),
		lang.L("Cancel"), lang.L("Restart"), func() {
			if m.d.powerAllowed(amt.BootToISO) {
				m.d.execute(amt.BootToISO)
			}
		})
	if m.d.onConfirmShown != nil {
		m.d.onConfirmShown()
	}
}

// powerChanged follows the Power menu: "Restart from ISO…" is enabled while Boot from ISO is.
func (m *mediaPanel) powerChanged() {
	if m == nil || m.ui.restart == nil {
		return
	}
	if m.d.powerAllowed(amt.BootToISO) {
		m.ui.restart.Enable()
	} else {
		m.ui.restart.Disable()
	}
}

// --- Closing the window ---

// requestClose closes the window through closeWin, unless an image is mounted or being
// prepared: then it asks first (Dialogs.dc.html "An ISO is still mounted"); "Unmount and
// Close" closes the window, whose close handler unmounts.
func (d *DeviceWindow) requestClose(closeWin func()) {
	m := d.media
	if !m.busy() {
		closeWin()
		return
	}
	if m.closeAsk != nil {
		return
	}
	d.askClose(closeWin, nil)
}

// askClose shows "An ISO is still mounted": Unmount and Close runs closeWin, Keep Open runs kept
// (may be nil). A question that is already open takes over closeWin and kept instead of a second
// one (the quit guard asks a window whose close button question is still open).
func (d *DeviceWindow) askClose(closeWin, kept func()) {
	m := d.media
	onClose := func() {
		m.closeAsk = nil
		closeWin()
	}
	onKeep := func() {
		m.closeAsk = nil
		if kept != nil {
			kept()
		}
	}
	if m.closeAsk != nil {
		m.closeAsk.spec.onAction, m.closeAsk.spec.onCancel = onClose, onKeep
		return
	}
	d.releaseKeys()
	args := map[string]any{"ISO": m.name, "Name": d.name}
	m.closeAsk = showQuestion(d.win, icons.Warning, toneWarning, lang.L("An ISO is still mounted"),
		lang.L("“{{.ISO}}” is mounted on “{{.Name}}”. Closing the window unmounts it. If the device is installing or flashing from it right now, that will fail.", args),
		lang.L("Keep Open"), lang.L("Unmount and Close"), onClose)
	m.closeAsk.spec.onCancel = onKeep
}

// --- Popover ---

// toggle is the Media button.
func (m *mediaPanel) toggle() {
	if m.pop != nil {
		m.dismiss()
		return
	}
	m.show()
}

// dismiss closes the popover; nil-safe.
func (m *mediaPanel) dismiss() {
	if m != nil && m.pop != nil {
		m.pop.Dismiss()
	}
}

// show opens the popover below the Media button, or shows the current state in the open one.
// The Media button stays active while it is open.
func (m *mediaPanel) show() {
	if m.d.aborted || m.d.closed || m.d.fullScreen {
		return
	}
	if m.pop == nil {
		m.d.releaseKeys()
		m.pop = newMediaPopover(m.d.win.Canvas())
		m.pop.onDismiss = func() {
			m.pop = nil
			m.ui = mediaControls{}
			m.d.mediaBtn.SetActive(false)
			if !m.d.fullScreen {
				m.d.focusView()
			}
		}
		m.d.mediaBtn.SetActive(true)
	}
	m.render()
	btn := m.d.mediaBtn
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(btn)
	top := (btn.Size().Height - style.ToolbarItemHeight) / 2
	m.pop.showAt(pos.Add(fyne.NewPos(menuOffsetX, top+menuOffsetY)))
}

// render builds the rows of the current state into the open popover.
func (m *mediaPanel) render() {
	m.ui = mediaControls{}
	if m.pop == nil {
		return
	}
	var rows []fyne.CanvasObject
	width := mediaPopWidth
	switch m.phase {
	case mediaIdle:
		m.ui.title = mediaTitle(lang.L("Media"))
		m.ui.iso = mediaButton(lang.L("Mount ISO file…"), icons.Media, false, m.chooseISO)
		m.ui.folder = mediaButton(lang.L("Mount folder as ISO…"), icons.Folder, false, m.chooseFolder)
		m.ui.hint = mutedText(lang.L("The device sees a read-only CD drive. A folder with EFI/BOOT/BOOTX64.EFI also boots in UEFI, for example into the EFI shell. Files must be smaller than 4 GB."))
		rows = []fyne.CanvasObject{m.ui.title, m.ui.iso, m.ui.folder, unpadRich(m.ui.hint)}
	case mediaPreparing:
		m.ui.title = mediaTitle(lang.L("Preparing “{{.Name}}”", map[string]any{"Name": m.name}))
		rows = []fyne.CanvasObject{m.ui.title}
		if m.folder {
			m.ui.bar = newMediaBar()
			m.ui.progress = mutedText("")
			rows = append(rows, m.ui.bar.Container, unpadRich(m.ui.progress))
		}
		m.ui.cancel = mediaButton(lang.L("Cancel"), "", false, m.cancelPrepare)
		rows = append(rows, container.NewHBox(layout.NewSpacer(), m.ui.cancel))
		m.refreshProgress()
	case mediaMounted:
		width = mediaPopWidthMounted
		m.ui.title = mediaTitle(m.name)
		m.ui.size = newEllipsisText(formatBytes(m.size), mediaSizeTextSize, style.ColorNameTextSecondary, false)
		dot := widgets.NewStatusDot(widgets.DotOn, mediaDotSize)
		head := container.New(&mediaHeadLayout{gap: mediaButtonGap}, dot, m.ui.title, m.ui.size)
		m.ui.stats = mutedText("")
		m.ui.check = widget.NewCheck(lang.L("Boot from this ISO on the next start"), nil)
		m.ui.check.SetChecked(m.bootNext)
		m.ui.check.OnChanged = m.setBootNext
		m.ui.unmount = mediaButton(lang.L("Unmount"), "", false, m.unmount)
		m.ui.restart = mediaButton(lang.L("Restart from ISO…"), "", true, func() { m.d.run(amt.BootToISO) })
		buttons := container.New(layout.NewCustomPaddedHBoxLayout(8), layout.NewSpacer(), m.ui.unmount, m.ui.restart)
		rows = []fyne.CanvasObject{head, unpadRich(m.ui.stats), m.ui.check, buttons}
		m.syncCheck()
		m.powerChanged()
		if m.mount != nil {
			s := m.mount.c.Stats()
			now := m.now()
			setMuted(m.ui.stats, statsLine(s, m.rate.rate(), now))
		}
	}
	m.pop.setContent(width, rows)
}

// refreshProgress shows the build progress in the open popover.
func (m *mediaPanel) refreshProgress() {
	if m.ui.bar == nil {
		return
	}
	frac := float32(0)
	if m.total > 0 {
		frac = float32(m.done) / float32(m.total)
	} else if m.info != nil {
		frac = 1
	}
	m.ui.bar.setValue(frac)
	setMuted(m.ui.progress, progressLine(m.done, m.total, m.info))
}

// mediaTitle is the 14 px semibold title of a state; long names end in "…".
func mediaTitle(s string) *ellipsisText {
	t := newEllipsisText(s, mediaTitleSize, theme.ColorNameForeground, true)
	t.shrink = true
	return t
}

// mediaButton is a 34 px button of the popover (Media.dc.html .btn, .btn-p) with regular text:
// white and bordered, or the accent button with high; with a symbol, symbol and text start at the left.
func mediaButton(text string, icon icons.Name, high bool, tapped func()) *widgets.PrimaryButton {
	var res fyne.Resource
	if icon != "" {
		res = icons.GetStroke(icon, icons.Foreground, 1.8)
	}
	b := widgets.NewPrimaryButton(text, res, tapped)
	b.Metrics = &widgets.ButtonMetrics{Height: mediaButtonHeight, Radius: mediaButtonRadius, PaddingH: mediaButtonPad,
		IconSize: mediaButtonIcon, Gap: mediaButtonGap, TextSize: mediaButtonText, Regular: true,
		Neutral: style.ColorNameField, NeutralBorder: style.ColorNameControlBorder}
	b.Leading = res != nil
	if !high {
		b.Importance = widget.MediumImportance
	}
	return b
}

// mutedText is a wrapped 12 px secondary text (Media.dc.html .muted).
func mutedText(s string) *widget.RichText {
	rt := widget.NewRichText(&widget.TextSegment{Text: s, Style: widget.RichTextStyle{
		ColorName: style.ColorNameTextSecondary, SizeName: style.SizeNameSmallText}})
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

func setMuted(rt *widget.RichText, s string) {
	seg := rt.Segments[0].(*widget.TextSegment)
	if seg.Text == s {
		return
	}
	seg.Text = s
	rt.Refresh()
}

// unpadRich removes the inner padding of a RichText, so its text lines up with the buttons.
func unpadRich(rt *widget.RichText) fyne.CanvasObject {
	p := -theme.InnerPadding()
	return container.New(layout.NewCustomPaddedLayout(p, p, p, p), rt)
}

// mediaHeadLayout places objs[0] (the dot) and objs[2] (the size) at their size, centred, at
// the start and the end; objs[1] (the name) takes the width between them.
type mediaHeadLayout struct{ gap float32 }

func (l *mediaHeadLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	a, b, c := objs[0].MinSize(), objs[1].MinSize(), objs[2].MinSize()
	return fyne.NewSize(a.Width+b.Width+c.Width+2*l.gap, max(a.Height, b.Height, c.Height))
}

func (l *mediaHeadLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	a, c := objs[0].MinSize(), objs[2].MinSize()
	objs[0].Move(fyne.NewPos(0, (size.Height-a.Height)/2))
	objs[0].Resize(a)
	objs[2].Move(fyne.NewPos(size.Width-c.Width, (size.Height-c.Height)/2))
	objs[2].Resize(c)
	x := a.Width + l.gap
	objs[1].Move(fyne.NewPos(x, 0))
	objs[1].Resize(fyne.NewSize(max(0, size.Width-x-c.Width-l.gap), size.Height))
}

// mediaBar is the 6 px progress bar in the accent color on a light track. Its Container goes
// into the layout (drivers walk only *fyne.Container children).
type mediaBar struct {
	*fyne.Container
	value float32
}

func newMediaBar() *mediaBar {
	track := style.BindRect(&canvas.Rectangle{CornerRadius: mediaBarRadius}, style.ColorNameSegment, "")
	fill := style.BindRect(&canvas.Rectangle{CornerRadius: mediaBarRadius}, theme.ColorNamePrimary, "")
	b := &mediaBar{}
	b.Container = container.New(&mediaBarLayout{b: b}, track, fill)
	return b
}

func (b *mediaBar) setValue(v float32) {
	b.value = min(max(v, 0), 1)
	b.Container.Refresh()
}

type mediaBarLayout struct{ b *mediaBar }

func (l *mediaBarLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, mediaBarHeight)
}

func (l *mediaBarLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(size)
	objs[1].Move(fyne.NewPos(0, 0))
	objs[1].Resize(fyne.NewSize(size.Width*l.b.value, size.Height))
}

// mediaPopover is the floating card under the Media button: an overlay over the whole canvas
// that closes on a tap outside the card or on Esc, like widgets.MenuPopup.
type mediaPopover struct {
	widget.BaseWidget
	canvas    fyne.Canvas
	bg        *canvas.Rectangle
	rows      *fyne.Container // the state's rows at the inner width
	card      fyne.CanvasObject
	width     float32
	pos       fyne.Position
	shown     bool
	onDismiss func()
}

var (
	_ fyne.Tappable  = (*mediaPopover)(nil)
	_ fyne.Focusable = (*mediaPopover)(nil)
)

func newMediaPopover(c fyne.Canvas) *mediaPopover {
	p := &mediaPopover{canvas: c, width: mediaPopWidth}
	p.ExtendBaseWidget(p)
	p.bg = &canvas.Rectangle{CornerRadius: mediaPopRadius, StrokeWidth: 1,
		Shadow: canvas.Shadow{BlurRadius: 32, Offset: fyne.NewPos(0, 12), Variant: canvas.DropShadow}}
	p.rows = container.NewStack()
	body := container.New(layout.NewCustomPaddedLayout(mediaPopPad, mediaPopPad, mediaPopPad, mediaPopPad), p.rows)
	p.card = container.NewThemeOverride(container.NewStack(p.bg, body), overrideTheme)
	p.recolor()
	return p
}

// setContent shows rows in a card of width.
func (p *mediaPopover) setContent(width float32, rows []fyne.CanvasObject) {
	p.width = width
	col := container.New(layout.NewCustomPaddedVBoxLayout(mediaPopGap), rows...)
	p.rows.Objects = []fyne.CanvasObject{container.New(&fixedWidthLayout{width: width - 2*mediaPopPad}, col)}
	p.rows.Refresh()
	if p.shown {
		p.Refresh()
	}
}

// showAt shows the card with its top left corner at pos (canvas coordinates) and takes the focus.
func (p *mediaPopover) showAt(pos fyne.Position) {
	p.pos = pos
	if !p.shown {
		p.shown = true
		p.canvas.Overlays().Add(p)
	}
	p.Refresh()
	p.canvas.Focus(p)
}

// Dismiss closes the popover and calls onDismiss; nothing happens if it is not shown.
func (p *mediaPopover) Dismiss() {
	if !p.shown {
		return
	}
	p.shown = false
	p.canvas.Overlays().Remove(p)
	if p.onDismiss != nil {
		p.onDismiss()
	}
}

func (p *mediaPopover) recolor() {
	p.bg.FillColor = style.Color(style.ColorNameMenuPanel)
	p.bg.StrokeColor = style.Color(style.ColorNameDialogBorder)
	p.bg.Shadow.Color = style.Color(style.ColorNameMenuShadow)
	p.bg.Refresh()
}

// Refresh lays the card out again in the colors of the current theme variant.
func (p *mediaPopover) Refresh() {
	p.recolor()
	p.BaseWidget.Refresh()
}

// Tapped outside the card closes the popover; taps on the card's padding do nothing.
func (p *mediaPopover) Tapped(e *fyne.PointEvent) {
	pos, size := p.card.Position(), p.card.Size()
	if e != nil && e.Position.X >= pos.X && e.Position.Y >= pos.Y && e.Position.X < pos.X+size.Width && e.Position.Y < pos.Y+size.Height {
		return
	}
	p.Dismiss()
}

func (p *mediaPopover) FocusGained()   {}
func (p *mediaPopover) FocusLost()     {}
func (p *mediaPopover) TypedRune(rune) {}

func (p *mediaPopover) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyEscape {
		p.Dismiss()
	}
}

func (p *mediaPopover) CreateRenderer() fyne.WidgetRenderer { return &mediaPopoverRenderer{p: p} }

type mediaPopoverRenderer struct{ p *mediaPopover }

func (r *mediaPopoverRenderer) Layout(size fyne.Size) {
	cs := fyne.NewSize(r.p.width, r.p.card.MinSize().Height)
	pos := r.p.pos
	pos.X = max(0, min(pos.X, size.Width-cs.Width))
	pos.Y = max(0, min(pos.Y, size.Height-cs.Height))
	r.p.card.Move(pos)
	r.p.card.Resize(cs)
}

func (r *mediaPopoverRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *mediaPopoverRenderer) Refresh()                     { r.Layout(r.p.Size()); canvas.Refresh(r.p) }
func (r *mediaPopoverRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.p.card} }
func (r *mediaPopoverRenderer) Destroy()                     {}

// --- Texts ---

// rateMeter is the read rate over the samples of the last mediaRateWindow.
type rateMeter struct{ samples []rateSample }

type rateSample struct {
	t time.Time
	n int64
}

// add records n bytes read in total at t and drops samples older than the window (the oldest
// one inside it stays as the base).
func (r *rateMeter) add(t time.Time, n int64) {
	r.samples = append(r.samples, rateSample{t, n})
	for len(r.samples) > 1 && t.Sub(r.samples[0].t) > mediaRateWindow {
		r.samples = r.samples[1:]
	}
}

// rate is bytes per second between the oldest and the newest sample; 0 with fewer than two.
func (r *rateMeter) rate() float64 {
	if len(r.samples) < 2 {
		return 0
	}
	a, b := r.samples[0], r.samples[len(r.samples)-1]
	dt := b.t.Sub(a.t).Seconds()
	if dt <= 0 || b.n < a.n {
		return 0
	}
	return float64(b.n-a.n) / dt
}

// formatBytes is a size in decimal units as in the design: "640 KB", "214 MB", "1.0 GB".
func formatBytes(n int64) string {
	if n < 1_000_000 {
		return fmt.Sprintf("%d KB", (n+500)/1000)
	}
	if mb := (n + 500_000) / 1_000_000; mb < 1000 {
		return fmt.Sprintf("%d MB", mb)
	}
	tenths := (n + 50_000_000) / 100_000_000
	return lang.L("{{.Int}}.{{.Frac}} GB", map[string]any{"Int": tenths / 10, "Frac": tenths % 10})
}

// formatRate is a read rate in bytes per second as "18.2 MB/s".
func formatRate(bps float64) string {
	tenths := int64(math.Round(max(bps, 0) / 100_000))
	return lang.L("{{.Int}}.{{.Frac}} MB/s", map[string]any{"Int": tenths / 10, "Frac": tenths % 10})
}

// formatAge is a duration in whole seconds, minutes or hours and minutes: "3 s", "6 min", "1 h 5 min".
func formatAge(d time.Duration) string {
	s := int64(max(d, 0) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min", s/60)
	}
	return fmt.Sprintf("%d h %d min", s/3600, s%3600/60)
}

// statsLine is the statistics of a mounted image (Media.dc.html): "Read 118 MB · 18.2 MB/s ·
// last read 3 s ago · connected 6 min"; "last read" only once something was read.
func statsLine(s ider.Stats, rate float64, now time.Time) string {
	parts := []string{lang.L("Read {{.Size}}", map[string]any{"Size": formatBytes(s.BytesRead)}), formatRate(rate)}
	if !s.LastRead.IsZero() {
		parts = append(parts, lang.L("last read {{.Time}} ago", map[string]any{"Time": formatAge(now.Sub(s.LastRead))}))
	}
	parts = append(parts, lang.L("connected {{.Time}}", map[string]any{"Time": formatAge(now.Sub(s.Started))}))
	return strings.Join(parts, " · ")
}

// progressLine is the build progress of a folder ISO (Media.dc.html): "62 % · 640 MB of 1.0 GB",
// followed by the number of files and "UEFI-bootable" once the image is built.
func progressLine(done, total int64, info *mkiso.Info) string {
	pct := int64(0)
	switch {
	case total > 0:
		pct = done * 100 / total
	case info != nil:
		pct = 100
	}
	parts := []string{fmt.Sprintf("%d %%", pct),
		lang.L("{{.Done}} of {{.Total}}", map[string]any{"Done": formatBytes(done), "Total": formatBytes(total)})}
	if info != nil {
		if info.Files == 1 {
			parts = append(parts, lang.L("1 file"))
		} else {
			parts = append(parts, lang.L("{{.Count}} files", map[string]any{"Count": info.Files}))
		}
		if info.Bootable {
			parts = append(parts, lang.L("UEFI-bootable"))
		}
	}
	return strings.Join(parts, " · ")
}
