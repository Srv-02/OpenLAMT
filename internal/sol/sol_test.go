package sol

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/sol/soltest"
)

func TestMain(m *testing.M) {
	// No keepalive noise unless a test asks for it.
	keepaliveInterval = time.Hour
	// Most fake peers never answer END_SOL; do not wait a second for each.
	closeTimeout = 100 * time.Millisecond
	os.Exit(m.Run())
}

func cfg(srv *soltest.Server) Config {
	return Config{Addr: srv.Addr, Username: "admin", Password: "pw"}
}

// dial runs Dial against srv while the peer plays a successful start.
func dial(t *testing.T, srv *soltest.Server) (*Client, *soltest.Peer) {
	t.Helper()
	peer := make(chan *soltest.Peer, 1)
	go func() { peer <- srv.AcceptSession(t) }()
	c, err := Dial(context.Background(), cfg(srv))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, <-peer
}

func start(t *testing.T) (*Client, *soltest.Peer) {
	t.Helper()
	return dial(t, soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"}))
}

// readN reads exactly n bytes from c with a timeout.
func readN(t *testing.T, c *Client, n int) []byte {
	t.Helper()
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		b := make([]byte, n)
		_, err := io.ReadFull(c, b)
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read: %v", r.err)
		}
		return r.b
	case <-time.After(5 * time.Second):
		t.Fatal("read timed out")
		return nil
	}
}

func waitDone(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
}

func TestDial_StartSequenceBytes(t *testing.T) {
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	type got struct{ start, setup soltest.Msg }
	ch := make(chan got, 1)
	go func() {
		p := srv.Accept(t)
		s := p.Read(t)
		p.StartReply(t, 0)
		ch <- got{s, p.Read(t)}
	}()
	c, err := Dial(context.Background(), cfg(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	g := <-ch
	// START_SOL (AMTRedirector.ts:393-414): MaxTxBuffer 10000, TxTimeout 100, TxOverflowTimeout 0,
	// RxTimeout 10000, RxFlushTimeout 100, Heartbeat 0, four zero bytes.
	want := []byte{0x10, 0x27, 100, 0, 0, 0, 0x10, 0x27, 100, 0, 0, 0, 0, 0, 0, 0}
	if g.start.Cmd != 0x20 || !bytes.Equal(g.start.Payload, want) {
		t.Fatalf("start %02x % x", g.start.Cmd, g.start.Payload)
	}
	if g.setup.Cmd != 0x27 || !bytes.Equal(g.setup.Payload, []byte{0, 0, 0x1b, 0, 0, 0}) {
		t.Fatalf("setup %02x % x", g.setup.Cmd, g.setup.Payload)
	}
	if g.start.Seq != 1 || g.setup.Seq != 2 {
		t.Fatalf("sequence %d %d", g.start.Seq, g.setup.Seq)
	}
	if c.Err() != nil {
		t.Fatalf("err = %v", c.Err())
	}
	select {
	case <-c.Done():
		t.Fatal("done after dial")
	default:
	}
}

func TestDial_StartRejected(t *testing.T) {
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	go func() {
		p := srv.Accept(t)
		p.Expect(t, 0x20)
		p.StartReply(t, 2)
	}()
	_, err := Dial(context.Background(), cfg(srv))
	if !errors.Is(err, ErrStartFailed) || !strings.Contains(err.Error(), "status 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestDial_WrongPassword(t *testing.T) {
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	_, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "wrong"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDial_ContextCancelWhileWaitingForReply(t *testing.T) {
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	go func() {
		p := srv.Accept(t)
		p.Expect(t, 0x20) // never answers
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, err := Dial(ctx, cfg(srv))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(t0) > 3*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(t0))
	}
}

func TestDial_EndReplyBeforeStart(t *testing.T) {
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	go func() {
		p := srv.Accept(t)
		p.Expect(t, 0x20)
		p.EndReply(t)
	}()
	if _, err := Dial(context.Background(), cfg(srv)); !errors.Is(err, ErrClosedByDevice) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadHostOutput(t *testing.T) {
	c, p := start(t)
	p.Data(t, []byte("login: "))
	if got := readN(t, c, 7); string(got) != "login: " {
		t.Fatalf("%q", got)
	}
}

func TestDataBeforeFirstReadIsKept(t *testing.T) {
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	go func() {
		p := srv.Accept(t)
		p.Expect(t, 0x20)
		// Host output in the same write as the reply, and before the client's 0x27.
		b := p.StartReplyMsg(0)
		b = append(b, p.DataMsg([]byte("BIOS "))...)
		p.Write(t, b)
		p.Expect(t, 0x27)
		p.Data(t, []byte("POST"))
	}()
	c, err := Dial(context.Background(), cfg(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(50 * time.Millisecond)
	if got := readN(t, c, 9); string(got) != "BIOS POST" {
		t.Fatalf("%q", got)
	}
}

// TestReplayedCapture feeds a reconstructed device capture: reply, port report, a Linux getty
// banner with escape sequences and CP437/UTF-8 bytes, heartbeats, all coalesced in one write and
// then split byte by byte. The client must deliver exactly the data bytes in order.
func TestReplayedCapture(t *testing.T) {
	banner := []byte("\x1b[2J\x1b[H\r\nUbuntu 24.04 LTS host ttyS0\r\n\r\nhost login: ")
	art := []byte{0xc9, 0xcd, 0xbb, 0x20, 0xe2, 0x94, 0x80} // CP437 box drawing, UTF-8 U+2500
	for _, split := range []bool{false, true} {
		srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
		go func() {
			p := srv.Accept(t)
			p.Expect(t, 0x20)
			var b []byte
			b = append(b, p.StartReplyMsg(0)...)
			b = append(b, p.ControlsMsg(0x03, 0x04)...)
			b = append(b, p.DataMsg(banner[:10])...)
			b = append(b, p.Message(0x2b, nil)...)
			b = append(b, p.DataMsg(banner[10:])...)
			b = append(b, p.DataMsg(nil)...)
			b = append(b, p.DataMsg(art)...)
			if !split {
				p.Write(t, b)
			} else {
				for i := range b {
					p.Write(t, b[i:i+1])
				}
			}
			p.Expect(t, 0x27)
		}()
		c, err := Dial(context.Background(), cfg(srv))
		if err != nil {
			t.Fatal(err)
		}
		want := append(append([]byte(nil), banner...), art...)
		if got := readN(t, c, len(want)); !bytes.Equal(got, want) {
			t.Fatalf("split=%v: %q", split, got)
		}
		if st := c.Status(); !st.Reported || !st.PowerOn || st.Loopback || st.TxOverflow {
			t.Fatalf("status %+v", st)
		}
		c.Close()
	}
}

func TestWriteSendsDataToHost(t *testing.T) {
	c, p := start(t)
	if n, err := c.Write([]byte("root\r")); n != 5 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
	m := p.Expect(t, 0x28)
	if string(m.Data()) != "root\r" || m.Seq != 3 {
		t.Fatalf("data %q seq %d", m.Data(), m.Seq)
	}
	if n, err := c.Write(nil); n != 0 || err != nil {
		t.Fatalf("empty write n=%d err=%v", n, err)
	}
}

func TestWriteChunks(t *testing.T) {
	c, p := start(t)
	data := make([]byte, 2500)
	for i := range data {
		data[i] = byte(i)
	}
	done := make(chan error, 1)
	go func() { _, err := c.Write(data); done <- err }()
	var got []byte
	var sizes []int
	for len(got) < len(data) {
		m := p.Expect(t, 0x28)
		sizes = append(sizes, len(m.Data()))
		got = append(got, m.Data()...)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) || len(sizes) != 3 || sizes[0] != 1000 || sizes[2] != 500 {
		t.Fatalf("sizes %v", sizes)
	}
}

func TestControlsUpdateStatus(t *testing.T) {
	c, p := start(t)
	if st := c.Status(); st.Reported {
		t.Fatalf("status before report %+v", st)
	}
	p.Controls(t, 0, 0x02|0x01) // loopback, overflow, power off
	p.Data(t, []byte("x"))
	readN(t, c, 1)
	if st := c.Status(); !st.Reported || st.PowerOn || !st.Loopback || !st.TxOverflow {
		t.Fatalf("status %+v", st)
	}
	p.Controls(t, 0, 0x04)
	p.Data(t, []byte("y"))
	readN(t, c, 1)
	if st := c.Status(); !st.PowerOn || st.Loopback || st.TxOverflow {
		t.Fatalf("status %+v", st)
	}
}

func TestHeartbeatFromDeviceIgnored(t *testing.T) {
	c, p := start(t)
	p.Heartbeat(t)
	p.Write(t, p.Message(0x24, nil)) // KEEP_ALIVE_PING
	p.Write(t, p.Message(0x25, nil)) // KEEP_ALIVE_PONG
	p.Data(t, []byte("ok"))
	if got := readN(t, c, 2); string(got) != "ok" || c.Err() != nil {
		t.Fatalf("%q %v", got, c.Err())
	}
}

func TestKeepalive(t *testing.T) {
	old := keepaliveInterval
	keepaliveInterval = 20 * time.Millisecond
	defer func() { keepaliveInterval = old }()
	_, p := start(t)
	a, b := p.Read(t), p.Read(t)
	if a.Cmd != 0x2b || b.Cmd != 0x2b || b.Seq != a.Seq+1 || a.Seq < 3 {
		t.Fatalf("%+v %+v", a, b)
	}
}

func TestUnknownCommandEndsSession(t *testing.T) {
	c, p := start(t)
	p.Write(t, []byte{0x55, 0, 0, 0, 0, 0, 0, 0})
	waitDone(t, c)
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "0x55") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read err = %v", err)
	}
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("write after end succeeded")
	}
}

func TestDeviceEndsSession(t *testing.T) {
	c, p := start(t)
	p.Data(t, []byte("bye"))
	p.EndReply(t)
	if m := p.Expect(t, 0x12); !bytes.Equal(m.Payload, []byte{0, 0, 0}) {
		t.Fatalf("end redirection % x", m.Payload)
	}
	waitDone(t, c)
	if !errors.Is(c.Err(), ErrClosedByDevice) {
		t.Fatalf("err = %v", c.Err())
	}
	// Output that arrived before the end is still readable, then EOF.
	if got := readN(t, c, 3); string(got) != "bye" {
		t.Fatalf("%q", got)
	}
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read err = %v", err)
	}
}

func TestDisconnectEndsSession(t *testing.T) {
	c, p := start(t)
	p.Disconnect()
	waitDone(t, c)
	if err := c.Err(); err == nil || errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestCloseSendsGoodbye(t *testing.T) {
	old := closeTimeout
	closeTimeout = 5 * time.Second // the peer answers; a timeout would hide a missing 0x12
	defer func() { closeTimeout = old }()
	c, p := start(t)
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	if m := p.Expect(t, 0x22); m.Seq != 3 {
		t.Fatalf("end sol seq %d", m.Seq)
	}
	p.EndReply(t)
	p.Expect(t, 0x12)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close hangs")
	}
	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("err = %v", c.Err())
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write err = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestCloseWithoutReplyTimesOut(t *testing.T) {
	c, p := start(t)
	t0 := time.Now()
	c.Close()
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("close took %s", d)
	}
	p.Expect(t, 0x22)
	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("err = %v", c.Err())
	}
}

func TestCloseUnblocksRead(t *testing.T) {
	c, _ := start(t)
	res := make(chan error, 1)
	go func() { _, err := c.Read(make([]byte, 8)); res <- err }()
	time.Sleep(20 * time.Millisecond)
	c.Close()
	select {
	case err := <-res:
		if err != io.EOF {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read still blocked")
	}
}

func TestCloseAfterDeviceEnd(t *testing.T) {
	c, p := start(t)
	p.Disconnect()
	waitDone(t, c)
	before := c.Err()
	if err := c.Close(); err != nil || c.Err() != before {
		t.Fatalf("close err=%v, Err %v -> %v", err, before, c.Err())
	}
}

func TestBackpressure(t *testing.T) {
	c, p := start(t)
	chunk := bytes.Repeat([]byte{'a'}, 60000)
	n := maxPending/len(chunk) + 3
	sent := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			p.Data(t, chunk)
		}
		close(sent)
	}()
	// The loop stops reading at maxPending; the rest waits in the socket buffers.
	time.Sleep(200 * time.Millisecond)
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending > maxPending+len(chunk) {
		t.Fatalf("pending %d", pending)
	}
	total := 0
	buf := make([]byte, 32*1024)
	for total < n*len(chunk) {
		k, err := c.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		total += k
	}
	<-sent
	// Close while the buffer is full again must not hang.
	go func() {
		for i := 0; i < n; i++ {
			if _, err := p.Conn().Write(p.DataMsg(chunk)); err != nil {
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close hangs with a full buffer")
	}
}

func TestTLS(t *testing.T) {
	cert, pool := selfSigned(t)
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{cert}}})
	go srv.AcceptSession(t)
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw",
		TLS: &tls.Config{RootCAs: pool, ServerName: "sol.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestDataMessageLength(t *testing.T) {
	// DATA_FROM_HOST length is LE16 at message offset 8.
	c, p := start(t)
	b := p.DataMsg([]byte("abc"))
	if binary.LittleEndian.Uint16(b[8:]) != 3 || len(b) != 13 {
		t.Fatalf("% x", b)
	}
	p.Write(t, b)
	if got := readN(t, c, 3); string(got) != "abc" {
		t.Fatalf("%q", got)
	}
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sol.example.com"},
		DNSNames:     []string{"sol.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}
