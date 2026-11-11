// Package kvmtest provides a fake AMT redirection server for tests. It deliberately does not
// import internal/kvm (import cycle with that package's tests).
package kvmtest

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/redir/redirtest"
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

// handshake runs the redirection protocol; it reports whether the client is authenticated
// and the KVM start exchange completed.
func (s *Server) handshake(c net.Conn) bool {
	authType, ok := redirtest.Handshake(c, redirtest.MagicKVM, redirtest.Options{
		Username: s.opts.Username, Password: s.opts.Password, Realm: "kvmtest",
		AuthTypes: s.opts.AuthTypes, StartStatus: s.opts.StartStatus, AuthDataLen: s.opts.AuthDataLen,
	})
	if authType != 0 {
		s.authType.Store(int32(authType))
	}
	if !ok {
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
