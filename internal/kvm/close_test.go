package kvm

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// stallProxy relays TCP to addr until Stall is called; from then on it stops reading from the
// client, so the client's send buffer fills up like with a stalled device.
type stallProxy struct {
	Addr    string
	stalled atomic.Bool
	release chan struct{}
}

func (p *stallProxy) Stall() { p.stalled.Store(true) }

func newStallProxy(t *testing.T, addr string) *stallProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &stallProxy{Addr: ln.Addr().String(), release: make(chan struct{})}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		up, err := net.Dial("tcp", addr)
		if err != nil {
			c.Close()
			return
		}
		go func() { _, _ = io.Copy(c, up) }()
		buf := make([]byte, 4096)
		for {
			if p.stalled.Load() {
				<-p.release
				break
			}
			_ = c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
			n, err := c.Read(buf)
			if n > 0 {
				_, _ = up.Write(buf[:n])
			}
			if ne, ok := err.(net.Error); err != nil && !(ok && ne.Timeout()) {
				break
			}
		}
		c.Close()
		up.Close()
	}()
	t.Cleanup(func() { close(p.release); ln.Close() })
	return p
}

// Close must not wait for the TLS close_notify (5 s write deadline) when the peer stopped
// reading and the send buffer is full.
func TestClient_CloseDoesNotWaitForCloseNotify(t *testing.T) {
	cert := selfSignedCert(t)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{cert}}})
	proxy := newStallProxy(t, srv.Addr)
	cfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	var raw net.Conn
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		raw = conn
		return conn, err
	}
	c, err := Dial(context.Background(), Config{Addr: proxy.Addr, Username: "admin", Password: "pw", TLS: cfg, DialContext: dial})
	if err != nil {
		t.Fatal(err)
	}
	proxy.Stall()
	time.Sleep(100 * time.Millisecond) // let the proxy see the flag

	fillSendBuffer(t, raw)

	start := time.Now()
	_ = c.Close()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Close took %v with a stalled peer, want well under the 5 s close_notify deadline", d)
	}
}

// fillSendBuffer writes garbage straight to the raw connection until its send buffer is
// completely full, so that no TLS write is in flight but the next write blocks. The peer does
// not read.
func fillSendBuffer(t *testing.T, raw net.Conn) {
	t.Helper()
	chunk := make([]byte, 64<<10)
	for _, size := range []int{len(chunk), 1} {
		for {
			_ = raw.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			if _, err := raw.Write(chunk[:size]); err != nil {
				break
			}
		}
	}
	_ = raw.SetWriteDeadline(time.Time{})
}
