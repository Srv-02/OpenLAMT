package kvm

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// solidUpdate builds one FramebufferUpdate covering w x h with solid 64x64 (or smaller edge) tiles.
func solidUpdate(z *kvmtest.ZlibStream, pixel []byte, w, h int) []byte {
	var rects []kvmtest.Rect
	for y := 0; y < h; y += 64 {
		for x := 0; x < w; x += 64 {
			rects = append(rects, z.ZRLERect(x, y, min(64, w-x), min(64, h-y), kvmtest.SolidTile(pixel)))
		}
	}
	return kvmtest.FramebufferUpdate(rects...)
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "device.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func waitCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDial_PlainTCPFirstFrame(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Name: "kvmtest", Updates: [][]byte{solidUpdate(z, []byte{0xe0}, 128, 64)}})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	n, err := c.WaitFrame(waitCtx(t), 0)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	img := c.Snapshot()
	if img.Bounds().Dx() != 128 || img.RGBAAt(100, 50) != pixelRGB332(0xe0) {
		t.Fatal("frame")
	}
	if w, h := c.Size(); w != 128 || h != 64 {
		t.Fatalf("size %dx%d", w, h)
	}
	if c.Name() != "kvmtest" || c.Quality() != QualityLow {
		t.Fatalf("name %q quality %v", c.Name(), c.Quality())
	}
	msgs := waitMessages(t, srv, 4) // SetEncodings, SetPixelFormat, first request, request after the frame
	// Both incremental, as MeshCommander's _SendRefresh (amt-desktop-0.0.2.js).
	if !bytes.Equal(msgs[2], framebufferUpdateRequest(true, 0, 0, 128, 64)) || !bytes.Equal(msgs[3], framebufferUpdateRequest(true, 0, 0, 128, 64)) {
		t.Fatalf("requests %x %x", msgs[2], msgs[3])
	}
}

func TestDial_DefaultQualityHigh(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{solidUpdate(z, []byte{0x1f, 0xf8}, 128, 64)}})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Quality() != QualityHigh {
		t.Fatalf("quality %v", c.Quality())
	}
	if _, err := c.WaitFrame(waitCtx(t), 0); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().RGBAAt(3, 3) != pixelRGB565(0xf81f) {
		t.Fatal("pixel")
	}
	msgs := waitMessages(t, srv, 3)
	if msgs[0][0] != msgSetEncodings || msgs[1][0] != msgFramebufferUpdateRequest {
		t.Fatalf("unexpected client messages, first two %x %x", msgs[0], msgs[1])
	}
}

func TestWaitFrame_ReturnsImmediatelyWhenAhead(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{solidUpdate(z, []byte{0xe0}, 128, 64)}})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.WaitFrame(waitCtx(t), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	n, err := c.WaitFrame(ctx, 0)
	if err != nil || n != 1 || c.Frame() != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestWaitFrame_ContextCancel(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw"})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.WaitFrame(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestDial_TLSWithPinnedVerifier(t *testing.T) {
	cert := selfSignedCert(t)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{cert}}})
	verified := false
	cfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			verified = bytes.Equal(cs.PeerCertificates[0].Raw, cert.Certificate[0])
			return nil
		}}
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", TLS: cfg})
	if err != nil || !verified {
		t.Fatalf("err=%v verified=%v", err, verified)
	}
	c.Close()
}

var errPinned = errors.New("pinned mismatch")

func TestDial_TLSVerifyErrorPropagates(t *testing.T) {
	cert := selfSignedCert(t)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{cert}}})
	cfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12,
		VerifyConnection: func(tls.ConnectionState) error { return errPinned }}
	_, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", TLS: cfg})
	if !errors.Is(err, errPinned) {
		t.Fatalf("err=%v", err)
	}
}

func TestDial_WrongPasswordNoLeak(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw"})
	_, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "hunter2-secret"})
	var re *RedirectionError
	if !errors.As(err, &re) {
		t.Fatalf("err=%v", err)
	}
	if bytes.Contains([]byte(err.Error()), []byte("hunter2-secret")) {
		t.Fatal("password in error")
	}
}

func TestDial_ContextCancelAbortsHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conns := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			conns <- c
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		select {
		case c := <-conns:
			c.Close()
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err = Dial(ctx, Config{Addr: ln.Addr().String(), Username: "admin", Password: "pw"})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestClient_SecondFrameAndFullRequest(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{
		solidUpdate(z, []byte{0xe0}, 128, 64), solidUpdate(z, []byte{0x03}, 128, 64)}})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := waitCtx(t)
	if n, err := c.WaitFrame(ctx, 0); err != nil || n < 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if n, err := c.WaitFrame(ctx, 1); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if c.Snapshot().RGBAAt(1, 1) != pixelRGB332(0x03) {
		t.Fatal("second frame pixel")
	}
	waitMessages(t, srv, 5) // SetEncodings, SetPixelFormat, 3 requests; the loop's last request is out
	if err := c.RequestFullUpdate(); err != nil {
		t.Fatal(err)
	}
	msgs := waitMessages(t, srv, 6)
	if want := []byte{3, 0, 0, 0, 0, 0, 0, 128, 0, 64}; !bytes.Equal(msgs[5], want) {
		t.Fatalf("last message %x", msgs[5])
	}
}

func TestClient_ServerClosesMidFrame(t *testing.T) {
	z := kvmtest.NewZlibStream()
	upd := solidUpdate(z, []byte{0xe0}, 128, 64)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{upd, upd[:20]}, CloseAfterUpdate: 2})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	ctx := waitCtx(t)
	if _, err := c.WaitFrame(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitFrame(ctx, 1); err == nil {
		t.Fatal("expected session error")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done not closed")
	}
	if c.Err() == nil || errors.Is(c.Err(), ErrClosed) || c.Snapshot().RGBAAt(1, 1) != pixelRGB332(0xe0) {
		t.Fatalf("err=%v snapshot lost", c.Err())
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if errors.Is(c.Err(), ErrClosed) {
		t.Fatal("Close must not overwrite the session error")
	}
}

func TestClient_CloseUnblocksWaitFrame(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw"})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(100*time.Millisecond, func() { c.Close() })
	if _, err := c.WaitFrame(waitCtx(t), 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("err=%v", err)
	}
	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Err=%v", c.Err())
	}
	if err := c.send([]byte{3}); !errors.Is(err, ErrClosed) {
		t.Fatalf("send after Close: %v", err)
	}
	if err := c.RequestFullUpdate(); !errors.Is(err, ErrClosed) {
		t.Fatalf("RequestFullUpdate after Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// failIncrementalConn fails the FramebufferUpdateRequest the read loop sends after a frame
// (the second one; Dial sends the first, also incremental).
type failIncrementalConn struct {
	net.Conn
	requests *int
}

func (c failIncrementalConn) Write(p []byte) (int, error) {
	if len(p) >= 2 && p[0] == msgFramebufferUpdateRequest && p[1] == 1 {
		if *c.requests++; *c.requests > 1 {
			return 0, errors.New("injected write failure")
		}
	}
	return c.Conn.Write(p)
}

func TestClient_RequestWriteFailureEndsSession(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{solidUpdate(z, []byte{0xe0}, 128, 64)}})
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		return failIncrementalConn{c, new(int)}, err
	}
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow, DialContext: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done not closed after failed request write")
	}
	if c.Err() == nil || errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("err=%v", c.Err())
	}
	if c.Snapshot().RGBAAt(1, 1) != pixelRGB332(0xe0) {
		t.Fatal("snapshot lost")
	}
}

func TestClient_InputAfterServerEndedSessionReturnsSessionError(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{solidUpdate(z, []byte{0xe0}, 128, 64)}, CloseAfterUpdate: 1})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("session did not end")
	}
	want := c.Err()
	if want == nil || errors.Is(want, ErrClosed) {
		t.Fatalf("session err=%v", want)
	}
	for name, err := range map[string]error{
		"KeyEvent":          c.KeyEvent(KeyA, true),
		"PointerEvent":      c.PointerEvent(1, 1, 0),
		"RequestFullUpdate": c.RequestFullUpdate(),
	} {
		if err != want {
			t.Errorf("%s = %v, want session error %v", name, err, want)
		}
	}
}

func TestClient_InputAfterCloseReturnsErrClosed(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{solidUpdate(z, []byte{0xe0}, 128, 64)}})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	for name, err := range map[string]error{
		"KeyEvent":          c.KeyEvent(KeyA, true),
		"PointerEvent":      c.PointerEvent(1, 1, 0),
		"RequestFullUpdate": c.RequestFullUpdate(),
	} {
		if !errors.Is(err, ErrClosed) {
			t.Errorf("%s = %v, want ErrClosed", name, err)
		}
	}
}

// failKeyConn fails every KeyEvent message write.
type failKeyConn struct{ net.Conn }

func (c failKeyConn) Write(p []byte) (int, error) {
	if len(p) > 0 && p[0] == 4 {
		return 0, errors.New("injected key write failure")
	}
	return c.Conn.Write(p)
}

func TestClient_SendWriteFailureEndsSession(t *testing.T) {
	z := kvmtest.NewZlibStream()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{solidUpdate(z, []byte{0xe0}, 128, 64)}})
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		return failKeyConn{c}, err
	}
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow, DialContext: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	first := c.KeyEvent(KeyA, true)
	if first == nil || errors.Is(first, ErrClosed) {
		t.Fatalf("first KeyEvent err=%v", first)
	}
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done not closed after failed send")
	}
	start := time.Now()
	err = c.KeyEvent(KeyA, false)
	if err == nil || !errors.Is(err, c.Err()) || time.Since(start) > time.Second {
		t.Fatalf("second KeyEvent err=%v (session err %v) after %v", err, c.Err(), time.Since(start))
	}
}
