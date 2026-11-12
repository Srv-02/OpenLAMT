package ui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
)

type kvmHarness struct {
	*deviceHarness
	srv     *kvmtest.Server
	changed chan struct{} // onKVMChanged: after kvmOpened and kvmEnded
	frames  chan struct{}
	asked   chan func(string, bool)
	hidden  atomic.Bool
}

// newKVMDevice builds a device window whose fake dials a kvmtest server with one red frame.
// Tests read window state only after a signal from changed or frames (see device_test.go).
func newKVMDevice(t *testing.T, dev *sessionFake, opts kvmtest.Options) *kvmHarness {
	t.Helper()
	englishUI(t)
	a := test.NewTempApp(t)
	if opts.Updates == nil {
		opts.Updates = [][]byte{redUpdate(128, 64)}
	}
	opts.Username, opts.Password = "admin", "pw"
	srv := kvmtest.Start(t, opts)
	dev.FP, dev.KVMAddr, dev.KVMUser, dev.KVMPassword = fp, srv.Addr, "admin", "pw"
	h := &kvmHarness{deviceHarness: newDevice(t, a, dev, true), srv: srv,
		changed: make(chan struct{}, 8), frames: make(chan struct{}, 16), asked: make(chan func(string, bool), 4)}
	h.d.goos = "linux"
	h.d.view.mapper = newKeyMapper("linux", func() bool { return true })
	h.d.onKVMChanged = func() { h.changed <- struct{}{} }
	h.d.wake = nil // the wake tests (kvm_wake_test.go) switch it back on
	h.d.view.onFrame = func() { h.frames <- struct{}{} }
	h.d.askConsent = func(answer func(string, bool)) func() { h.asked <- answer; return func() { h.hidden.Store(true) } }
	h.show(t)
	return h
}

type sessionFake = session.Fake

// connect taps "Connect KVM" and waits for the session and, with frame, for the first frame.
// The fake server hands out its updates once per process, so only the first session of a
// test receives a frame.
func (h *kvmHarness) connect(t *testing.T, frame bool) *kvm.Client {
	t.Helper()
	tapKVM(h.d)
	recv(t, h.changed)
	if frame {
		recv(t, h.frames)
	}
	if h.d.kvmClient == nil {
		t.Fatalf("not connected: busy=%v status=%q errs=%d", h.d.kvmBusy, h.d.kvmStatus.Text, len(h.errs))
	}
	return h.d.kvmClient
}

func (h *kvmHarness) disconnect(t *testing.T) {
	t.Helper()
	tapKVM(h.d)
	recv(t, h.changed)
}

func TestKVMConnectShowsFrameAndDisconnects(t *testing.T) {
	dev := &sessionFake{}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	c := h.connect(t, true)
	if !h.d.kvmOn() || h.d.quality.Disabled() || h.d.fullBtn.Disabled() || h.d.cadBtn.Disabled() || h.d.clipBtn.Disabled() {
		t.Fatalf("controls while connected: active=%v", h.d.kvmOn())
	}
	if h.d.kvmStatus.Text != "" || h.d.view.img.Image == nil || h.d.view.front.RGBAAt(1, 1).R < 240 {
		t.Fatalf("status %q, image set %v", h.d.kvmStatus.Text, h.d.view.img.Image != nil)
	}
	if h.d.win.Canvas().Focused() != h.d.view {
		t.Fatal("view not focused after connect")
	}
	h.disconnect(t)
	if h.d.kvmClient != nil || h.d.view.Client() != nil || h.d.kvmOn() || h.d.quality.Disabled() {
		t.Fatalf("after disconnect: client=%v active=%v", h.d.kvmClient, h.d.kvmOn())
	}
	if h.d.kvmStatus.Text != "KVM session closed." || !errors.Is(c.Err(), kvm.ErrClosed) || !h.d.fullBtn.Disabled() {
		t.Fatalf("status %q err %v", h.d.kvmStatus.Text, c.Err())
	}
	// The quality applies to the next connection.
	test.Tap(h.d.quality)
	tapMenuItem(t, h.d.qualityPopup, h.d.qualityMenu, h.d.qualityItems[kvm.QualityLow])
	h.connect(t, false)
	h.disconnect(t)
	if q := dev.Qualities(); len(q) != 2 || q[0] != kvm.QualityHigh || q[1] != kvm.QualityLow {
		t.Fatalf("qualities %v", q)
	}
	if h.srv.Connections() != 2 || len(h.errs) != 0 {
		t.Fatalf("connections=%d errs=%d", h.srv.Connections(), len(h.errs))
	}
}

func TestKVMConsentDialog(t *testing.T) {
	dev := &sessionFake{Consent: true, ConsentCode: "123456"}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	tapKVM(h.d)
	answer := <-h.asked
	if !h.d.kvmOn() || !h.d.quality.Disabled() || h.d.kvmStatus.Text != "Connecting KVM…" {
		t.Fatalf("controls while connecting: active=%v %q", h.d.kvmOn(), h.d.kvmStatus.Text)
	}
	answer("123456", true)
	recv(t, h.changed)
	recv(t, h.frames)
	if h.d.kvmClient == nil {
		t.Fatal("not connected after consent")
	}
	h.disconnect(t)

	tapKVM(h.d) // cancelled dialog: silent
	(<-h.asked)("", false)
	recv(t, h.changed)
	if h.d.kvmClient != nil || len(h.errs) != 0 || h.d.kvmStatus.Text != "" || h.d.kvmOn() {
		t.Fatalf("cancel: errs=%d status=%q", len(h.errs), h.d.kvmStatus.Text)
	}

	tapKVM(h.d) // wrong code: error shown
	(<-h.asked)("654321", true)
	recv(t, h.changed)
	var ce *amt.ConsentError
	if err := <-h.errs; !errors.As(err, &ce) || !strings.Contains(h.d.kvmStatus.Text, "did not accept the consent code") {
		t.Fatalf("wrong code: %v %q", err, h.d.kvmStatus.Text)
	}

	tapKVM(h.d) // "Disconnect KVM" while the dialog is open cancels and hides it
	<-h.asked
	tapKVM(h.d)
	recv(t, h.changed)
	if h.d.kvmClient != nil || !h.hidden.Load() || len(h.errs) != 0 {
		t.Fatalf("cancel by disconnect: hidden=%v errs=%d", h.hidden.Load(), len(h.errs))
	}
	if h.srv.Connections() != 1 {
		t.Fatalf("connections=%d", h.srv.Connections())
	}
}

func TestKVMOpenErrorsAreShown(t *testing.T) {
	dev := &sessionFake{KVMErr: &amt.KVMError{Step: "KVMRedirectionSAP.RequestStateChange", ReturnValue: 2}}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	tapKVM(h.d)
	recv(t, h.changed)
	if err := <-h.errs; !strings.Contains(describe(err), "refused to enable KVM (return value 2)") {
		t.Fatalf("err %v", err)
	}
	if h.d.kvmClient != nil || !h.d.kvmToggleEnabled() || h.d.kvmOn() || h.d.quality.Disabled() {
		t.Fatal("controls not reset after error")
	}
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	dev.KVMErr = mm
	tapKVM(h.d)
	recv(t, h.changed)
	if err := <-h.errs; !errors.Is(err, mm) || !h.d.aborted || h.d.kvmToggleEnabled() {
		t.Fatalf("mismatch: %v aborted=%v", err, h.d.aborted)
	}
	recv(t, h.closed)
	waitStopped(t, h.d)
}

func TestKVMSessionEndedByServerShowsMessage(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{CloseAfterUpdate: 1})
	tapKVM(h.d)
	recv(t, h.changed) // opened
	recv(t, h.frames)
	recv(t, h.changed) // ended by the server
	if h.d.kvmClient != nil || !strings.HasPrefix(h.d.kvmStatus.Text, "KVM session ended: ") || h.d.kvmOn() || !h.d.kvmToggleEnabled() {
		t.Fatalf("status %q active %v", h.d.kvmStatus.Text, h.d.kvmOn())
	}
	if h.d.view.img.Image != nil || len(h.errs) != 0 {
		t.Fatal("image still shown or error dialog raised")
	}
}

func TestCloseWindowEndsKVMSession(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	c := h.connect(t, true)
	h.d.win.Close()
	recv(t, h.closed)
	recv(t, h.changed)
	if !errors.Is(c.Err(), kvm.ErrClosed) || h.d.kvmClient != nil {
		t.Fatalf("session still open: %v", c.Err())
	}
	assertViewFreed(t, h.d.view)
	waitStopped(t, h.d)
}

// assertViewFreed checks that a closed window keeps no copy of the remote screen: Fyne keeps
// closed windows reachable (kvmView.free), so every buffer left here would grow the app's
// memory by one screen per closed window.
func assertViewFreed(t *testing.T, v *kvmView) {
	t.Helper()
	if v.front != nil || v.back != nil || v.img.Image != nil || v.client != nil {
		t.Fatalf("closed window keeps buffers: front=%v back=%v image=%v client=%v",
			v.front != nil, v.back != nil, v.img.Image != nil, v.client != nil)
	}
}

func TestCloseWindowAfterDisconnectFreesView(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.connect(t, true)
	h.disconnect(t)
	if h.d.view.front == nil {
		t.Fatal("precondition: the ended session leaves its buffer for the next one")
	}
	h.d.win.Close()
	recv(t, h.closed)
	assertViewFreed(t, h.d.view)
	waitStopped(t, h.d)
}

// Closing the window while a connect waits for the consent code cancels it and frees the
// buffers of the previous session.
func TestCloseWindowDuringConnectFreesView(t *testing.T) {
	dev := &sessionFake{Consent: true, ConsentCode: "123456"}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	tapKVM(h.d)
	(<-h.asked)("123456", true)
	recv(t, h.changed)
	recv(t, h.frames)
	h.disconnect(t)
	tapKVM(h.d)
	<-h.asked // the connect waits for the consent code
	h.d.win.Close()
	recv(t, h.closed)
	recv(t, h.changed) // kvmOpened with the cancelled connect
	assertViewFreed(t, h.d.view)
	waitStopped(t, h.d)
}

func TestMismatchDuringKVMClosesSession(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	c := h.connect(t, true)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h.d.apply(amt.Status{}, mm)
	recv(t, h.changed)
	<-h.errs
	if !errors.Is(c.Err(), kvm.ErrClosed) || h.d.kvmClient != nil || h.d.kvmToggleEnabled() || !h.d.fullBtn.Disabled() || !h.d.quality.Disabled() {
		t.Fatalf("after abort: err=%v disabled=%v", c.Err(), !h.d.kvmToggleEnabled())
	}
	tapKVM(h.d) // disabled
	time.Sleep(10 * time.Millisecond)
	if h.srv.Connections() != 1 {
		t.Fatal("reconnected after abort")
	}
	waitStopped(t, h.d)
}

func TestCtrlAltDelAndFullScreen(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.d.setFullScreen(true) // without a session: nothing
	if h.d.fullScreen || h.d.win.FullScreen() {
		t.Fatal("full screen without session")
	}
	h.connect(t, true)
	test.Tap(h.d.cadBtn)
	got := waitInput(t, h.srv, 6)
	want := [][]byte{keyMsg(0xffe3, true), keyMsg(0xffe9, true), keyMsg(0xffff, true), keyMsg(0xffff, false), keyMsg(0xffe9, false), keyMsg(0xffe3, false)}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d: %x", i, got[i])
		}
	}
	if h.d.win.Canvas().Focused() != h.d.view {
		t.Fatal("view lost focus after Ctrl+Alt+Del")
	}
	test.Tap(h.d.fullBtn)
	if !h.d.win.FullScreen() || !h.d.fullScreen {
		t.Fatal("not full screen")
	}
	if !fullScreenShows(h.d, h.d.kvmStage) || h.d.consolePane.Visible() || !containsObject(h.d.kvmArea, h.d.view) {
		t.Fatalf("full screen content %T", h.d.win.Content())
	}
	if h.d.barSwitch.Selected() != viewKVM || h.d.barSwitch.Disabled() {
		t.Fatal("the full screen bar must offer the view switch")
	}
	if h.d.bar.Position().Y != 0 || h.d.bar.Size().Width <= 0 || h.d.win.Canvas().Focused() != h.d.view {
		t.Fatalf("bar at %v size %v", h.d.bar.Position(), h.d.bar.Size())
	}
	test.Tap(h.d.barCad)
	waitInput(t, h.srv, 12)
	test.Tap(h.d.exitFullBtn)
	if h.d.win.FullScreen() || h.d.win.Content() != h.d.content || h.d.win.Canvas().Focused() != h.d.view {
		t.Fatal("normal content not restored")
	}
	if !containsObject(h.d.kvmArea, h.d.view) {
		t.Fatal("view not back in the KVM area")
	}
	test.Tap(h.d.fullBtn)
	h.disconnect(t) // session end leaves full screen
	if h.d.win.FullScreen() || h.d.win.Content() != h.d.content || !containsObject(h.d.kvmArea, h.d.view) {
		t.Fatal("full screen not left after disconnect")
	}
}

func containsObject(c *fyne.Container, o fyne.CanvasObject) bool {
	for _, x := range c.Objects {
		if x == o {
			return true
		}
	}
	return false
}

func TestTypeClipboardHookEnablesButtons(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	if h.d.onTypeClipboard == nil {
		t.Fatal("buildKVM installs no hook")
	}
	calls := 0
	h.d.onTypeClipboard = func() { calls++ } // replaces the dialog to count the taps
	h.d.setKVMControls()
	if !h.d.clipBtn.Disabled() {
		t.Fatal("enabled without session")
	}
	h.connect(t, true)
	if h.d.clipBtn.Disabled() || h.d.barClip.Disabled() {
		t.Fatal("hook set and connected: buttons must be enabled")
	}
	test.Tap(h.d.clipBtn)
	test.Tap(h.d.barClip)
	if calls != 2 {
		t.Fatalf("hook called %d times", calls)
	}
	h.disconnect(t)
	if !h.d.clipBtn.Disabled() {
		t.Fatal("still enabled without session")
	}
}

// A dialog opening while a key is held must release it first: the overlay gets a new focus
// manager and the view never sees the key-up.
func TestDialogReleasesHeldKeys(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.connect(t, true)
	h.d.onError = h.d.showErrorDialog // the real path; the harness replaces it with a recorder
	h.d.win.Canvas().Focus(h.d.view)
	h.d.view.KeyDown(&fyne.KeyEvent{Physical: fyne.HardwareKey{ScanCode: 30}}) // KEY_A
	waitInput(t, h.srv, 1)
	h.d.onError(errors.New("x"))
	got := waitInput(t, h.srv, 2)
	if !bytes.Equal(got[1], keyMsg(0x61, false)) {
		t.Fatalf("messages %x", got)
	}
	h.disconnect(t)
}

func TestConsentCodeWithCancelledContextShowsNoDialog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &consentPrompt{ctx: ctx, ask: func(func(string, bool)) func() { t.Error("dialog shown"); return nil }}
	if _, err := p.ConsentCode(); !errors.Is(err, errConsentCancelled) {
		t.Fatalf("err=%v", err)
	}
}

func TestKVMIdleTimeoutDisconnects(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.d.idleTimeout = func() time.Duration { return 80 * time.Millisecond }
	// No window state is read between the two signals: the timer goroutine runs kvmEnded.
	tapKVM(h.d)
	recv(t, h.changed) // connected
	recv(t, h.changed) // the idle timer ended the session
	if h.d.kvmClient != nil || h.d.kvmStatus.Text != "KVM disconnected after 1 minutes without input." {
		t.Fatalf("client %v status %q", h.d.kvmClient, h.d.kvmStatus.Text)
	}
	// The next session that ends by hand reports a plain close again.
	h.d.idleTimeout = func() time.Duration { return 0 }
	h.connect(t, false)
	h.disconnect(t)
	if h.d.kvmStatus.Text != "KVM session closed." {
		t.Fatalf("status %q", h.d.kvmStatus.Text)
	}
}

func TestKVMInputDefersIdleTimeout(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.d.idleTimeout = func() time.Duration { return 150 * time.Millisecond }
	h.connect(t, true)
	h.d.win.Canvas().Focus(h.d.view)
	for i := 0; i < 8; i++ { // 400 ms of input, every 50 ms
		time.Sleep(50 * time.Millisecond)
		h.d.view.onInput()
	}
	if h.d.idleTimer == nil || len(h.changed) != 0 {
		t.Fatal("session ended despite input")
	}
	h.disconnect(t)
	if h.d.idleTimer != nil {
		t.Fatal("idle timer left running")
	}
}

// A manual disconnect never reports an idle timeout, even if the timer fires before kvmEnded.
func TestKVMManualDisconnectStopsIdleTimer(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.d.idleTimeout = func() time.Duration { return time.Hour }
	h.connect(t, true)
	h.disconnect(t)
	if h.d.idleTimer != nil || h.d.kvmStatus.Text != "KVM session closed." {
		t.Fatalf("timer %v status %q", h.d.idleTimer, h.d.kvmStatus.Text)
	}
}

// AMT refuses Power Off and Power Cycle while a KVM session is open (ReturnValue 2): the window
// closes the session first and sends the action only once it is gone. Reset keeps the session.
// Shutdown does the same as a precaution (not measured).
func TestPowerOffAndCycleCloseKVMFirst(t *testing.T) {
	for _, act := range []amt.PowerAction{amt.PowerOff, amt.PowerCycle, amt.PowerShutdown} {
		t.Run(act.String(), func(t *testing.T) {
			dev := &sessionFake{St: amt.Status{PowerState: 2}}
			h := newKVMDevice(t, dev, kvmtest.Options{})
			c := h.connect(t, true)
			gate := make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(gate) }) }
			t.Cleanup(release) // a failed check must not leave the fake blocked
			dev.PowerGate = gate
			h.d.execute(act)
			deadline := time.Now().Add(5 * time.Second)
			for !dev.PowerStarted() && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !dev.PowerStarted() {
				t.Fatal("power action not sent")
			}
			select {
			case <-c.Done():
			default:
				t.Fatal("power action sent while the KVM session was open")
			}
			release()
			recv(t, h.actionDone)
			if p := dev.Powered(); len(p) != 1 || p[0] != act {
				t.Fatalf("powered %v", p)
			}
		})
	}
}

func TestPowerResetKeepsKVM(t *testing.T) {
	dev := &sessionFake{St: amt.Status{PowerState: 2}}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	c := h.connect(t, true)
	h.d.execute(amt.PowerReset)
	recv(t, h.actionDone)
	select {
	case <-c.Done():
		t.Fatal("reset closed the KVM session")
	default:
	}
	if h.d.kvmClient != c {
		t.Fatal("session replaced")
	}
}

// A connect that gets through just after Power Off cancelled it: the window closes that session
// first and sends the action only then, otherwise AMT refuses it (ReturnValue 2).
func TestPowerOffWaitsForCancelledConnect(t *testing.T) {
	dev := &sessionFake{St: amt.Status{PowerState: 2}}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	dev.KVMGate = gate
	tapKVM(h.d) // connecting; the fake holds the session after the dial
	if !h.d.kvmBusy {
		t.Fatal("not connecting")
	}
	time.Sleep(100 * time.Millisecond) // let the dial get through before the cancel
	h.d.execute(amt.PowerOff)
	release() // OpenKVM returns the open session despite the cancel
	recv(t, h.actionDone)
	if p := dev.Powered(); len(p) != 1 || p[0] != amt.PowerOff {
		t.Fatalf("powered %v", p)
	}
	if h.d.kvmClient != nil {
		t.Fatal("the late session stayed open")
	}
}
