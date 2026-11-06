// Package kvmtest provides a fake AMT redirection server for tests. It deliberately does not
// import internal/kvm (import cycle with that package's tests).
package kvmtest

import (
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Options configures the fake server.
type Options struct {
	Username, Password string
	AuthTypes          []byte        // offered in the query reply; nil = {4}
	StartStatus        byte          // StartRedirectionSessionReply status; 0 = success
	AuthDataLen        int           // pad the query reply's authData to this length (0 = natural)
	TLS                *tls.Config   // nil = plain TCP
	Width, Height      int           // ServerInit; 0 = 128x64
	Name               string        // ServerInit name
	PixelFormat        []byte        // 16 bytes in ServerInit; nil = RGB565 little-endian
	Updates            [][]byte      // byte strings sent in order, one per FramebufferUpdateRequest
	UpdatesPerConn     bool          // every connection starts again at Updates[0] (default: once per server)
	CloseAfterUpdate   int           // close the connection after this many updates (0 = never)
	CloseAfter         time.Duration // close a connection this long after its handshake (0 = never), as AMT does with a black screen
	CloseConns         int           // with CloseAfter: only the first CloseConns connections (0 = all)
}

// Server is a running fake redirection server.
type Server struct {
	Addr string // 127.0.0.1:port

	opts     Options
	ln       net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    []net.Conn
	closed   bool
	nconns   atomic.Int32
	authType atomic.Int32
	updates  atomic.Int32
	msgs     [][]byte // guarded by mu
}

const connDeadline = 10 * time.Second

// Start listens on 127.0.0.1:0; each connection runs the redirection handshake (digest verified
// with its own md5). Server goroutines never call t.Fatal/t.Error; t.Cleanup closes the
// listener and every connection and waits for all goroutines.
func Start(t *testing.T, o Options) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if o.TLS != nil {
		ln = tls.NewListener(ln, o.TLS)
	}
	s := &Server{Addr: ln.Addr().String(), opts: o, ln: ln}
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

// Connections returns the number of completed redirection handshakes.
func (s *Server) Connections() int { return int(s.nconns.Load()) }

// AuthType returns the authType of the last digest message received.
func (s *Server) AuthType() byte { return byte(s.authType.Load()) }

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
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(connDeadline))
			if s.handshake(c) {
				if n := int(s.nconns.Load()); s.opts.CloseAfter > 0 && (s.opts.CloseConns == 0 || n <= s.opts.CloseConns) {
					t := time.AfterFunc(s.opts.CloseAfter, func() { c.Close() })
					defer t.Stop()
				}
				s.rfb(c)
			}
		}()
	}
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func challenge(status byte, nonce string) []byte {
	var data []byte
	for _, f := range []string{"kvmtest", nonce, "auth"} {
		data = append(data, byte(len(f)))
		data = append(data, f...)
	}
	return authReply(status, 4, data)
}

func authReply(status, authType byte, data []byte) []byte {
	b := []byte{0x14, status, 0, 0, authType, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[5:], uint32(len(data)))
	return append(b, data...)
}

// readAuth reads one authentication message: 9-byte header plus LE32 length bytes.
func readAuth(c net.Conn) (hdr [9]byte, body []byte, ok bool) {
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return
	}
	n := binary.LittleEndian.Uint32(hdr[5:])
	if n > 4096 {
		return
	}
	body = make([]byte, n)
	if _, err := io.ReadFull(c, body); err != nil {
		return
	}
	return hdr, body, true
}

// handshake runs the redirection protocol; it reports whether the client is authenticated
// and the KVM start exchange completed.
func (s *Server) handshake(c net.Conn) bool {
	start := make([]byte, 8)
	if _, err := io.ReadFull(c, start); err != nil || !bytes.Equal(start, []byte{0x10, 0x01, 0x00, 0x00, 'K', 'V', 'M', 'R'}) {
		return false
	}
	reply := make([]byte, 13)
	reply[0], reply[1] = 0x11, s.opts.StartStatus
	if _, err := c.Write(reply); err != nil || s.opts.StartStatus != 0 {
		return false
	}

	hdr, _, ok := readAuth(c)
	if !ok || hdr[0] != 0x13 || hdr[4] != 0 {
		return false
	}
	types := s.opts.AuthTypes
	if types == nil {
		types = []byte{4}
	}
	data := append([]byte(nil), types...)
	if s.opts.AuthDataLen > len(data) {
		data = append(data, make([]byte, s.opts.AuthDataLen-len(data))...)
	}
	if _, err := c.Write(authReply(0, 0, data)); err != nil {
		return false
	}

	// User message, then challenge.
	hdr, ubody, ok := readAuth(c)
	if !ok || hdr[0] != 0x13 {
		return false
	}
	wantUser := append([]byte{byte(len(s.opts.Username))}, s.opts.Username...)
	wantUser = append(wantUser, 0, 0, byte(len("/RedirectionService")))
	wantUser = append(wantUser, "/RedirectionService"...)
	wantUser = append(wantUser, 0, 0, 0, 0)
	userOK := hdr[4] == 4 && bytes.Equal(ubody, wantUser)
	const nonce = "0123456789abcdef"
	if _, err := c.Write(challenge(1, nonce)); err != nil {
		return false
	}

	// Digest message: user, realm, nonce, uri, cnonce, nc, response, qop.
	hdr, body, ok := readAuth(c)
	if !ok || hdr[0] != 0x13 {
		return false
	}
	s.authType.Store(int32(hdr[4]))
	var f [8]string
	for i := range f {
		if len(body) < 1 || len(body) < 1+int(body[0]) {
			return false
		}
		f[i] = string(body[1 : 1+int(body[0])])
		body = body[1+int(body[0]):]
	}
	ha1 := md5hex(s.opts.Username + ":" + "kvmtest" + ":" + s.opts.Password)
	ha2 := md5hex("POST:" + f[3])
	want := md5hex(ha1 + ":" + nonce + ":" + f[5] + ":" + f[4] + ":" + f[7] + ":" + ha2)
	strict := userOK && f[3] == "/RedirectionService" && f[5] == "00000001" && f[7] == "auth" && len(f[4]) == 32
	if !strict || f[0] != s.opts.Username || f[1] != "kvmtest" || f[2] != nonce || f[6] != want {
		// Like AMT: a fresh challenge with status 1, then the connection closes.
		_, _ = c.Write(challenge(1, "fedcba9876543210"))
		return false
	}
	if _, err := c.Write(authReply(0, 4, nil)); err != nil {
		return false
	}

	kvm := make([]byte, 8)
	if _, err := io.ReadFull(c, kvm); err != nil || kvm[0] != 0x40 {
		return false
	}
	// Count before replying: the client returns as soon as it reads 0x41.
	s.nconns.Add(1)
	_, err := c.Write([]byte{0x41, 0, 0, 0, 0, 0, 0, 0})
	return err == nil
}
