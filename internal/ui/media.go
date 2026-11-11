package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/mkiso"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Media of the device window (Extras.dc.html, Dialogs.dc.html): the Media button opens a
// drop-down menu like Power, with three states. Nothing mounted offers "Mount ISO…" and "Mount
// Folder…" (after the hint "Mount a folder", unless it was hidden with "Don’t show again");
// preparing shows the name, for a folder ISO the build progress, and Cancel; mounted shows the
// name and the read rate, "Boot from ISO on next start" with a check mark, "Restart from ISO…"
// and Unmount. The image is mounted over IDE-R as a read-only CD drive. While it is mounted the
// status bar reads "ISO mounted · x MB/s" and the Power menu offers "Boot from ISO…". Closing the
// window with an image mounted (or being prepared) asks first.

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

	mount    *mediaMount   // the running session while mounted
	bootNext bool          // "Boot from ISO on next start" is checked
	bootBusy bool          // NextBootFromISO or ClearISOBoot of that item runs
	bootDone chan struct{} // closed when that call returned; set while bootBusy
	rate     rateMeter
	stopTick func()

	askIDER  *panel // "Enable media redirection?" while shown; it owns the image then
	closeAsk *panel // "An ISO is still mounted" while shown

	menu  *widgets.MenuPopup // open menu or nil
	shown *fyne.Menu         // items of the open menu; nil while it is closed
	line  *widgets.MenuInfo  // info line of the open menu; nil for none
	items mediaItems         // items of the open menu's state; zero while it is closed

	// Hooks, replaced in tests.
	pickISO, pickFolder func(chosen func(path string))  // Fyne file and folder dialogs
	ticker              func(each func()) (stop func()) // 1 s statistics while mounted
	now                 func() time.Time                // clock of the read rate
	onBuildProgress     func(done, total int64)         // called on the build goroutine; nil in production
	onEvent             func(string)                    // end of an asynchronous step; nil in production
	beforeBoot          func()                          // on the goroutine of the boot item, before the session call; nil in production
}

// mediaItems are the items of the menu state that is shown.
type mediaItems struct {
	iso, folder, boot, restart, unmount, cancel *fyne.MenuItem
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
	m.mount, m.info = nil, nil
	m.done, m.total = 0, 0
	m.bootNext, m.bootBusy, m.bootDone = false, false, nil
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

// chooseFolder is "Mount Folder…": the hint "Mount a folder" first, unless it was hidden, then
// the folder dialog.
func (m *mediaPanel) chooseFolder() {
	m.dismiss()
	if m.phase != mediaIdle {
		return
	}
	m.d.releaseKeys()
	pick := func() { m.pickFolder(func(path string) { m.start(path, true) }) }
	if m.d.settings.HideFolderMountHint() {
		pick()
		return
	}
	showFolderHint(m.d.win, func(hide bool) {
		if hide {
			m.d.settings.SetHideFolderMountHint(true)
		}
		pick()
	})
}

// showFolderHint shows "Mount a folder" (Extras.dc.html): what a mounted folder is, "Don’t show
// again" and the buttons Cancel and "Choose Folder…". onChoose gets whether the box is ticked;
// Cancel ends there.
func showFolderHint(parent fyne.Window, onChoose func(hide bool)) *panel {
	var check *dialogCheck
	return showPanel(parent, panelSpec{title: lang.L("Mount a folder"), action: lang.L("Choose Folder…"), cancel: lang.L("Cancel"),
		onAction: func() { onChoose(check.Checked) },
	}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(lang.L("The folder is mounted on the device as a CD drive."))
		msg.Wrapping = fyne.TextWrapWord
		check = p.newCheck(lang.L("Don’t show again"), false)
		return []fyne.CanvasObject{msg, bulletList(lang.L("Read only"), lang.L("Each file must be smaller than 4 GB"),
			lang.L("Large folders take a moment to prepare")), check}
	})
}

// bulletList is a list with a bullet before each line.
func bulletList(lines ...string) *widget.RichText {
	var items []widget.RichTextSegment
	for _, l := range lines {
		items = append(items, &widget.ParagraphSegment{Texts: []widget.RichTextSegment{&widget.TextSegment{Text: l, Style: widget.RichTextStyleInline}}})
	}
	rt := widget.NewRichText(&widget.ListSegment{Items: items})
	rt.Wrapping = fyne.TextWrapWord
	return rt
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
// it (prepared, settingsRead, open, opened). The Media menu opens and shows the progress.
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

// prepared runs when the image is open or built; it reads whether IDE-R is enabled.
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
	p := showQuestion(m.d.win, icons.Media, toneInfo, lang.L("Enable media redirection?"),
		lang.L("Media redirection (IDE-R) is disabled on “{{.Name}}”. Enable it to mount ISO files and folders on this device? The setting stays enabled until you disable it in Settings.", args),
		lang.L("Cancel"), lang.L("Enable"), func() {
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
	m.phase, m.mount = mediaMounted, mt
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
	if m.line != nil {
		m.line.SetDetail(formatRate(r))
	}
}

// ended runs when the device or the network ended the session: back to "nothing mounted", the
// boot option is cleared if it was set, and the cause is reported.
func (m *mediaPanel) ended(mt *mediaMount) {
	defer m.event("ended")
	if m.mount != mt {
		return
	}
	err := mt.c.Err()
	boot, after := m.bootToClear()
	m.reset()
	if boot {
		m.clearBoot(after)
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
	mt := m.mount
	boot, after := m.bootToClear()
	mt.detached.Store(true)
	m.reset()
	sess := m.d.sess
	done := m.d.exitWork.add()
	go func() {
		defer done()
		mt.c.Close()
		var err error
		if boot {
			waitBoot(after)
			err = sess.ClearISOBoot()
		}
		fyne.Do(func() {
			m.report(err)
			m.event("unmounted")
		})
	}()
}

// release ends media for a window that closes or is aborted: the menu and an open
// redirection question close, a pending attempt is cancelled and a mounted session detached.
// It returns the blocking rest (closing the session, clearing the boot option if clearBoot and
// it was set), which shutdown runs before it releases the session. nil-safe.
func (m *mediaPanel) release(clearBoot bool) func() {
	if m == nil || m.phase == mediaIdle && m.menu == nil && m.askIDER == nil {
		return func() {} // nothing to end: the closing window is left alone
	}
	m.dismiss()
	if q := m.askIDER; q != nil {
		m.askIDER = nil
		q.Dismiss() // closes the image
	}
	mt := m.mount
	boot, after := m.bootToClear()
	boot = boot && clearBoot
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
			waitBoot(after)
			_ = sess.ClearISOBoot()
		}
	}
}

// --- Boot ---

// setBootNext is "Boot from ISO on next start": on sets the IDE-R boot option for the next
// start, off clears it. A failure takes the check mark back and is reported.
func (m *mediaPanel) setBootNext(on bool) {
	if m.phase != mediaMounted || m.bootBusy || on == m.bootNext {
		return
	}
	done := make(chan struct{})
	m.bootNext, m.bootBusy, m.bootDone = on, true, done
	m.syncBoot()
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
		close(done)
		fyne.Do(func() { m.bootSet(mt, on, err) })
	}()
}

// bootSet ends a change of the boot item. After an unmount (or a closed window) meanwhile, the
// unmount has cleared the option after this call (bootToClear), so only the error is left.
func (m *mediaPanel) bootSet(mt *mediaMount, on bool, err error) {
	defer m.event("boot")
	if m.mount != mt {
		m.report(err)
		return
	}
	m.bootBusy = false
	if err != nil {
		m.bootNext = !on
	}
	m.syncBoot()
	m.report(err)
}

// bootToClear reports whether ending the mount must clear the boot option: it is set, or a
// change of the boot item still runs (it may set it after a clear sent now). after is closed when
// that change returned; the clear has to wait for it (waitBoot). Call before reset.
func (m *mediaPanel) bootToClear() (boot bool, after <-chan struct{}) {
	if m.bootBusy {
		return true, m.bootDone
	}
	return m.bootNext, nil
}

// waitBoot waits for a running change of the boot item (bootToClear); off the Fyne thread.
func waitBoot(after <-chan struct{}) {
	if after != nil {
		<-after
	}
}

// clearBoot clears the IDE-R boot option off the Fyne thread, after the change of the boot item
// that after belongs to (nil for none).
func (m *mediaPanel) clearBoot(after <-chan struct{}) {
	sess := m.d.sess
	done := m.d.exitWork.add()
	go func() {
		waitBoot(after)
		err := sess.ClearISOBoot()
		done()
		fyne.Do(func() {
			m.report(err)
			m.event("cleared")
		})
	}()
}

// syncBoot shows bootNext as the check mark of the boot item, which is locked while a change
// runs.
func (m *mediaPanel) syncBoot() {
	if m.items.boot == nil {
		return
	}
	m.applyBoot()
	m.menu.Refresh()
}

func (m *mediaPanel) applyBoot() {
	m.items.boot.Checked, m.items.boot.Disabled = m.bootNext, m.bootBusy
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
	if m == nil || m.items.restart == nil {
		return
	}
	m.applyRestart()
	m.menu.Refresh()
}

func (m *mediaPanel) applyRestart() {
	it := m.items.restart
	it.Disabled = !m.d.powerAllowed(amt.BootToISO)
	v := icons.Foreground
	if it.Disabled {
		v = icons.Disabled
	}
	it.Icon = icons.Get(icons.Reset, v)
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

// --- Menu ---

// toggle is the Media button.
func (m *mediaPanel) toggle() {
	if m.menu != nil {
		m.dismiss()
		return
	}
	m.show()
}

// dismiss closes the menu; nil-safe.
func (m *mediaPanel) dismiss() {
	if m != nil && m.menu != nil {
		m.menu.Dismiss()
	}
}

// show opens the menu below the Media button like the Power menu, or shows the current state in
// the open one.
func (m *mediaPanel) show() {
	if m.d.aborted || m.d.closed || m.d.fullScreen {
		return
	}
	if m.menu != nil {
		m.render()
		return
	}
	menu, line := m.build()
	m.menu = m.d.showMenuWith(m.d.mediaBtn, menu, line, func() {
		m.menu, m.shown, m.line, m.items = nil, nil, nil, mediaItems{}
	})
}

// render shows the current state in the open menu.
func (m *mediaPanel) render() {
	if m.menu == nil {
		return
	}
	menu, line := m.build()
	m.menu.SetMenu(menu, line)
}

// build creates the items and the info line of the current state (Extras.dc.html).
func (m *mediaPanel) build() (*fyne.Menu, *widgets.MenuInfo) {
	m.items, m.line = mediaItems{}, nil
	sep := fyne.NewMenuItemSeparator
	var items []*fyne.MenuItem
	switch m.phase {
	case mediaIdle:
		m.items.iso = fyne.NewMenuItem(lang.L("Mount ISO…"), m.chooseISO)
		m.items.iso.Icon = icons.Get(icons.Media, icons.Foreground)
		m.items.folder = fyne.NewMenuItem(lang.L("Mount Folder…"), m.chooseFolder)
		m.items.folder.Icon = icons.Get(icons.Folder, icons.Foreground)
		items = []*fyne.MenuItem{m.items.iso, m.items.folder}
	case mediaPreparing:
		m.line = widgets.NewMenuInfo(m.name, "")
		m.items.cancel = fyne.NewMenuItem(lang.L("Cancel"), m.cancelPrepare)
		items = []*fyne.MenuItem{sep(), m.items.cancel}
		m.refreshProgress()
	case mediaMounted:
		m.line = widgets.NewMenuInfo(m.name, formatRate(m.rate.rate()))
		m.items.boot = fyne.NewMenuItem(lang.L("Boot from ISO on next start"), func() { m.setBootNext(!m.bootNext) })
		m.items.restart = fyne.NewMenuItem(lang.L("Restart from ISO…"), func() { m.d.run(amt.BootToISO) })
		m.items.unmount = fyne.NewMenuItem(lang.L("Unmount"), m.unmount)
		m.items.unmount.Icon = icons.Get(icons.Unmount, icons.Foreground)
		m.applyBoot()
		m.applyRestart()
		items = []*fyne.MenuItem{sep(), m.items.boot, m.items.restart, sep(), m.items.unmount}
	}
	m.shown = fyne.NewMenu("", items...)
	return m.shown, m.line
}

// refreshProgress shows the build progress of a folder ISO in the open menu: the percentage
// once it is known and the bar.
func (m *mediaPanel) refreshProgress() {
	if m.line == nil || m.phase != mediaPreparing || !m.folder {
		return
	}
	frac, pct := buildProgress(m.done, m.total, m.info != nil)
	m.line.SetDetail(pct)
	m.line.SetProgress(frac)
}

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

// formatRate is a read rate in bytes per second as "18.2 MB/s".
func formatRate(bps float64) string {
	tenths := int64(math.Round(max(bps, 0) / 100_000))
	return lang.L("{{.Int}}.{{.Frac}} MB/s", map[string]any{"Int": tenths / 10, "Frac": tenths % 10})
}

// buildProgress is the share of a folder ISO built and its percentage ("62 %"); the
// percentage is empty while the size is not known yet, built is 100 %.
func buildProgress(done, total int64, built bool) (float32, string) {
	switch {
	case total > 0:
		return float32(done) / float32(total), fmt.Sprintf("%d %%", done*100/total)
	case built:
		return 1, "100 %"
	}
	return 0, ""
}
