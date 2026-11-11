// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/AMTRedirector.ts, Serial-over-LAN parts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920,
// cross-checked against MeshCommander (github.com/Ylianst/MeshCommander, amt-redir-ws-0.1.0.js).
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

// Package sol implements the client side of Intel AMT Serial-over-LAN (SOL): the serial console
// of the managed host as a byte stream. Client is an io.ReadWriteCloser; terminal emulation
// (escape sequences, character set, line endings) belongs to the caller.
//
// The original's AMTTerminal.ts holds only CP437 tables for a terminal renderer and is not
// ported; golang.org/x/text/encoding/charmap.CodePage437 covers that if a caller needs it.
package sol

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Srv-02/openlamt/internal/redir"
)

// Config describes one SOL session; the caller (internal/amt) wires TLS pinning.
type Config struct {
	Addr        string // host:port, 16994 (plain) or 16995 (TLS)
	Username    string
	Password    string
	TLS         *tls.Config                                                       // nil = plain TCP
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error) // nil = net.Dialer
}

// HostStatus is the last CONTROLS_FROM_HOST (0x29) report about the host's serial port.
type HostStatus struct {
	Reported   bool // false until the first report
	PowerOn    bool // status bit SYSTEM_POWER_STATE: the host is powered on
	Loopback   bool // the port is in loopback mode; input may not reach the host's software
	TxOverflow bool // AMT's transmit buffer overflowed; host output was lost
}

var (
	// ErrClosed is the session error after Close ended a healthy session.
	ErrClosed = errors.New("sol: session closed")
	// ErrClosedByDevice means the device ended the session (END_SOL_REPLY 0x23).
	ErrClosedByDevice = errors.New("sol: session closed by device")
	// ErrStartFailed means the device rejected START_SOL; the error text carries the status.
	ErrStartFailed = errors.New("sol: device rejected the session start")

	errProtocol = errors.New("sol: protocol error")
)

// Message ids (AMTRedirector.ts:393-460, 515-534; 0x12, 0x22, 0x23, 0x24, 0x25 after amtterm).
const (
	cmdEndRedirection = 0x12 // END_REDIRECTION_SESSION, 4 bytes, no sequence number
	cmdStart          = 0x20
	cmdStartReply     = 0x21
	cmdEnd            = 0x22
	cmdEndReply       = 0x23
	cmdPing           = 0x24
	cmdPong           = 0x25
	cmdSetup          = 0x27 // sent after the start reply; meaning undocumented in the sources
	cmdDataToHost     = 0x28
	cmdControls       = 0x29
	cmdDataFromHost   = 0x2a
	cmdHeartbeat      = 0x2b
)

// START_SOL parameters (AMTRedirector.ts:395-400), milliseconds or bytes.
const (
	maxTxBuffer       = 10000
	txTimeout         = 100
	txOverflowTimeout = 0
	rxTimeout         = 10000 // AMT ends the session after 10 s without a message from us
	rxFlushTimeout    = 100
	heartbeat         = 0 // AMT sends no heartbeats; we send our own every 2 s
)

const (
	headerLen     = 8
	startReplyLen = 23
	controlsLen   = 10
	dataHeaderLen = 10

	maxChunk     = 1000    // bytes per DATA_TO_HOST message
	maxPending   = 1 << 20 // received bytes not yet read; the loop stops reading beyond this
	writeTimeout = 10 * time.Second
)

// keepaliveInterval is the heartbeat period (AMTRedirector.ts:436), a variable for tests.
var keepaliveInterval = 2 * time.Second

// closeTimeout bounds each step of the goodbye in Close, a variable for tests.
var closeTimeout = time.Second

// Client is one SOL session. Read returns the host's output, Write sends input to the host.
type Client struct {
	conn net.Conn
	raw  net.Conn
	br   *bufio.Reader
	w    io.Writer

	wmu    sync.Mutex // covers socket writes and outSeq
	outSeq uint32

	active bool // start reply seen; Dial goroutine, then loop goroutine only

	mu      sync.Mutex
	cond    *sync.Cond // signals pending, closing, failErr and err changes
	pending []byte
	status  HostStatus
	closing bool
	failErr error // first write or keepalive error, ends the session
	err     error // set when the loop has finished
	done    chan struct{}
}

// Dial connects, authenticates, sends START_SOL (0x20) and waits for START_SOL_REPLY (0x21,
// status 0); it then sends 0x27 like the original and starts the read loop and the keepalive.
// ctx bounds the whole setup.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	c := &Client{done: make(chan struct{})}
	c.cond = sync.NewCond(&c.mu)
	rc, err := redir.Dial(ctx, redir.Config{
		Addr: cfg.Addr, Username: cfg.Username, Password: cfg.Password,
		TLS: cfg.TLS, DialContext: cfg.DialContext,
	}, redir.SOL, c.start)
	if err != nil {
		return nil, err
	}
	c.conn, c.raw = rc.Conn, rc.Raw // start already set br and w
	go c.run()
	go c.keepalive(keepaliveInterval)
	return c, nil
}

// start runs under redir.Dial's context protection.
func (c *Client) start(r *bufio.Reader, w io.Writer) error {
	c.br, c.w = r, w
	p := make([]byte, 16)
	for i, v := range []uint16{maxTxBuffer, txTimeout, txOverflowTimeout, rxTimeout, rxFlushTimeout, heartbeat} {
		binary.LittleEndian.PutUint16(p[2*i:], v)
	}
	if err := c.send(writeTimeout, cmdStart, p); err != nil {
		return err
	}
	for !c.active {
		if err := c.handle(false); err != nil {
			return err
		}
	}
	return nil
}

// send writes one message (header and payload in a single write) under the write mutex, which
// also covers the sequence number. Sequence numbers start at 1 (MeshCommander); AMT does not
// check them (ui-toolkit and amtterm always send 0).
func (c *Client) send(timeout time.Duration, cmd byte, payload ...[]byte) error {
	n := headerLen
	for _, p := range payload {
		n += len(p)
	}
	buf := make([]byte, headerLen, n)
	buf[0] = cmd
	for _, p := range payload {
		buf = append(buf, p...)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	// Checked under the write mutex: nothing follows END_SOL, and a send that passed its own
	// closing check earlier cannot reset the short deadline Close set.
	c.mu.Lock()
	closing := c.closing
	c.mu.Unlock()
	if closing {
		if cmd != cmdEnd {
			return ErrClosed
		}
		timeout = min(timeout, closeTimeout)
	}
	c.outSeq++
	binary.LittleEndian.PutUint32(buf[4:], c.outSeq)
	return c.writeLocked(timeout, buf)
}

func (c *Client) writeLocked(timeout time.Duration, b []byte) error {
	if c.raw != nil {
		_ = c.raw.SetWriteDeadline(time.Now().Add(timeout))
	}
	_, err := c.w.Write(b)
	return err
}

// handle reads and processes one message. wait is false during Dial: received output is then
// buffered without the maxPending limit, as nothing reads it yet.
func (c *Client) handle(wait bool) error {
	cmd, err := c.br.ReadByte()
	if err != nil {
		return err
	}
	switch cmd {
	case cmdStartReply: // AMTRedirector.ts:425-441
		var b [startReplyLen - 1]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return err
		}
		if c.active {
			return nil // a repeated reply changes nothing
		}
		// The original does not check the status; amtterm does.
		if b[0] != 0 {
			return fmt.Errorf("%w: status %d", ErrStartFailed, b[0])
		}
		if err := c.send(writeTimeout, cmdSetup, []byte{0x00, 0x00, 0x1b, 0x00, 0x00, 0x00}); err != nil {
			return err
		}
		c.active = true
	case cmdControls: // :442-446; byte 8 control lines, byte 9 status (amtterm)
		var b [controlsLen - 1]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return err
		}
		st := b[8]
		c.mu.Lock()
		c.status = HostStatus{Reported: true, TxOverflow: st&1 != 0, Loopback: st&2 != 0, PowerOn: st&4 != 0}
		c.mu.Unlock()
	case cmdDataFromHost: // :447-456
		var b [dataHeaderLen - 1]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return err
		}
		data := make([]byte, binary.LittleEndian.Uint16(b[7:9]))
		if _, err := io.ReadFull(c.br, data); err != nil {
			return err
		}
		if err := c.deliver(data, wait); err != nil {
			return err
		}
	case cmdHeartbeat, cmdPing, cmdPong: // :457-460; 0x24/0x25 per amtterm, nothing to do
		if _, err := c.br.Discard(headerLen - 1); err != nil {
			return err
		}
	case cmdEndReply:
		if _, err := c.br.Discard(headerLen - 1); err != nil {
			return err
		}
		// Like amtterm: end the redirection session, then close.
		c.wmu.Lock()
		_ = c.writeLocked(closeTimeout, []byte{cmdEndRedirection, 0, 0, 0})
		c.wmu.Unlock()
		return ErrClosedByDevice // finish turns it into ErrClosed after Close
	default: // :473-480: unknown command stops the session
		return fmt.Errorf("%w: unknown command 0x%02x", errProtocol, cmd)
	}
	return nil
}

// deliver appends host output for Read. With wait it blocks while maxPending bytes are
// unread (backpressure: the loop stops reading the socket); it never blocks once Close started
// or the session failed, and drops the data after Close. Without wait (during Dial, when nothing
// reads yet) more than maxPending unread bytes are a protocol error instead of unbounded memory.
func (c *Client) deliver(data []byte, wait bool) error {
	if len(data) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !wait && len(c.pending)+len(data) > maxPending {
		return fmt.Errorf("%w: more than %d bytes of output before the session start", errProtocol, maxPending)
	}
	for wait && len(c.pending) >= maxPending && !c.closing && c.failErr == nil {
		c.cond.Wait()
	}
	if c.closing {
		return nil
	}
	c.pending = append(c.pending, data...)
	c.cond.Broadcast()
	return nil
}

// run is the read loop; it ends the session on the first error.
func (c *Client) run() {
	for {
		if err := c.handle(true); err != nil {
			c.finish(err)
			return
		}
	}
}

// keepalive sends HEARTBEAT (0x2B) every d until the session ends or Close starts
// (AMTRedirector.ts:436, :531-534). A write error ends the session.
func (c *Client) keepalive(d time.Duration) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.mu.Lock()
			stop := c.closing
			c.mu.Unlock()
			if stop {
				return
			}
			if err := c.send(writeTimeout, cmdHeartbeat); err != nil {
				c.fail(err)
				return
			}
		}
	}
}

// fail records a write error and aborts the read loop, which then ends the session.
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.failErr == nil && !c.closing {
		c.failErr = err
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	_ = c.raw.SetDeadline(time.Unix(1, 0))
}

// finish records the session error (a recorded write error first, else ErrClosed after Close,
// else err), closes the connection, wakes Read and closes Done. Only the read loop calls it.
func (c *Client) finish(err error) {
	c.mu.Lock()
	switch {
	case c.failErr != nil:
		err = c.failErr
	case c.closing:
		err = ErrClosed
	}
	c.err = err
	c.cond.Broadcast()
	c.mu.Unlock()
	c.abort()
	close(c.done)
}

// abort closes the raw connection. tls.Conn.Close would first send close_notify with its own
// 5 s write deadline, which blocks when the peer stopped reading.
func (c *Client) abort() {
	_ = c.raw.SetDeadline(time.Unix(1, 0))
	_ = c.raw.Close()
}

// Read returns host output. It blocks until output is available; after the session ended (by
// Close, the device or an error) it returns the remaining output and then io.EOF. Err tells why
// the session ended. Read must be called continuously: beyond 1 MiB of unread output the client
// stops reading from the device.
func (c *Client) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.pending) == 0 && c.err == nil && !c.closing {
		c.cond.Wait()
	}
	if len(c.pending) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	if len(c.pending) == 0 {
		c.pending = nil
	}
	c.cond.Broadcast()
	return n, nil
}

// Write sends p to the host's serial port in DATA_TO_HOST messages of at most 1000 bytes. After
// the session ended or Close started it returns the session error (ErrClosed after Close). A
// write error ends the session. Write is not meant for concurrent use: chunks of concurrent
// calls may interleave.
func (c *Client) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if err := c.writeErr(); err != nil {
			return n, err
		}
		k := min(len(p), maxChunk)
		var l [2]byte
		binary.LittleEndian.PutUint16(l[:], uint16(k))
		if err := c.send(writeTimeout, cmdDataToHost, l[:], p[:k]); err != nil {
			c.fail(err)
			if werr := c.writeErr(); werr != nil {
				return n, werr
			}
			return n, err
		}
		n += k
		p = p[k:]
	}
	return n, nil
}

func (c *Client) writeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.failErr != nil:
		return c.failErr
	case c.err != nil:
		return c.err
	case c.closing:
		return ErrClosed
	}
	return nil
}

// Done is closed when the session has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns nil while the session is active, ErrClosed after Close ended a healthy session,
// ErrClosedByDevice if the device ended it, otherwise the first error that ended it.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Status returns the last serial port report of the host; poll it, there is no notification.
func (c *Client) Status() HostStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Close ends the session and waits for the read loop; it is idempotent and returns nil. It sends
// END_SOL (0x22) and waits up to 1 s for END_SOL_REPLY (0x23), which the loop answers with
// END_REDIRECTION (0x12) like amtterm; otherwise it aborts the connection. The original only
// closes the socket; the goodbye lets AMT free the SOL session at once. Close can block for about
// 2 s, so do not call it from the UI thread.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closing || c.err != nil {
		c.mu.Unlock()
		<-c.done
		return nil
	}
	c.closing = true
	broken := c.failErr != nil // a failed write may have left half a frame on the wire
	c.cond.Broadcast()         // wake Read and a loop waiting in deliver
	c.mu.Unlock()

	if !broken {
		// A Write blocked in the socket would hold the write mutex: shorten its deadline first.
		_ = c.raw.SetWriteDeadline(time.Now().Add(closeTimeout))
		if err := c.send(closeTimeout, cmdEnd); err == nil {
			select {
			case <-c.done:
				return nil
			case <-time.After(closeTimeout):
			}
		}
	}
	c.abort()
	<-c.done
	return nil
}
