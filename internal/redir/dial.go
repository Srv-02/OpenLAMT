package redir

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"time"
)

// HandshakeTimeout bounds Dial when the context has no deadline.
const HandshakeTimeout = 30 * time.Second

// Config describes one redirection connection; the caller (internal/amt) wires TLS pinning.
type Config struct {
	Addr        string // host:port, 16994 (plain) or 16995 (TLS)
	Username    string
	Password    string
	TLS         *tls.Config                                                       // nil = plain TCP
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error) // nil = net.Dialer
}

// Conn is an authenticated redirection connection.
type Conn struct {
	net.Conn               // TLS or plain connection; write to this
	R        *bufio.Reader // reads from Conn; may already hold bytes that followed the handshake
	Raw      net.Conn      // underlying TCP connection, for deadlines that also abort TLS I/O
}

// Dial connects, optionally starts TLS (ServerName defaults to the host of Addr), runs the
// redirection handshake for p and then after (may be nil) for the service's own start exchange.
// The whole sequence runs under ctx (30 s if it has no deadline): cancelling ctx aborts blocked
// reads and writes through a past deadline on the raw connection. The deadline is cleared only
// after after succeeded. On error the connection is closed and the ctx error is returned if
// ctx ended.
func Dial(ctx context.Context, cfg Config, p Protocol, after func(r *bufio.Reader, w io.Writer) error) (*Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, HandshakeTimeout)
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
	fail := func(err error) (*Conn, error) {
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
	if err := Handshake(struct {
		io.Reader
		io.Writer
	}{br, conn}, p, cfg.Username, cfg.Password, nil); err != nil {
		return fail(err)
	}
	if after != nil {
		if err := after(br, conn); err != nil {
			return fail(err)
		}
	}
	if !stop() { // ctx fired after the handshakes: treat as cancelled
		conn.Close()
		return nil, ctx.Err()
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return &Conn{Conn: conn, R: br, Raw: raw}, nil
}
