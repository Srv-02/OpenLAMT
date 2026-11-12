// Package soltest provides a fake AMT Serial-over-LAN server for tests. It performs the
// redirection handshake (digest authentication) server side and then lets a test script the SOL
// peer: answer START_SOL, send host output, serial port reports and heartbeats, and read and
// decode the client's messages. It deliberately imports neither internal/redir nor internal/sol
// (import cycles with their tests), so all wire values here are written out.
package soltest

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

// Start listens on 127.0.0.1:0. Every connection runs the redirection handshake with the SOL
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
			_, ok := redirtest.Handshake(c, redirtest.MagicSOL, redirtest.Options{
				Username: s.opts.Username, Password: s.opts.Password, Realm: "soltest",
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
		t.Fatal("soltest: no authenticated connection")
		return nil
	}
}

// AcceptSession accepts a connection and plays the device side of a successful sol.Dial: it
// reads START_SOL (0x20), replies START_SOL_REPLY (0x21) with status 0 and reads the 0x27 the
// client sends next. Run it in its own goroutine while the client dials.
func (s *Server) AcceptSession(t *testing.T) *Peer {
	t.Helper()
	p := s.Accept(t)
	p.Expect(t, 0x20)
	p.StartReply(t, 0)
	p.Expect(t, 0x27)
	return p
}

// Msg is one message received from the client.
type Msg struct {
	Cmd     byte
	Seq     uint32 // 0 for END_REDIRECTION (0x12), which has no sequence number
	Payload []byte // everything after the 8-byte header; for 0x28 the LE16 length and the data
}

// Data returns the data of a DATA_TO_HOST message (0x28) without its length field.
func (m Msg) Data() []byte {
	if m.Cmd != 0x28 || len(m.Payload) < 2 {
		return nil
	}
	return m.Payload[2:]
}

// Peer is the SOL device side of one connection. Its methods are for the test goroutine.
type Peer struct {
	conn   net.Conn
	outSeq uint32
}

// Conn returns the underlying connection.
func (p *Peer) Conn() net.Conn { return p.conn }

// Read returns the client's next message (t.Fatal on error or after 5 s).
func (p *Peer) Read(t *testing.T) Msg {
	t.Helper()
	m, err := p.TryRead(ioTimeout)
	if err != nil {
		t.Fatalf("soltest: read: %v", err)
	}
	return m
}

// Expect reads the next message that is not a heartbeat (0x2B) and fails unless it is cmd.
func (p *Peer) Expect(t *testing.T, cmd byte) Msg {
	t.Helper()
	for {
		m := p.Read(t)
		if m.Cmd == 0x2b && cmd != 0x2b {
			continue
		}
		if m.Cmd != cmd {
			t.Fatalf("soltest: got command 0x%02x, want 0x%02x", m.Cmd, cmd)
		}
		return m
	}
}

// TryRead is Read with a timeout and without t.Fatal. Sizes are those of the messages a SOL
// client sends: 0x20 (24 bytes), 0x27 (14), 0x28 (10 + n), 0x2B and 0x22 (8), 0x12 (4).
func (p *Peer) TryRead(timeout time.Duration) (Msg, error) {
	_ = p.conn.SetReadDeadline(time.Now().Add(timeout))
	var cmd [1]byte
	if _, err := io.ReadFull(p.conn, cmd[:]); err != nil {
		return Msg{}, err
	}
	m := Msg{Cmd: cmd[0]}
	if m.Cmd == 0x12 {
		rest := make([]byte, 3)
		if _, err := io.ReadFull(p.conn, rest); err != nil {
			return m, err
		}
		m.Payload = rest
		return m, nil
	}
	var h [7]byte
	if _, err := io.ReadFull(p.conn, h[:]); err != nil {
		return m, err
	}
	m.Seq = binary.LittleEndian.Uint32(h[3:])
	var n int
	switch m.Cmd {
	case 0x20:
		n = 16
	case 0x27:
		n = 6
	case 0x28:
		n = 2
	case 0x2b, 0x22:
		n = 0
	default:
		return m, fmt.Errorf("soltest: unknown client command 0x%02x", m.Cmd)
	}
	m.Payload = make([]byte, n)
	if _, err := io.ReadFull(p.conn, m.Payload); err != nil {
		return m, err
	}
	if m.Cmd == 0x28 {
		data := make([]byte, int(binary.LittleEndian.Uint16(m.Payload)))
		if _, err := io.ReadFull(p.conn, data); err != nil {
			return m, err
		}
		m.Payload = append(m.Payload, data...)
	}
	return m, nil
}

// Message returns the bytes of a device message with header and the next sequence number, for
// tests that send several messages in one write or split them.
func (p *Peer) Message(cmd byte, body []byte) []byte {
	b := make([]byte, 8, 8+len(body))
	b[0] = cmd
	binary.LittleEndian.PutUint32(b[4:], p.outSeq)
	p.outSeq++
	return append(b, body...)
}

// Write sends raw bytes.
func (p *Peer) Write(t *testing.T, b []byte) {
	t.Helper()
	_ = p.conn.SetWriteDeadline(time.Now().Add(ioTimeout))
	if _, err := p.conn.Write(b); err != nil {
		t.Fatalf("soltest: write: %v", err)
	}
}

// StartReplyMsg is START_SOL_REPLY 0x21 (23 bytes) with the given status and the host settings
// AMT echoes (MaxTxBuffer 10000, TxTimeout 100, RxTimeout 10000, RxFlushTimeout 100).
func (p *Peer) StartReplyMsg(status byte) []byte {
	body := make([]byte, 15)
	binary.LittleEndian.PutUint16(body[0:], 10000)
	binary.LittleEndian.PutUint16(body[2:], 100)
	binary.LittleEndian.PutUint16(body[6:], 10000)
	binary.LittleEndian.PutUint16(body[8:], 100)
	m := p.Message(0x21, body)
	m[1] = status
	return m
}

// StartReply sends START_SOL_REPLY with the given status.
func (p *Peer) StartReply(t *testing.T, status byte) { t.Helper(); p.Write(t, p.StartReplyMsg(status)) }

// DataMsg is DATA_FROM_HOST 0x2A: LE16 length and data.
func (p *Peer) DataMsg(data []byte) []byte {
	body := make([]byte, 2, 2+len(data))
	binary.LittleEndian.PutUint16(body, uint16(len(data)))
	return p.Message(0x2a, append(body, data...))
}

// Data sends host output.
func (p *Peer) Data(t *testing.T, data []byte) { t.Helper(); p.Write(t, p.DataMsg(data)) }

// ControlsMsg is CONTROLS_FROM_HOST 0x29 (10 bytes): control lines and status bits.
func (p *Peer) ControlsMsg(control, status byte) []byte {
	return p.Message(0x29, []byte{control, status})
}

// Controls sends CONTROLS_FROM_HOST.
func (p *Peer) Controls(t *testing.T, control, status byte) {
	t.Helper()
	p.Write(t, p.ControlsMsg(control, status))
}

// Heartbeat sends HEARTBEAT 0x2B, EndReply END_SOL_REPLY 0x23 (both 8 bytes).
func (p *Peer) Heartbeat(t *testing.T) { t.Helper(); p.Write(t, p.Message(0x2b, nil)) }
func (p *Peer) EndReply(t *testing.T)  { t.Helper(); p.Write(t, p.Message(0x23, nil)) }

// Disconnect closes the connection.
func (p *Peer) Disconnect() { p.conn.Close() }
