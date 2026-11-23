package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/sol"
)

// Limits of the SOL tools.
const (
	defaultSOLRead = 8192
	minSOLRead     = 256
	maxSOLRead     = 65536
	maxSOLText     = 4000 // characters per sol_type_text call
	maxSOLKeys     = 50
)

// openSOL opens a SOL session to device: login check, consent with code. The WS-Man session is
// closed again; the SOL client has its own socket.
func (s *server) openSOL(ctx context.Context, device string, code consentCode) (*sol.Client, []string, error) {
	sess, _, _, warnings, err := s.openDevice(ctx, device)
	if err != nil {
		return nil, nil, err
	}
	defer sess.Close()
	c, err := sess.OpenSOL(ctx, code)
	if err != nil {
		return nil, nil, &redirOpenError{kind: "sol", err: err}
	}
	return c, warnings, nil
}

func (s *server) registerSOL(srv *mcp.Server) {
	view, input := s.cfg.Enabled(GroupRead), s.cfg.Enabled(GroupKVMInput)
	if !view && !input {
		return
	}
	closed := boolPtr(false)
	addTool(s, srv, &mcp.Tool{Name: "sol_connect", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: closed},
		Description: "Open the serial console (Serial over LAN, SOL) of a device and keep it for the other sol_ tools. " +
			"The console output is buffered from now on (the newest 1 MiB per device); read it with sol_read. " +
			"AMT allows one SOL session per device: while this server holds it, the Console view of the OpenLAMT app, MeshCommander " +
			"and other clients cannot connect, and the other way round. SOL is turned on without a question when it is off on the device " +
			"(not in read_only mode). Devices that require user consent show a six-digit code on their screen: call without consent_code first, " +
			"ask a person at the device for the code, then call again with it. The session closes after the idle timeout without calls, " +
			"with sol_disconnect, or when this server stops. Calling it again returns the open session; after the session ended it opens " +
			"a new one and keeps the buffered output and its offsets."},
		s.solConnect)
	addTool(s, srv, &mcp.Tool{Name: "sol_disconnect", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: closed},
		Description: "Close the serial console (SOL) session of a device, so the OpenLAMT app can use it again. The buffered output is dropped."},
		s.solDisconnect)
	if view {
		addTool(s, srv, &mcp.Tool{Name: "sol_read", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closed},
			Description: "Read the serial console output of a device since sol_connect as plain text: terminal escape sequences and control " +
				"characters are dropped (raw keeps them), line endings become \\n. Without offset it continues where the last sol_read ended; " +
				"next_offset is where the next one goes on, more says that more output is buffered. Use wait_ms to wait for output after a command. " +
				"Output that is not UTF-8 (for example the CP437 box drawing of a BIOS setup) shows as U+FFFD; use KVM for such screens. " +
				"Needs sol_connect first; the output of a session that ended stays readable."},
			s.solRead)
	}
	if !input {
		return
	}
	act := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: closed}
	addTool(s, srv, &mcp.Tool{Name: "sol_type_text", Annotations: act,
		Description: fmt.Sprintf("Write text to the serial console of a device (at most %d characters per call): printable ASCII, "+
			"line breaks and tabs only. Line breaks are sent as Enter (CR), but trailing line breaks are dropped so no command runs by accident: "+
			"press Enter with sol_send_keys. Needs sol_connect first.", maxSOLText)},
		s.solTypeText)
	addTool(s, srv, &mcp.Tool{Name: "sol_send_keys", Annotations: act,
		Description: "Press keys on the serial console of a device, in the given order, for example [\"Ctrl+C\"], [\"Up\", \"Enter\"] or [\"Esc\", \"2\"]. " +
			"Keys: Enter, Tab, Shift+Tab, Backspace, Escape (Esc), Space, Up, Down, Left, Right, Home, End, Insert, Delete, PageUp, PageDown, " +
			"F1 to F12, Ctrl+<letter>, Alt+<key> or a single printable ASCII character. They are sent as xterm/VT100 sequences; " +
			"BIOS setup over SOL often expects other codes for function keys (for example Esc then a digit): send those as [\"Esc\", \"2\"] or use KVM. " +
			"Needs sol_connect first."},
		s.solSendKeys)
}

// --- sol_connect and sol_disconnect

type solConnectOutput struct {
	Device             string   `json:"device"`
	AlreadyOpen        bool     `json:"already_open" jsonschema:"the session was open already; nothing new was opened"`
	NextOffset         int64    `json:"next_offset" jsonschema:"stream offset of the next output; sol_read starts at the end of the last read"`
	IdleTimeoutSeconds int      `json:"idle_timeout_seconds" jsonschema:"the session closes after this many seconds without a SOL tool call; 0 means never"`
	Warnings           []string `json:"warnings,omitempty"`
}

func (s *server) solConnect(ctx context.Context, _ *mcp.CallToolRequest, in kvmConnectInput, rec *record) (*mcp.CallToolResult, solConnectOutput, error) {
	rec.device = in.Device
	code, err := parseConsentCode(in.ConsentCode)
	if err != nil {
		return nil, solConnectOutput{}, err
	}
	if code != "" {
		rec.action = "with consent code" // never the code itself
	}
	if _, err := s.lookup(in.Device); err != nil {
		return nil, solConnectOutput{}, err
	}
	var out solConnectOutput
	err = s.sol.connect(ctx, in.Device, code, func(e *solEntry, already bool) error {
		out = solConnectOutput{Device: in.Device, AlreadyOpen: already, NextOffset: e.out.end(),
			IdleTimeoutSeconds: int(s.cfg.SOLIdleTimeout / time.Second), Warnings: e.warnings}
		return nil
	})
	if err != nil {
		return nil, solConnectOutput{}, err
	}
	return nil, out, nil
}

func (s *server) solDisconnect(_ context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, kvmDisconnectOutput, error) {
	rec.device = in.Device
	if _, err := s.lookup(in.Device); err != nil {
		return nil, kvmDisconnectOutput{}, err
	}
	return nil, kvmDisconnectOutput{Device: in.Device, Closed: s.sol.disconnect(in.Device)}, nil
}

// --- sol_read

type solReadInput struct {
	Device   string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Offset   *int64 `json:"offset,omitempty" jsonschema:"stream offset to read from; default: where the last sol_read ended"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"read about this many bytes of output (256 to 65536, default 8192)"`
	WaitMS   int    `json:"wait_ms,omitempty" jsonschema:"wait up to this many milliseconds (at most 10000) for output after offset when there is none yet"`
	Raw      bool   `json:"raw,omitempty" jsonschema:"keep terminal escape sequences and control characters"`
}

type solReadOutput struct {
	Device       string   `json:"device"`
	Text         string   `json:"text"`
	Offset       int64    `json:"offset" jsonschema:"stream offset where text starts"`
	NextOffset   int64    `json:"next_offset" jsonschema:"stream offset where the next sol_read goes on"`
	SkippedBytes int64    `json:"skipped_bytes" jsonschema:"output that was dropped from the buffer before it was read"`
	More         bool     `json:"more" jsonschema:"more output after next_offset is buffered"`
	Ended        bool     `json:"ended" jsonschema:"the session ended; sol_connect opens a new one"`
	EndReason    string   `json:"end_reason,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

func (s *server) solRead(ctx context.Context, _ *mcp.CallToolRequest, in solReadInput, rec *record) (*mcp.CallToolResult, solReadOutput, error) {
	rec.device = in.Device
	limit := in.MaxBytes
	if limit == 0 {
		limit = defaultSOLRead
	}
	switch {
	case limit < minSOLRead || limit > maxSOLRead:
		return nil, solReadOutput{}, refuse("max_bytes must be between %d and %d.", minSOLRead, maxSOLRead)
	case in.WaitMS < 0 || in.WaitMS > maxWaitMS:
		return nil, solReadOutput{}, refuse("wait_ms must be between 0 and %d.", maxWaitMS)
	case in.Offset != nil && *in.Offset < 0:
		return nil, solReadOutput{}, refuse("offset must be 0 or more.")
	}
	if _, err := s.lookup(in.Device); err != nil {
		return nil, solReadOutput{}, err
	}
	var (
		entry    *solEntry
		from     int64
		warnings []string
	)
	err := s.sol.use(in.Device, func(e *solEntry) error {
		entry, from, warnings = e, e.cursor, e.warnings
		if in.Offset != nil {
			from = *in.Offset
		}
		if end := e.out.end(); from > end {
			return refuse("offset %d is beyond the end of the output (%d).", from, end)
		}
		return nil
	})
	if err != nil {
		return nil, solReadOutput{}, err
	}
	// The wait runs without the entry lock, so input tools are not held up meanwhile.
	b := entry.out
	r := b.read(from, limit, in.Raw)
	if r.next == r.offset && !r.more && in.WaitMS > 0 {
		b.wait(ctx, r.end, time.Duration(in.WaitMS)*time.Millisecond)
		r = b.read(from, limit, in.Raw)
	}
	entry.mu.Lock()
	if !entry.removed {
		entry.cursor, entry.lastUsed = r.next, time.Now()
	}
	entry.mu.Unlock()

	out := solReadOutput{Device: in.Device, Text: r.text, Offset: r.offset, NextOffset: r.next, SkippedBytes: r.skipped,
		More: r.more, Ended: r.ended, Warnings: warnings}
	if r.ended {
		out.EndReason = solEndReason(r.endErr)
	}
	text := fmt.Sprintf("[offset %d, next_offset %d, more %v, ended %v, skipped %d bytes]\n",
		out.Offset, out.NextOffset, out.More, out.Ended, out.SkippedBytes)
	if out.Ended {
		if out.EndReason != "" {
			text += "The serial console session ended: " + out.EndReason + " sol_connect opens it again.\n"
		} else {
			text += "The serial console session ended. sol_connect opens it again.\n"
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text + out.Text}}}, out, nil
}

// --- sol_type_text and sol_send_keys

// confirmSOL asks the person in the MCP client before SOL input, when the config wants it, and
// only for a call that can run: the device needs a running session.
func (s *server) confirmSOL(req *mcp.CallToolRequest, device, what string) (*mcp.CallToolResult, error) {
	fav, err := s.lookup(device)
	if err != nil {
		return nil, err
	}
	if err := s.sol.use(device, func(e *solEntry) error {
		if !e.alive() {
			return e.endedError()
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.confirm(req, confirmMessage(what, fav))
}

// solWrite sends p to the running session of device in one Write.
func (s *server) solWrite(device string, p []byte) error {
	return s.sol.use(device, func(e *solEntry) error {
		if !e.alive() {
			return e.endedError()
		}
		_, err := e.client.Write(p)
		return err
	})
}

type solTypeInput struct {
	Device string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Text   string `json:"text" jsonschema:"the text to write: printable ASCII, line breaks and tabs"`
	Secret bool   `json:"secret,omitempty" jsonschema:"the text is a password or another secret: a confirmation prompt shows only its length, not the text. Only for a single line without line breaks or tabs"`
}

type solTypeOutput struct {
	Device       string `json:"device"`
	SentBytes    int    `json:"sent_bytes"`
	EnterPresses int    `json:"enter_presses" jsonschema:"line breaks sent as Enter (CR)"`
}

// solTypePrompt is what the person confirming sees before sol_type_text runs: the text as it
// will be sent, line breaks shown as ⏎ and counted, because each one is an Enter press.
func solTypePrompt(text string, enters int, secret bool) string {
	if secret {
		// The agent is not trusted: say that it chose to hide the text. solTypeText accepts
		// secret only for a single line.
		return fmt.Sprintf("write %d characters to the serial console. The AI agent chose to hide this text (marked as secret); "+
			"it is one line without Enter presses or other control characters", len(text))
	}
	r := []rune(visible(text))
	preview := string(r[:min(len(r), maxPreview)])
	if len(r) > maxPreview {
		preview += fmt.Sprintf("… (%d more characters)", len(r)-maxPreview)
	}
	return fmt.Sprintf("write %d characters with %d Enter %s to the serial console: «%s»",
		len(text), enters, plural(enters, "press", "presses"), preview)
}

func (s *server) solTypeText(_ context.Context, req *mcp.CallToolRequest, in solTypeInput, rec *record) (*mcp.CallToolResult, solTypeOutput, error) {
	rec.device = in.Device
	if n := utf8.RuneCountInString(in.Text); n > maxSOLText {
		return nil, solTypeOutput{}, refuse("The text has %d characters; at most %d can be sent per call.", n, maxSOLText)
	}
	// Serial consoles often are not UTF-8, and any other control character is a key with its
	// own effect: those go through sol_send_keys.
	for i := 0; i < len(in.Text); i++ {
		if c := in.Text[i]; (c < 0x20 || c > 0x7e) && c != '\t' && c != '\n' && c != '\r' {
			return nil, solTypeOutput{}, refuse("sol_type_text sends printable ASCII characters, line breaks and tabs only " +
				"(serial consoles often are not UTF-8). Send keys such as Ctrl+C or Escape with sol_send_keys. Nothing was sent.")
		}
	}
	if in.Secret && strings.ContainsAny(in.Text, "\t\n\r") {
		return nil, solTypeOutput{}, refuse("secret is only allowed for a single line of text without line breaks, tabs or other " +
			"control characters, so the person confirming knows that no Enter press is hidden. Nothing was sent.")
	}
	// Line breaks are Enter; trailing ones are dropped so no command runs by accident.
	text := strings.ReplaceAll(strings.ReplaceAll(in.Text, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil, solTypeOutput{}, refuse("text is empty.")
	}
	enters := strings.Count(text, "\n")
	// Never the text itself, also when it is not secret.
	rec.action = fmt.Sprintf("%d characters, %d Enter", len(text), enters)
	if in.Secret {
		rec.action += ", secret"
	}
	if res, err := s.confirmSOL(req, in.Device, solTypePrompt(text, enters, in.Secret)); res != nil || err != nil {
		return res, solTypeOutput{}, err
	}
	p := []byte(strings.ReplaceAll(text, "\n", "\r"))
	if err := s.solWrite(in.Device, p); err != nil {
		return nil, solTypeOutput{}, err
	}
	return nil, solTypeOutput{Device: in.Device, SentBytes: len(p), EnterPresses: enters}, nil
}

type solKeysInput struct {
	Device string   `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Keys   []string `json:"keys" jsonschema:"keys, sent one after the other, e.g. [\"Ctrl+C\"] or [\"Down\", \"Down\", \"Enter\"]"`
}

func (s *server) solSendKeys(_ context.Context, req *mcp.CallToolRequest, in solKeysInput, rec *record) (*mcp.CallToolResult, sendKeysOutput, error) {
	rec.device = in.Device
	if len(in.Keys) == 0 || len(in.Keys) > maxSOLKeys {
		return nil, sendKeysOutput{}, refuse("keys must hold 1 to %d keys or key combinations.", maxSOLKeys)
	}
	var p []byte
	names := make([]string, 0, len(in.Keys))
	for _, k := range in.Keys {
		seq, name, ok := solKey(k)
		if !ok {
			return nil, sendKeysOutput{}, refuse("unknown key %q; use Enter, Tab, Shift+Tab, Backspace, Escape, Space, Up, Down, Left, Right, "+
				"Home, End, Insert, Delete, PageUp, PageDown, F1 to F12, Ctrl+<letter>, Alt+<key> or a single printable ASCII character. "+
				"Nothing was sent.", k)
		}
		p = append(p, seq...)
		names = append(names, name)
	}
	// The canonical names, never the agent's spelling: bounded by maxSOLKeys known names.
	list := strings.Join(names, ", ")
	rec.action = list
	if res, err := s.confirmSOL(req, in.Device, "press "+list+" on the serial console"); res != nil || err != nil {
		return res, sendKeysOutput{}, err
	}
	if err := s.solWrite(in.Device, p); err != nil {
		return nil, sendKeysOutput{}, err
	}
	return nil, sendKeysOutput{Device: in.Device, Sent: len(names)}, nil
}
