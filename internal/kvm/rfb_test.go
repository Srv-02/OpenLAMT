package kvm

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image/color"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/redir"
)

// startRFB connects, runs the redirection handshake and rfbHandshake against the fake server.
func startRFB(t *testing.T, o kvmtest.Options, q Quality) (*bufio.Reader, net.Conn, *kvmtest.Server, int, int, string, error) {
	t.Helper()
	o.Username, o.Password = "admin", "pw"
	if o.Name == "" {
		o.Name = "kvmtest"
	}
	srv := kvmtest.Start(t, o)
	conn := dialRedirection(t, srv)
	br := bufio.NewReader(conn)
	if err := redir.Handshake(struct {
		io.Reader
		io.Writer
	}{br, conn}, redir.KVM, "admin", "pw", nil); err != nil {
		t.Fatal(err)
	}
	if err := kvmStart(br, conn); err != nil {
		t.Fatal(err)
	}
	w, h, name, err := rfbHandshake(br, conn, q)
	return br, conn, srv, w, h, name, err
}

// pipeSession runs rfbHandshake + a session against the fake server and returns both ends.
func pipeSession(t *testing.T, o kvmtest.Options, q Quality) (*session, *kvmtest.Server, net.Conn) {
	t.Helper()
	br, conn, srv, w, h, name, err := startRFB(t, o, q)
	if err != nil {
		t.Fatal(err)
	}
	if w != 128 || h != 64 || name != "kvmtest" {
		t.Fatalf("server init %dx%d %q", w, h, name)
	}
	send := func(b []byte) error { _, err := conn.Write(b); return err }
	return newSession(br, send, newFramebuffer(w, h), q), srv, conn
}

// waitMessages polls srv.ClientMessages until it holds n messages (t.Fatal after 2 s).
func waitMessages(t *testing.T, srv *kvmtest.Server, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if m := srv.ClientMessages(); len(m) >= n {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d client messages, want %d", len(srv.ClientMessages()), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readOne requests one update and reads one message.
func readOne(t *testing.T, s *session, conn net.Conn) (bool, error) {
	t.Helper()
	if _, err := conn.Write(framebufferUpdateRequest(false, 0, 0, 128, 64)); err != nil {
		t.Fatal(err)
	}
	return s.readMessage()
}

func wantRFBError(t *testing.T, err error, stage string) {
	t.Helper()
	var re *RFBError
	if !errors.As(err, &re) || re.Stage != stage {
		t.Fatalf("err = %v, want RFBError stage %q", err, stage)
	}
}

func TestRFBHandshakeLowSendsEncodingsAndRGB332(t *testing.T) {
	_, srv, _ := pipeSession(t, kvmtest.Options{}, QualityLow)
	msgs := waitMessages(t, srv, 2)
	if !bytes.Equal(msgs[0], []byte{2, 0, 0, 4, 0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0x04, 0x44, 0xff, 0xff, 0xff, 0x21}) {
		t.Fatalf("SetEncodings %x", msgs[0])
	}
	if !bytes.Equal(msgs[1], pixelFormatRGB332) {
		t.Fatalf("SetPixelFormat %x", msgs[1])
	}
}

func TestRFBHandshakeHighKeepsServerDefault(t *testing.T) {
	_, srv, conn := pipeSession(t, kvmtest.Options{}, QualityHigh)
	conn.Write(framebufferUpdateRequest(false, 0, 0, 128, 64))
	msgs := waitMessages(t, srv, 2)
	if msgs[1][0] != msgFramebufferUpdateRequest {
		t.Fatalf("second message should be our request, got %x", msgs[1])
	}
}

func TestRFBHandshakeHighSendsRGB565WhenServerDiffers(t *testing.T) {
	pf := []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
	_, srv, _ := pipeSession(t, kvmtest.Options{PixelFormat: pf}, QualityHigh)
	msgs := waitMessages(t, srv, 2)
	if !bytes.Equal(msgs[1], pixelFormatRGB565) {
		t.Fatalf("SetPixelFormat %x", msgs[1])
	}
}

func TestRFBHandshakeServerInitBounds(t *testing.T) {
	_, _, _, _, _, _, err := startRFB(t, kvmtest.Options{Width: 9000}, QualityLow)
	wantRFBError(t, err, "serverinit")
	_, _, _, _, _, _, err = startRFB(t, kvmtest.Options{Name: strings.Repeat("n", 2000)}, QualityLow)
	wantRFBError(t, err, "serverinit")
	_, _, _, w, _, _, err := startRFB(t, kvmtest.Options{Width: 8192}, QualityLow)
	if err != nil || w != 8192 {
		t.Fatalf("8192 should pass: %d %v", w, err)
	}
}

func TestReadUpdate_RawAndZRLELow(t *testing.T) {
	z := kvmtest.NewZlibStream()
	upd := kvmtest.FramebufferUpdate(
		kvmtest.RawRect(0, 0, 4, 4, bytes.Repeat([]byte{0xe0}, 16)),
		z.ZRLERect(64, 0, 64, 64, kvmtest.SolidTile([]byte{0x03})),
		z.ZRLERect(4, 4, 2, 2, kvmtest.RawTile([]byte{0x1c, 0x1c, 0x1c, 0x1c})))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	done, err := readOne(t, s, conn)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	img := s.fb.Snapshot()
	if img.RGBAAt(0, 0) != pixelRGB332(0xe0) || img.RGBAAt(127, 63) != pixelRGB332(0x03) || img.RGBAAt(5, 5) != pixelRGB332(0x1c) {
		t.Fatal("pixels")
	}
	if img.RGBAAt(10, 10) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("untouched pixel changed")
	}
}

func TestReadUpdate_RawAndZRLEHigh(t *testing.T) {
	z := kvmtest.NewZlibStream()
	upd := kvmtest.FramebufferUpdate(
		kvmtest.RawRect(0, 0, 2, 2, bytes.Repeat([]byte{0xe0, 0x07}, 4)),
		z.ZRLERect(64, 0, 64, 64, kvmtest.SolidTile([]byte{0x1f, 0x00})))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityHigh)
	if done, err := readOne(t, s, conn); err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	img := s.fb.Snapshot()
	if img.RGBAAt(1, 1) != pixelRGB565(0x07e0) || img.RGBAAt(100, 10) != pixelRGB565(0x001f) {
		t.Fatal("pixels")
	}
}

func TestReadUpdate_IntelSample(t *testing.T) {
	// Encoding.ts:120: two ZRLE rectangles from an 8-bit session, prefixed with a FramebufferUpdate header
	sample, _ := hex.DecodeString("0000000000400040000000100000000A789C626400000000FFFF00400000004000400000001000000008626400000000FFFF")
	upd := append([]byte{0, 0, 0, 2}, sample...)
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	done, err := readOne(t, s, conn)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if img := s.fb.Snapshot(); img.RGBAAt(63, 63) != pixelRGB332(0) || img.RGBAAt(127, 0) != pixelRGB332(0) {
		t.Fatal("sample tiles")
	}
}

func TestReadUpdate_StoredBlockBypass(t *testing.T) {
	z := kvmtest.NewZlibStream()
	upd := kvmtest.FramebufferUpdate(
		kvmtest.StoredRect(0, 0, 2, 2, kvmtest.RawTile([]byte{0xe0, 0xe0, 0xe0, 0xe0})),
		z.ZRLERect(64, 0, 64, 64, kvmtest.SolidTile([]byte{0x03})))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	if _, err := readOne(t, s, conn); err != nil {
		t.Fatal(err)
	}
	img := s.fb.Snapshot()
	if img.RGBAAt(1, 1) != pixelRGB332(0xe0) || img.RGBAAt(100, 10) != pixelRGB332(0x03) {
		t.Fatal("pixels")
	}
	if !s.inf.started || len(s.inf.window) != 2 {
		t.Fatalf("window holds %d bytes, stored block must not enter it", len(s.inf.window))
	}
}

func TestReadUpdate_ZRLEStreamSpansUpdates(t *testing.T) {
	z := kvmtest.NewZlibStream()
	u1 := kvmtest.FramebufferUpdate(z.ZRLERect(0, 0, 64, 64, kvmtest.SolidTile([]byte{0xe0})))
	u2 := kvmtest.FramebufferUpdate(z.ZRLERect(64, 0, 64, 64, kvmtest.SolidTile([]byte{0x03})))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{u1, u2}}, QualityLow)
	for i := 0; i < 2; i++ {
		if done, err := readOne(t, s, conn); err != nil || !done {
			t.Fatalf("update %d: done=%v err=%v", i, done, err)
		}
	}
	img := s.fb.Snapshot()
	if img.RGBAAt(0, 0) != pixelRGB332(0xe0) || img.RGBAAt(100, 10) != pixelRGB332(0x03) {
		t.Fatal("pixels")
	}
}

func TestReadUpdate_DesktopSizeResizesAndRequestsFull(t *testing.T) {
	upd := kvmtest.FramebufferUpdate(kvmtest.DesktopSizeRect(256, 128))
	s, srv, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	if _, err := readOne(t, s, conn); err != nil {
		t.Fatal(err)
	}
	if w, h := s.fb.Size(); w != 256 || h != 128 {
		t.Fatalf("size %dx%d", w, h)
	}
	msgs := waitMessages(t, srv, 4)
	if !bytes.Equal(msgs[3], []byte{3, 0, 0, 0, 0, 0, 1, 0, 0, 128}) {
		t.Fatalf("full request %x", msgs[3])
	}
}

func TestReadUpdate_DesktopSizeBounds(t *testing.T) {
	upd := kvmtest.FramebufferUpdate(kvmtest.DesktopSizeRect(9000, 10))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	_, err := readOne(t, s, conn)
	wantRFBError(t, err, "rectangle")
	if w, h := s.fb.Size(); w != 128 || h != 64 {
		t.Fatalf("size changed to %dx%d", w, h)
	}
}

func TestReadUpdate_BellAndCutTextIgnored(t *testing.T) {
	raw := kvmtest.FramebufferUpdate(kvmtest.RawRect(0, 0, 1, 1, []byte{0xe0}))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{kvmtest.Concat(kvmtest.Bell(), kvmtest.ServerCutText("clip"), raw)}}, QualityLow)
	for i := 0; i < 3; i++ {
		done, err := readOne2(t, s, conn, i == 0)
		if err != nil || done != (i == 2) {
			t.Fatalf("message %d: done=%v err=%v", i, done, err)
		}
	}
	if s.fb.Snapshot().RGBAAt(0, 0) != pixelRGB332(0xe0) {
		t.Fatal("pixel")
	}
}

// readOne2 sends a request only when first is set.
func readOne2(t *testing.T, s *session, conn net.Conn, first bool) (bool, error) {
	t.Helper()
	if first {
		return readOne(t, s, conn)
	}
	return s.readMessage()
}

func TestReadUpdate_CutTextLargeIsDiscarded(t *testing.T) {
	raw := kvmtest.FramebufferUpdate(kvmtest.RawRect(0, 0, 1, 1, []byte{0xe0}))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{kvmtest.Concat(kvmtest.ServerCutText(strings.Repeat("x", 100000)), raw)}}, QualityLow)
	if done, err := readOne(t, s, conn); err != nil || done {
		t.Fatalf("cut text: done=%v err=%v", done, err)
	}
	if done, err := s.readMessage(); err != nil || !done {
		t.Fatalf("update: done=%v err=%v", done, err)
	}
}

func TestReadUpdate_TileTooLargeFails(t *testing.T) {
	upd := kvmtest.FramebufferUpdate(kvmtest.RawRect(0, 0, 65, 1, make([]byte, 65)))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	_, err := readOne(t, s, conn)
	wantRFBError(t, err, "rectangle")
}

func TestReadUpdate_ZRLELengthBound(t *testing.T) {
	data := binary.BigEndian.AppendUint32(nil, 70000)
	data = append(data, 1, 2, 3, 4)
	upd := kvmtest.FramebufferUpdate(kvmtest.Rect{W: 8, H: 8, Encoding: 16, Data: data})
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	_, err := readOne(t, s, conn)
	wantRFBError(t, err, "rectangle")
}

func TestReadUpdate_BadTileIsNotDrawn(t *testing.T) {
	z := kvmtest.NewZlibStream()
	// palette index beyond the palette after partial writes: sub 130 (2 colours) with index 5
	tile := []byte{130, 0xe0, 0x03, 5}
	upd := kvmtest.FramebufferUpdate(z.ZRLERect(0, 0, 2, 2, tile))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	var te *TileError
	if _, err := readOne(t, s, conn); !errors.As(err, &te) {
		t.Fatalf("err = %v", err)
	}
	if s.fb.Snapshot().RGBAAt(0, 0) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("failed tile was drawn")
	}
}

func TestReadUpdate_RectBeyondScreenIsClipped(t *testing.T) {
	upd := kvmtest.FramebufferUpdate(kvmtest.RawRect(120, 60, 16, 16, bytes.Repeat([]byte{0xe0}, 256)))
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	if done, err := readOne(t, s, conn); err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	img := s.fb.Snapshot()
	if img.RGBAAt(127, 63) != pixelRGB332(0xe0) || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 64 {
		t.Fatal("clipping")
	}
}

func TestReadUpdate_UnknownEncodingFails(t *testing.T) {
	upd := kvmtest.FramebufferUpdate(kvmtest.Rect{W: 1, H: 1, Encoding: 7})
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
	_, err := readOne(t, s, conn)
	wantRFBError(t, err, "encoding")
}

func TestReadUpdate_UnknownMessageFails(t *testing.T) {
	s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{{9}}}, QualityLow)
	_, err := readOne(t, s, conn)
	wantRFBError(t, err, "message")
}

func TestFramebufferUpdateRequestBytes(t *testing.T) {
	if got := framebufferUpdateRequest(true, 0, 0, 1024, 768); !bytes.Equal(got, []byte{3, 1, 0, 0, 0, 0, 4, 0, 3, 0}) {
		t.Fatalf("%x", got)
	}
}

// serverInitStream builds the server side of a handshake (version, security None, result OK,
// ServerInit) so sizes the fake server cannot produce (0) can be fed to rfbHandshake.
func serverInitStream(w, h int) *bufio.Reader {
	b := []byte("RFB 003.008\n")
	b = append(b, 1, 1)       // one security type: None
	b = append(b, 0, 0, 0, 0) // SecurityResult OK
	si := make([]byte, 24)
	binary.BigEndian.PutUint16(si[0:], uint16(w))
	binary.BigEndian.PutUint16(si[2:], uint16(h))
	copy(si[4:], pixelFormatRGB565[4:17])
	b = append(b, si...)
	return bufio.NewReader(bytes.NewReader(b))
}

func TestRFBHandshakeServerInitSizeTable(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		ok   bool
	}{
		{8192, 8193, false}, {8193, 8192, false}, {0, 64, false}, {64, 0, false},
		{8192, 8192, true}, {1, 1, true},
	} {
		w, h, _, err := rfbHandshake(serverInitStream(tc.w, tc.h), io.Discard, QualityHigh)
		if tc.ok {
			if err != nil || w != tc.w || h != tc.h {
				t.Errorf("%dx%d: got %dx%d, %v", tc.w, tc.h, w, h, err)
			}
			continue
		}
		wantRFBError(t, err, "serverinit")
	}
}

func TestReadUpdate_DesktopSizeBoundsTable(t *testing.T) {
	for _, tc := range [][2]int{{10, 9000}, {9000, 10}, {0, 10}, {10, 0}} {
		upd := kvmtest.FramebufferUpdate(kvmtest.DesktopSizeRect(tc[0], tc[1]))
		s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
		_, err := readOne(t, s, conn)
		wantRFBError(t, err, "rectangle")
		if w, h := s.fb.Size(); w != 128 || h != 64 {
			t.Errorf("%v: size changed to %dx%d", tc, w, h)
		}
	}
}

func TestReadUpdate_RectSizeBoundsTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
		enc  int32
	}{
		{"raw 1x65", 1, 65, 0}, {"raw 65x1", 65, 1, 0}, {"raw 0x1", 0, 1, 0}, {"raw 1x0", 1, 0, 0},
		{"zrle 65x1", 65, 1, 16}, {"zrle 1x65", 1, 65, 16}, {"zrle 0x1", 0, 1, 16}, {"zrle 1x0", 1, 0, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upd := kvmtest.FramebufferUpdate(kvmtest.Rect{W: tc.w, H: tc.h, Encoding: tc.enc, Data: make([]byte, 16)})
			s, _, conn := pipeSession(t, kvmtest.Options{Updates: [][]byte{upd}}, QualityLow)
			_, err := readOne(t, s, conn)
			wantRFBError(t, err, "rectangle")
		})
	}
}
