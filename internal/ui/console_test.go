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
	gate       *sync.Mutex // see sync
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
	h.d.con.run = func(term *terminal.Terminal, c *sol.Client) error {
		return term.RunWithConnection(c, readSignal{c, h.reads, h.gate})
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

func TestConsoleAsksToTurnOnSOL(t *testing.T) {
	h := newConsoleDevice(t, false)
	h.d.viewSwitch.Tap(viewConsole)
	recv(t, h.conChanged) // the question
	top := h.d.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("no question")
	}
	findLabel(t, top, "Turn on Serial over LAN?")
	findLabel(t, top, "Serial over LAN (SOL) is turned off on “Büro”. Turn it on to open the text console of this device? The setting stays on until you turn it off in Settings.")
	bs := panelButtons(top)
	if got := texts(bs); got != "Cancel|Turn On" {
		t.Fatalf("buttons %q", got)
	}
	test.Tap(bs[0]) // Cancel: nothing opens, the view stays on an empty Console
	recv(t, h.conChanged)
	if h.dev.SOL.Calls() != 0 || h.d.con.client != nil || h.d.con.busy || !h.d.consoleActive() ||
		len(h.d.consoleStage.Objects) != 0 || h.d.consoleLine.Visible() || len(h.errs) != 0 {
		t.Fatalf("cancel: calls %d, busy %v, stage %d", h.dev.SOL.Calls(), h.d.con.busy, len(h.d.consoleStage.Objects))
	}

	h.d.viewSwitch.Tap(viewConsole) // the selected segment asks again
	recv(t, h.conChanged)
	h.accept(t)
	test.Tap(panelButtons(h.d.win.Canvas().Overlays().Top())[1]) // Turn On
	recv(t, h.conChanged)
	if h.dev.SOL.Calls() != 1 || h.d.con.client == nil || h.d.consoleLine.Text != "Console connected" {
		t.Fatalf("turn on: calls %d, client %v", h.dev.SOL.Calls(), h.d.con.client != nil)
	}
	recv(t, h.reads)
	h.peer(t)
}

func TestConsoleConsentAndOpenErrors(t *testing.T) {
	h := newConsoleDevice(t, true)
	h.dev.Consent, h.dev.ConsentCode = true, "123456"
	h.d.viewSwitch.Tap(viewConsole)
	(<-h.asked)("", false) // cancelled consent: silent
	recv(t, h.conChanged)
	if h.d.con.client != nil || h.d.con.busy || len(h.errs) != 0 {
		t.Fatalf("cancelled consent: busy %v errs %d", h.d.con.busy, len(h.errs))
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
	h.d.con.run = func(*terminal.Terminal, *sol.Client) error { panic("parser") }
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
	h.d.con.run = func(term *terminal.Terminal, c *sol.Client) error {
		return term.RunWithConnection(c, readSignal{c, h.reads, h.gate})
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
