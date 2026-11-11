package ui

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/typing"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// DeviceWindow shows one connected device (design SessionA/SessionA2, issue #44, Main.dc.html):
// a toolbar (Power menu, KVM|Console, Media, Quality, Ctrl+Alt+Del, Type Clipboard, Keyboard,
// Manage, Full Screen), banners for the TLS warning and a failed status read, the KVM or
// Console stage, the device management sidebar and a status bar (device_chrome.go and
// device_views.go build these). KVM logic is in device_kvm.go. The status is polled by a
// session.Poller.
type DeviceWindow struct {
	win      fyne.Window
	sess     *session.Session
	poller   *session.Poller
	settings Settings
	name     string
	goos     string // runtime.GOOS; tests set another value for the key tables

	// Toolbar, banners and status bar (device_chrome.go).
	title, host               *ellipsisText
	powerBtn, keyboard        *widgets.ToolbarButton
	powerMenu                 *fyne.Menu
	powerItems                map[amt.PowerAction]*fyne.MenuItem
	powerPopup, qualityPopup  *widgets.MenuPopup // open menu or nil
	keyboardPopup             *widgets.MenuPopup // open Keyboard menu or nil (device_keyboard.go)
	powerDot, headDot         *widgets.StatusDot // status bar dot, toolbar dot before the name
	power, version, mode, tls *ellipsisText
	kvmLine                   *ellipsisText // KVM state or kvmStatus in the status bar
	warning                   fyne.CanvasObject
	message                   *widget.Label // failed status read; its banner is hidden while empty
	messageBanner             fyne.CanvasObject
	emptyState                fyne.CanvasObject
	emptyBox, emptyCenter     *fyne.Container
	emptyBtnBox               *fyne.Container // "Connect KVM" with its gap; hidden while the device is off
	statusRow                 *fyne.Container
	emptyText                 *widget.RichText
	emptyBtn                  *widgets.PrimaryButton
	powerState                models.PowerState // last read state, valid if powerKnown
	powerKnown, powerBusy     bool              // powerBusy: a power action runs
	canShutdown               bool              // the device lists Off - Soft Graceful (12) in its power states
	content                   fyne.CanvasObject // normal window content
	kvmArea                   *fyne.Container   // holds the view (moved out in full screen)
	aborted                   bool              // certificate mismatch: session is dead
	released                  bool              // onClosed already called
	closed                    bool              // user closed the window
	qualityItems              map[kvm.Quality]*fyne.MenuItem
	qualityMenu               *fyne.Menu
	kvmQuality                kvm.Quality      // quality of the next connection
	qualityChosen             bool             // the user picked one before the favorite was read
	qualitySave               sync.Mutex       // serialises SetQuality writes
	qualityWant               string           // latest value to store; guarded by qualitySave
	onQualitySaved            func()           // test hook, nil in production
	onFavoriteChanged         func()           // the favorite's quality was stored; the start window reloads
	onQualityLoaded           func()           // test hook, nil in production
	startPoller               func()           // Show starts the poller through it; tests replace it (nil: poller.Start)
	onConfirmShown            func()           // test hook, nil in production
	autoWait                  bool             // Show: KVM may still connect automatically once (autoConnect)
	autoLoaded                bool             // the favorite's auto-connect override is known
	autoOverride              string           // favorites.AutoConnectOn, AutoConnectOff or empty (global)
	adoptPending              bool             // the form changed the quality while a connect was pending
	resumeKVM                 bool             // the window closed KVM for a power action and connects again (resumeCheck)
	powerAfterConnect         *amt.PowerAction // sent once the cancelled connect has reported (kvmOpened), see execute
	resumeAt                  time.Time        // earliest resume attempt, set when a power action ends
	resumeNeedOff             bool             // after Power Off or Shutdown an On read counts only after an Off read
	resumeNow                 bool             // a power action failed: the next good read decides (On connects, else forget)
	resumeTries               int              // failed resume attempts since the last power action
	resuming                  bool             // the pending connect is a resume: no wake key, no error dialog
	resumedClient             *kvm.Client      // session of the last resume; an early error counts as a failed attempt

	// Views, Media and Manage (device_views.go), only touched on the Fyne thread.
	toolbarItems         *fyne.Container    // the toolbar buttons and dividers, in order
	viewSwitch           *widgets.Segmented // KVM | Console
	consoleView          bool               // the Console view is shown (consoleActive)
	kvmStage             fyne.CanvasObject  // letterbox, KVM view and empty state
	consolePane          fyne.CanvasObject  // dark background and consoleStage
	consoleStage         *fyne.Container    // content of the Console view; empty until the console fills it
	onConsoleSelected    func()             // the user switched to the Console view; nil = nothing
	onConsoleReselected  func()             // the user tapped the selected Console segment; nil = nothing
	consoleCtrlAltDel    func()             // toolbar Ctrl+Alt+Del in the Console view; nil = nothing
	consoleTypeClipboard func()             // toolbar Type Clipboard in the Console view; nil = nothing
	mediaBtn, manageBtn  *widgets.ToolbarButton
	onMedia              func()                   // Media button; nil = nothing
	body                 *fyne.Container          // stage and sidebar
	managePane           fyne.CanvasObject        // sidebar with its left line; hidden while closed
	manageSlot           *fyne.Container          // content of the sidebar, manageWidth wide
	manageContent        func() fyne.CanvasObject // builds the sidebar content at the first opening; nil = empty
	manageBuilt          bool                     // manageContent was called
	mediaLine            *ellipsisText            // "ISO mounted · …" in the status bar; hidden while empty
	consoleLine          *ellipsisText            // "Console connected" in the status bar; hidden while empty
	con                  serialConsole            // the serial console of the Console view (console.go)

	// Keyboard menu (device_keyboard.go), only touched on the Fyne thread.
	keyboardMenu    *fyne.Menu
	layoutItems     map[string]*fyne.MenuItem // by stored layout name; "" is Default
	layoutChoice    string                    // layout of "Type clipboard" for this window: favorite override or the session's choice
	layoutChosen    bool                      // the user (menu or form) set layoutChoice; it wins over the stored value
	isFavorite      bool                      // the device is a favorite (known once the favorite was read)
	layoutWrite     latestWriter
	onKeyboardSaved func() // test hook, nil in production

	// KVM state, only touched on the Fyne thread.
	view                         *kvmView
	fullBtn, cadBtn, clipBtn     *widgets.ToolbarButton
	exitFullBtn, barCad, barClip *widgets.ToolbarButton
	quality                      *widgets.ToolbarButton
	kvmStatus                    *statusText // transient KVM message; empty shows the plain state
	bar                          *dragBar
	barLayer                     *fyne.Container
	kvmClient                    *kvm.Client                      // running session or nil
	kvmCancel                    context.CancelFunc               // pending connect (dial or consent) or nil
	kvmBusy                      bool                             // connect in progress
	kvmReconnecting              bool                             // AMT dropped the session, reconnect pending (kvmClient nil)
	qualitySwitch                *kvm.Client                      // session closed for a new quality; kvmEnded reconnects
	idleTimer                    *time.Timer                      // ends the KVM session without input; nil = never
	idleAfter                    time.Duration                    // timeout of idleTimer
	idleExpired                  time.Duration                    // set when idleTimer ended the session, for the status
	idleTimeout                  func() time.Duration             // test hook; nil = settings
	wake                         func(*kvm.Client, time.Duration) // wakeScreen; nil in most tests (no extra key messages)
	lastInput                    time.Time                        // last local input (touchIdle)
	fullScreen                   bool
	askConsent                   func(answer func(code string, ok bool)) (hide func()) // dialog; replaced in tests
	onTypeClipboard              func()                                                // "Type clipboard" dialog (type_clipboard.go); nil keeps the buttons disabled

	// "Type clipboard" (type_clipboard.go), only touched on the Fyne thread.
	typing         *typeJob             // pending or running "Type clipboard" or nil
	layouts        favoriteStore        // favorites; nil = nothing is read or saved
	readClipboard  func() string        // replaced in tests
	detectLayout   func() typing.Layout // replaced in tests
	typeDelay      time.Duration        // pause between two strokes
	onTypeDone     func()               // test hook, nil in production
	onTypeProgress func(done, total int)

	confirm  func(action amt.PowerAction, proceed func()) // dialog; replaced in tests
	onError  func(error)
	onClosed func()                          // start window registry; called once, on close or abort
	onStatus func(st amt.Status, known bool) // every status read on the Fyne thread; known: st is valid. nil = nobody listens

	onApplied, onActionDone, onKVMChanged func() // test hooks, nil in production
}

// statusText is a transient KVM message (connecting, session ended, typing progress) shown in
// the status bar; empty shows "KVM connected" or "KVM not connected".
type statusText struct {
	Text    string
	changed func()
}

func (s *statusText) SetText(t string) {
	s.Text = t
	if s.changed != nil {
		s.changed()
	}
}

// powerOrder is the order of the Power menu; separators follow PowerOff and BootToBIOS.
var powerOrder = []amt.PowerAction{amt.PowerOn, amt.PowerOff, amt.PowerReset, amt.PowerCycle, amt.BootToBIOS, amt.PowerShutdown}

// powerStateText is the power state in the status bar.
func powerStateText(ps models.PowerState) string {
	switch ps {
	case models.PowerStateOn:
		return lang.L("Powered on")
	case models.PowerStateOffHard, models.PowerStateOffSoft:
		return lang.L("Powered off")
	case models.PowerStateSleepLight, models.PowerStateSleepDeep:
		return lang.L("Sleeping")
	case models.PowerStateHibernate:
		return lang.L("Hibernating")
	}
	return fmt.Sprintf("%s (%d)", ps, int(ps))
}

func isOff(ps models.PowerState) bool {
	return ps == models.PowerStateOffHard || ps == models.PowerStateOffSoft
}

func isAsleep(ps models.PowerState) bool {
	return ps == models.PowerStateSleepLight || ps == models.PowerStateSleepDeep || ps == models.PowerStateHibernate
}

// NewDeviceWindow builds the window; onClosed runs once when the user closes it or the session
// is aborted after a certificate mismatch (the poller is stopped on a goroutine before that).
func NewDeviceWindow(a fyne.App, sess *session.Session, name string, st amt.Status, settings Settings, onClosed func()) *DeviceWindow {
	d := &DeviceWindow{win: a.NewWindow(name), sess: sess, settings: settings, name: name,
		onClosed: onClosed, goos: runtime.GOOS, kvmStatus: &statusText{},
		readClipboard: defaultReadClipboard, detectLayout: func() typing.Layout { return typing.DetectLocalLayout(string(lang.SystemLocale())) }, typeDelay: typing.DefaultDelay}
	d.onError = d.showErrorDialog
	d.confirm = d.confirmDialog
	d.kvmStatus.changed = d.refreshKVMLine

	d.buildViewControls()
	d.buildConsole()
	d.buildKVM()
	d.content = d.buildContent()
	d.poller = session.NewPoller(sess, settings.PollInterval(), func(st amt.Status, err error) {
		fyne.Do(func() { d.apply(st, err) })
	})
	d.apply(st, nil)

	d.win.SetPadded(false) // toolbar and status bar reach the window edges, as in the design
	d.win.SetContent(d.content)
	d.win.Resize(fyne.NewSize(1120, 760))
	d.win.SetOnClosed(func() {
		// Without a session, connect or reconnect in flight nothing touches the view any more;
		// otherwise kvmOpened or endKVM frees it once that event has arrived.
		idle := d.kvmClient == nil && !d.kvmBusy && !d.kvmReconnecting
		d.closed = true
		d.forgetResume()
		d.shutdown()
		d.release()
		d.closeConsole()
		d.disconnectKVM() // last: kvmEnded then only clears state (closed window)
		if idle {
			d.view.free()
		}
	})
	return d
}

// shutdown stops the poller and then drops the idle WS-Man sockets. Off the Fyne thread, because
// Stop waits for a running status read; the order matters, so a read cannot re-pool a socket
// after Close. The session is not used afterwards (KVM keeps its own connection).
func (d *DeviceWindow) shutdown() {
	go func() {
		d.poller.Stop()
		d.sess.Close()
	}()
}

// release tells the start window that this session is gone; safe to call twice.
func (d *DeviceWindow) release() {
	if d.released {
		return
	}
	d.released = true
	if d.onClosed != nil {
		d.onClosed()
	}
}

// Show starts polling, reads the favorite's KVM quality and auto-connect override and shows
// the window.
func (d *DeviceWindow) Show() {
	d.autoWait = true
	d.autoLoaded = d.layouts == nil // no favorites: the global setting decides, at the first status read
	if d.startPoller != nil {
		d.startPoller()
	} else {
		d.poller.Start()
	}
	d.loadQuality()
	d.win.Show()
}

// apply updates toolbar, status bar and banner from a status read; runs on the Fyne thread.
func (d *DeviceWindow) apply(st amt.Status, err error) {
	switch {
	case d.aborted: // the abort warning stays; a read racing the poller stop must not erase it
	case err != nil && isMismatch(err):
		d.abort(err)
	case err != nil:
		d.powerKnown = false
		d.showPower()
		d.setMessage(describe(err))
	default:
		d.powerState, d.powerKnown = st.PowerState, true
		d.canShutdown = slices.Contains(st.PowerStates, power.PowerOffSoftGraceful)
		d.showPower()
		d.setVersion(st.AMTVersion, string(st.ControlMode))
		d.setMessage("")
	}
	d.autoConnect()
	d.resumeCheck()
	if d.onStatus != nil {
		d.onStatus(st, !d.aborted && err == nil)
	}
	if d.onApplied != nil {
		d.onApplied()
	}
}

func isMismatch(err error) bool {
	var mm *amt.FingerprintMismatchError
	return errors.As(err, &mm)
}

// abort ends the session after a certificate mismatch: polling stops, the controls stay
// disabled, the warning dialog is shown once and the start window may connect to the device
// again (after "Reset fingerprint").
func (d *DeviceWindow) abort(err error) {
	if d.aborted {
		return
	}
	d.aborted = true
	d.forgetResume()
	d.shutdown()
	d.powerKnown = false
	d.closeMenus()
	d.showPower()
	d.setKVMControls()
	d.setMessage(describe(err))
	d.release()
	if !d.closed {
		d.onError(err)
	}
	d.closeConsole()
	d.disconnectKVM() // last: the frame loop reports kvmEnded on the Fyne thread afterwards
}

func (d *DeviceWindow) run(action amt.PowerAction) {
	if !d.powerAllowed(action) {
		return
	}
	if action.Destructive() && !d.onlyPowersOn(action) {
		d.confirm(action, func() { d.execute(action) })
		return
	}
	d.execute(action)
}

// onlyPowersOn reports whether action just switches the device on, like Power On, so it needs
// no question: Reboot to BIOS on a device that was read as off (issue #120).
func (d *DeviceWindow) onlyPowersOn(action amt.PowerAction) bool {
	return action == amt.BootToBIOS && d.powerKnown && isOff(d.powerState)
}

// endsKVM reports whether AMT refuses action while a KVM session is open (ReturnValue 2,
// measured on AMT 16 for Power Off and Power Cycle; Reset and Boot to BIOS work and keep the
// session, so POST and the BIOS stay visible). Shutdown is included as a precaution: not
// measured, because the test devices do not offer it (no LMS).
func endsKVM(action amt.PowerAction) bool {
	return action == amt.PowerOff || action == amt.PowerCycle || action == amt.PowerShutdown
}

// kvmCloseWait bounds the wait for a closing KVM session before a power action that ends it.
const kvmCloseWait = 5 * time.Second

func (d *DeviceWindow) execute(action amt.PowerAction) {
	d.setPowerBusy(true)
	if !endsKVM(action) || (d.kvmClient == nil && d.kvmCancel == nil) {
		d.sendPower(action)
		return
	}
	d.resumeKVM = true // the app closes the session, not the user: it comes back (resumeCheck)
	var closed <-chan struct{}
	if c := d.kvmClient; c != nil {
		closed = c.Done()
	}
	d.disconnectKVM()
	if closed == nil {
		// Only a pending connect, cancelled. Its dial may have got through just before: kvmOpened
		// then closes that session and sends the action; the timer covers a connect that never
		// reports.
		a := action
		d.powerAfterConnect = &a
		time.AfterFunc(kvmCloseWait, func() { fyne.Do(d.flushPowerAfterConnect) })
		return
	}
	go func() {
		t := time.NewTimer(kvmCloseWait)
		defer t.Stop()
		select {
		case <-closed:
		case <-t.C: // send anyway; AMT reports the failure if the session is still open
		}
		fyne.Do(func() { d.sendPower(action) })
	}()
}

// flushPowerAfterConnect sends the action execute held back for a cancelled connect, once.
func (d *DeviceWindow) flushPowerAfterConnect() {
	if p := d.powerAfterConnect; p != nil {
		d.powerAfterConnect = nil
		d.sendPower(*p)
	}
}

// sendPower hands action to the poller and reports its result on the Fyne thread.
func (d *DeviceWindow) sendPower(action amt.PowerAction) {
	d.poller.Power(action, func(err error) {
		fyne.Do(func() {
			switch {
			case errors.Is(err, session.ErrStopped): // window closed or session aborted meanwhile
			case err != nil && isMismatch(err):
				d.abort(err)
			case err != nil:
				d.setPowerBusy(false)
				if !d.closed {
					d.onError(err)
				}
				d.resumeAfterPower(action, err)
			default:
				d.setPowerBusy(false)
				d.resumeAfterPower(action, nil)
			}
			if d.onActionDone != nil {
				d.onActionDone()
			}
		})
	})
}

// setPowerBusy locks the Power menu while an action runs.
func (d *DeviceWindow) setPowerBusy(busy bool) {
	d.powerBusy = busy
	if busy {
		d.closeMenus()
	}
	d.showPower()
}

// powerAllowed reports whether the Power menu offers action in the current state. Unknown
// state (failed read) offers everything, as before the redesign. Shutdown also needs the device
// to list Off - Soft Graceful (12), which AMT does only while LMS runs; unknown power states
// (not read yet or failed) lock it.
func (d *DeviceWindow) powerAllowed(action amt.PowerAction) bool {
	if d.aborted || d.powerBusy {
		return false
	}
	if action == amt.PowerShutdown && !d.canShutdown {
		return false
	}
	if !d.powerKnown {
		return true
	}
	switch {
	case d.powerState == models.PowerStateOn:
		return action != amt.PowerOn
	case isOff(d.powerState):
		return action == amt.PowerOn || action == amt.BootToBIOS // BootToBIOS powers on a machine that is off
	}
	return true // sleeping, hibernating or a state without a name: every action applies
}

// releaseKeys frees every held key and button on the remote before a dialog opens: Fyne creates
// a new focus manager for the overlay and never calls FocusLost on the view, so the key-up
// would go to the dialog and the key would stay pressed on the remote.
//
// While "Type clipboard" runs nothing is released: startTyping released the live keys before
// it set d.typing, the paused view drops live input, and the keys now held belong to the sender
// goroutine (Shift, AltGr of the running stroke). Releasing them from an error dialog (failed
// favorite read, power action) would send the next character without its modifier.
func (d *DeviceWindow) releaseKeys() {
	if d.typing != nil {
		return
	}
	if d.view != nil {
		d.view.releaseAll()
	}
}

func (d *DeviceWindow) showErrorDialog(err error) {
	d.releaseKeys()
	showError(d.win, err)
}

func (d *DeviceWindow) confirmDialog(action amt.PowerAction, proceed func()) {
	d.releaseKeys()
	showPowerConfirm(d.win, action, d.name, proceed)
	if d.onConfirmShown != nil {
		d.onConfirmShown()
	}
}

// validateInterval accepts whole seconds between MinPollInterval and MaxPollInterval.
func validateInterval(text string) error {
	n, err := strconv.Atoi(text)
	if err != nil || time.Duration(n)*time.Second < MinPollInterval || time.Duration(n)*time.Second > MaxPollInterval {
		return userError(lang.L("Enter a whole number of seconds between 1 and 3600."))
	}
	return nil
}

// idleLabel names an idle timeout for the settings choice.
func idleLabel(d time.Duration) string {
	if d == 0 {
		return lang.L("Never")
	}
	return lang.L("{{.Minutes}} minutes", map[string]any{"Minutes": int(d / time.Minute)})
}
