package mcpserver

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/sol"
)

// solOpener opens a SOL session to the host and user of fav with the consent code (may be
// empty). agreed: the person in the MCP client agreed to turn SOL on (see server.mayEnable).
type solOpener func(ctx context.Context, fav favorites.Favorite, code consentCode, agreed bool) (*sol.Client, []string, error)

// solManager keeps one serial console (SOL) session per device across tool calls, like
// kvmManager: sol_connect opens it, the other SOL tools use it, an idle session is closed after
// the idle timeout, and Close ends all of them. A session that ended stays ended until
// sol_connect, so output is never lost to a silent reopen; its buffered output stays readable.
//
// Lock order: an entry's mu may be held while taking m.mu, never the other way round.
type solManager struct {
	mu      sync.Mutex
	entries map[string]*solEntry // by favKey
	closed  bool
	idle    time.Duration // 0: never
	open    solOpener
	now     func() time.Time // clock of the idle timeout; tests replace it
}

// solEntry is the SOL session of one device.
type solEntry struct {
	mu       sync.Mutex  // serializes connect, writes and the cursor; never held while sol_read waits
	key      string      // favKey of the device
	device   string      // favorite name, for messages
	client   *sol.Client // nil only before the first open
	warnings []string    // of the last open, e.g. no TLS
	tls      bool        // the open session uses TLS (the favorite may have changed since)
	removed  bool        // disconnected, idle or shut down: the entry is no longer in the manager
	lastUsed time.Time
	timer    *time.Timer
	out      *solBuffer    // the console output of every session of this entry
	cursor   int64         // where the last sol_read ended
	pumpDone chan struct{} // closed when the pump of client has returned; nil before the first open
}

func newSOLManager(idle time.Duration, open solOpener) *solManager {
	return &solManager{entries: map[string]*solEntry{}, idle: idle, open: open, now: time.Now}
}

func noSOLSession(fav favorites.Favorite) error {
	return refuse("No serial console (SOL) session is open for %q: call sol_connect first.", fav.DisplayName())
}

// alive reports whether the client runs.
func (e *solEntry) alive() bool {
	if e.client == nil {
		return false
	}
	select {
	case <-e.client.Done():
		return false
	default:
		return true
	}
}

// endedError refuses input to a session that has ended; the caller holds e.mu.
func (e *solEntry) endedError() error {
	_, endErr := e.out.state()
	reason := solEndReason(endErr)
	if reason == "" {
		reason = "The serial console session was closed."
	}
	return refuse("The serial console session of %q has ended (%s). Call sol_connect to open it again.",
		e.device, strings.TrimSuffix(reason, "."))
}

// shut closes the client and waits for its pump; the caller holds e.mu.
func (e *solEntry) shut() {
	if e.timer != nil {
		e.timer.Stop()
	}
	if e.client != nil {
		_ = e.client.Close()
	}
	if e.pumpDone != nil {
		<-e.pumpDone
	}
}

// connect opens the session of the device of fav, or keeps the running one (already true), and
// runs fn with the entry locked. A session that ended is opened again with code; the buffer and
// its offsets go on. agreed goes to the opener.
func (m *solManager) connect(ctx context.Context, fav favorites.Favorite, code consentCode, agreed bool, fn func(e *solEntry, already bool) error) error {
	key := favKey(fav)
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return errShuttingDown
		}
		e, created := m.entries[key], false
		if e == nil {
			e, created = &solEntry{key: key, device: fav.DisplayName(), out: newSOLBuffer(solBufferSize)}, true
			m.entries[key] = e
		}
		m.mu.Unlock()

		e.mu.Lock()
		if e.removed { // closed meanwhile: start over
			e.mu.Unlock()
			continue
		}
		already := e.alive()
		if !already {
			if e.pumpDone != nil {
				<-e.pumpDone // the old client is done, so its pump returns at once
			}
			c, warnings, err := m.open(ctx, fav, code, agreed)
			if err != nil {
				if created {
					m.remove(e)
				}
				e.mu.Unlock()
				return err
			}
			e.client, e.warnings, e.tls = c, warnings, fav.UseTLS
			e.out.restart()
			e.pumpDone = make(chan struct{})
			go e.out.pump(c, e.pumpDone)
			if created && m.idle > 0 {
				e.timer = time.AfterFunc(m.idle, func() { m.expire(e) })
			}
		}
		e.lastUsed = m.now()
		err := fn(e, already)
		e.lastUsed = m.now()
		e.mu.Unlock()
		return err
	}
}

// use runs fn on the entry of the device of fav with the entry locked; the session may have ended.
func (m *solManager) use(fav favorites.Favorite, fn func(e *solEntry) error) error {
	m.mu.Lock()
	closed, e := m.closed, m.entries[favKey(fav)]
	m.mu.Unlock()
	switch {
	case closed:
		return errShuttingDown
	case e == nil:
		return noSOLSession(fav)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return noSOLSession(fav)
	}
	e.lastUsed = m.now()
	err := fn(e)
	e.lastUsed = m.now()
	return err
}

// remove takes e out of the manager; the caller holds e.mu.
func (m *solManager) remove(e *solEntry) {
	e.removed = true
	m.mu.Lock()
	if m.entries[e.key] == e {
		delete(m.entries, e.key)
	}
	m.mu.Unlock()
}

// expire closes e once it was not used for the idle timeout; a call in progress delays it.
func (m *solManager) expire(e *solEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return
	}
	if left := m.idle - m.now().Sub(e.lastUsed); left > 0 {
		e.timer.Reset(left)
		return
	}
	m.remove(e)
	e.shut()
}

// disconnect closes and removes the session of the device of fav; it reports whether there was one.
func (m *solManager) disconnect(fav favorites.Favorite) bool {
	m.mu.Lock()
	e := m.entries[favKey(fav)]
	m.mu.Unlock()
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return false
	}
	m.remove(e)
	e.shut()
	return true
}

// closeAll ends every session and refuses new ones (the client went away).
func (m *solManager) closeAll() {
	m.mu.Lock()
	m.closed = true
	entries := make([]*solEntry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	clear(m.entries)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Go(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.removed = true
			e.shut()
		})
	}
	wg.Wait()
}

// solEndReason is why a session ended, for the agent; empty after a Close of ours.
func solEndReason(err error) string {
	switch {
	case err == nil, errors.Is(err, sol.ErrClosed):
		return ""
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "The connection to the device was closed."
	}
	return explain(err)
}

// --- output buffer

// solBufferSize is how much console output is kept per device; older output is dropped.
const solBufferSize = 1 << 20

// solBuffer is the console output of a device as one stream with offsets that go on across
// sessions.
type solBuffer struct {
	mu     sync.Mutex
	size   int           // at most this many bytes are kept
	data   []byte        // the newest output
	start  int64         // stream offset of data[0]
	wake   chan struct{} // closed and replaced on every append and at the end
	ended  bool          // the current session ended
	endErr error         // why (sol.Client.Err; sol.ErrClosed for nil)
}

func newSOLBuffer(size int) *solBuffer { return &solBuffer{size: size, wake: make(chan struct{})} }

// signal wakes the waiters; the caller holds b.mu.
func (b *solBuffer) signal() {
	close(b.wake)
	b.wake = make(chan struct{})
}

func (b *solBuffer) append(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if drop := len(b.data) - b.size; drop > 0 {
		b.start += int64(drop)
		b.data = b.data[:copy(b.data, b.data[drop:])]
	}
	b.signal()
}

// restart marks a new session as running.
func (b *solBuffer) restart() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ended, b.endErr = false, nil
}

// finish marks the session as ended with err.
func (b *solBuffer) finish(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ended, b.endErr = true, err
	b.signal()
}

// pump copies the output of c into b until the session ends, then records why and closes done.
func (b *solBuffer) pump(c *sol.Client, done chan struct{}) {
	defer close(done)
	p := make([]byte, 32<<10)
	for {
		n, err := c.Read(p)
		if n > 0 {
			b.append(p[:n])
		}
		if err != nil {
			break
		}
	}
	<-c.Done() // Read may return io.EOF while Close still runs; Err is final only then
	err := c.Err()
	if err == nil {
		err = sol.ErrClosed
	}
	b.finish(err)
}

// end is the stream offset after the newest output.
func (b *solBuffer) end() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.start + int64(len(b.data))
}

// state reports whether the current session ended, and why.
func (b *solBuffer) state() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ended, b.endErr
}

// solChunk is one read of the buffer.
type solChunk struct {
	text         string
	offset, next int64 // where text starts and where the next read goes on
	end          int64 // the end of the buffer at the time of the read
	skipped      int64 // output dropped from the buffer before it was read
	more         bool  // more output after next is buffered
	ended        bool
	endErr       error
}

// read renders the output from offset from (moved to the oldest kept byte if that was
// dropped) for the agent, up to about limit bytes.
func (b *solBuffer) read(from int64, limit int, raw bool) solChunk {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := solChunk{end: b.start + int64(len(b.data)), ended: b.ended, endErr: b.endErr}
	if from < b.start {
		r.skipped, from = b.start-from, b.start
	}
	from = min(from, r.end)
	render := consoleText
	if raw {
		render = rawText
	}
	text, used, cut := render(b.data[from-b.start:], limit, b.ended)
	r.text, r.offset, r.next, r.more = text, from, from+int64(used), cut
	return r
}

// wait returns when the buffer reaches past after, the session ended, d passed or ctx ended.
func (b *solBuffer) wait(ctx context.Context, after int64, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		b.mu.Lock()
		if b.start+int64(len(b.data)) > after || b.ended {
			b.mu.Unlock()
			return
		}
		w := b.wake
		b.mu.Unlock()
		select {
		case <-w:
		case <-t.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// --- rendering for the agent

// maxEscape bounds an escape sequence; a longer one without its end is broken.
const maxEscape = 256

// consoleText turns raw serial output into plain text for a model: terminal escape sequences
// (CSI, OSC, DCS/SOS/PM/APC and two-byte escapes) are dropped, CR LF and a lone CR become LF,
// tabs and LF stay, other C0 and C1 controls and DEL are dropped, and invalid UTF-8 becomes
// U+FFFD. It processes the tokens (sequence, rune, CR LF) that start before limit, completing
// one that ends after it; cut is true when it stopped at the limit. b is all buffered output
// from the read offset: a token that the end of b cuts is held back (not counted in used)
// unless final, so the next read continues there.
func consoleText(b []byte, limit int, final bool) (text string, used int, cut bool) {
	var out strings.Builder
	i := 0
	for i < len(b) {
		if i >= limit {
			return out.String(), i, true
		}
		c := b[i]
		switch {
		case c == 0x1b:
			n, state := escapeLen(b[i:])
			switch state {
			case escComplete:
				i += n
			case escBroken:
				i++ // drop the ESC only and go on
			default: // cut by the end of b
				if !final {
					return out.String(), i, false
				}
				i = len(b)
			}
		case c == '\r':
			switch {
			case i+1 < len(b):
				if b[i+1] == '\n' {
					i++
				}
			case !final:
				return out.String(), i, false // the LF may follow
			}
			out.WriteByte('\n')
			i++
		case c == '\n' || c == '\t':
			out.WriteByte(c)
			i++
		case c < 0x20 || c == 0x7f:
			i++
		case c < 0x80:
			out.WriteByte(c)
			i++
		default:
			if !final && !utf8.FullRune(b[i:]) {
				return out.String(), i, false
			}
			r, size := utf8.DecodeRune(b[i:])
			if r < 0x80 || r > 0x9f { // C1 controls are dropped; RuneError is U+FFFD already
				out.WriteRune(r)
			}
			i += size
		}
	}
	return out.String(), i, false
}

// rawText is consoleText for raw: true. Everything is kept but invalid UTF-8, which becomes
// U+FFFD; only a rune cut by the end of b is held back.
func rawText(b []byte, limit int, final bool) (text string, used int, cut bool) {
	var out strings.Builder
	i := 0
	for i < len(b) {
		if i >= limit {
			return out.String(), i, true
		}
		if !final && !utf8.FullRune(b[i:]) {
			return out.String(), i, false
		}
		r, size := utf8.DecodeRune(b[i:])
		out.WriteRune(r)
		i += size
	}
	return out.String(), i, false
}

type escState int

const (
	escComplete escState = iota
	escBroken            // not a valid sequence, or longer than maxEscape without its end
	escCut               // b ends before the sequence does
)

// escapeLen measures the escape sequence at the start of b (b[0] is ESC).
func escapeLen(b []byte) (int, escState) {
	if len(b) < 2 {
		return 0, escCut
	}
	switch b[1] {
	case '[': // CSI: parameters and intermediates 0x20–0x3F, final byte 0x40–0x7E
		for j := 2; ; j++ {
			switch {
			case j >= maxEscape:
				return 0, escBroken
			case j >= len(b):
				return 0, escCut
			case b[j] >= 0x40 && b[j] <= 0x7e:
				return j + 1, escComplete
			case b[j] < 0x20 || b[j] > 0x3f:
				return 0, escBroken
			}
		}
	case ']', 'P', 'X', '^', '_': // OSC (ends with BEL or ST), DCS, SOS, PM, APC (end with ST)
		for j := 2; ; j++ {
			switch {
			case j >= maxEscape:
				return 0, escBroken
			case j >= len(b):
				return 0, escCut
			case b[j] == 0x07 && b[1] == ']':
				return j + 1, escComplete
			case b[j] == 0x1b:
				if j+1 >= len(b) {
					return 0, escCut
				}
				if b[j+1] == '\\' {
					return j + 2, escComplete
				}
				return j, escComplete // an ESC ends the string and starts the next sequence
			}
		}
	}
	// Two-byte escape: intermediates 0x20–0x2F, then a final byte 0x30–0x7E.
	for j := 1; ; j++ {
		switch {
		case j >= maxEscape:
			return 0, escBroken
		case j >= len(b):
			return 0, escCut
		case b[j] >= 0x20 && b[j] <= 0x2f:
		case b[j] >= 0x30 && b[j] <= 0x7e:
			return j + 1, escComplete
		default:
			return 0, escBroken
		}
	}
}
