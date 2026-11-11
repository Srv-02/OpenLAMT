// Package idertest provides a fake AMT IDE-R server for tests. It performs the redirection
// handshake (digest authentication) server side and then lets a test script the IDE-R peer:
// reply to OPEN_SESSION, send STATUS_DATA, SCSI commands and device-initiated messages, and read
// and decode the client's messages. It deliberately imports neither internal/redir nor
// internal/ider (import cycles with their tests), so all wire values here are written out.
package idertest

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/redir/redirtest"
)

// Options configures the fake server.
type Options struct {
	Username, Password string
	TLS                *tls.Config // nil = plain TCP
	AuthTypes          []byte      // offered in the authentication query reply; nil = {4}
	StartStatus        byte        // StartRedirectionSessionReply status; 0 = success
}

// Server is a running fake redirection server on 127.0.0.1.
type Server struct {
	Addr string // 127.0.0.1:port

	opts   Options
	ln     net.Listener
	wg     sync.WaitGroup
	peers  chan *Peer
	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

const ioTimeout = 5 * time.Second

// Start listens on 127.0.0.1:0. Every connection runs the redirection handshake with the IDER
// start magic; connections that authenticate are handed out by Accept. Server goroutines never
// call t.Fatal/t.Error; t.Cleanup closes the listener and every connection.
func Start(t *testing.T, o Options) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if o.TLS != nil {
		ln = tls.NewListener(ln, o.TLS)
	}
	s := &Server{Addr: ln.Addr().String(), opts: o, ln: ln, peers: make(chan *Peer, 16)}
	s.wg.Add(1)
	go s.acceptLoop()
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		s.closed = true
		for _, c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return s
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			return
		}
		s.conns = append(s.conns, c)
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			_ = c.SetDeadline(time.Now().Add(ioTimeout))
			_, ok := redirtest.Handshake(c, redirtest.MagicIDER, redirtest.Options{
				Username: s.opts.Username, Password: s.opts.Password, Realm: "idertest",
				AuthTypes: s.opts.AuthTypes, StartStatus: s.opts.StartStatus,
			})
			if !ok {
				c.Close()
				return
			}
			_ = c.SetDeadline(time.Time{})
			s.peers <- &Peer{conn: c}
		}()
	}
}

// Accept returns the next connection that completed the redirection handshake (t.Fatal after 5 s).
func (s *Server) Accept(t *testing.T) *Peer {
	t.Helper()
	select {
	case p := <-s.peers:
		return p
	case <-time.After(ioTimeout):
		t.Fatal("idertest: no authenticated connection")
		return nil
	}
}

// AcceptSession accepts a connection and plays the device side of a successful ider.Dial:
// it reads OPEN_SESSION, replies (buffers 8192, protocol 0), reads the register toggle and
// answers STATUS_DATA type 3 with value 1. Run it in its own goroutine while the client dials,
// or after Dial started in one; it blocks until the sequence is done.
func (s *Server) AcceptSession(t *testing.T) *Peer {
	t.Helper()
	p := s.Accept(t)
	p.Read(t)
	p.OpenSessionReply(t, 8192, 8192, 0)
	p.Read(t)
	p.StatusData(t, 3, 1)
	return p
}

// Msg is one message received from the client.
type Msg struct {
	Cmd, Attr byte
	Seq       uint32
	Payload   []byte // everything after the 8-byte header
}

// Peer is the IDE-R device side of one connection. Its methods are for the test goroutine.
type Peer struct {
	conn   net.Conn
	outSeq uint32 // next sequence number we send
	inSeq  uint32 // next sequence number expected from the client
}

// Conn returns the underlying connection.
func (p *Peer) Conn() net.Conn { return p.conn }

// Read returns the client's next message and checks its sequence number (t.Fatal on error or
// after 5 s). Payload sizes are those of the messages an IDE-R client sends.
func (p *Peer) Read(t *testing.T) Msg {
	t.Helper()
	m, err := p.TryRead(ioTimeout)
	if err != nil {
		t.Fatalf("idertest: read: %v", err)
	}
	return m
}

// TryRead is Read with a timeout and without t.Fatal.
func (p *Peer) TryRead(timeout time.Duration) (Msg, error) {
	_ = p.conn.SetReadDeadline(time.Now().Add(timeout))
	var h [8]byte
	if _, err := io.ReadFull(p.conn, h[:]); err != nil {
		return Msg{}, err
	}
	m := Msg{Cmd: h[0], Attr: h[3], Seq: binary.LittleEndian.Uint32(h[4:])}
	var n int
	switch m.Cmd {
	case 0x40:
		n = 10
	case 0x45, 0x47, 0x44, 0x4b:
		n = 0
	case 0x48:
		n = 5
	case 0x51, 0x52:
		n = 23
	case 0x54:
		n = 26
	default:
		return m, &unknownError{m.Cmd}
	}
	m.Payload = make([]byte, n)
	if _, err := io.ReadFull(p.conn, m.Payload); err != nil {
		return m, err
	}
	if m.Cmd == 0x54 { // data follows, length is LE16 at payload offset 1
		data := make([]byte, int(binary.LittleEndian.Uint16(m.Payload[1:3])))
		if _, err := io.ReadFull(p.conn, data); err != nil {
			return m, err
		}
		m.Payload = append(m.Payload, data...)
	}
	if m.Seq != p.inSeq {
		return m, &seqError{m.Seq, p.inSeq}
	}
	p.inSeq++
	return m, nil
}

type unknownError struct{ cmd byte }

func (e *unknownError) Error() string { return fmt.Sprintf("unknown client command 0x%02x", e.cmd) }

type seqError struct{ got, want uint32 }

func (e *seqError) Error() string {
	return fmt.Sprintf("client sequence %d, expected %d", e.got, e.want)
}

// Send sends a device message with the next sequence number.
func (p *Peer) Send(t *testing.T, cmd, attr byte, body []byte) {
	t.Helper()
	p.SendSeq(t, cmd, attr, p.outSeq, body)
	p.outSeq++
}

// SendSeq sends a device message with an explicit sequence number (for sequence error tests);
// it does not advance the peer's own counter.
func (p *Peer) SendSeq(t *testing.T, cmd, attr byte, seq uint32, body []byte) {
	t.Helper()
	b := make([]byte, 8, 8+len(body))
	b[0], b[3] = cmd, attr
	binary.LittleEndian.PutUint32(b[4:], seq)
	b = append(b, body...)
	_ = p.conn.SetWriteDeadline(time.Now().Add(ioTimeout))
	if _, err := p.conn.Write(b); err != nil {
		t.Fatalf("idertest: write: %v", err)
	}
}

// OpenSessionReply sends OPEN_SESSION reply 0x41 (30 bytes): AMT version 1.0, firmware 1.0,
// the given buffer sizes and protocol, IANA 0x157 and no trailing data.
func (p *Peer) OpenSessionReply(t *testing.T, readbfr, writebfr uint16, proto byte) {
	t.Helper()
	body := make([]byte, 22)
	body[0], body[1], body[2], body[3] = 1, 0, 1, 0
	binary.LittleEndian.PutUint16(body[8:], readbfr)
	binary.LittleEndian.PutUint16(body[10:], writebfr)
	body[13] = proto
	binary.LittleEndian.PutUint32(body[17:], 0x157)
	p.Send(t, 0x41, 0, body)
}

// StatusData sends STATUS_DATA 0x49 with the given type and value.
func (p *Peer) StatusData(t *testing.T, typ byte, value uint32) {
	t.Helper()
	body := make([]byte, 5)
	body[0] = typ
	binary.LittleEndian.PutUint32(body[1:], value)
	p.Send(t, 0x49, 0, body)
}

// Written sends COMMAND_WRITTEN 0x50 (28 bytes) with a 12-byte CDB. deviceFlags goes to message
// byte 14 (bit 4 selects the CD, 0xB0; otherwise the floppy, 0xA0), feature to byte 9 (bit 0 DMA).
func (p *Peer) Written(t *testing.T, cdb []byte, deviceFlags, feature byte) {
	t.Helper()
	body := make([]byte, 20)
	body[1] = feature
	body[6] = deviceFlags
	copy(body[8:], cdb)
	p.Send(t, 0x50, 0, body)
}

// Ping sends KEEPALIVEPING 0x44, Pong 0x45, Heartbeat 0x4B, Reset RESETOCCURRED 0x46, Close CLOSE 0x43.
func (p *Peer) Ping(t *testing.T)      { t.Helper(); p.Send(t, 0x44, 0, nil) }
func (p *Peer) Pong(t *testing.T)      { t.Helper(); p.Send(t, 0x45, 0, nil) }
func (p *Peer) Heartbeat(t *testing.T) { t.Helper(); p.Send(t, 0x4b, 0, nil) }
func (p *Peer) Reset(t *testing.T)     { t.Helper(); p.Send(t, 0x46, 0, []byte{1}) }
func (p *Peer) Close(t *testing.T)     { t.Helper(); p.Send(t, 0x43, 0, nil) }

// Disconnect closes the connection.
func (p *Peer) Disconnect() { p.conn.Close() }
