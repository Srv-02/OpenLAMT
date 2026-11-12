package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"github.com/fyne-io/terminal"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/sol"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// The Console view (Console.dc.html, issue #1): the serial console of the host over Serial over
// LAN in a terminal widget (github.com/fyne-io/terminal). The first switch to the view connects;
// after the session has ended, selecting Console again connects anew. If SOL is disabled on
// the device, the window asks first (Dialogs.dc.html, "Enable Serial over LAN?"); OpenSOL enables
// it. Consent works as for KVM. The session closes with the window; there is no Disconnect.
// Ctrl+Alt+Del of the toolbar goes over KVM, connecting it in the background if needed; Type
// Clipboard writes the clipboard to the console.

// Padding of the terminal on the console stage (Console.dc.html: padding 16px 20px).
const (
	consolePadV float32 = 16
	consolePadH float32 = 20
)

// serialConsole is the state of the Console view, only touched on the Fyne thread.
type serialConsole struct {
	busy       bool               // settings read, question or connect in progress
	cancel     context.CancelFunc // pending OpenSOL (consent prompt) or nil
	client     *sol.Client        // running session or nil
	out        *consoleWriter     // all writes to client: the terminal's keys and Type Clipboard
	term       *terminal.Terminal // terminal of the last session; nil before the first one
	cadPending bool               // Ctrl+Alt+Del waits for the KVM connect it started

	// run drives the terminal with the session until it ends, writing to out; tests replace it.
	run       func(term *terminal.Terminal, c *sol.Client, out io.WriteCloser) error
	onChanged func() // test hook: question shown or closed, session opened or ended; nil in production
}

// runTerminal is serialConsole.run in production: *sol.Client is the terminal's output (Read
// ends with io.EOF, the cause is in Err), out its input.
func runTerminal(term *terminal.Terminal, c *sol.Client, out io.WriteCloser) error {
	return term.RunWithConnection(out, c)
}

// consoleWriter is the only writer of a console session: sol.Client.Write is not meant for
// concurrent use, and a text longer than one SOL message (1000 bytes) could otherwise be split
// by a key the terminal sends meanwhile. Write queues a copy and returns at once, so the Fyne
// thread (the terminal writes keys from it) never waits for a slow link; one goroutine sends the
// queue in order, every Write as one block. It stops when the session has ended; a write error
// ends the session, which consoleEnded reports.
type consoleWriter struct {
	c     *sol.Client
	mu    sync.Mutex
	queue [][]byte
	wake  chan struct{}
}

func newConsoleWriter(c *sol.Client) *consoleWriter {
	w := &consoleWriter{c: c, wake: make(chan struct{}, 1)}
	go w.loop()
	return w
}

func (w *consoleWriter) Write(p []byte) (int, error) {
	select {
	case <-w.c.Done():
		return 0, w.c.Err()
	default:
	}
	w.mu.Lock()
	w.queue = append(w.queue, append([]byte(nil), p...))
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return len(p), nil
}

// Close closes the session (the terminal closes its input when it stops).
func (w *consoleWriter) Close() error { return w.c.Close() }

func (w *consoleWriter) loop() {
	for {
		select {
		case <-w.c.Done():
			return
		case <-w.wake:
		}
		for {
			w.mu.Lock()
			q := w.queue
			w.queue = nil
			w.mu.Unlock()
			if len(q) == 0 {
				break
			}
			for _, b := range q {
				if _, err := w.c.Write(b); err != nil {
					return
				}
			}
		}
	}
}

// buildConsole wires the console hooks of the view switch and the toolbar. Called once from
// NewDeviceWindow after buildViewControls.
func (d *DeviceWindow) buildConsole() {
	d.con.run = runTerminal
	d.onConsoleSelected = d.consoleSelected
	d.onConsoleReselected = d.consoleSelected
	d.consoleCtrlAltDel = d.consoleCAD
	d.consoleTypeClipboard = d.typeConsoleClipboard
}

func (d *DeviceWindow) consoleChanged() {
	if d.con.onChanged != nil {
		d.con.onChanged()
	}
}

// consoleSelected runs when the user switches to the Console view or taps its selected
// segment: the terminal gets the keyboard, and without a session the console connects.
func (d *DeviceWindow) consoleSelected() {
	d.focusConsole()
	d.connectConsole()
}

// focusConsole gives the keyboard to the terminal while the Console view is shown.
func (d *DeviceWindow) focusConsole() {
	if d.con.term != nil && d.consoleView && !d.closed {
		d.win.Canvas().Focus(d.con.term)
	}
}

// blurConsole takes the keyboard from the hidden terminal when the KVM view returns.
func (d *DeviceWindow) blurConsole() {
	if t := d.con.term; t != nil && d.win.Canvas().Focused() == t {
		d.win.Canvas().Unfocus()
	}
}

// connectConsole reads the settings on a goroutine and continues in consoleSettingsRead;
// nothing happens while a session runs or a connect is pending.
func (d *DeviceWindow) connectConsole() {
	if d.aborted || d.closed || d.con.busy || d.con.client != nil {
		return
	}
	d.con.busy = true
	go func() {
		st, err := d.sess.Settings()
		fyne.Do(func() { d.consoleSettingsRead(st, err) })
	}()
}

// consoleSettingsRead asks to enable SOL if it is disabled and opens the session otherwise.
func (d *DeviceWindow) consoleSettingsRead(st amt.Settings, err error) {
	switch {
	case d.closed || d.aborted:
		d.con.busy = false
	case err != nil:
		d.con.busy = false
		d.consoleFailed(err)
		d.leaveEmptyConsole()
	case !st.Features.SOL:
		d.releaseKeys()
		p := showQuestion(d.win, icons.Console, toneInfo, lang.L("Enable Serial over LAN?"),
			lang.L("Serial over LAN (SOL) is disabled on “{{.Name}}”. Enable it to open the text console of this device? The setting stays enabled until you disable it in Settings.",
				map[string]any{"Name": d.name}),
			lang.L("Cancel"), lang.L("Enable"), d.openConsole)
		p.spec.onCancel = func() {
			d.con.busy = false
			d.leaveEmptyConsole()
			d.consoleChanged()
		}
		d.consoleChanged()
	default:
		d.openConsole()
	}
}

// openConsole opens the session on a goroutine (amt.OpenSOL enables SOL); the consent dialog
// blocks that goroutine as for KVM.
func (d *DeviceWindow) openConsole() {
	if d.closed || d.aborted {
		d.con.busy = false
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.con.cancel = cancel
	prompt := &consentPrompt{ctx: ctx, ask: d.askConsent}
	go func() {
		c, err := d.sess.OpenSOL(ctx, prompt)
		fyne.Do(func() { d.consoleOpened(c, err) })
	}()
}

// consoleOpened runs on the Fyne thread with the result of OpenSOL.
func (d *DeviceWindow) consoleOpened(c *sol.Client, err error) {
	d.con.busy = false
	if d.con.cancel != nil {
		d.con.cancel() // the context only bounded dial and consent
		d.con.cancel = nil
	}
	switch {
	case err == nil && (d.closed || d.aborted):
		go c.Close()
	case err == nil:
		d.startConsole(c)
	case d.closed || d.aborted:
	case errors.Is(err, errConsentCancelled), errors.Is(err, context.Canceled):
		d.leaveEmptyConsole()
	default:
		d.consoleFailed(err)
		d.leaveEmptyConsole()
	}
	d.consoleChanged()
}

// leaveEmptyConsole returns to the KVM view (in the window or in full screen) when a connect
// was cancelled or failed before any session of this window had filled the console, so no
// empty console stays behind. After a session the view keeps its terminal.
func (d *DeviceWindow) leaveEmptyConsole() {
	if d.con.term == nil && d.consoleView && !d.closed && !d.aborted {
		d.setConsoleView(false)
	}
}

// consoleFailed shows an error of the settings read or OpenSOL; a certificate mismatch ends the
// whole session.
func (d *DeviceWindow) consoleFailed(err error) {
	if isMismatch(err) {
		d.abort(err)
		return
	}
	if !d.closed {
		d.onError(err)
	}
}

// startConsole shows a new terminal for c and runs it on a goroutine. A panic in the terminal
// (its parser has panicked on device output before) closes the session and is reported like a
// session error; the app keeps running.
func (d *DeviceWindow) startConsole(c *sol.Client) {
	term := terminal.New()
	out := newConsoleWriter(c)
	d.con.term, d.con.client, d.con.out = term, c, out
	pad := layout.NewCustomPaddedLayout(consolePadV, consolePadV, consolePadH, consolePadH)
	d.consoleStage.Objects = []fyne.CanvasObject{
		container.New(pad, container.NewThemeOverride(term, style.ConsoleTheme{Theme: overrideTheme})),
	}
	d.consoleStage.Refresh()
	d.setConsoleStatus(lang.L("Console connected"))
	d.setKVMControls() // Full Screen of the Console view
	d.focusConsole()
	run := d.con.run
	go func() {
		err := guardTerminal(run, term, c, out)
		c.Close() // idempotent; the terminal closes it on a normal end, a panic does not
		if err == nil {
			err = c.Err()
		}
		fyne.Do(func() { d.consoleEnded(c, err) })
	}()
}

// guardTerminal runs the terminal and turns a panic into an error.
func guardTerminal(run func(*terminal.Terminal, *sol.Client, io.WriteCloser) error, term *terminal.Terminal, c *sol.Client, out io.WriteCloser) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("serial console stopped: %v", r)
			fyne.LogError("serial console panic\n"+string(debug.Stack()), err)
		}
	}()
	_ = run(term, c, out) // its only error is that of Close, always nil for *sol.Client
	return nil
}

// consoleEnded runs on the Fyne thread once the terminal of c has stopped. The device ending the
// session cleanly and Close (window closed, abort) are silent; any other cause is shown, including
// a connection that dropped or was aborted after the TCP user timeout.
func (d *DeviceWindow) consoleEnded(c *sol.Client, err error) {
	if d.con.client != c {
		return
	}
	d.con.client, d.con.out = nil, nil
	if !d.closed {
		d.setConsoleStatus("")
		if d.consoleView {
			d.setFullScreen(false)
		}
		d.setKVMControls()
		if !d.aborted && err != nil && !errors.Is(err, sol.ErrClosed) {
			d.onError(err)
		}
	}
	d.consoleChanged()
}

// closeConsole cancels a pending connect and closes a running session (window closed, abort);
// consoleEnded follows for a running one.
func (d *DeviceWindow) closeConsole() {
	d.con.cadPending = false
	if d.con.cancel != nil {
		d.con.cancel()
	}
	if c := d.con.client; c != nil {
		go c.Close() // blocks up to about 2 s for END_SOL
	}
}

// consoleLineEnds turns CR LF and LF into CR, the Return key of a serial terminal.
func consoleLineEnds(s string) string {
	return strings.NewReplacer("\r\n", "\r", "\n", "\r").Replace(s)
}

// typeConsoleClipboard is Type Clipboard in the Console view: the clipboard goes to the console
// as it is, with CR line ends, as one block behind the keys typed before (consoleWriter).
// Without a session nothing happens; a write error ends the session, which consoleEnded reports.
func (d *DeviceWindow) typeConsoleClipboard() {
	out := d.con.out
	if out == nil || d.closed {
		return
	}
	read := d.readClipboard
	if read == nil {
		read = defaultReadClipboard
	}
	text := consoleLineEnds(read())
	d.focusConsole() // the button tap took the focus
	if text == "" {
		return
	}
	_, _ = out.Write([]byte(text))
}

// consoleCAD is Ctrl+Alt+Del in the Console view: it goes over KVM. Without a KVM session it
// connects KVM in the background like the KVM segment (the view stays on Console) and sends
// Ctrl+Alt+Del once that session stands; a failed connect shows its error as usual.
func (d *DeviceWindow) consoleCAD() {
	if d.aborted || d.closed {
		return
	}
	if d.kvmClient != nil {
		d.ctrlAltDel()
		d.focusConsole()
		return
	}
	d.con.cadPending = true
	if d.kvmBusy || d.kvmReconnecting {
		return // the pending connect sends it
	}
	d.autoWait = false // as a click on the KVM segment (toggleKVM)
	d.forgetResume()
	d.connectKVM(false)
}

// sendPendingCAD sends the Ctrl+Alt+Del that consoleCAD held back, once a KVM session stands.
func (d *DeviceWindow) sendPendingCAD() {
	if !d.con.cadPending {
		return
	}
	d.con.cadPending = false
	d.ctrlAltDel()
	d.focusConsole()
}
