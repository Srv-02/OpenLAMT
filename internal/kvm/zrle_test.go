package kvm

import (
	"bytes"
	"compress/zlib"
	"errors"
	"image"
	"image/color"
	"io"
	"math/rand"
	"testing"
)

func TestPixelRGB332(t *testing.T) {
	cases := map[uint16]color.RGBA{0x00: {0, 0, 0, 255}, 0xff: {224, 224, 224, 255}, 0xe0: {224, 0, 0, 255},
		0x1c: {0, 224, 0, 255}, 0x03: {0, 0, 224, 255}, 0x02: {0, 0, 160, 255}, 0x01: {0, 0, 64, 255}}
	for v, want := range cases {
		if got := pixelRGB332(v); got != want {
			t.Errorf("%#x: %v, want %v", v, got, want)
		}
	}
}

func TestPixelRGB565(t *testing.T) {
	cases := map[uint16]color.RGBA{0x0000: {0, 0, 0, 255}, 0xffff: {248, 252, 248, 255}, 0xf800: {248, 0, 0, 255},
		0x07e0: {0, 252, 0, 255}, 0x001f: {0, 0, 248, 255}, 0x0800: {8, 0, 0, 255}}
	for v, want := range cases {
		if got := pixelRGB565(v); got != want {
			t.Errorf("%#x: %v, want %v", v, got, want)
		}
	}
	if got := readPixel([]byte{0xe0, 0x07}, 2); got != pixelRGB565(0x07e0) {
		t.Fatalf("little-endian read: %v", got)
	}
}

func tile(w, h int) *image.RGBA { return image.NewRGBA(image.Rect(0, 0, w, h)) }

func TestDecodeTileRaw(t *testing.T) {
	dst := tile(2, 2)
	n, err := decodeTile([]byte{0, 0xe0, 0x1c, 0x03, 0xff}, 2, 2, 1, dst)
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.RGBAAt(1, 1) != pixelRGB332(0xff) || dst.RGBAAt(0, 0) != pixelRGB332(0xe0) {
		t.Fatal("pixels")
	}
}

func TestDecodeTileRaw16(t *testing.T) {
	dst := tile(2, 1)
	n, err := decodeTile([]byte{0, 0xe0, 0x07, 0x1f, 0x00}, 2, 1, 2, dst)
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.RGBAAt(0, 0) != pixelRGB565(0x07e0) || dst.RGBAAt(1, 0) != pixelRGB565(0x001f) {
		t.Fatal("pixels")
	}
}

func TestDecodeTileIntoLargerBuffer(t *testing.T) {
	dst := tile(64, 64)
	n, err := decodeTile([]byte{0, 0xe0, 0x1c, 0x03, 0xff}, 2, 2, 1, dst)
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.RGBAAt(0, 0) != pixelRGB332(0xe0) || dst.RGBAAt(1, 0) != pixelRGB332(0x1c) ||
		dst.RGBAAt(0, 1) != pixelRGB332(0x03) || dst.RGBAAt(1, 1) != pixelRGB332(0xff) {
		t.Fatal("pixels")
	}
	if dst.RGBAAt(2, 0) != (color.RGBA{}) {
		t.Fatal("pixel outside tile touched")
	}
}

func TestDecodeTileSolid(t *testing.T) {
	dst := tile(3, 2)
	n, err := decodeTile([]byte{1, 0x1c}, 3, 2, 1, dst)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for i := 0; i < 6; i++ {
		if dst.RGBAAt(i%3, i/3) != pixelRGB332(0x1c) {
			t.Fatalf("pixel %d", i)
		}
	}
	dst = tile(3, 2)
	n, err = decodeTile([]byte{1, 0xe0, 0x07}, 3, 2, 2, dst)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.RGBAAt(2, 1) != pixelRGB565(0x07e0) {
		t.Fatal("solid 16")
	}
}

func TestDecodeTilePackedPalette(t *testing.T) {
	dst := tile(4, 2)
	n, err := decodeTile([]byte{2, 0x00, 0xff, 0xb2}, 4, 2, 1, dst)
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := []byte{1, 0, 1, 1, 0, 0, 1, 0}
	for i, idx := range want {
		if dst.RGBAAt(i%4, i/4) != pixelRGB332([]uint16{0x00, 0xff}[idx]) {
			t.Fatalf("pixel %d", i)
		}
	}
}

func TestDecodeTilePackedPaletteUnpaddedBitstream(t *testing.T) {
	dst := tile(3, 2)
	n, err := decodeTile([]byte{3, 0x00, 0xe0, 0x1c, 0x18, 0x60}, 3, 2, 1, dst)
	if err != nil || n != 6 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for i, idx := range []int{0, 1, 2, 0, 1, 2} {
		if dst.RGBAAt(i%3, i/3) != pixelRGB332([]uint16{0x00, 0xe0, 0x1c}[idx]) {
			t.Fatalf("pixel %d", i)
		}
	}
}

func TestDecodeTilePackedPaletteRowPadded(t *testing.T) {
	dst := tile(5, 2)
	n, err := decodeTile([]byte{3, 0x00, 0xe0, 0x1c, 0x18, 0x80, 0x18, 0x80}, 5, 2, 1, dst)
	if err != nil || n != 8 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.RGBAAt(4, 1) != pixelRGB332(0x1c) || dst.RGBAAt(3, 1) != pixelRGB332(0x00) {
		t.Fatal("row 2")
	}
}

func TestDecodeTilePackedPaletteLengthMismatch(t *testing.T) {
	var te *TileError
	_, err := decodeTile([]byte{3, 0x00, 0xe0, 0x1c, 1, 2, 3, 4, 5}, 5, 2, 1, tile(5, 2))
	if !errors.As(err, &te) || te.Reason != "packed palette length" {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeTilePlainRLE(t *testing.T) {
	dst := tile(7, 37)
	n, err := decodeTile([]byte{128, 0xe0, 255, 2, 0x03, 0}, 7, 37, 1, dst)
	if err != nil || n != 6 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.RGBAAt(6, 36) != pixelRGB332(0x03) || dst.RGBAAt(0, 36) != pixelRGB332(0xe0) {
		t.Fatal("runs")
	}
}

func TestDecodeTilePaletteRLE(t *testing.T) {
	dst := tile(5, 1)
	n, err := decodeTile([]byte{130, 0x00, 0xff, 0, 0x81, 2, 0}, 5, 1, 1, dst)
	if err != nil || n != 7 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for i, want := range []uint16{0x00, 0xff, 0xff, 0xff, 0x00} {
		if dst.RGBAAt(i, 0) != pixelRGB332(want) {
			t.Fatalf("pixel %d", i)
		}
	}
}

func TestDecodeTileErrors(t *testing.T) {
	var te *TileError
	if _, err := decodeTile([]byte{0, 1, 2}, 2, 2, 1, tile(2, 2)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short raw: %v", err)
	}
	if _, err := decodeTile([]byte{17, 0}, 1, 1, 1, tile(1, 1)); !errors.As(err, &te) {
		t.Fatalf("subencoding 17: %v", err)
	}
	if _, err := decodeTile([]byte{129, 0}, 1, 1, 1, tile(1, 1)); !errors.As(err, &te) {
		t.Fatalf("subencoding 129: %v", err)
	}
	if _, err := decodeTile([]byte{130, 1, 2, 5}, 1, 1, 1, tile(1, 1)); !errors.As(err, &te) {
		t.Fatalf("palette index: %v", err)
	}
	if _, err := decodeTile([]byte{128, 1, 10}, 2, 2, 1, tile(2, 2)); !errors.As(err, &te) {
		t.Fatalf("run overflow: %v", err)
	}
	if _, err := decodeTile([]byte{128, 1}, 2, 2, 1, tile(2, 2)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short run: %v", err)
	}
	if _, err := decodeTile([]byte{1, 0x00, 0x00}, 2, 2, 1, tile(2, 2)); !errors.As(err, &te) || te.Reason != "trailing data" {
		t.Fatalf("trailing: %v", err)
	}
	if _, err := decodeTile(nil, 2, 2, 1, tile(2, 2)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := decodeTile([]byte{3, 0, 0, 0, 0xc0}, 1, 1, 1, tile(1, 1)); !errors.As(err, &te) {
		t.Fatalf("palette index in packed tile: %v", err)
	}
}

func TestDecodeTileBadGeometry(t *testing.T) {
	var te *TileError
	if _, err := decodeTile([]byte{1, 0}, 4, 4, 1, tile(2, 2)); !errors.As(err, &te) {
		t.Fatalf("tile larger than dst: %v", err)
	}
	if _, err := decodeTile([]byte{1, 0}, 0, 4, 1, tile(2, 2)); !errors.As(err, &te) {
		t.Fatalf("zero width: %v", err)
	}
	if _, err := decodeTile([]byte{1, 0}, 1, 1, 3, tile(2, 2)); !errors.As(err, &te) {
		t.Fatalf("bad bpp: %v", err)
	}
}

func TestDecodeRaw(t *testing.T) {
	dst := tile(2, 1)
	if err := decodeRaw([]byte{0xe0, 0x1c}, 2, 1, 1, dst); err != nil {
		t.Fatal(err)
	}
	if dst.RGBAAt(1, 0) != pixelRGB332(0x1c) {
		t.Fatal("pixel")
	}
	if err := decodeRaw([]byte{0xe0}, 2, 1, 1, dst); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short: %v", err)
	}
}

// zlibRects compresses rects as one zlib stream with a sync flush after each (what AMT does).
func zlibRects(t *testing.T, rects ...[]byte) [][]byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
	var out [][]byte
	for _, r := range rects {
		start := buf.Len()
		zw.Write(r)
		zw.Flush()
		out = append(out, append([]byte(nil), buf.Bytes()[start:]...))
	}
	return out
}

func TestInflaterKeepsWindowAcrossRectangles(t *testing.T) {
	a := bytes.Repeat([]byte("abcdefgh"), 512)
	b := bytes.Repeat([]byte("abcdefgh"), 512)
	c := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	z := newInflater()
	for i, in := range zlibRects(t, a, b, c) {
		got, err := z.inflate(in)
		if err != nil {
			t.Fatalf("rect %d: %v", i, err)
		}
		if !bytes.Equal(got, [][]byte{a, b, c}[i]) {
			t.Fatalf("rect %d: %d bytes", i, len(got))
		}
	}
}

func TestInflaterIntelSample(t *testing.T) {
	// Encoding.ts:120, zlib payloads of the two rectangles
	z := newInflater()
	for i, in := range [][]byte{{0x78, 0x9c, 0x62, 0x64, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff}, {0x62, 0x64, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff}} {
		got, err := z.inflate(in)
		if err != nil || !bytes.Equal(got, []byte{0x01, 0x00}) {
			t.Fatalf("rect %d: %x %v", i, got, err)
		}
	}
}

func TestInflaterRejectsMissingHeader(t *testing.T) {
	in := []byte{0x00, 0x01, 0x00, 0xfe, 0xff, 'x'} // a bare stored block, no zlib header
	if _, err := newInflater().inflate(in); !errors.Is(err, ErrZlibHeader) {
		t.Fatalf("err = %v", err)
	}
}

func TestInflaterCorruptDataFails(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	data := make([]byte, 2000)
	for i := range data {
		data[i] = byte(r.Intn(8)) // compressible, Huffman-coded
	}
	in := zlibRects(t, data)[0]
	in[2] |= 0x06 // block type 3 is reserved: deterministic corruption
	if _, err := newInflater().inflate(in); err == nil {
		t.Fatal("corrupt block accepted")
	}
}

func TestInflaterOutputLimit(t *testing.T) {
	in := zlibRects(t, make([]byte, maxTileBytes+1))[0]
	if _, err := newInflater().inflate(in); !errors.Is(err, ErrTileTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestInflaterWindowStaysBounded(t *testing.T) {
	// The same random block in every rectangle: each one compresses to a back-reference into
	// the previous rectangle, so a wrong dictionary after the 32 KiB trim corrupts the output.
	block := make([]byte, 4000)
	rand.New(rand.NewSource(1)).Read(block)
	rects := make([][]byte, 20) // 80000 bytes in total, well beyond inflateWindow
	for i := range rects {
		rects[i] = block
	}
	z := newInflater()
	for i, in := range zlibRects(t, rects...) {
		got, err := z.inflate(in)
		if err != nil || !bytes.Equal(got, block) {
			t.Fatalf("rect %d: %v", i, err)
		}
		if len(z.window) > inflateWindow {
			t.Fatalf("window %d", len(z.window))
		}
	}
	if len(z.window) != inflateWindow {
		t.Fatalf("final window %d, want %d", len(z.window), inflateWindow)
	}
}
