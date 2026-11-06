// Session logic ported from MeshCommander (github.com/Ylianst/MeshCommander), amt-desktop-0.0.2.js
// (ProcessData state 3) and index.html. MeshCommander by Ylian Saint-Hilaire and contributors.
// SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"image"
	"io"
	"net"
	"sync"
	"time"
)

// Config describes one redirection session; the caller (internal/amt) wires TLS pinning.
type Config struct {
	Addr        string // host:port, 16994 (plain) or 16995 (TLS)
	Username    string
	Password    string
	Quality     Quality                                                           // zero value High
	AMTMajor    int                                                               // AMT major version (CIM_SoftwareIdentity), 0 = unknown
	TLS         *tls.Config                                                       // nil = plain TCP
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error) // nil = net.Dialer
}

const (
	handshakeTimeout = 30 * time.Second // used when ctx has no deadline
	writeTimeout     = 10 * time.Second
)

// ErrClosed is the session error after Close.
var ErrClosed = errors.New("kvm: session closed")

// Client is one authenticated KVM session with a running read loop.
type Client struct {
	conn    net.Conn
	wmu     sync.Mutex
	fb      *Framebuffer
	name    string
	quality Quality
	started time.Time // end of the handshakes, MeshCommander's connectTime

	mu      sync.Mutex
	seq     uint64
	frameCh chan struct{} // closed and replaced on every completed frame, guarded by mu with seq
	done    chan struct{}
	err     error
	closed  bool

	inMu    sync.Mutex     // input.go: makes pressed-state change and write one step; taken before mu and wmu
	pressed map[Key]uint32 // used by input.go, guarded by inMu
}

// Dial connects, authenticates, completes the RFB handshake and continues like MeshCommander
// after ServerInit (amt-desktop-0.0.2.js ProcessData state 3, index.html state 3): on AMT 12
// and later it queues the "present" message for the data channel ack, on AMT > 15 it sends
// KvmExtCmd 4,0 (zlib off), then the first FramebufferUpdateRequest (incremental, as
// _SendRefresh) and starts the read loop. ctx bounds dial and handshakes only; cancelling it
// aborts a blocked handshake read.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, handshakeTimeout)
		defer cancel()
	}
	dial := cfg.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	raw, err := dial(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, err
	}
	conn := net.Conn(raw)
	// The deadline goes on the raw connection; it also aborts blocked TLS reads and writes.
	stop := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Unix(1, 0)) })
	fail := func(err error) (*Client, error) {
		stop()
		conn.Close()
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, err
	}

	if cfg.TLS != nil {
		tc := cfg.TLS.Clone()
		if tc.ServerName == "" {
			if host, _, err := net.SplitHostPort(cfg.Addr); err == nil {
				tc.ServerName = host
			}
		}
		tconn := tls.Client(conn, tc)
		if err := tconn.HandshakeContext(ctx); err != nil {
			return fail(err)
		}
		conn = tconn
	}

	br := bufio.NewReader(conn)
	if err := redirectionHandshake(struct {
		io.Reader
		io.Writer
	}{br, conn}, cfg.Username, cfg.Password, nil); err != nil {
		return fail(err)
	}
	w, h, name, err := rfbHandshake(br, conn, cfg.Quality)
	if err != nil {
		return fail(err)
	}
	dc := newDataChannel()
	if cfg.AMTMajor >= 12 {
		dc.pending = append(dc.pending, presentMessage) // sendKvmData before the ack only queues
	}
	if cfg.AMTMajor > 15 {
		if _, err := conn.Write(kvmExtCmd(4, 0)); err != nil {
			return fail(err)
		}
	}
	if _, err := conn.Write(framebufferUpdateRequest(true, 0, 0, w, h)); err != nil {
		return fail(err)
	}
	if !stop() { // ctx fired after the handshakes: treat as cancelled
		conn.Close()
		return nil, ctx.Err()
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}

	c := &Client{
		conn: conn, fb: newFramebuffer(w, h), name: name, quality: cfg.Quality, started: time.Now(),
		frameCh: make(chan struct{}), done: make(chan struct{}),
	}
	s := newSession(br, c.send, c.fb, cfg.Quality)
	s.dc = dc
	go c.readLoop(s)
	return c, nil
}

func (c *Client) readLoop(s *session) {
	for {
		done, err := s.readMessage()
		if err != nil { // the inflater window is stale after an error: always end the session
			c.finish(err)
			return
		}
		if done {
			c.mu.Lock()
			c.seq++
			close(c.frameCh)
			c.frameCh = make(chan struct{})
			c.mu.Unlock()
			w, h := c.fb.Size()
			if err := c.send(framebufferUpdateRequest(true, 0, 0, w, h)); err != nil {
				c.finish(err) // no request, no more updates: the read would block forever
				return
			}
		}
	}
}

// finish records the session error (ErrClosed if Close ended it), closes Done and the connection.
func (c *Client) finish(err error) {
	c.mu.Lock()
	select {
	case <-c.done:
	default:
		if c.closed {
			err = ErrClosed
		}
		c.err = err
		close(c.done)
	}
	c.mu.Unlock()
	c.conn.Close()
}

// Size returns the current screen size.
func (c *Client) Size() (w, h int) { return c.fb.Size() }

// Started returns when the session was established (handshakes done).
func (c *Client) Started() time.Time { return c.started }

// Name returns the desktop name from ServerInit.
func (c *Client) Name() string { return c.name }

// Quality returns the negotiated quality.
func (c *Client) Quality() Quality { return c.quality }

// Snapshot returns a copy of the last image, also after the session ended.
func (c *Client) Snapshot() *image.RGBA { return c.fb.Snapshot() }

// SnapshotInto copies the last image into dst (reused when the size matches, see
// Framebuffer.SnapshotInto) and returns it.
func (c *Client) SnapshotInto(dst *image.RGBA) *image.RGBA { return c.fb.SnapshotInto(dst) }

// Frame returns the number of complete FramebufferUpdates received so far.
func (c *Client) Frame() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}

// WaitFrame returns as soon as Frame() > after (immediately if already true), with the new count;
// otherwise the ctx error or the session error (ErrClosed if Close ended the session,
// otherwise the error that ended it).
func (c *Client) WaitFrame(ctx context.Context, after uint64) (uint64, error) {
	for {
		c.mu.Lock()
		if c.seq > after {
			seq := c.seq
			c.mu.Unlock()
			return seq, nil
		}
		ch := c.frameCh
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return c.Frame(), ctx.Err()
		case <-c.done:
			return c.Frame(), c.Err()
		}
	}
}

// RequestFullUpdate asks for the whole screen non-incrementally (Encoding.ts:78-82).
func (c *Client) RequestFullUpdate() error {
	w, h := c.fb.Size()
	return c.send(framebufferUpdateRequest(false, 0, 0, w, h))
}

// Done is closed when the session has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns nil while running, ErrClosed if Close ended the session, otherwise the error
// that ended it.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the session and waits for the read loop; it is idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.conn.Close()
	<-c.done
	return nil
}

func (c *Client) send(msg []byte) error {
	select {
	case <-c.done:
		return c.Err()
	default:
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := c.conn.Write(msg)
	if err == nil {
		return nil
	}
	// A failed write may be the result of Close or of the session ending; report that instead
	// of the raw "use of closed network connection".
	c.mu.Lock()
	closed = c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	select {
	case <-c.done:
		return c.Err()
	default:
	}
	// A failed send ends the session, so later sends return at once instead of blocking
	// on a dead link until the write timeout.
	c.finish(err)
	return err
}
