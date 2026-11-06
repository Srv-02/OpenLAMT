package kvmtest

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"net"
)

const (
	encRaw         int32 = 0
	encZRLE        int32 = 16
	encDesktopSize int32 = -223
)

var defaultPixelFormat = []byte{16, 16, 0, 1, 0, 31, 0, 63, 0, 31, 11, 5, 0, 0, 0, 0}

// ClientMessages returns copies of all complete RFB client messages received so far
// (SetEncodings, SetPixelFormat, requests, key and pointer events), in order.
func (s *Server) ClientMessages() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.msgs))
	for i, m := range s.msgs {
		out[i] = append([]byte(nil), m...)
	}
	return out
}

// Updates returns how many Options.Updates elements have been sent.
func (s *Server) Updates() int { return int(s.updates.Load()) }

func (s *Server) record(m []byte) {
	s.mu.Lock()
	s.msgs = append(s.msgs, m)
	s.mu.Unlock()
}

// rfb runs the RFB server side after the redirection handshake; it returns when the
// connection fails or is closed.
func (s *Server) rfb(c net.Conn) {
	if _, err := c.Write([]byte("RFB 003.008\n")); err != nil {
		return
	}
	buf := make([]byte, 12)
	if _, err := io.ReadFull(c, buf); err != nil {
		return
	}
	if _, err := c.Write([]byte{1, 1}); err != nil {
		return
	}
	one := make([]byte, 1)
	if _, err := io.ReadFull(c, one); err != nil {
		return
	}
	if _, err := c.Write([]byte{0, 0, 0, 0}); err != nil {
		return
	}
	if _, err := io.ReadFull(c, one); err != nil { // ClientInit
		return
	}
	w, h := s.opts.Width, s.opts.Height
	if w == 0 {
		w = 128
	}
	if h == 0 {
		h = 64
	}
	pf := s.opts.PixelFormat
	if pf == nil {
		pf = defaultPixelFormat
	}
	init := binary.BigEndian.AppendUint16(nil, uint16(w))
	init = binary.BigEndian.AppendUint16(init, uint16(h))
	init = append(init, pf...)
	init = binary.BigEndian.AppendUint32(init, uint32(len(s.opts.Name)))
	init = append(init, s.opts.Name...)
	if _, err := c.Write(init); err != nil {
		return
	}

	sent := 0 // updates sent on this connection
	for {
		if _, err := io.ReadFull(c, one); err != nil {
			return
		}
		msg := []byte{one[0]}
		var extra int
		switch one[0] {
		case 0:
			extra = 19
		case 2:
			rest := make([]byte, 3)
			if _, err := io.ReadFull(c, rest); err != nil {
				return
			}
			msg = append(msg, rest...)
			extra = 4 * int(binary.BigEndian.Uint16(rest[1:]))
		case 3:
			extra = 9
		case 4:
			extra = 7
		case 5:
			extra = 5
		case 6:
			rest := make([]byte, 7)
			if _, err := io.ReadFull(c, rest); err != nil {
				return
			}
			msg = append(msg, rest...)
			extra = int(binary.BigEndian.Uint32(rest[3:]))
			if extra > 1<<20 {
				return
			}
		default:
			return
		}
		rest := make([]byte, extra)
		if _, err := io.ReadFull(c, rest); err != nil {
			return
		}
		msg = append(msg, rest...)
		s.record(msg)
		if one[0] != 3 {
			continue
		}
		n := int(s.updates.Load())
		if s.opts.UpdatesPerConn {
			n = sent
		}
		if n < len(s.opts.Updates) {
			if _, err := c.Write(s.opts.Updates[n]); err != nil {
				return
			}
			s.updates.Add(1)
			sent++
			if s.opts.CloseAfterUpdate > 0 && n+1 >= s.opts.CloseAfterUpdate {
				return
			}
		}
	}
}

// Rect is one FramebufferUpdate rectangle. Data follows the 12-byte rectangle header; for
// ZRLE it includes the 4-byte length prefix.
type Rect struct {
	X, Y, W, H int
	Encoding   int32
	Data       []byte
}

// FramebufferUpdate builds a FramebufferUpdate message: header plus rectangles.
func FramebufferUpdate(rects ...Rect) []byte {
	b := []byte{0, 0}
	b = binary.BigEndian.AppendUint16(b, uint16(len(rects)))
	for _, r := range rects {
		for _, v := range []int{r.X, r.Y, r.W, r.H} {
			b = binary.BigEndian.AppendUint16(b, uint16(v))
		}
		b = binary.BigEndian.AppendUint32(b, uint32(r.Encoding))
		b = append(b, r.Data...)
	}
	return b
}

// RawRect is a RAW rectangle.
func RawRect(x, y, w, h int, pixels []byte) Rect {
	return Rect{x, y, w, h, encRaw, pixels}
}

// DesktopSizeRect announces a new screen size.
func DesktopSizeRect(w, h int) Rect { return Rect{0, 0, w, h, encDesktopSize, nil} }

func zrleData(payload []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(payload))), payload...)
}

// StoredRect frames a ZRLE rectangle as AMT's bare stored block: 00 LEN(LE16) NLEN(LE16) tile.
func StoredRect(x, y, w, h int, tile []byte) Rect {
	p := []byte{0}
	p = binary.LittleEndian.AppendUint16(p, uint16(len(tile)))
	p = binary.LittleEndian.AppendUint16(p, ^uint16(len(tile)))
	return Rect{x, y, w, h, encZRLE, zrleData(append(p, tile...))}
}

// ZlibStream compresses tiles as one zlib stream with a sync flush per rectangle.
type ZlibStream struct {
	buf bytes.Buffer
	zw  *zlib.Writer
}

func NewZlibStream() *ZlibStream {
	z := &ZlibStream{}
	z.zw = zlib.NewWriter(&z.buf)
	return z
}

// ZRLERect compresses tile (subencoding byte + payload) into the stream.
func (z *ZlibStream) ZRLERect(x, y, w, h int, tile []byte) Rect {
	_, _ = z.zw.Write(tile)
	_ = z.zw.Flush()
	data := append([]byte(nil), z.buf.Bytes()...)
	z.buf.Reset()
	return Rect{x, y, w, h, encZRLE, zrleData(data)}
}

// SolidTile is ZRLE subencoding 1 with one pixel.
func SolidTile(pixel []byte) []byte { return append([]byte{1}, pixel...) }

// RawTile is ZRLE subencoding 0.
func RawTile(pixels []byte) []byte { return append([]byte{0}, pixels...) }

// ServerCutText builds a ServerCutText message.
func ServerCutText(text string) []byte {
	b := binary.BigEndian.AppendUint32([]byte{3, 0, 0, 0}, uint32(len(text)))
	return append(b, text...)
}

// Bell builds a Bell message.
func Bell() []byte { return []byte{2} }

// Concat joins several messages into one Updates element.
func Concat(msgs ...[]byte) []byte { return bytes.Join(msgs, nil) }
