package redir

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/redir/redirtest"
)

func TestDigestResponse(t *testing.T) {
	// RFC 2617 section 3.5 example values with method POST (HA1 939e7578ed9e3c518a452acee763bce9,
	// HA2 = md5("POST:/dir/index.html") = c0b64819c3e244af0be89086df86e3fa); computed with md5sum.
	got := digestResponse("Mufasa", "testrealm@host.com", "Circle Of Life", "dcd98b7102dd2f0e8b11d0f600bfb0c093",
		"/dir/index.html", "0a4f113b", "00000001", "auth")
	if got != "440c5a7b9ed304fecd2ddd39c9c7b726" {
		t.Fatalf("got %s", got)
	}
}

func TestCnonceFormat(t *testing.T) {
	fixed := bytes.Repeat([]byte{0xab, 0x01}, 8)
	got, err := cnonce(bytes.NewReader(fixed))
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("ab01", 8) {
		t.Fatalf("got %q", got)
	}
	r, err := cnonce(nil)
	if err != nil || len(r) != cnonceLen || strings.ToLower(r) != r {
		t.Fatalf("random cnonce %q err %v", r, err)
	}
}

type testServer struct {
	ln       net.Listener
	conns    atomic.Int32
	authType atomic.Int32
	wg       sync.WaitGroup
}

func (s *testServer) Connections() int { return int(s.conns.Load()) }
func (s *testServer) AuthType() byte   { return byte(s.authType.Load()) }

// startServer serves one redirection handshake per connection (magic is the expected start).
func startServer(t *testing.T, magic []byte, o redirtest.Options) *testServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testServer{ln: ln}
	t.Cleanup(func() { ln.Close(); s.wg.Wait() })
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				at, ok := redirtest.Handshake(c, magic, o)
				if at != 0 {
					s.authType.Store(int32(at))
				}
				if ok {
					s.conns.Add(1)
					_, _ = io.Copy(io.Discard, c) // keep the connection until the client closes
				}
			}()
		}
	}()
	return s
}

// waitFor polls cond for up to 2 s; the server records its counters after the client returned.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *testServer) addr() string { return s.ln.Addr().String() }

func dialRedirection(t *testing.T, srv *testServer) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestRedirectionHandshake_Digest(t *testing.T) {
	srv := startServer(t, redirtest.MagicKVM, redirtest.Options{Username: "admin", Password: "Geheim-42"})
	conn := dialRedirection(t, srv)
	if err := Handshake(conn, KVM, "admin", "Geheim-42", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return srv.Connections() == 1 && srv.AuthType() == 4 })
}

func TestRedirectionHandshake_PicksQOPAmongOthers(t *testing.T) {
	srv := startServer(t, redirtest.MagicKVM, redirtest.Options{Username: "admin", Password: "Geheim-42", AuthTypes: []byte{1, 3, 4}})
	conn := dialRedirection(t, srv)
	if err := Handshake(conn, KVM, "admin", "Geheim-42", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return srv.AuthType() == 4 })
}

func TestRedirectionHandshake_WrongPassword(t *testing.T) {
	srv := startServer(t, redirtest.MagicKVM, redirtest.Options{Username: "admin", Password: "Geheim-42"})
	conn := dialRedirection(t, srv)
	err := Handshake(conn, KVM, "admin", "falsch-1", nil)
	var re *RedirectionError
	if !errors.As(err, &re) || re.Stage != "auth" || re.Status != 1 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "falsch-1") {
		t.Fatal("password in error")
	}
	if srv.Connections() != 0 {
		t.Fatalf("connections %d", srv.Connections())
	}
}

func TestRedirectionHandshake_StartRejected(t *testing.T) {
	srv := startServer(t, redirtest.MagicKVM, redirtest.Options{Username: "admin", Password: "x", StartStatus: 2})
	conn := dialRedirection(t, srv)
	err := Handshake(conn, KVM, "admin", "x", nil)
	var re *RedirectionError
	if !errors.As(err, &re) || re.Stage != "start" || re.Status != 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectionHandshake_NoDigestOffered(t *testing.T) {
	srv := startServer(t, redirtest.MagicKVM, redirtest.Options{Username: "admin", Password: "x", AuthTypes: []byte{1, 3}})
	conn := dialRedirection(t, srv)
	err := Handshake(conn, KVM, "admin", "x", nil)
	if !errors.Is(err, ErrAuthUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectionHandshake_AuthDataTooLong(t *testing.T) {
	srv := startServer(t, redirtest.MagicKVM, redirtest.Options{Username: "admin", Password: "x", AuthDataLen: 2000})
	conn := dialRedirection(t, srv)
	cw := &countWriter{Conn: conn}
	err := Handshake(cw, KVM, "admin", "x", nil)
	if !errors.Is(err, ErrAuthDataTooLong) {
		t.Fatalf("err = %v", err)
	}
	// Only the 8-byte start and the 9-byte query may have been written.
	if cw.n != 8+9 {
		t.Fatalf("%d bytes written, want 17", cw.n)
	}
}

type countWriter struct {
	net.Conn
	n int
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	return c.Conn.Write(p)
}

func TestRedirectionHandshake_UserTooLong(t *testing.T) {
	var buf bytes.Buffer
	rw := struct {
		*bytes.Buffer
	}{&buf}
	err := Handshake(rw, KVM, strings.Repeat("u", 256), "x", nil)
	if !errors.Is(err, ErrFieldTooLong) {
		t.Fatalf("err = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes written", buf.Len())
	}
}

func TestRedirectionHandshake_Truncated(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 8)
		if _, err := c.Read(buf); err != nil {
			return
		}
		_, _ = c.Write(append([]byte{0x11, 0}, make([]byte, 11)...))
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := Handshake(conn, KVM, "admin", "x", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestHandshake_IDERMagic(t *testing.T) {
	srv := startServer(t, redirtest.MagicIDER, redirtest.Options{Username: "admin", Password: "pw"})
	conn := dialRedirection(t, srv)
	if err := Handshake(conn, IDER, "admin", "pw", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return srv.Connections() == 1 })
}

func TestHandshake_SOLMagic(t *testing.T) {
	srv := startServer(t, redirtest.MagicSOL, redirtest.Options{Username: "admin", Password: "pw"})
	conn := dialRedirection(t, srv)
	if err := Handshake(conn, SOL, "admin", "pw", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return srv.Connections() == 1 })
}

func TestHandshake_WrongMagicRejected(t *testing.T) {
	// A KVM-only server does not answer the IDER start: the client sees the closed connection.
	srv := startServer(t, redirtest.MagicKVM, redirtest.Options{Username: "admin", Password: "pw"})
	conn := dialRedirection(t, srv)
	if err := Handshake(conn, IDER, "admin", "pw", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestHandshake_UnknownProtocol(t *testing.T) {
	var buf bytes.Buffer
	if err := Handshake(&buf, Protocol(99), "a", "b", nil); err == nil || buf.Len() != 0 {
		t.Fatalf("err %v, %d bytes written", err, buf.Len())
	}
}

func TestStartMagics(t *testing.T) {
	k, _ := KVM.startMagic()
	i, _ := IDER.startMagic()
	s, _ := SOL.startMagic()
	if !bytes.Equal(k, []byte("\x10\x01\x00\x00KVMR")) || !bytes.Equal(i, []byte("\x10\x00\x00\x00IDER")) ||
		!bytes.Equal(s, []byte("\x10\x00\x00\x00SOL ")) {
		t.Fatalf("%x %x %x", k, i, s)
	}
}

func TestDial_AfterRunsUnderContext(t *testing.T) {
	srv := startServer(t, redirtest.MagicIDER, redirtest.Options{Username: "admin", Password: "pw"})
	var gotR *bufio.Reader
	c, err := Dial(context.Background(), Config{Addr: srv.addr(), Username: "admin", Password: "pw"}, IDER,
		func(r *bufio.Reader, w io.Writer) error {
			gotR = r
			_, err := w.Write([]byte{0x40})
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.R != gotR || c.Raw == nil || c.Conn == nil {
		t.Fatalf("conn %+v", c)
	}
	// The deadline was cleared: a read waits (and times out only by our own deadline).
	_ = c.Raw.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := c.R.ReadByte(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestDial_AfterErrorClosesConn(t *testing.T) {
	srv := startServer(t, redirtest.MagicIDER, redirtest.Options{Username: "admin", Password: "pw"})
	boom := errors.New("boom")
	c, err := Dial(context.Background(), Config{Addr: srv.addr(), Username: "admin", Password: "pw"}, IDER,
		func(*bufio.Reader, io.Writer) error { return boom })
	if c != nil || !errors.Is(err, boom) {
		t.Fatalf("conn %v err %v", c, err)
	}
}

func TestDial_ContextCancelAbortsAfter(t *testing.T) {
	srv := startServer(t, redirtest.MagicIDER, redirtest.Options{Username: "admin", Password: "pw"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Dial(ctx, Config{Addr: srv.addr(), Username: "admin", Password: "pw"}, IDER,
			func(r *bufio.Reader, w io.Writer) error {
				_, err := r.ReadByte() // the server never sends anything: blocks
				return err
			})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dial did not abort")
	}
}

func TestDial_DeadlineAbortsHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			_, _ = io.Copy(io.Discard, c) // accepts, never answers
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = Dial(ctx, Config{Addr: ln.Addr().String(), Username: "a", Password: "b"}, KVM, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
