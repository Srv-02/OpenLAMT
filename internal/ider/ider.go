// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/AMTIDER.ts, IDERDataProcessor.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920,
// cross-checked against MeshCommander (github.com/Ylianst/MeshCommander, amt-ider-ws-0.0.1.js).
// Copyright (c) Intel Corporation 2023. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

// Package ider implements the client side of Intel AMT IDE-Redirection (IDE-R, USB-R since
// AMT 11): a local ISO image appears as a CD-ROM drive on the managed host. Only the CD-ROM
// device (0xB0) is served; the floppy device (0xA0) answers like the original with no floppy.
package ider

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Srv-02/openlamt/internal/redir"
)

// Image is the medium; the client owns it and closes it on Close or when the session ends.
type Image interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

type fileImage struct {
	*os.File
	size int64
}

func (f *fileImage) Size() int64 { return f.size }

// OpenISO opens a local ISO file as Image. It fails for directories and empty files.
func OpenISO(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("ider: %s is not a regular file", path)
	}
	if fi.Size() == 0 {
		f.Close()
		return nil, fmt.Errorf("ider: %s is empty", path)
	}
	return &fileImage{File: f, size: fi.Size()}, nil
}

// StartMode selects when the drive becomes visible to the host (REGS_TOGGLE value).
type StartMode int

const (
	StartNow      StartMode = iota // register toggle 0x01+0x18: drive at the host immediately (default)
	StartOnReboot                  // 0x01+0x08: drive at the next start
)

func (m StartMode) toggleValue() (uint32, error) {
	switch m {
	case StartNow:
		return 0x01 + 0x18, nil // IDERDataProcessor.ts:75 (iderStart 2)
	case StartOnReboot:
		return 0x01 + 0x08, nil // IDERDataProcessor.ts:71 (iderStart 0)
	}
	return 0, fmt.Errorf("ider: unknown start mode %d", int(m))
}

// Config describes one IDE-R session; the caller (internal/amt) wires TLS pinning.
type Config struct {
	Addr        string // host:port, 16994 (plain) or 16995 (TLS)
	Username    string
	Password    string
	TLS         *tls.Config                                                       // nil = plain TCP
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error) // nil = net.Dialer
	Image       Image
	Start       StartMode
}

// Stats is a snapshot of the session counters.
type Stats struct {
	Started   time.Time
	ImageSize int64
	BytesRead int64     // image data bytes sent to the host (a padded last sector counts in full)
	Reads     int64     // served READ_6/READ_10/READ_12 commands
	LastRead  time.Time // zero = nothing read yet
}

var (
	// ErrClosed is the session error after Close ended a healthy session.
	ErrClosed = errors.New("ider: session closed")
	// ErrClosedByDevice means the device sent CLOSE (0x43).
	ErrClosedByDevice = errors.New("ider: session closed by device")
	// ErrToggleFailed means the device rejected the register toggle (STATUS_DATA type 3, value != 1).
	ErrToggleFailed = errors.New("ider: device rejected the drive toggle")

	errProtocol = errors.New("ider: protocol error")
)

const (
	writeTimeout = 10 * time.Second // per socket write, like kvm

	maxBuffer   = 8192 // IDERDataProcessor.ts:53-60
	sectorSize  = 2048
	deviceFlopp = 0xa0
	deviceCD    = 0xb0

	rxTimeout = 30000 // AMTIDER.ts:25-27
	txTimeout = 0
	heartbeat = 20000
	version   = 1

	regsToggle = 3 // Constants.ts:5
)

// readTimeout is 3 x the 20 s heartbeat we ask for in OPEN_SESSION. Renewed for every received
// message, including AMT's PONG to our keepalive ping, so it only fires when AMT stays silent for
// a full minute; it then ends the session with a deadline error (never io.EOF). Applies to reads
// only. A variable so tests can shorten it.
var readTimeout = 60 * time.Second

// keepaliveInterval is the period of the client's KEEPALIVEPING (0x44) from the OPEN_SESSION
// reply until the session ends. A host that does not poll the drive (UEFI firmware, unlike a running
// OS) otherwise leaves the session without client traffic, and AMT closed it on the device after
// about a minute. MeshCommander and MeshCentral carry this timer commented out since their first
// commit, with a 5 s period and no reason given (amt-ider-ws-0.0.1.js:103,
// amt/amt-ider-module.js:142, agents/modules_meshcmd/amt-ider.js:89); ui-toolkit has none.
// AMT answers with KEEPALIVEPONG (0x45), which also renews readTimeout. A variable so tests can
// shorten it.
var keepaliveInterval = 2 * time.Second

// toggleTimeout is how long Dial waits for STATUS_DATA type 3 after the toggle (spec R6).
var toggleTimeout = 5 * time.Second

// Command ids.
const (
	cmdOpenSession      = 0x40
	cmdOpenSessionReply = 0x41
	cmdDataToHostReq    = 0x52 // GetDataFromHost (SCSI_CHUNK)
	cmdClose            = 0x43
	cmdPing             = 0x44
	cmdPong             = 0x45
	cmdResetOccurred    = 0x46
	cmdResetReply       = 0x47
	cmdDisableEnable    = 0x48
	cmdStatusData       = 0x49
	cmdError            = 0x4a
	cmdHeartbeat        = 0x4b
	cmdWritten          = 0x50
	cmdCommandEnd       = 0x51
	cmdDataFromHost     = 0x53
	cmdDataToHost       = 0x54
)

// Client is one IDE-R session with a running read loop.
type Client struct {
	img     Image
	size    int64 // image size in bytes
	toggle  uint32
	w       io.Writer // plain/TLS connection; set in Dial
	conn    net.Conn
	raw     net.Conn
	br      *bufio.Reader
	started time.Time

	wmu    sync.Mutex // covers socket writes and outSeq
	outSeq uint32

	inSeq   uint32 // loop goroutine only (and Dial before the loop starts)
	readBuf int    // OPEN_SESSION readbfr
	opened  bool
	toggled bool // STATUS_DATA type 3 seen
	toggleV uint32
	cdReady bool

	bytesRead atomic.Int64
	reads     atomic.Int64
	lastRead  atomic.Int64 // unix nanoseconds, 0 = never

	closing atomic.Bool
	mu      sync.Mutex
	err     error
	imgErr  error
	done    chan struct{}

	pingStop  chan struct{} // closed by stopPinger; nil until the OPEN_SESSION reply
	pingDone  chan struct{} // closed when the ping goroutine has returned
	pingOnce  sync.Once
	pingsSent atomic.Int64
	finished  bool
}

// Dial connects, authenticates, opens the IDE-R session (OPEN_SESSION 0x40, rx_timeout 30000,
// tx_timeout 0, heartbeat 20000, version 1), checks the reply (proto 0, buffers 1..8192) and
// sends the register toggle for cfg.Start. It waits up to 5 s for the toggle result (STATUS_DATA
// type 3, value 1) and then starts the read loop. ctx bounds the whole setup.
//
// Image ownership: on success the Client owns cfg.Image and closes it when the session ends; on
// error Dial leaves it open and the caller keeps it.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Image == nil {
		return nil, errors.New("ider: no image")
	}
	toggle, err := cfg.Start.toggleValue()
	if err != nil {
		return nil, err
	}
	c := &Client{img: cfg.Image, size: cfg.Image.Size(), toggle: toggle, done: make(chan struct{})}
	rc, err := redir.Dial(ctx, redir.Config{
		Addr: cfg.Addr, Username: cfg.Username, Password: cfg.Password,
		TLS: cfg.TLS, DialContext: cfg.DialContext,
	}, redir.IDER, c.open)
	if err != nil {
		c.stopPinger()
		return nil, err
	}
	// After the authentication success there is no further redirection-layer command: the IDE-R
	// messages start right away (AMTRedirector.ts:417-421), the client sends OPEN_SESSION first.
	c.wmu.Lock() // the ping goroutine may already be sending
	c.conn, c.raw, c.br, c.w = rc.Conn, rc.Raw, rc.R, rc.Conn
	c.wmu.Unlock()
	c.started = time.Now()
	go c.run()
	return c, nil
}

// open runs under redir.Dial's context protection: OPEN_SESSION, the reply, the toggle and its
// result. Messages are handled by the same code as in the read loop.
func (c *Client) open(r *bufio.Reader, w io.Writer) error {
	c.w, c.br = w, r
	if err := c.sendOpenSession(); err != nil {
		return err
	}
	var toggleDeadline time.Time
	for !c.toggled {
		if c.opened && toggleDeadline.IsZero() {
			toggleDeadline = time.Now().Add(toggleTimeout)
		}
		if !toggleDeadline.IsZero() {
			if rd, ok := w.(interface{ SetReadDeadline(time.Time) error }); ok {
				_ = rd.SetReadDeadline(toggleDeadline)
			}
		}
		m, err := readMessage(r)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) && !toggleDeadline.IsZero() && time.Now().After(toggleDeadline) {
				return fmt.Errorf("%w: no reply within %s", ErrToggleFailed, toggleTimeout)
			}
			return err
		}
		if err := c.dispatch(m); err != nil {
			return err
		}
	}
	if c.toggleV != 1 {
		return ErrToggleFailed
	}
	return nil
}

// send writes one message (header and payload in a single write) under the write mutex, which
// also covers the outgoing sequence number. attrs: bit 1 "completed" is set for cmd > 50 when
// completed is true; bit 0 is the DMA attribute (AMTIDER.ts:113-117).
func (c *Client) send(cmd byte, completed bool, dma bool, payload ...[]byte) error {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	buf := make([]byte, 8, n)
	buf[0] = cmd
	if cmd > 50 && completed {
		buf[3] = 2
	}
	if dma {
		buf[3]++
	}
	for _, p := range payload {
		buf = append(buf, p...)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	binary.LittleEndian.PutUint32(buf[4:8], c.outSeq)
	c.outSeq++
	if c.raw != nil {
		_ = c.raw.SetWriteDeadline(time.Now().Add(writeTimeout))
	}
	_, err := c.w.Write(buf)
	return err
}

func (c *Client) sendOpenSession() error {
	p := make([]byte, 10)
	binary.LittleEndian.PutUint16(p[0:], rxTimeout)
	binary.LittleEndian.PutUint16(p[2:], txTimeout)
	binary.LittleEndian.PutUint16(p[4:], heartbeat)
	binary.LittleEndian.PutUint32(p[6:], version)
	return c.send(cmdOpenSession, false, false, p)
}

func (c *Client) sendToggle() error {
	p := make([]byte, 5)
	p[0] = regsToggle
	binary.LittleEndian.PutUint32(p[1:], c.toggle)
	return c.send(cmdDisableEnable, false, false, p)
}

// message is one received IDE-R message without its 8-byte header.
type message struct {
	cmd, attr byte
	seq       uint32
	body      []byte
}

// readMessage reads exactly one message. Sizes are those of IDERDataProcessor.ts, minus the 8
// header bytes.
func readMessage(r io.Reader) (message, error) {
	var h [8]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return message{}, err
	}
	m := message{cmd: h[0], attr: h[3], seq: binary.LittleEndian.Uint32(h[4:])}
	var fixed int
	switch m.cmd {
	case cmdOpenSessionReply:
		fixed = 22 // 30 bytes in total, the last one is the length of the trailing data
	case cmdClose, cmdPing, cmdPong, cmdHeartbeat:
		fixed = 0
	case cmdResetOccurred:
		fixed = 1
	case cmdStatusData:
		fixed = 5
	case cmdError:
		fixed = 3
	case cmdWritten:
		fixed = 20
	case cmdDataFromHost:
		fixed = 6 // 14 bytes in total, LE16 data length at offset 9
	default:
		return message{}, fmt.Errorf("%w: unknown command 0x%02x", errProtocol, m.cmd)
	}
	m.body = make([]byte, fixed)
	if _, err := io.ReadFull(r, m.body); err != nil {
		return message{}, err
	}
	extra := 0
	switch m.cmd {
	case cmdOpenSessionReply:
		extra = int(m.body[21])
	case cmdDataFromHost:
		extra = int(binary.LittleEndian.Uint16(m.body[1:3]))
	}
	if extra > 0 {
		rest := make([]byte, extra)
		if _, err := io.ReadFull(r, rest); err != nil {
			return message{}, err
		}
		m.body = append(m.body, rest...)
	}
	return m, nil
}

// dispatch handles one received message (IDERDataProcessor.ts interpretCommandData). The
// sequence number is checked first: a gap ends the session (AMTIDER.ts:100-107).
func (c *Client) dispatch(m message) error {
	if m.seq != c.inSeq {
		return fmt.Errorf("%w: sequence %d, expected %d", errProtocol, m.seq, c.inSeq)
	}
	c.inSeq++
	b := m.body
	if !c.opened {
		switch m.cmd {
		case cmdWritten, cmdDataFromHost, cmdResetOccurred:
			// Device commands need the negotiated read buffer; before the OPEN_SESSION reply
			// they are a protocol error (the original would read with an undefined buffer size).
			return fmt.Errorf("%w: command 0x%02x before OPEN_SESSION reply", errProtocol, m.cmd)
		}
	}
	switch m.cmd {
	case cmdOpenSessionReply:
		return c.handleOpenSession(b)
	case cmdClose: // IDERDataProcessor.ts:84
		return ErrClosedByDevice
	case cmdPing: // :31
		return c.send(cmdPong, false, false)
	case cmdPong, cmdHeartbeat: // :34, :147: nothing to do
		return nil
	case cmdResetOccurred:
		// :107-125. The original defers the reply while a read is running; reads are
		// synchronous in this loop, so nothing is ever pending here.
		return c.send(cmdResetReply, false, false)
	case cmdStatusData:
		return c.handleStatusData(b)
	case cmdError: // :139: the original only logs "ABORT" and goes on
		return nil
	case cmdWritten:
		return c.handleWritten(b)
	case cmdDataFromHost:
		// :168-196: a fixed CHECK CONDITION 07/27/00 (write protected) for device 0xA0. We never
		// request host data for the CD, so this is only a robustness reply.
		return c.send(cmdCommandEnd, true, false, make([]byte, 12),
			[]byte{0x87, 0x70, 0x03, 0x00, 0x00, 0x00, 0xa0, 0x51, 0x07, 0x27, 0x00})
	}
	return fmt.Errorf("%w: unknown command 0x%02x", errProtocol, m.cmd)
}

func (c *Client) handleOpenSession(b []byte) error {
	// IDERDataProcessor.ts:40-80. Offsets are those of the original minus 8.
	readbfr := int(binary.LittleEndian.Uint16(b[8:]))
	writebfr := int(binary.LittleEndian.Uint16(b[10:]))
	proto := b[13]
	if proto != 0 {
		return fmt.Errorf("%w: unknown IDE-R protocol %d", errProtocol, proto)
	}
	if readbfr == 0 || readbfr > maxBuffer {
		return fmt.Errorf("%w: illegal read buffer size %d", errProtocol, readbfr)
	}
	if writebfr > maxBuffer {
		return fmt.Errorf("%w: illegal write buffer size %d", errProtocol, writebfr)
	}
	c.readBuf = readbfr
	c.opened = true
	c.startPinger()
	return c.sendToggle()
}

// startPinger starts the KEEPALIVEPING timer once (see keepaliveInterval). Pings go through send,
// so they take the write mutex and never interleave with a data reply.
func (c *Client) startPinger() {
	if c.pingStop != nil {
		return
	}
	c.pingStop, c.pingDone = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(c.pingDone)
		t := time.NewTicker(keepaliveInterval)
		defer t.Stop()
		for {
			select {
			case <-c.pingStop:
				return
			case <-t.C:
			}
			if err := c.send(cmdPing, false, false); err != nil {
				return // a broken connection ends the session through the read loop
			}
			c.pingsSent.Add(1)
		}
	}()
}

// stopPinger stops the ping goroutine and waits for it; safe without a started pinger and twice.
func (c *Client) stopPinger() {
	if c.pingStop == nil {
		return
	}
	c.pingOnce.Do(func() { close(c.pingStop) })
	<-c.pingDone
}

func (c *Client) handleStatusData(b []byte) error {
	typ := b[0]
	value := binary.LittleEndian.Uint32(b[1:5])
	switch typ {
	case 1: // REGS_AVAIL: toggle again when the registers become available (:99-111)
		if value&1 != 0 {
			return c.sendToggle()
		}
	case 2: // REGS_STATUS: only a reply to a status query, which neither the original nor this
		// client sends (MeshCommander has it commented out); measured: AMT 16 never sends it
		// unsolicited, so it carries no information here.
	case 3: // REGS_TOGGLE: Dial evaluates the first reply; later ones are ignored like the original
		if !c.toggled {
			c.toggled, c.toggleV = true, value
		}
	}
	return nil
}

// run is the read loop; it ends the session on the first error.
func (c *Client) run() {
	for {
		if !c.closing.Load() {
			_ = c.raw.SetReadDeadline(time.Now().Add(readTimeout))
		}
		m, err := readMessage(c.br)
		if err == nil {
			err = c.dispatch(m)
		}
		if err != nil {
			c.finish(err)
			return
		}
	}
}

// finish records the first error (ErrClosed if Close ended a healthy session), closes the
// connection and then the Image, and finally closes Done. Only the read loop calls it.
func (c *Client) finish(err error) {
	c.mu.Lock()
	if c.err == nil {
		if c.closing.Load() {
			err = ErrClosed
		}
		c.err = err
	}
	c.mu.Unlock()
	_ = c.raw.SetDeadline(time.Unix(1, 0))
	_ = c.raw.Close() // not c.conn: tls.Conn.Close would block up to 5 s sending close_notify
	c.stopPinger()    // after the past deadline, so a ping blocked in Write returns at once
	imgErr := c.img.Close()
	c.mu.Lock()
	c.imgErr = imgErr
	c.mu.Unlock()
	close(c.done)
}

// Done is closed when the session has ended and the Image is closed.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns nil while the session is active, ErrClosed after Close ended a healthy session,
// ErrClosedByDevice if the device closed it, otherwise the first error that ended it.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Stats returns a snapshot of the counters; it never blocks the read loop.
func (c *Client) Stats() Stats {
	s := Stats{
		Started:   c.started,
		ImageSize: c.size,
		BytesRead: c.bytesRead.Load(),
		Reads:     c.reads.Load(),
	}
	if ns := c.lastRead.Load(); ns != 0 {
		s.LastRead = time.Unix(0, ns)
	}
	return s
}

// Close ends the session and waits for the read loop; it is idempotent and returns the error of
// closing the Image. A running transfer is aborted first (past deadline on the raw connection,
// so a blocked TLS write cannot hold the close).
//
// There is no goodbye message: MeshCommander (amt-ider-ws-0.0.1.js Stop) and ui-toolkit
// (AMTIDER.stop, AMTRedirector.stop) only close the socket, and neither sends a register toggle
// "disable" or a CLOSE (0x42) before. AMT removes the drive when the redirection session ends.
func (c *Client) Close() error {
	c.closing.Store(true)
	_ = c.raw.SetDeadline(time.Unix(1, 0))
	_ = c.raw.Close() // not c.conn: tls.Conn.Close would block up to 5 s sending close_notify
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imgErr
}
