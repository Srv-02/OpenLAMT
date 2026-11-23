package capture

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// riffChunk is one chunk of a parsed RIFF file: id, the list type for LIST and RIFF, the data
// and its offset in the file.
type riffChunk struct {
	id, list string
	data     []byte
	off      int
	children []riffChunk
}

// parseRIFF parses b[off:end] as a sequence of chunks; LIST and RIFF chunks are parsed
// recursively. It fails the test on any size that does not fit.
func parseRIFF(t *testing.T, b []byte, off, end int) []riffChunk {
	t.Helper()
	var out []riffChunk
	for off < end {
		if off+8 > end {
			t.Fatalf("truncated chunk header at %d", off)
		}
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4:]))
		if off+8+size > end {
			t.Fatalf("chunk %s at %d: size %d beyond %d", id, off, size, end)
		}
		c := riffChunk{id: id, data: b[off+8 : off+8+size], off: off}
		if id == "RIFF" || id == "LIST" {
			c.list = string(b[off+8 : off+12])
			c.children = parseRIFF(t, b, off+12, off+8+size)
		}
		out = append(out, c)
		off += 8 + size + size%2
	}
	return out
}

func find(t *testing.T, cs []riffChunk, id, list string) riffChunk {
	t.Helper()
	for _, c := range cs {
		if c.id == id && c.list == list {
			return c
		}
	}
	t.Fatalf("chunk %s %q missing", id, list)
	return riffChunk{}
}

func jpegFrame(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestAVIWriterProducesValidRIFF writes two frames and a repeat and checks the file chunk by
// chunk: RIFF size, headers (frame count, rate, size, MJPG), the movi chunks and the index.
func TestAVIWriterProducesValidRIFF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.avi")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewAVIWriter(f, 64, 48, 10)
	if err != nil {
		t.Fatal(err)
	}
	red, blue := jpegFrame(t, 64, 48, color.RGBA{R: 255}), jpegFrame(t, 64, 48, color.RGBA{B: 255})
	if err := w.WriteFrame(red); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRepeat(); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFrame(blue); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if w.Size() != int64(len(b)) {
		t.Fatalf("Size %d, file %d", w.Size(), len(b))
	}

	top := parseRIFF(t, b, 0, len(b))
	if len(top) != 1 || top[0].id != "RIFF" || top[0].list != "AVI " || len(top[0].data)+8 != len(b) {
		t.Fatalf("RIFF header: %+v", top)
	}
	hdrl := find(t, top[0].children, "LIST", "hdrl")
	avih := find(t, hdrl.children, "avih", "")
	le := binary.LittleEndian
	if len(avih.data) != 56 {
		t.Fatalf("avih size %d", len(avih.data))
	}
	if us := le.Uint32(avih.data[0:]); us != 100000 {
		t.Fatalf("microseconds per frame %d", us)
	}
	if flags := le.Uint32(avih.data[12:]); flags&0x10 == 0 {
		t.Fatalf("AVIF_HASINDEX missing: %#x", flags)
	}
	if frames := le.Uint32(avih.data[16:]); frames != 3 {
		t.Fatalf("total frames %d", frames)
	}
	if streams := le.Uint32(avih.data[24:]); streams != 1 {
		t.Fatalf("streams %d", streams)
	}
	if wd, ht := le.Uint32(avih.data[32:]), le.Uint32(avih.data[36:]); wd != 64 || ht != 48 {
		t.Fatalf("avih size %dx%d", wd, ht)
	}
	strl := find(t, hdrl.children, "LIST", "strl")
	strh := find(t, strl.children, "strh", "")
	if string(strh.data[0:4]) != "vids" || string(strh.data[4:8]) != "MJPG" {
		t.Fatalf("strh type %q handler %q", strh.data[0:4], strh.data[4:8])
	}
	if scale, rate, length := le.Uint32(strh.data[20:]), le.Uint32(strh.data[24:]), le.Uint32(strh.data[32:]); scale != 1 || rate != 10 || length != 3 {
		t.Fatalf("strh scale %d rate %d length %d", scale, rate, length)
	}
	if buf := le.Uint32(strh.data[36:]); int(buf) < len(red) || int(buf) < len(blue) {
		t.Fatalf("suggested buffer %d", buf)
	}
	strf := find(t, strl.children, "strf", "")
	if le.Uint32(strf.data[0:]) != 40 || int32(le.Uint32(strf.data[4:])) != 64 || int32(le.Uint32(strf.data[8:])) != 48 || string(strf.data[16:20]) != "MJPG" {
		t.Fatalf("BITMAPINFOHEADER %x", strf.data)
	}

	movi := find(t, top[0].children, "LIST", "movi")
	if len(movi.children) != 3 {
		t.Fatalf("%d movi chunks", len(movi.children))
	}
	for i, c := range movi.children {
		if c.id != "00dc" {
			t.Fatalf("movi chunk %d is %s", i, c.id)
		}
	}
	if !bytes.Equal(movi.children[0].data, red) || len(movi.children[1].data) != 0 || !bytes.Equal(movi.children[2].data, blue) {
		t.Fatal("movi chunk contents")
	}
	img, err := jpeg.Decode(bytes.NewReader(movi.children[2].data))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("frame does not decode: %v", err)
	}

	idx := find(t, top[0].children, "idx1", "")
	if len(idx.data) != 3*16 {
		t.Fatalf("idx1 size %d", len(idx.data))
	}
	moviData := movi.off + 8 // offsets count from the "movi" list type
	for i := 0; i < 3; i++ {
		e := idx.data[i*16:]
		id, flags, off, size := string(e[0:4]), le.Uint32(e[4:]), int(le.Uint32(e[8:])), int(le.Uint32(e[12:]))
		c := movi.children[i]
		if id != "00dc" || moviData+off != c.off || size != len(c.data) {
			t.Fatalf("index %d: %s off %d (chunk at %d) size %d", i, id, off, c.off-moviData, size)
		}
		if key := flags&0x10 != 0; key != (size > 0) {
			t.Fatalf("index %d: keyframe flag %v for size %d", i, key, size)
		}
	}
}

// TestAVIWriterPadsOddChunks: RIFF chunks are word aligned; an odd JPEG gets a pad byte that the
// chunk size does not count.
func TestAVIWriterPadsOddChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd.avi")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewAVIWriter(f, 8, 8, 5)
	if err != nil {
		t.Fatal(err)
	}
	odd := []byte{0xFF, 0xD8, 0xFF, 0xD9, 0x00}
	if err := w.WriteFrame(odd); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFrame(odd); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, _ := os.ReadFile(path)
	top := parseRIFF(t, b, 0, len(b))
	movi := find(t, top[0].children, "LIST", "movi")
	if len(movi.children) != 2 || len(movi.children[1].data) != 5 {
		t.Fatalf("movi %+v", movi.children)
	}
}
