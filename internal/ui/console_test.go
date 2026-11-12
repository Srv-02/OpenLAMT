package ui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fyne-io/terminal"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/sol"
	"github.com/Srv-02/openlamt/internal/sol/soltest"
)

// consoleHarness is a device window whose fake opens SOL against a soltest server (and KVM
// against the kvmtest server of kvmHarness). Tests read window state only after a signal from
// conChanged; the terminal is read only after a signal from reads or after the session ended.
type consoleHarness struct {
	*kvmHarness
	sol        *soltest.Server
	peers      chan *soltest.Peer
	conChanged chan struct{} // onConsoleChanged: question shown or closed, session opened or ended
	reads      chan struct{} // every Read call of the terminal loop (readSignal)
	accepted   []*soltest.Peer
	gate       *sync.Mutex    // see sync
	termOut    io.WriteCloser // the terminal's input of the last session; set before its first read
}

// readSignal reports every Read of the terminal loop on reads before it reads: the first one
// comes after RunWithConnection has wired the terminal's input, a later one after the output of
// the previous one has been handled. After each read it passes gate (see consoleHarness.sync).
type readSignal struct {
	r     io.Reader
	reads chan struct{}
	gate  *sync.Mutex
}

func (s readSignal) Read(p []byte) (int, error) {
	select {
	case s.reads <- struct{}{}:
	default:
	}
	n, err := s.r.Read(p)
	s.gate.Lock()
	s.gate.Unlock()
	return n, err
}

// sync orders what the test goroutine did so far before what the terminal loop does after its
// next read. The test driver runs fyne.Do inline, so the loop's goroutine stands in for the Fyne
// thread; without this the race detector cannot see that a device message the test sends
// (through the kernel) comes after the test's own reads of the window state.
func (h *consoleHarness) sync() {
	h.gate.Lock()
	h.gate.Unlock()
}

func newConsoleDevice(t *testing.T, solOn bool) *consoleHarness {
	t.Helper()
	dev := &sessionFake{}
	dev.Cfg.Settings.Features.SOL = solOn
	h := &consoleHarness{kvmHarness: newKVMDevice(t, dev, kvmtest.Options{}),
		peers: make(chan *soltest.Peer, 4), conChanged: make(chan struct{}, 16), reads: make(chan struct{}, 64), gate: &sync.Mutex{}}
	h.sol = soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	dev.SOL.Addr, dev.SOL.User, dev.SOL.Password = h.sol.Addr, "admin", "pw"
	h.d.con.onChanged = func() { h.conChanged <- struct{}{} }
	h.d.con.run = func(term *terminal.Terminal, c *sol.Client, out io.WriteCloser) error {
		h.termOut = out
		return term.RunWithConnection(out, readSignal{c, h.reads, h.gate})
	}
	// Runs before the cleanups of the window and the servers: close the window and wait until
	// the console and KVM sessions it ends have reported, so no session goroutine touches the
	// window while the test goroutine tears it down.
	t.Cleanup(func() {
		if h.wasClosed {
			return
		}
		console, kvm := h.d.con.client != nil, h.d.kvmClient != nil
		h.d.win.Close()
		for _, p := range h.accepted {
			p.Disconnect() // Close need not wait for END_SOL_REPLY
		}
		if console {
			recv(t, h.conChanged)
		}
		if kvm {
			recv(t, h.changed)
		}
	})
	return h
}

// accept plays the device side of the next SOL session.
func (h *consoleHarness) accept(t *testing.T) {
	go func() { h.peers <- h.sol.AcceptSession(t) }()
}

func (h *consoleHarness) peer(t *testing.T) *soltest.Peer {
	t.Helper()
	select {
	case p := <-h.peers:
		h.accepted = append(h.accepted, p)
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("no SOL session")
		return nil
	}
}

// open switches to the Console view and waits for the session (SOL is on).
func (h *consoleHarness) open(t *testing.T) *soltest.Peer {
	t.Helper()
	h.accept(t)
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged)
	if h.d.con.client == nil {
		t.Fatalf("console not connected: errs=%d", len(h.errs))
	}
	recv(t, h.reads) // the terminal loop is wired
	return h.peer(t)
}

// waitEnded waits until the session has ended and its end was handled.
func (h *consoleHarness) waitEnded(t *testing.T) {
	t.Helper()
	recv(t, h.conChanged)
	if h.d.con.client != nil {
		t.Fatal("session still registered")
	}
}

func TestConsoleOpensWhenSOLIsOn(t *testing.T) {
	h := newConsoleDevice(t, true)
	p := h.open(t)
	if h.dev.SOL.Calls() != 1 || open(h.d.win) {
		t.Fatalf("OpenSOL calls %d, dialog open %v", h.dev.SOL.Calls(), open(h.d.win))
	}
	if h.d.consoleLine.Text != "Console connected" || !h.d.consoleLine.Visible() {
		t.Fatalf("status %q", h.d.consoleLine.Text)
	}
	if h.d.win.Canvas().Focused() != h.d.con.term {
		t.Fatal("terminal not focused")
	}
	if len(h.d.consoleStage.Objects) != 1 {
		t.Fatalf("console stage holds %d objects", len(h.d.consoleStage.Objects))
	}
	// The selected segment and a second switch to the view neither ask nor connect again.
	h.d.viewSwitch.Tap(viewConsole)
	tapKVM(h.d)
	if h.d.win.Canvas().Focused() == h.d.con.term {
		t.Fatal("the hidden terminal kept the focus")
	}
	h.d.viewSwitch.Tap(viewConsole)
	if h.dev.SOL.Calls() != 1 || h.d.win.Canvas().Focused() != h.d.con.term {
		t.Fatalf("calls %d, focus back %v", h.dev.SOL.Calls(), h.d.win.Canvas().Focused() == h.d.con.term)
	}

	// Typing in the terminal reaches the host; Return is a carriage return.
	h.d.con.term.TypedRune('l')
	h.d.con.term.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	var got []byte
	for len(got) < 2 {
		got = append(got, p.Expect(t, 0x28).Data()...)
	}
	if string(got) != "l\r" {
		t.Fatalf("sent %q", got)
	}

	// Output of the host lands in the terminal; the device ends the session: no message.
	h.sync()
	p.Data(t, []byte("\x1b[1mserver login:\x1b[0m "))
	p.EndReply(t)
	h.waitEnded(t)
	if txt := h.d.con.term.Text(); !strings.Contains(txt, "server login:") {
		t.Fatalf("terminal text %q", txt)
	}
	if h.d.consoleLine.Text != "" || h.d.consoleLine.Visible() || len(h.errs) != 0 {
		t.Fatalf("after the end: status %q, errs %d", h.d.consoleLine.Text, len(h.errs))
	}
	if !h.d.consoleActive() {
		t.Fatal("the view must stay on Console")
	}

	// Selecting Console again connects a new session in a new terminal.
	old := h.d.con.term
	h.accept(t)
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged)
	if h.d.con.client == nil || h.d.con.term == old || h.dev.SOL.Calls() != 2 {
		t.Fatalf("reconnect: client %v, new terminal %v, calls %d", h.d.con.client != nil, h.d.con.term != old, h.dev.SOL.Calls())
	}
	recv(t, h.reads)
	h.sync()
	h.peer(t).Disconnect() // a lost connection is an error
	h.waitEnded(t)
	if err := <-h.errs; err == nil || errors.Is(err, sol.ErrClosedByDevice) || h.d.consoleLine.Visible() {
		t.Fatalf("lost connection: %v, status %q", err, h.d.consoleLine.Text)
	}
}

func TestConsoleAsksToEnableSOL(t *testing.T) {
	h := newConsoleDevice(t, false)
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged) // the question
	top := h.d.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("no question")
	}
	findLabel(t, top, "Enable Serial over LAN?")
	findLabel(t, top, "Serial over LAN (SOL) is disabled on “Büro”. Enable it to open the text console of this device? The setting stays enabled until you disable it in Settings.")
	bs := panelButtons(top)
	if got := texts(bs); got != "Cancel|Enable" {
		t.Fatalf("buttons %q", got)
	}
	test.Tap(bs[0]) // Cancel: nothing opens, the KVM view returns instead of an empty Console
	recv(t, h.conChanged)
	if h.dev.SOL.Calls() != 0 || h.d.con.client != nil || h.d.con.busy || h.d.consoleActive() ||
		h.d.viewSwitch.Selected() != viewKVM || !h.d.kvmStage.Visible() || h.d.consolePane.Visible() ||
		h.d.consoleLine.Visible() || len(h.errs) != 0 {
		t.Fatalf("cancel: calls %d, busy %v, console view %v", h.dev.SOL.Calls(), h.d.con.busy, h.d.consoleActive())
	}

	h.d.viewSwitch.Tap(viewConsole) // Console again asks again
	recv(t, h.conChanged)
	h.accept(t)
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[1]) // Enable
	recv(t, h.conChanged)
	if h.dev.SOL.Calls() != 1 || h.d.con.client == nil || h.d.consoleLine.Text != "Console connected" {
		t.Fatalf("enable: calls %d, client %v", h.dev.SOL.Calls(), h.d.con.client != nil)
	}
	recv(t, h.reads)
	h.peer(t)
}

func TestConsoleConsentAndOpenErrors(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.dev.Consent, h.dev.ConsentCode = true, "123456"
	h.d.viewSwitch.Tap(viewConsole)
	(<-h.asked)("", false) // cancelled consent: silent, back to the KVM view
	recv(t, h.conChanged)
	if h.d.con.client != nil || h.d.con.busy || len(h.errs) != 0 || h.d.consoleActive() {
		t.Fatalf("cancelled consent: busy %v errs %d console view %v", h.d.con.busy, len(h.errs), h.d.consoleActive())
	}

	h.accept(t)
	h.d.viewSwitch.Tap(viewConsole)
	(<-h.asked)("123456", true)
	recv(t, h.conChanged)
	if h.d.con.client == nil {
		t.Fatal("not connected after consent")
	}
	recv(t, h.reads)
	h.sync()
	h.peer(t).EndReply(t)
	h.waitEnded(t)

	boom := errors.New("boom")
	h.dev.SOL.Err = boom
	h.d.viewSwitch.Tap(viewConsole) // the fake fails before it asks for consent
	recv(t, h.conChanged)
	if err := <-h.errs; !errors.Is(err, boom) || h.d.con.busy || h.d.consoleLine.Visible() {
		t.Fatalf("open error %v, busy %v", err, h.d.con.busy)
	}
	if !h.d.consoleActive() {
		t.Fatal("after an earlier session the view must stay on its terminal")
	}
}

// TestConsoleFirstOpenFailureReturnsToKVM: an error before any session was open leaves no
// empty console: the error shows and the KVM view returns.
func TestConsoleFirstOpenFailureReturnsToKVM(t *testing.T) {
	h := newConsoleDevice(t, true)
	boom := errors.New("boom")
	h.dev.SOL.Err = boom
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged)
	if err := <-h.errs; !errors.Is(err, boom) || h.d.con.busy {
		t.Fatalf("open error %v, busy %v", err, h.d.con.busy)
	}
	if h.d.consoleActive() || h.d.viewSwitch.Selected() != viewKVM || !h.d.kvmStage.Visible() {
		t.Fatal("the KVM view must return")
	}
}

// TestConsoleCancelInFullScreenReturnsToKVM: Cancel on the SOL question asked from the full
// screen bar returns to the KVM view in full screen.
func TestConsoleCancelInFullScreenReturnsToKVM(t *testing.T) {
	h := newConsoleDevice(t, false)
	h.connect(t, true)
	test.Tap(h.d.fullBtn)
	h.d.barSwitch.Tap(viewConsole)
	recv(t, h.conChanged) // the question
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[0])
	recv(t, h.conChanged)
	if h.d.consoleActive() || h.d.barSwitch.Selected() != viewKVM || !fullScreenShows(h.d, h.d.kvmStage) ||
		h.d.win.Canvas().Focused() != h.d.view {
		t.Fatalf("console view %v, full screen %v", h.d.consoleActive(), h.d.fullScreen)
	}
}

func TestConsoleTypeClipboardWritesCarriageReturns(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.d.readClipboard = func() string { return "root\r\npass\nword" }
	h.d.typeConsoleClipboard() // no session yet: nothing happens
	p := h.open(t)

	test.Tap(h.d.clipBtn)
	want := "root\rpass\rword"
	var got []byte
	for len(got) < len(want) {
		got = append(got, p.Expect(t, 0x28).Data()...)
	}
	if string(got) != want {
		t.Fatalf("typed %q", got)
	}
	if h.kvmHarness.d.typing != nil || h.srv.Connections() != 0 {
		t.Fatal("Type Clipboard of the console must not use KVM")
	}
}

// TestConsoleTypeClipboardFollowsTheSession: in the Console view Type Clipboard is enabled only
// while a console session runs; the KVM view keeps its own rule (KVM session).
func TestConsoleTypeClipboardFollowsTheSession(t *testing.T) {
	h := newConsoleDevice(t, true)
	p := h.open(t)
	if h.d.clipBtn.Disabled() || h.d.barClip.Disabled() {
		t.Fatal("Type Clipboard disabled with a console session")
	}
	tapKVM(h.d)
	if !h.d.clipBtn.Disabled() {
		t.Fatal("KVM view without a KVM session: Type Clipboard must be disabled")
	}
	h.d.viewSwitch.Tap(viewConsole)
	h.sync()
	p.EndReply(t)
	h.waitEnded(t)
	if !h.d.clipBtn.Disabled() || !h.d.barClip.Disabled() {
		t.Fatal("Type Clipboard enabled after the console session ended")
	}
}

// TestConsoleClipboardIsOneBlockBetweenKeys: Type Clipboard and the terminal's keys share one
// writer, so a text longer than one SOL message is not split by keys typed meanwhile, and both
// keep their order.
func TestConsoleClipboardIsOneBlockBetweenKeys(t *testing.T) {
	h := newConsoleDevice(t, true)
	text := strings.Repeat("a", 3000) // three SOL messages
	h.d.readClipboard = func() string { return text }
	p := h.open(t)
	out := h.termOut
	if _, err := out.Write([]byte("<")); err != nil { // a key before the text
		t.Fatal(err)
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() { // keys while the text is sent
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = out.Write([]byte("Z"))
			time.Sleep(50 * time.Microsecond)
		}
	}()
	test.Tap(h.d.clipBtn)
	var got []byte
	for strings.Count(string(got), "a") < len(text) {
		got = append(got, p.Expect(t, 0x28).Data()...)
	}
	close(stop)
	<-stopped
	s := string(got)
	if !strings.HasPrefix(s, "<") || !strings.Contains(s, text) {
		t.Fatalf("clipboard split or out of order: %d keys inside", strings.Count(strings.Trim(s[1:], "Z"), "Z"))
	}
}

func TestConsoleCtrlAltDelConnectsKVMInTheBackground(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.open(t)
	test.Tap(h.d.cadBtn)
	recv(t, h.changed) // KVM opened
	got := waitInput(t, h.srv, 6)
	want := [][]byte{keyMsg(0xffe3, true), keyMsg(0xffe9, true), keyMsg(0xffff, true), keyMsg(0xffff, false), keyMsg(0xffe9, false), keyMsg(0xffe3, false)}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d: %x", i, got[i])
		}
	}
	if !h.d.consoleActive() || h.d.kvmStage.Visible() || h.d.kvmClient == nil {
		t.Fatal("the view must stay on Console with KVM connected behind it")
	}
	if h.d.win.Canvas().Focused() != h.d.con.term {
		t.Fatal("the terminal lost the focus")
	}
	test.Tap(h.d.cadBtn) // over the open session, no second connect
	waitInput(t, h.srv, 12)
	if h.srv.Connections() != 1 {
		t.Fatalf("connections %d", h.srv.Connections())
	}
}

func TestConsoleCtrlAltDelConnectFailureShowsError(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.dev.KVMErr = errors.New("kvm refused")
	h.open(t)
	test.Tap(h.d.cadBtn)
	recv(t, h.changed)
	if err := <-h.errs; err == nil || h.d.kvmClient != nil || h.d.con.cadPending {
		t.Fatalf("err %v, pending %v", err, h.d.con.cadPending)
	}
	h.dev.KVMErr = nil
	tapKVM(h.d)
	h.connect(t, false) // a later manual connect sends nothing on its own
	if n := len(inputMessages(h.srv)); n != 0 {
		t.Fatalf("%d input messages after a manual connect", n)
	}
}

func TestCloseWindowClosesConsole(t *testing.T) {
	h := newConsoleDevice(t, true)
	p := h.open(t)
	c := h.d.con.client
	h.d.win.Close()
	recv(t, h.closed)
	p.Expect(t, 0x22) // END_SOL
	p.EndReply(t)
	h.waitEnded(t)
	if !errors.Is(c.Err(), sol.ErrClosed) || len(h.errs) != 0 {
		t.Fatalf("err %v, errs %d", c.Err(), len(h.errs))
	}
	waitStopped(t, h.d)
}

func TestCloseWindowCancelsConsoleConsent(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.dev.Consent, h.dev.ConsentCode = true, "123456"
	h.d.viewSwitch.Tap(viewConsole)
	<-h.asked
	h.d.win.Close()
	recv(t, h.closed)
	recv(t, h.conChanged)
	if !h.hidden.Load() || h.d.con.client != nil || len(h.errs) != 0 {
		t.Fatalf("hidden %v errs %d", h.hidden.Load(), len(h.errs))
	}
	waitStopped(t, h.d)
}

func TestMismatchClosesConsole(t *testing.T) {
	h := newConsoleDevice(t, true)
	p := h.open(t)
	h.d.apply(amt.Status{}, &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)})
	<-h.errs
	p.Expect(t, 0x22)
	p.EndReply(t)
	h.waitEnded(t)
	if h.d.consoleLine.Visible() || len(h.errs) != 0 {
		t.Fatalf("status %q errs %d", h.d.consoleLine.Text, len(h.errs))
	}
	h.d.viewSwitch.Tap(viewConsole) // aborted: no new session
	if h.d.con.busy || h.dev.SOL.Calls() != 1 {
		t.Fatalf("connect after abort: busy %v calls %d", h.d.con.busy, h.dev.SOL.Calls())
	}
	waitStopped(t, h.d)
}

func TestConsolePanicKeepsTheAppRunning(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.d.con.run = func(*terminal.Terminal, *sol.Client, io.WriteCloser) error { panic("parser") }
	h.accept(t)
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged) // opened
	p := h.peer(t)
	p.Expect(t, 0x22) // the guard closes the session
	p.EndReply(t)
	h.waitEnded(t)
	if err := <-h.errs; err == nil || !strings.Contains(describe(err), "parser") || h.d.consoleLine.Visible() {
		t.Fatalf("panic reported as %v, status %q", err, h.d.consoleLine.Text)
	}
	// The window keeps working: a new session opens.
	h.d.con.run = func(term *terminal.Terminal, c *sol.Client, out io.WriteCloser) error {
		h.termOut = out
		return term.RunWithConnection(out, readSignal{c, h.reads, h.gate})
	}
	h.accept(t)
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged)
	if h.d.con.client == nil || h.dev.SOL.Calls() != 2 {
		t.Fatal("no new session after the panic")
	}
	recv(t, h.reads)
	h.peer(t)
}

func TestConsoleLineEnds(t *testing.T) {
	for in, want := range map[string]string{"a\r\nb\nc\r": "a\rb\rc\r", "": "", "x": "x", "\n\n": "\r\r"} {
		if got := consoleLineEnds(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// fullScreenShows reports whether the full screen content holds o, shown, with the bar on top.
func fullScreenShows(d *DeviceWindow, o fyne.CanvasObject) bool {
	stack, ok := d.win.Content().(*fyne.Container)
	return ok && d.win.FullScreen() && d.fullScreen && stack != d.content && containsObject(stack, o) && o.Visible() &&
		len(stack.Objects) > 0 && stack.Objects[len(stack.Objects)-1] == d.barLayer
}

// TestConsoleFullScreen: Full Screen in the Console view shows the console; the KVM|Console
// switch of the full screen bar changes the view in full screen; Exit Full Screen restores the
// window in the view last chosen; the end of the console session leaves full screen.
func TestConsoleFullScreen(t *testing.T) {
	h := newConsoleDevice(t, true)
	p := h.open(t)
	if h.d.fullBtn.Disabled() {
		t.Fatal("Full Screen disabled with a console session")
	}
	test.Tap(h.d.fullBtn)
	if !fullScreenShows(h.d, h.d.consolePane) || h.d.kvmStage.Visible() {
		t.Fatalf("console not in full screen: content %T", h.d.win.Content())
	}
	if h.d.barSwitch.Selected() != viewConsole || h.d.win.Canvas().Focused() != h.d.con.term {
		t.Fatalf("bar switch %d, terminal focused %v", h.d.barSwitch.Selected(), h.d.win.Canvas().Focused() == h.d.con.term)
	}

	// Type Clipboard of the bar types into the console.
	h.d.readClipboard = func() string { return "x" }
	test.Tap(h.d.barClip)
	if got := p.Expect(t, 0x28).Data(); string(got) != "x" {
		t.Fatalf("bar Type Clipboard sent %q", got)
	}

	h.d.barSwitch.Tap(viewKVM) // the KVM view in full screen
	if h.d.consoleActive() || h.d.viewSwitch.Selected() != viewKVM || !fullScreenShows(h.d, h.d.kvmStage) || h.d.consolePane.Visible() {
		t.Fatalf("KVM in full screen: console %v, toolbar switch %d", h.d.consoleActive(), h.d.viewSwitch.Selected())
	}
	if h.d.win.Canvas().Focused() == h.d.con.term {
		t.Fatal("the hidden terminal kept the focus")
	}
	h.d.barSwitch.Tap(viewConsole) // and back
	if !h.d.consoleActive() || h.d.viewSwitch.Selected() != viewConsole || !fullScreenShows(h.d, h.d.consolePane) ||
		h.d.win.Canvas().Focused() != h.d.con.term || h.dev.SOL.Calls() != 1 {
		t.Fatalf("console in full screen again: calls %d", h.dev.SOL.Calls())
	}

	test.Tap(h.d.exitFullBtn)
	if h.d.win.FullScreen() || h.d.fullScreen || h.d.win.Content() != h.d.content || !h.d.consoleActive() {
		t.Fatal("normal content not restored")
	}
	if !containsObject(h.d.stage, h.d.consolePane) || !containsObject(h.d.stage, h.d.kvmStage) || !h.d.consolePane.Visible() ||
		h.d.win.Canvas().Focused() != h.d.con.term {
		t.Fatal("stages not back in the window")
	}

	test.Tap(h.d.fullBtn)
	h.sync()
	p.EndReply(t) // the device ends the session: full screen ends with it
	h.waitEnded(t)
	if h.d.win.FullScreen() || h.d.win.Content() != h.d.content || !h.d.consoleActive() || !h.d.fullBtn.Disabled() {
		t.Fatalf("after the end: full screen %v, Full Screen enabled %v", h.d.win.FullScreen(), !h.d.fullBtn.Disabled())
	}
}

// TestKVMEndKeepsConsoleFullScreen: the end of the KVM session leaves full screen only while
// the KVM view is shown.
func TestKVMEndKeepsConsoleFullScreen(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.connect(t, true)
	h.open(t)
	test.Tap(h.d.fullBtn)
	h.d.disconnectKVM()
	recv(t, h.changed)
	if !fullScreenShows(h.d, h.d.consolePane) || h.d.kvmClient != nil {
		t.Fatal("console full screen left when KVM ended")
	}
}
