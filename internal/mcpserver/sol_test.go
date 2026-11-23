package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/redir"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/sol"
	"github.com/Srv-02/openlamt/internal/sol/soltest"
)

// solDevice is the fake SOL server of one device and the sessions it accepted.
type solDevice struct {
	t     *testing.T
	srv   *soltest.Server
	peers chan *soltest.Peer
}

// addSOL gives the fake device a fake SOL server.
func addSOL(t *testing.T, f *session.Fake) *solDevice {
	t.Helper()
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "sol-pw"})
	f.SOL.Addr, f.SOL.User, f.SOL.Password = srv.Addr, "admin", "sol-pw"
	return &solDevice{t: t, srv: srv, peers: make(chan *soltest.Peer, 4)}
}

// accept plays the device side of the next SOL session (as internal/ui's console tests do).
// Call it before the tool call that opens the session, and only when one is expected.
func (d *solDevice) accept() {
	go func() { d.peers <- d.srv.AcceptSession(d.t) }()
}

// peer returns the session accepted last. The cleanup drops the connection before the server
// closes, so Close need not wait for END_SOL_REPLY.
func (d *solDevice) peer() *soltest.Peer {
	d.t.Helper()
	select {
	case p := <-d.peers:
		d.t.Cleanup(p.Disconnect)
		return p
	case <-time.After(5 * time.Second):
		d.t.Fatal("no SOL session")
		return nil
	}
}

// answerEnd answers END_SOL like a device, so a Close of the client returns at once. It owns
// the peer from now on and never calls t.Fatal.
func answerEnd(p *soltest.Peer) {
	go func() {
		for {
			m, err := p.TryRead(10 * time.Second)
			if err != nil {
				return
			}
			if m.Cmd == 0x22 {
				_, _ = p.Conn().Write(p.Message(0x23, nil))
				return
			}
		}
	}()
}

// expectData returns the data of the next DATA_TO_HOST message.
func expectData(t *testing.T, p *soltest.Peer) string {
	t.Helper()
	return string(p.Expect(t, 0x28).Data())
}

func (h *harness) solEntry(name string) *solEntry {
	h.srv.s.sol.mu.Lock()
	defer h.srv.s.sol.mu.Unlock()
	return h.srv.s.sol.entries[name]
}

// solRead runs sol_read and returns its text content and structured output.
func (h *harness) solRead(args map[string]any) (string, map[string]any) {
	h.t.Helper()
	text, out, isErr := h.call("sol_read", args)
	if isErr {
		h.t.Fatalf("sol_read %v: %s", args, text)
	}
	return text, out
}

// TestSOLConnectReadWrite: one sol_connect, output read with the cursor, text and keys reach
// the host, a second sol_connect keeps the session.
func TestSOLConnectReadWrite(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.start()
	d.accept()
	out := h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	if out["already_open"] != false || out["next_offset"] != float64(0) || out["idle_timeout_seconds"] != float64(300) {
		t.Fatalf("%v", out)
	}
	if w, _ := out["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "not encrypted") {
		t.Fatalf("warnings %v", out["warnings"])
	}
	p.Data(t, []byte("login: "))
	text, r := h.solRead(dev("NUC", "wait_ms", 3000))
	if r["text"] != "login: " || r["offset"] != float64(0) || r["next_offset"] != float64(7) || r["more"] != false || r["ended"] != false {
		t.Fatalf("%v", r)
	}
	if !strings.HasPrefix(text, "[offset 0, next_offset 7, more false, ended false, skipped 0 bytes]\n") || !strings.HasSuffix(text, "login: ") {
		t.Fatalf("text %q", text)
	}
	start := time.Now()
	_, r = h.solRead(dev("NUC", "wait_ms", 50))
	if r["text"] != "" || r["offset"] != float64(7) || r["next_offset"] != float64(7) || r["more"] != false {
		t.Fatalf("%v", r)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("did not wait")
	}
	if out := h.mustOK("sol_type_text", dev("NUC", "text", "root")); out["sent_bytes"] != float64(4) || out["enter_presses"] != float64(0) {
		t.Fatalf("%v", out)
	}
	if got := expectData(t, p); got != "root" {
		t.Fatalf("host got %q", got)
	}
	if out := h.mustOK("sol_send_keys", dev("NUC", "keys", []string{"Enter"})); out["sent"] != float64(1) {
		t.Fatalf("%v", out)
	}
	if got := expectData(t, p); got != "\r" {
		t.Fatalf("host got %q", got)
	}
	out = h.mustOK("sol_connect", dev("NUC"))
	if out["already_open"] != true || out["next_offset"] != float64(7) {
		t.Fatalf("second connect %v", out)
	}
	if n := f.SOL.Calls(); n != 1 {
		t.Fatalf("SOL sessions %d", n)
	}
	if n := len(h.connectCalls()); n != 1 {
		t.Fatalf("WS-Man logins %d", n)
	}
}

// TestSOLToolsNeedConnect: without sol_connect nothing reaches the device.
func TestSOLToolsNeedConnect(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addSOL(t, f)
	h.start()
	h.mustFail("sol_read", dev("NUC"), "call sol_connect first")
	h.mustFail("sol_type_text", dev("NUC", "text", "hi"), "call sol_connect first")
	h.mustFail("sol_send_keys", dev("NUC", "keys", []string{"Enter"}), "call sol_connect first")
	h.mustFail("sol_connect", dev("Ghost"), "No favorite named")
	if out := h.mustOK("sol_disconnect", dev("NUC")); out["closed"] != false {
		t.Fatalf("%v", out)
	}
	if f.SOL.Calls() != 0 || len(h.connectCalls()) != 0 {
		t.Fatalf("contacted the device: %d %d", f.SOL.Calls(), len(h.connectCalls()))
	}
}

// TestSOLReadCursorAndLimits: offset, max_bytes, skipped output and the limits.
func TestSOLReadCursorAndLimits(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	data := make([]byte, 1000)
	for i := range data {
		data[i] = 'a' + byte(i%26)
	}
	p.Data(t, data)
	b := h.solEntry("NUC").out
	eventually(t, "the output", func() bool { return b.end() == 1000 })

	_, r := h.solRead(dev("NUC", "max_bytes", 256))
	if r["text"] != string(data[:256]) || r["offset"] != float64(0) || r["next_offset"] != float64(256) || r["more"] != true {
		t.Fatalf("%v", r)
	}
	_, r = h.solRead(dev("NUC", "max_bytes", 256)) // continues at the cursor
	if r["text"] != string(data[256:512]) || r["next_offset"] != float64(512) {
		t.Fatalf("%v", r)
	}
	_, r = h.solRead(dev("NUC", "offset", 100, "max_bytes", 256))
	if r["text"] != string(data[100:356]) || r["offset"] != float64(100) || r["next_offset"] != float64(356) {
		t.Fatalf("%v", r)
	}
	_, r = h.solRead(dev("NUC")) // the cursor is where the last read ended
	if r["text"] != string(data[356:]) || r["next_offset"] != float64(1000) || r["more"] != false {
		t.Fatalf("%v", r)
	}
	h.mustFail("sol_read", dev("NUC", "offset", 1001), "offset 1001 is beyond the end of the output (1000)")
	h.mustFail("sol_read", dev("NUC", "offset", -1), "offset")
	h.mustFail("sol_read", dev("NUC", "max_bytes", 255), "max_bytes must be between 256 and 65536")
	h.mustFail("sol_read", dev("NUC", "max_bytes", 65537), "max_bytes must be between 256 and 65536")
	h.mustFail("sol_read", dev("NUC", "wait_ms", 10001), "wait_ms must be between 0 and 10000")

	// Output beyond the buffer size drops the oldest bytes; a read from before them says so.
	b.mu.Lock()
	b.size = 1200
	b.mu.Unlock()
	p.Data(t, data[:500])
	eventually(t, "more output", func() bool { return b.end() == 1500 })
	text, r := h.solRead(dev("NUC", "offset", 0, "max_bytes", 256))
	if r["skipped_bytes"] != float64(300) || r["offset"] != float64(300) || r["next_offset"] != float64(556) || r["text"] != string(data[300:556]) {
		t.Fatalf("%v", r)
	}
	if !strings.HasPrefix(text, "[offset 300, next_offset 556, more true, ended false, skipped 300 bytes]\n") {
		t.Fatalf("text %q", text)
	}
}

// TestSOLReadStripsEscapes: terminal sequences and control characters are dropped by default
// and kept with raw.
func TestSOLReadStripsEscapes(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	raw := "\x1b[2J\x1b[1;32mgreen\x1b[0m \x1b]0;title\x07ok\r\nnext\a"
	p.Data(t, []byte(raw))
	b := h.solEntry("NUC").out
	eventually(t, "the output", func() bool { return b.end() == int64(len(raw)) })
	_, r := h.solRead(dev("NUC"))
	if r["text"] != "green ok\nnext" || r["next_offset"] != float64(len(raw)) {
		t.Fatalf("%q", r["text"])
	}
	_, r = h.solRead(dev("NUC", "offset", 0, "raw", true))
	if r["text"] != raw {
		t.Fatalf("raw %q", r["text"])
	}
}

// TestSOLReadHoldsBackSplitSequence: an escape sequence cut by the end of the buffered output
// is read with the next call, once the rest has arrived.
func TestSOLReadHoldsBackSplitSequence(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	p.Data(t, []byte("ab\x1b[1;3"))
	b := h.solEntry("NUC").out
	eventually(t, "the output", func() bool { return b.end() == 7 })
	_, r := h.solRead(dev("NUC"))
	if r["text"] != "ab" || r["next_offset"] != float64(2) {
		t.Fatalf("%v", r)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = p.Conn().Write(p.DataMsg([]byte("2mcd")))
	}()
	_, r = h.solRead(dev("NUC", "wait_ms", 3000)) // nothing usable yet: waits for more output
	if r["text"] != "cd" || r["offset"] != float64(2) || r["next_offset"] != float64(11) {
		t.Fatalf("%v", r)
	}
}

// TestSOLTypeTextLineBreaksAndConfirmation: line breaks become CR, trailing ones are dropped,
// the prompt shows the text with ⏎, only printable ASCII is sent, secret hides a single line
// only, and the action log never holds the text.
func TestSOLTypeTextLineBreaksAndConfirmation(t *testing.T) {
	var asked []string
	answer := &mcp.ElicitResult{Action: "decline"}
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return answer, nil
	}
	h.start()
	h.mustFail("sol_type_text", dev("NUC", "text", "ls"), "call sol_connect first")
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	if len(asked) != 0 {
		t.Fatalf("asked %q", asked)
	}
	h.mustFail("sol_type_text", dev("NUC", "text", "ls\nexit\n"), "not confirmed")
	if len(asked) != 1 || !strings.Contains(asked[0], "write 7 characters with 1 Enter press to the serial console: «ls⏎exit»") ||
		!strings.Contains(asked[0], `device "NUC"`) {
		t.Fatalf("asked %q", asked)
	}
	for _, text := range []string{"a\x1bb", "a\x03", "grüß", "a\u202eb", "\x7f"} {
		h.mustFail("sol_type_text", dev("NUC", "text", text), "printable ASCII characters, line breaks and tabs only")
	}
	h.mustFail("sol_type_text", dev("NUC", "text", "\n\r\n"), "text is empty.")
	h.mustFail("sol_type_text", dev("NUC", "text", ""), "text is empty.")
	h.mustFail("sol_type_text", dev("NUC", "text", strings.Repeat("x", maxSOLText+1)), "at most 4000")
	for _, text := range []string{"pass\n", "pass\r", "a\tb"} {
		h.mustFail("sol_type_text", dev("NUC", "text", text, "secret", true), "secret is only allowed for a single line")
	}
	if len(asked) != 1 {
		t.Fatalf("asked for refused calls: %q", asked)
	}
	h.mustFail("sol_type_text", dev("NUC", "text", "Hunter2", "secret", true), "not confirmed")
	if len(asked) != 2 || strings.Contains(asked[1], "Hunter2") || !strings.Contains(asked[1], "write 7 characters to the serial console. The AI agent chose to hide this text") {
		t.Fatalf("asked %q", asked)
	}
	h.mustFail("sol_type_text", dev("NUC", "text", "a\tb\r\nc"), "not confirmed")
	if !strings.Contains(asked[2], "«a⇥b⏎c»") {
		t.Fatalf("asked %q", asked[2])
	}

	answer = &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}
	out := h.mustOK("sol_type_text", dev("NUC", "text", "ls\r\nexit\r\n\r\n"))
	if out["sent_bytes"] != float64(7) || out["enter_presses"] != float64(1) {
		t.Fatalf("%v", out)
	}
	if got := expectData(t, p); got != "ls\rexit" {
		t.Fatalf("host got %q", got)
	}
	h.mustOK("sol_type_text", dev("NUC", "text", "Hunter2", "secret", true))
	if got := expectData(t, p); got != "Hunter2" {
		t.Fatalf("host got %q", got)
	}

	var actions []string
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "exit") || strings.Contains(string(b), "Hunter2") {
			t.Fatalf("action log holds the text: %s", b)
		}
		if e["tool"] == "sol_type_text" && e["outcome"] == "ok" {
			actions = append(actions, e["action"].(string))
		}
	}
	if !reflect.DeepEqual(actions, []string{"7 characters, 1 Enter", "7 characters, 0 Enter, secret"}) {
		t.Fatalf("logged %q", actions)
	}
}

// TestSOLSendKeys: names in any case become the xterm bytes, all in one write; the prompt and
// the log show the canonical names; unknown keys and too many keys send nothing.
func TestSOLSendKeys(t *testing.T) {
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	for _, k := range []string{"Ctrl+Alt+Delete", "Ctrl+[", "Ctrl+1", "Shift+a", "Enterß", "ſtrl+c", "F13", "Alt+", "", "Ctrl+Alt+x"} {
		h.mustFail("sol_send_keys", dev("NUC", "keys", []string{"Enter", k}), "unknown key")
	}
	h.mustFail("sol_send_keys", dev("NUC", "keys", []string{}), "1 to 50")
	h.mustFail("sol_send_keys", dev("NUC", "keys", slicesRepeat("Enter", 51)), "1 to 50")
	if len(asked) != 0 {
		t.Fatalf("asked %q", asked)
	}
	out := h.mustOK("sol_send_keys", dev("NUC", "keys", []string{"ctrl + c", "ARROWUP", "f10", "alt+x", "shift+TAB", "q", "Esc", "Ctrl+z", "PgDn", "+"}))
	if out["sent"] != float64(10) {
		t.Fatalf("%v", out)
	}
	want := "\x03\x1b[A\x1b[21~\x1bx\x1b[Zq\x1b\x1a\x1b[6~+"
	if got := expectData(t, p); got != want {
		t.Fatalf("host got %q, want %q", got, want)
	}
	list := "Ctrl+C, Up, F10, Alt+x, Shift+Tab, q, Escape, Ctrl+Z, PageDown, +"
	if len(asked) != 1 || !strings.Contains(asked[0], "press "+list+" on the serial console on device \"NUC\"") {
		t.Fatalf("asked %q", asked)
	}
	var logged string
	for _, e := range h.logEntries() {
		if e["tool"] == "sol_send_keys" && e["outcome"] == "ok" {
			logged, _ = e["action"].(string)
		}
	}
	if logged != list {
		t.Fatalf("logged %q", logged)
	}
}

func slicesRepeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// TestSOLKeyTable: every documented key name and its bytes.
func TestSOLKeyTable(t *testing.T) {
	for name, want := range map[string]string{
		"Enter": "\r", "Return": "\r", "Tab": "\t", "Shift+Tab": "\x1b[Z", "Backspace": "\x7f", "Bksp": "\x7f",
		"Escape": "\x1b", "Esc": "\x1b", "Space": " ", "Up": "\x1b[A", "ArrowUp": "\x1b[A", "Down": "\x1b[B",
		"ArrowDown": "\x1b[B", "Right": "\x1b[C", "ArrowRight": "\x1b[C", "Left": "\x1b[D", "ArrowLeft": "\x1b[D",
		"Home": "\x1b[H", "End": "\x1b[F", "Insert": "\x1b[2~", "Ins": "\x1b[2~", "Delete": "\x1b[3~", "Del": "\x1b[3~",
		"PageUp": "\x1b[5~", "PgUp": "\x1b[5~", "PageDown": "\x1b[6~", "PgDn": "\x1b[6~",
		"F1": "\x1bOP", "F2": "\x1bOQ", "F3": "\x1bOR", "F4": "\x1bOS", "F5": "\x1b[15~", "F6": "\x1b[17~",
		"F7": "\x1b[18~", "F8": "\x1b[19~", "F9": "\x1b[20~", "F10": "\x1b[21~", "F11": "\x1b[23~", "F12": "\x1b[24~",
		"Ctrl+a": "\x01", "Ctrl+Z": "\x1a", "Alt+Enter": "\x1b\r", "Alt+F1": "\x1b\x1bOP", "Alt+1": "\x1b1",
		"~": "~", "Z": "Z", " Q ": "Q",
	} {
		b, _, ok := solKey(name)
		if !ok || string(b) != want {
			t.Errorf("%q: %q %v, want %q", name, b, ok, want)
		}
	}
}

// TestSOLConsentFlow: CCM consent with consent_code, like kvm_connect.
func TestSOLConsentFlow(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Consent, f.ConsentCode = true, "123456"
	f.St.ControlMode = amt.ControlModeClient
	d := addSOL(t, f)
	h.start()
	h.mustFail("sol_connect", dev("NUC"), "call sol_connect again with consent_code")
	h.mustFail("sol_connect", dev("NUC", "consent_code", "654321"), "did not accept the consent code")
	h.mustFail("sol_connect", dev("NUC", "consent_code", "12a456"), "six digits")
	h.mustFail("sol_read", dev("NUC"), "call sol_connect first")
	d.accept()
	h.mustOK("sol_connect", dev("NUC", "consent_code", "123456"))
	d.peer()
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "123456") || strings.Contains(string(b), "654321") {
			t.Fatalf("action log holds a consent code: %s", b)
		}
	}
}

// TestSOLRefusedWhenAnotherClientHoldsSession: the SOL open errors name SOL, the app's Console
// view and sol_connect, never KVM.
func TestSOLRefusedWhenAnotherClientHoldsSession(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addSOL(t, f)
	h.start()
	f.SOL.Err = &redir.RedirectionError{Stage: "start", Status: 1}
	text := h.mustFail("sol_connect", dev("NUC"), "Another SOL session to this device is open")
	if !strings.Contains(text, "Console view") || !strings.Contains(text, "MeshCommander") || !strings.Contains(text, "call sol_connect again") ||
		strings.Contains(text, "KVM") {
		t.Fatalf("%q", text)
	}
	h.mustFail("sol_read", dev("NUC"), "call sol_connect first")
	f.SOL.Err = &redir.RedirectionError{Stage: "auth", Status: 1}
	h.mustFail("sol_connect", dev("NUC"), "SOL login failed")
	f.SOL.Err = fmt.Errorf("%w: status 1", sol.ErrStartFailed)
	h.mustFail("sol_connect", dev("NUC"), "did not start the serial console")
	f.SOL.Err = &amt.SOLError{Step: "RedirectionService.RequestStateChange", ReturnValue: 2}
	h.mustFail("sol_connect", dev("NUC"), "refused to enable Serial over LAN (AMT ReturnValue 2)")
	if h.solEntry("NUC") != nil {
		t.Fatal("a failed connect left an entry")
	}
}

// TestSOLSessionEndKeepsOutput: output of a session the device ended stays readable; input is
// refused; sol_connect opens a new session and the offsets continue.
func TestSOLSessionEndKeepsOutput(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.cfg.RequireClientConfirmation = true
	asked := 0
	h.elicit = func(*mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked++
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	c := h.solEntry("NUC").client
	p.Data(t, []byte("bye\r\n"))
	p.EndReply(t)
	<-c.Done()
	b := h.solEntry("NUC").out
	eventually(t, "the end of the session", func() bool { return b.read(0, 256, false).ended })
	text, r := h.solRead(dev("NUC"))
	if r["text"] != "bye\n" || r["ended"] != true || r["end_reason"] != "The device closed the serial console session." || r["next_offset"] != float64(5) {
		t.Fatalf("%v", r)
	}
	if !strings.Contains(text, "The serial console session ended: The device closed the serial console session. sol_connect opens it again.") {
		t.Fatalf("text %q", text)
	}
	h.mustFail("sol_type_text", dev("NUC", "text", "ls"), "has ended")
	h.mustFail("sol_send_keys", dev("NUC", "keys", []string{"Enter"}), "Call sol_connect to open it again")
	if asked != 0 {
		t.Fatalf("asked %d times for calls that cannot run", asked)
	}

	d.accept()
	out := h.mustOK("sol_connect", dev("NUC"))
	p = d.peer()
	if out["already_open"] != false || out["next_offset"] != float64(5) {
		t.Fatalf("%v", out)
	}
	p.Data(t, []byte("again"))
	_, r = h.solRead(dev("NUC", "wait_ms", 3000))
	if r["text"] != "again" || r["offset"] != float64(5) || r["next_offset"] != float64(10) || r["ended"] != false || r["end_reason"] != nil {
		t.Fatalf("%v", r)
	}
	h.mustOK("sol_type_text", dev("NUC", "text", "ls"))
	if got := expectData(t, p); got != "ls" {
		t.Fatalf("host got %q", got)
	}
	if f.SOL.Calls() != 2 {
		t.Fatalf("SOL opens %d", f.SOL.Calls())
	}
}

// TestSOLIdleTimeoutAndClose: the idle timeout, sol_disconnect and Server.Close end sessions.
func TestSOLIdleTimeoutAndClose(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.cfg.SOLIdleTimeout = time.Hour // the real timer never fires; the clock is moved instead
	h.start()
	clock, skew := skewClock()
	h.srv.s.sol.now = clock
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	answerEnd(d.peer())
	e := h.solEntry("NUC")
	c := e.client
	skew.Store(int64(2 * time.Hour))
	h.srv.s.sol.expire(e)
	if h.solEntry("NUC") != nil {
		t.Fatal("idle session still in the manager")
	}
	select {
	case <-c.Done(): // expire closed the client before it returned
	default:
		t.Fatal("client still running")
	}
	h.mustFail("sol_read", dev("NUC"), "call sol_connect first")

	h = newHarness(t)
	f = h.addDevice("NUC", "192.0.2.10", false)
	g := h.addDevice("Lab", "lab.example.com", false)
	d, dg := addSOL(t, f), addSOL(t, g)
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	answerEnd(d.peer())
	c = h.solEntry("NUC").client
	if out := h.mustOK("sol_disconnect", dev("NUC")); out["closed"] != true {
		t.Fatalf("%v", out)
	}
	<-c.Done()
	if out := h.mustOK("sol_disconnect", dev("NUC")); out["closed"] != false {
		t.Fatalf("second disconnect %v", out)
	}
	h.mustFail("sol_read", dev("NUC"), "call sol_connect first")

	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	answerEnd(d.peer())
	dg.accept()
	h.mustOK("sol_connect", dev("Lab"))
	answerEnd(dg.peer())
	c1, c2 := h.solEntry("NUC").client, h.solEntry("Lab").client
	h.srv.Close()
	for _, c := range []*sol.Client{c1, c2} {
		select {
		case <-c.Done():
		default:
			t.Fatal("session still open after Close")
		}
	}
	h.mustFail("sol_connect", dev("NUC"), "shutting down")
}

// TestReadOnlySOLConnectNeverEnablesSOL: read_only never switches SOL on and offers no SOL input.
func TestReadOnlySOLConnectNeverEnablesSOL(t *testing.T) {
	h := newHarness(t)
	h.cfg.ReadOnly = true
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Redirection().SOLOff = true
	addSOL(t, f)
	h.start()
	for _, name := range h.toolNames() {
		if strings.HasPrefix(name, "sol_") && name != "sol_connect" && name != "sol_read" && name != "sol_disconnect" {
			t.Fatalf("read_only offers %s", name)
		}
	}
	text := h.mustFail("sol_connect", dev("NUC"), "read_only in the openlamt-mcp config forbids turning it on")
	if !strings.Contains(text, "Serial over LAN (SOL)") {
		t.Fatalf("%q", text)
	}
	if got := f.RedirectionEnabled(); len(got) != 0 {
		t.Fatalf("enabled %v", got)
	}
	for _, e := range h.logEntries() {
		if e["tool"] == "sol_connect" && e["outcome"] != "refused" {
			t.Fatalf("log entry %v", e)
		}
	}

	// read_only, SOL on: connect and read work.
	h = newHarness(t)
	h.cfg.ReadOnly = true
	f = h.addDevice("NUC", "192.0.2.10", false)
	d := addSOL(t, f)
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	d.peer().Data(t, []byte("ok"))
	if _, r := h.solRead(dev("NUC", "wait_ms", 3000)); r["text"] != "ok" {
		t.Fatalf("%v", r)
	}

	// No read_only, SOL off: sol_connect enables SOL like the app.
	h = newHarness(t)
	f = h.addDevice("NUC", "192.0.2.10", false)
	f.Redirection().SOLOff = true
	d = addSOL(t, f)
	h.start()
	d.accept()
	h.mustOK("sol_connect", dev("NUC"))
	d.peer()
	if got := f.RedirectionEnabled(); !reflect.DeepEqual(got, []string{"SOL"}) {
		t.Fatalf("enabled %v", got)
	}
}

// TestConsoleText: escape sequences and control characters are dropped, line endings become
// \n, and a token cut by the end of the buffer is held back while the session runs.
func TestConsoleText(t *testing.T) {
	long := "\x1b[" + strings.Repeat("1;", 200) + "xy" // no final byte within 256 bytes
	for _, c := range []struct {
		name  string
		in    string
		limit int
		final bool
		text  string
		used  int
		cut   bool
	}{
		{"plain", "hello", 100, false, "hello", 5, false},
		{"colors", "\x1b[1;31mred\x1b[0m", 100, false, "red", 14, false},
		{"cursor moves", "a\x1b[2;5Hb\x1b[Kc\x1b[?25l", 100, false, "abc", 18, false},
		{"osc title bel", "\x1b]0;my title\x07x", 100, false, "x", 14, false},
		{"osc title st", "\x1b]2;t\x1b\\x", 100, false, "x", 8, false},
		{"dcs", "\x1bPq#0\x1b\\x", 100, false, "x", 8, false},
		{"two byte escapes", "\x1b7a\x1b(Bb\x1b=c", 100, false, "abc", 10, false},
		{"crlf", "a\r\nb", 100, false, "a\nb", 4, false},
		{"lone cr", "a\rb", 100, false, "a\nb", 3, false},
		{"tab and lf", "a\tb\n", 100, false, "a\tb\n", 4, false},
		{"bel and backspace", "a\x07b\x08c\x7f", 100, false, "abc", 6, false},
		{"c1 rune", "a\u0085b\u009bc", 100, false, "abc", 7, false},
		{"utf8", "grüß", 100, false, "grüß", 6, false},
		{"invalid utf8", "a\xffb\xc3(", 100, false, "a�b�(", 5, false},
		{"split escape running", "ab\x1b[1;3", 100, false, "ab", 2, false},
		{"split escape final", "ab\x1b[1;3", 100, true, "ab", 7, false},
		{"lone esc running", "ab\x1b", 100, false, "ab", 2, false},
		{"split osc running", "ab\x1b]0;ti", 100, false, "ab", 2, false},
		{"split st running", "ab\x1b]0;ti\x1b", 100, false, "ab", 2, false},
		{"split utf8 running", "ab\xc3", 100, false, "ab", 2, false},
		{"split utf8 final", "ab\xc3", 100, true, "ab�", 3, false},
		{"trailing cr running", "ab\r", 100, false, "ab", 2, false},
		{"trailing cr final", "ab\r", 100, true, "ab\n", 3, false},
		{"broken long sequence", long, 1000, false, "[" + strings.Repeat("1;", 200) + "xy", len(long), false},
		{"limit", "abcdef", 3, false, "abc", 3, true},
		{"limit completes a sequence", "ab\x1b[31mc", 3, false, "ab", 7, true},
		{"limit completes crlf", "ab\r\nc", 3, false, "ab\n", 4, true},
		{"limit completes a rune", "abü", 3, false, "abü", 4, false},
		{"limit at the end", "abc", 3, false, "abc", 3, false},
	} {
		text, used, cut := consoleText([]byte(c.in), c.limit, c.final)
		if text != c.text || used != c.used || cut != c.cut {
			t.Errorf("%s: got %q %d %v, want %q %d %v", c.name, text, used, cut, c.text, c.used, c.cut)
		}
	}
}

// TestRawText: raw keeps everything but replaces invalid UTF-8 and holds back a split rune.
func TestRawText(t *testing.T) {
	for _, c := range []struct {
		in    string
		limit int
		final bool
		text  string
		used  int
		cut   bool
	}{
		{"\x1b[31mred\r\n", 100, false, "\x1b[31mred\r\n", 10, false},
		{"a\xffb", 100, false, "a�b", 3, false},
		{"ab\xc3", 100, false, "ab", 2, false},
		{"ab\xc3", 100, true, "ab�", 3, false},
		{"abü", 3, false, "abü", 4, false},
		{"abcd", 2, false, "ab", 2, true},
	} {
		text, used, cut := rawText([]byte(c.in), c.limit, c.final)
		if text != c.text || used != c.used || cut != c.cut {
			t.Errorf("%q: got %q %d %v, want %q %d %v", c.in, text, used, cut, c.text, c.used, c.cut)
		}
	}
}

// TestSOLBuffer: the buffer keeps the newest output, offsets go on, and wait returns on output,
// on the end of the session and on the timeout.
func TestSOLBuffer(t *testing.T) {
	b := newSOLBuffer(8)
	b.append([]byte("0123456789"))
	if b.end() != 10 {
		t.Fatalf("end %d", b.end())
	}
	r := b.read(0, 256, false)
	if r.skipped != 2 || r.offset != 2 || r.text != "23456789" || r.next != 10 {
		t.Fatalf("%+v", r)
	}
	start := time.Now()
	b.wait(t.Context(), 10, 30*time.Millisecond)
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("wait returned early")
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		b.append([]byte("x"))
	}()
	b.wait(t.Context(), 10, 5*time.Second)
	if b.end() != 11 {
		t.Fatalf("end %d", b.end())
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		b.finish(sol.ErrClosedByDevice)
	}()
	start = time.Now()
	b.wait(t.Context(), 11, 5*time.Second)
	if time.Since(start) > 3*time.Second {
		t.Fatal("wait missed the end")
	}
	if r := b.read(11, 256, false); !r.ended || r.endErr != sol.ErrClosedByDevice {
		t.Fatalf("%+v", r)
	}
}

// TestVisible: control and format characters are shown, never acted on.
func TestVisible(t *testing.T) {
	for in, want := range map[string]string{
		"ls\nexit":         "ls⏎exit",
		"a\tb\rc":          "a⇥b␍c",
		"\x00\x1b\x1f\x7f": "␀␛␟␡",
		"a\u0085b":         "a<U+0085>b",
		"evil\u202etxt":    "evil<U+202E>txt",
		"zero\u200bwidth":  "zero<U+200B>width",
		"grüß /tmp/x.iso":  "grüß /tmp/x.iso",
	} {
		if got := visible(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
