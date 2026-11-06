package kvm

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
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

func dialRedirection(t *testing.T, srv *kvmtest.Server) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Addr)
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
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "Geheim-42"})
	conn := dialRedirection(t, srv)
	if err := redirectionHandshake(conn, "admin", "Geheim-42", nil); err != nil {
		t.Fatal(err)
	}
	if srv.Connections() != 1 || srv.AuthType() != 4 {
		t.Fatalf("connections %d authType %d", srv.Connections(), srv.AuthType())
	}
}

func TestRedirectionHandshake_PicksQOPAmongOthers(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "Geheim-42", AuthTypes: []byte{1, 3, 4}})
	conn := dialRedirection(t, srv)
	if err := redirectionHandshake(conn, "admin", "Geheim-42", nil); err != nil {
		t.Fatal(err)
	}
	if srv.AuthType() != 4 {
		t.Fatalf("authType %d", srv.AuthType())
	}
}

func TestRedirectionHandshake_WrongPassword(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "Geheim-42"})
	conn := dialRedirection(t, srv)
	err := redirectionHandshake(conn, "admin", "falsch-1", nil)
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
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "x", StartStatus: 2})
	conn := dialRedirection(t, srv)
	err := redirectionHandshake(conn, "admin", "x", nil)
	var re *RedirectionError
	if !errors.As(err, &re) || re.Stage != "start" || re.Status != 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectionHandshake_NoDigestOffered(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "x", AuthTypes: []byte{1, 3}})
	conn := dialRedirection(t, srv)
	err := redirectionHandshake(conn, "admin", "x", nil)
	if !errors.Is(err, ErrAuthUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectionHandshake_AuthDataTooLong(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "x", AuthDataLen: 2000})
	conn := dialRedirection(t, srv)
	cw := &countWriter{Conn: conn}
	err := redirectionHandshake(cw, "admin", "x", nil)
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
	err := redirectionHandshake(rw, strings.Repeat("u", 256), "x", nil)
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
	if err := redirectionHandshake(conn, "admin", "x", nil); err == nil {
		t.Fatal("expected error")
	}
}
