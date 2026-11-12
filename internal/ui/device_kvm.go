package ui

import (
	"context"
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Automatic reconnect as in MeshCommander (index.html onDesktopStateChange, disconnectCode
// 50001): if AMT ends a session that ran longer than reconnectMinUptime, the client connects
// again after reconnectDelay. Variables for tests.
var (
	reconnectMinUptime = 5 * time.Second
	reconnectDelay     = 200 * time.Millisecond
)

// errConsentCancelled: the user dismissed the consent dialog or the window went away.
var errConsentCancelled = errors.New("consent cancelled")

// consentPrompt implements amt.ConsentPrompt: ConsentCode runs on the KVM connect goroutine,
// shows the dialog on the Fyne thread and blocks until the user answers or ctx ends (window
// closed, "Disconnect KVM"), in which case the dialog is hidden.
type consentPrompt struct {
	ctx context.Context
	ask func(answer func(code string, ok bool)) (hide func())
}

func (p *consentPrompt) ConsentCode() (string, error) {
	type reply struct {
		code string
		ok   bool
	}
	if p.ctx.Err() != nil { // "Disconnect KVM" came first: do not flash the dialog
		return "", errConsentCancelled
	}
	ch := make(chan reply, 1)
	var hide func()
	fyne.Do(func() {
		hide = p.ask(func(code string, ok bool) {
			select { // hide() also answers (dialog.Hide runs the callback); never block the Fyne thread
			case ch <- reply{code, ok}:
			default:
			}
		})
	})
	select {
	case r := <-ch:
		if !r.ok {
			return "", errConsentCancelled
		}
		return r.code, nil
	case <-p.ctx.Done():
		fyne.Do(func() {
			if hide != nil {
				hide()
			}
		})
		return "", errConsentCancelled
	}
}

// buildKVM creates the KVM toolbar buttons, the Quality menu, the view and the full screen bar;
// kvmArea holds the view. Called once from NewDeviceWindow, before the toolbar is laid out.
func (d *DeviceWindow) buildKVM() {
	d.view = newKVMView(newKeyMapper(d.goos, waylandPlatform))
	d.askConsent = func(answer func(string, bool)) func() {
		d.releaseKeys()
		return showConsentDialog(d.win, answer)
	}
	d.buildQualityMenu()
	d.fullBtn = widgets.NewToolbarButton(icons.Fullscreen, lang.L("Full Screen"), func() { d.setFullScreen(true) })
	d.cadBtn = widgets.NewToolbarButton(icons.Keyboard, "Ctrl+Alt+Del", d.toolbarCtrlAltDel)
	d.clipBtn = widgets.NewToolbarButton(icons.ClipboardType, lang.L("Type Clipboard"), d.toolbarTypeClipboard)
	d.kvmArea = container.NewStack(d.view)

	d.exitFullBtn = widgets.NewToolbarButton(icons.Fullscreen, lang.L("Exit Full Screen"), func() { d.setFullScreen(false) })
	d.barCad = widgets.NewToolbarButton(icons.Keyboard, "Ctrl+Alt+Del", d.toolbarCtrlAltDel)
	d.barClip = widgets.NewToolbarButton(icons.ClipboardType, lang.L("Type Clipboard"), d.toolbarTypeClipboard)
	d.bar = newDragBar(d.barSwitch, d.exitFullBtn, d.barCad, d.barClip)
	d.barLayer = d.bar.layer()
	d.onTypeClipboard = d.startTyping
	d.view.onInput = d.touchIdle
	d.wake = wakeScreen
	d.setKVMControls()
}

// setKVMControls enables the controls for the current state: the view switch is locked only
// after an abort (the selected KVM segment connects or disconnects, kvmToggleEnabled); the
// quality can be changed in the KVM view except while a connect or reconnect is pending (a
// running session reconnects with it, switchQuality); full screen needs the session of the
// shown view (fullScreenAllowed); Ctrl+Alt+Del needs a session; "Type clipboard" additionally
// needs its hook (set in buildKVM). In the Console view Ctrl+Alt+Del goes to the console and
// stays enabled; Type Clipboard follows syncClipButtons. The full screen bar mirrors the
// toolbar. Manage follows syncManageBtn. Status bar and empty state follow.
func (d *DeviceWindow) setKVMControls() {
	connected := d.kvmClient != nil || d.kvmReconnecting // a reconnect keeps the controls as they are
	console := d.consoleView
	enable := func(w fyne.Disableable, on bool) {
		if on && !d.aborted {
			w.Enable()
		} else {
			w.Disable()
		}
	}
	enable(d.viewSwitch, true)
	enable(d.barSwitch, true)
	enable(d.mediaBtn, true)
	enable(d.quality, !console && !d.kvmBusy && !d.kvmReconnecting)
	enable(d.fullBtn, d.fullScreenAllowed())
	enable(d.cadBtn, connected || console)
	enable(d.barCad, connected || console)
	d.syncClipButtons()
	d.syncManageBtn()
	d.refreshKVMLine()
	d.refreshEmptyState()
}

// syncClipButtons enables both Type Clipboard buttons (window and full screen bar): in the KVM
// view with a KVM session and the typing hook, in the Console view with a console session. A
// running KVM typing job keeps them enabled in both views, as they read "Stop typing" then.
func (d *DeviceWindow) syncClipButtons() {
	on := (d.kvmClient != nil || d.kvmReconnecting) && d.onTypeClipboard != nil
	if d.consoleView {
		on = d.con.out != nil
	}
	for _, b := range []*widgets.ToolbarButton{d.clipBtn, d.barClip} {
		if (on || d.typing != nil) && !d.aborted {
			b.Enable()
		} else {
			b.Disable()
		}
	}
}

func (d *DeviceWindow) toggleKVM() {
	d.autoWait = false // a manual click uses up the single automatic chance (autoConnect)
	d.forgetResume()   // and decides about KVM instead of a pending resume (resumeCheck)
	if d.kvmClient != nil || d.kvmBusy || d.kvmReconnecting {
		d.disconnectKVM()
		return
	}
	d.connectKVM(false)
}

// connectKVM opens the session on a goroutine with the selected quality; the consent dialog
// blocks that goroutine (and, through the session mutex, the poller) until answered. resume
// marks the connect of resumeCheck: no wake key, a failure shows no dialog.
func (d *DeviceWindow) connectKVM(resume bool) {
	if d.aborted || d.closed || d.kvmBusy || d.kvmReconnecting || d.kvmClient != nil {
		return
	}
	d.resuming = resume
	d.kvmBusy = true
	d.closeMenus()
	d.kvmStatus.SetText(lang.L("Connecting KVM…"))
	d.setKVMControls()
	ctx, cancel := context.WithCancel(context.Background())
	d.kvmCancel = cancel
	q := d.kvmQuality
	prompt := &consentPrompt{ctx: ctx, ask: d.askConsent}
	go func() {
		c, err := d.sess.OpenKVM(ctx, q, prompt)
		fyne.Do(func() { d.kvmOpened(c, err) })
	}()
}

// autoConnect connects KVM once per opened window (Show), at the first successful status read
// after the favorite's override is known: only when the device is on, or the account may not
// read the power state (a KVM-only account), and the override, else the global setting, asks
// for it. A device that is off, or any other state, ends the single chance without an attempt;
// failed reads wait for the next one. The consent dialog appears as for a click. With a denied
// power state a device that is off gives the normal KVM error, as for a click.
func (d *DeviceWindow) autoConnect() {
	if !d.autoWait || !d.autoLoaded || !(d.powerKnown || d.powerDenied) || d.aborted || d.closed {
		return
	}
	d.autoWait = false
	if (d.powerDenied || d.powerState == models.PowerStateOn) && d.settings.AutoConnectFor(d.autoOverride) {
		d.connectKVM(false)
	}
}

// Resume after a power action: Power Off, Power Cycle and Shutdown close KVM first (execute),
// because AMT refuses them while a session is open. Because the app closed the session, not the
// user, the window connects again once the device is on, regardless of the auto-connect
// setting. kvmResumeWait gives AMT time to accept redirection again; a failed attempt is
// repeated once (kvmResumeTries). Variables for tests.
var (
	kvmResumeWait  = 5 * time.Second
	kvmResumeTries = 2
)

// resumeAfterPower runs when a power action ends while a resume is pending. Success starts the
// wait (each further action restarts it); the status read at its end decides (resumeCheck).
// After Power Off or Shutdown the device has to be read as off first, or be switched on by a
// later action. A failed action ends the pending resume: the read that follows it reconnects
// at once if the device is on, the session must not be lost to a refused command.
func (d *DeviceWindow) resumeAfterPower(action amt.PowerAction, err error) {
	if !d.resumeKVM {
		return
	}
	if err != nil {
		d.resumeNow, d.resumeNeedOff, d.resumeAt = true, false, time.Now()
		return
	}
	d.resumeNow, d.resumeTries = false, 0
	d.resumeNeedOff = action == amt.PowerOff || action == amt.PowerShutdown
	d.armResume()
}

// armResume allows the next attempt after kvmResumeWait and asks for a status read then, so a
// long poll interval does not delay it.
func (d *DeviceWindow) armResume() {
	d.resumeAt = time.Now().Add(kvmResumeWait)
	time.AfterFunc(kvmResumeWait, d.poller.Refresh)
}

// resumeCheck runs at every status read and connects KVM again (connectKVM with resume: the
// quality of the next connection, no wake key, no error dialog) once a pending resume may: the
// device is on, the wait is over and, after Power Off or Shutdown, it was read as off before.
func (d *DeviceWindow) resumeCheck() {
	if !d.resumeKVM || d.resumeAt.IsZero() || !d.powerKnown || d.powerBusy || d.aborted || d.closed ||
		d.kvmClient != nil || d.kvmBusy || d.kvmReconnecting {
		return
	}
	if isOff(d.powerState) {
		d.resumeNeedOff = false
	}
	switch {
	case d.powerState == models.PowerStateOn && !d.resumeNeedOff && !time.Now().Before(d.resumeAt):
		d.resumeKVM, d.resumeNow = false, false
		d.connectKVM(true)
	case d.resumeNow: // the action failed and the device is not on: nothing to come back to
		d.forgetResume()
	}
}

// retryResume counts a failed resume (connect error or a resumed session that AMT ended with an
// error before reconnectMinUptime) and tries once more after kvmResumeWait.
func (d *DeviceWindow) retryResume() {
	d.resumeTries++
	if d.resumeTries < kvmResumeTries && !d.closed && !d.aborted {
		d.resumeKVM, d.resumeNeedOff, d.resumeNow = true, false, false
		d.armResume()
	}
}

// forgetResume drops a pending resume: the user connected or disconnected KVM, the window
// closed or the session was aborted.
func (d *DeviceWindow) forgetResume() {
	d.resumeKVM, d.resumeNow, d.resumedClient = false, false, nil
}

// adoptQuality takes over a quality changed in the favorites form (the file is already
// written): the next connection uses it and a running session reconnects with it like after a
// choice in the Quality menu (switchQuality). A connect or reconnect in flight is checked
// when it has opened (recheckQuality).
func (d *DeviceWindow) adoptQuality(q kvm.Quality) {
	if d.aborted || d.closed || q == d.kvmQuality {
		return
	}
	d.qualityChosen = true
	d.applyQuality(q)
	if d.kvmBusy || d.kvmReconnecting {
		d.adoptPending = true
		return
	}
	d.switchQuality()
}

// recheckQuality reconnects a session that opened with the quality adoptQuality replaced.
func (d *DeviceWindow) recheckQuality() {
	if d.adoptPending {
		d.adoptPending = false
		d.switchQuality()
	}
}

// kvmOpened runs on the Fyne thread with the result of OpenKVM.
func (d *DeviceWindow) kvmOpened(c *kvm.Client, err error) {
	resumed := d.resuming
	d.resuming = false
	d.kvmBusy = false
	d.kvmCancel() // the context only bounded dial and consent (kvm.Dial keeps no reference)
	d.kvmCancel = nil
	if d.powerAfterConnect != nil { // execute cancelled this connect for Power Off, Cycle or Shutdown
		if err == nil { // too late: AMT refuses the action while this session is open
			done := c.Done()
			go c.Close()
			go func() {
				t := time.NewTimer(kvmCloseWait)
				defer t.Stop()
				select {
				case <-done:
				case <-t.C:
				}
				fyne.Do(d.flushPowerAfterConnect)
			}()
			c, err = nil, context.Canceled
		} else {
			d.flushPowerAfterConnect()
		}
	}
	switch {
	case err == nil && (d.closed || d.aborted):
		go c.Close()
	case err == nil:
		// Controls first, the frame loop last: in production both run on the Fyne thread, in
		// tests the loop goroutine must not touch the window while this function still does.
		d.kvmClient = c
		d.kvmStatus.SetText("")
		d.setKVMControls()
		d.focusView()
		idle := d.startIdle()
		d.view.bind(c, true)
		if idle != nil {
			idle.Reset(d.idleAfter) // armed after the view is attached
		}
		if resumed {
			d.resumedClient = c
		} else if d.wake != nil {
			go d.wake(c, wakeRepeat) // read here on the UI thread, not in the goroutine
		}
		d.sendPendingCAD() // Ctrl+Alt+Del of the Console view that started this connect
		if d.onKVMChanged != nil {
			d.onKVMChanged()
		}
		d.recheckQuality() // before the loop starts: its end then finds the switch registered
		d.view.run(c, func(err error) { d.kvmEnded(c, err) })
		return
	case errors.Is(err, errConsentCancelled), errors.Is(err, context.Canceled), d.closed:
		d.kvmStatus.SetText("")
	case isMismatch(err):
		d.kvmStatus.SetText("")
		d.abort(err)
	case resumed: // no dialog: the user did not ask for this connect
		d.kvmStatus.SetText(describe(err))
		d.retryResume()
	default:
		d.kvmStatus.SetText(describe(err))
		d.onError(err)
	}
	if d.closed {
		d.view.free() // a connect cancelled by closing the window: nothing uses the view any more
	}
	d.con.cadPending = false // the console's Ctrl+Alt+Del had no session to go to
	d.setKVMControls()
	if d.onKVMChanged != nil {
		d.onKVMChanged()
	}
}

// wakeRepeat is the pause between the two wake keys. A single F15 right after the first frame
// (about 0.5 s after connect) was lost once; with a second one 0.5 s later a sleeping host
// screen showed its content about 2.5 s after "Connect KVM". Variable for tests.
var wakeRepeat = 500 * time.Millisecond

// wakeScreen wakes a sleeping host screen once the session of a user-initiated connect
// stands: one F15 right at its first FramebufferUpdate and a second one wakeRepeat later
// (kvm.Client.WakeScreen: press and release, nothing while a key is held).
// Automatic reconnects never call it: AMT drops a black session after about 31 s, so a key on
// every reconnect would keep the host awake for good and defeat its screen saver and lock. It
// is no local input and leaves the idle timeout alone.
func wakeScreen(c *kvm.Client, repeat time.Duration) {
	if _, err := c.WaitFrame(context.Background(), 0); err != nil { // returns when the session ends
		return
	}
	_ = c.WakeScreen()
	t := time.NewTimer(repeat)
	defer t.Stop()
	select {
	case <-t.C:
		_ = c.WakeScreen()
	case <-c.Done():
	}
}

// disconnectKVM cancels a pending connect (dial or consent dialog) and closes a running
// session; the frame loop then reports kvmEnded. Safe to call at any time.
//
// If the "Type clipboard" dialog is sending, its goroutine finishes the stroke it is in (it
// stops only between characters: at most one stroke, a dead key two pauses). Keys are released
// and the socket closed only after that, otherwise its key-down could follow the release and
// its key-up reach a closed socket, leaving the key pressed on the remote.
func (d *DeviceWindow) disconnectKVM() {
	d.stopIdle()          // a timer firing before kvmEnded must not report this as an idle timeout
	d.qualitySwitch = nil // a pending quality switch ends here, without its reconnect
	d.closeKVM()
}

// closeKVM ends the typing job, cancels a pending connect and closes a running session (see
// disconnectKVM); kvmEnded follows for a running one.
func (d *DeviceWindow) closeKVM() {
	var sender <-chan struct{}
	if td := d.typing; td != nil && td.sending {
		sender = td.done
	}
	d.closeTyping()
	if d.kvmCancel != nil {
		d.kvmCancel()
	}
	c := d.kvmClient
	if c == nil {
		return
	}
	if sender == nil {
		d.view.releaseAll() // nothing stays pressed on the remote when the socket goes
		go c.Close()
		return
	}
	d.view.releaseButtons() // mouse buttons are Fyne-thread state; the keys follow below
	go func() {
		<-sender
		_ = c.ReleaseAll()
		c.Close()
	}()
}

// kvmEnded runs on the Fyne thread once the frame loop of c has stopped. A session that AMT
// ended after more than reconnectMinUptime is reconnected; Close (manual, idle timeout, window
// closed) never is.
func (d *DeviceWindow) kvmEnded(c *kvm.Client, err error) {
	if d.kvmClient != c {
		return
	}
	d.kvmClient = nil
	switched := d.qualitySwitch == c
	d.qualitySwitch = nil
	resumed := d.resumedClient == c
	if resumed {
		d.resumedClient = nil
	}
	switch {
	case d.closed || d.aborted:
	case switched:
		d.reconnectKVM(d.kvmQuality)
		return
	case !errors.Is(err, kvm.ErrClosed) && time.Since(c.Started()) > reconnectMinUptime:
		d.reconnectKVM(c.Quality())
		return
	}
	d.endKVM(err)
	if resumed && !errors.Is(err, kvm.ErrClosed) { // dropped right after a resume, e.g. while the device restarts
		d.retryResume()
	}
	d.resumeCheck() // the status read after a failed power action may have come before this end
}

// switchQuality reconnects the running session with the quality just chosen (SessionA.html: the
// Quality menu works while KVM is connected). The session is closed and kvmEnded opens the new
// one like an automatic reconnect: no wake key, no message, the idle timeout keeps running and
// the last image stays until the first frame; AMT may ask for the consent code again.
func (d *DeviceWindow) switchQuality() {
	c := d.kvmClient
	if c == nil || c.Quality() == d.kvmQuality {
		return
	}
	d.touchIdle() // the menu choice is local input
	d.qualitySwitch = c
	d.closeKVM()
}

// reconnectKVM opens a new session with quality q after reconnectDelay, as MeshCommander's
// setTimeout(connectDesktop, 200). Status text, buttons, full screen, the idle timer and the
// last image stay as they are; "Disconnect KVM" cancels. A reconnect does not wake the host
// screen (wakeScreen).
func (d *DeviceWindow) reconnectKVM(q kvm.Quality) {
	d.kvmReconnecting = true
	d.closeTyping() // its session is gone
	d.view.suspend()
	ctx, cancel := context.WithCancel(context.Background())
	d.kvmCancel = cancel
	prompt := &consentPrompt{ctx: ctx, ask: d.askConsent}
	if d.onKVMChanged != nil { // test hook, before the reconnect goroutine starts
		d.onKVMChanged()
	}
	go func() {
		t := time.NewTimer(reconnectDelay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			fyne.Do(func() { d.kvmReopened(ctx, nil, ctx.Err()) })
			return
		case <-t.C:
		}
		c, err := d.sess.OpenKVM(ctx, q, prompt)
		fyne.Do(func() { d.kvmReopened(ctx, c, err) })
	}()
}

// kvmReopened runs on the Fyne thread with the result of the reconnect: the new client
// continues the session, a failure ends it like a session error, a cancel like Close.
func (d *DeviceWindow) kvmReopened(ctx context.Context, c *kvm.Client, err error) {
	cancelled := ctx.Err() != nil || d.closed || d.aborted // before the own cancel below
	d.kvmReconnecting = false
	if d.kvmCancel != nil {
		d.kvmCancel() // the context only bounded dial and consent
		d.kvmCancel = nil
	}
	switch {
	case err == nil && !cancelled:
		d.kvmClient = c
		d.view.bind(c, false) // the last image stays until the first frame
		d.sendPendingCAD()
		if d.onKVMChanged != nil {
			d.onKVMChanged()
		}
		d.recheckQuality() // before the loop starts: its end then finds the switch registered
		d.view.run(c, func(err error) { d.kvmEnded(c, err) })
		return
	case err == nil:
		go c.Close()
		err = kvm.ErrClosed
	case cancelled, errors.Is(err, errConsentCancelled), errors.Is(err, context.Canceled):
		err = kvm.ErrClosed
	case isMismatch(err):
		d.endKVM(kvm.ErrClosed)
		d.kvmStatus.SetText("")
		d.abort(err)
		return
	}
	d.endKVM(err)
}

// endKVM finishes a session that is over (no reconnect): view, full screen, status, controls.
func (d *DeviceWindow) endKVM(err error) {
	d.stopIdle()
	d.con.cadPending = false
	d.closeTyping() // also when the server ended the session; nothing left to type into
	if d.closed {   // the window is gone: no widget work, only the state
		d.view.free() // the frame loop has ended (kvmEnded) or never ran (reconnect, cancel)
		if d.onKVMChanged != nil {
			d.onKVMChanged()
		}
		return
	}
	d.view.detach()
	if !d.consoleView { // full screen of the Console view stays
		d.setFullScreen(false)
	}
	idle := d.idleExpired
	d.idleExpired = 0
	switch {
	case idle > 0 && errors.Is(err, kvm.ErrClosed):
		d.kvmStatus.SetText(lang.L("KVM disconnected after {{.Minutes}} minutes without input.", map[string]any{"Minutes": max(1, int(idle/time.Minute))}))
	case errors.Is(err, kvm.ErrClosed):
		d.kvmStatus.SetText(lang.L("KVM session closed."))
	default:
		d.kvmStatus.SetText(lang.L("KVM session ended: {{.Err}}", map[string]any{"Err": describe(err)}))
	}
	d.setKVMControls()
	if d.onKVMChanged != nil {
		d.onKVMChanged()
	}
}

func (d *DeviceWindow) ctrlAltDel() {
	if c := d.kvmClient; c != nil {
		d.touchIdle()
		_ = c.CtrlAltDel()
		d.focusView()
	}
}

// startIdle prepares the idle timeout of a new session (setting "Disconnect KVM without input
// after") and returns the stopped timer, or nil for "never"; the caller arms it. Every local
// input re-arms it (touchIdle); automatic reconnects keep it running. The value applies from
// the next session.
func (d *DeviceWindow) startIdle() *time.Timer {
	timeout := d.settings.KVMIdleTimeout()
	if d.idleTimeout != nil {
		timeout = d.idleTimeout()
	}
	if timeout <= 0 {
		return nil
	}
	var t *time.Timer
	t = time.AfterFunc(time.Hour, func() {
		fyne.Do(func() {
			if d.idleTimer != t || d.closed { // endKVM clears idleTimer; a reconnect keeps it
				return
			}
			if rest := timeout - time.Since(d.lastInput); rest > 0 { // input arrived while this callback was queued
				t.Reset(rest)
				return
			}
			d.idleExpired = timeout
			d.disconnectKVM()
		})
	})
	t.Stop()
	d.idleTimer, d.idleAfter, d.lastInput = t, timeout, time.Now()
	return t
}

// touchIdle re-arms the idle timeout after local input (keys, mouse, typing progress).
func (d *DeviceWindow) touchIdle() {
	d.lastInput = time.Now()
	if d.idleTimer != nil {
		d.idleTimer.Reset(d.idleAfter)
	}
}

func (d *DeviceWindow) stopIdle() {
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
}

// typeClipboard calls the "Type clipboard" hook (startTyping) while a session runs.
func (d *DeviceWindow) typeClipboard() {
	if d.onTypeClipboard != nil && d.kvmClient != nil {
		d.onTypeClipboard()
	}
}

// fullScreenAllowed reports whether Full Screen may start: the shown view needs its session.
func (d *DeviceWindow) fullScreenAllowed() bool {
	if d.consoleView {
		return d.con.client != nil
	}
	return d.kvmClient != nil
}

// setFullScreen shows only the stages with the drag bar on top (every key goes to the remote,
// so the bar is the way back), or restores the normal content. The bar's switch changes the
// view in full screen as the toolbar's does in the window.
func (d *DeviceWindow) setFullScreen(on bool) {
	if on == d.fullScreen || (on && !d.fullScreenAllowed()) {
		return
	}
	d.fullScreen = on
	if on {
		d.stage.RemoveAll()
		d.win.SetContent(container.NewStack(d.kvmStage, d.consolePane, d.barLayer))
		d.win.SetFullScreen(true)
		d.bar.recentre()
	} else {
		d.win.SetFullScreen(false)
		d.win.SetContent(d.content)
		d.stage.Objects = []fyne.CanvasObject{d.kvmStage, d.consolePane}
		d.stage.Refresh()
	}
	if d.consoleView {
		d.focusConsole()
	} else {
		d.focusView()
	}
}
