package capture

import (
	"encoding/binary"
	"errors"
	"io"
)

// AVI 1.0 (RIFF "AVI ") with one Motion-JPEG video stream, written in pure Go (gesamtdesign V10):
// VLC and the standard players of Windows and macOS play it without extra codecs. The layout is
//
//	RIFF 'AVI '
//	  LIST 'hdrl'
//	    avih                      main header (56 bytes)
//	    LIST 'strl'
//	      strh                    stream header 'vids' 'MJPG' (56 bytes)
//	      strf                    BITMAPINFOHEADER (40 bytes)
//	  LIST 'movi'
//	    00dc <JPEG> ...           one chunk per frame slot; an empty chunk repeats the previous frame
//	  idx1                        16 bytes per chunk, offsets relative to the 'movi' list type
//
// The frame count, the largest chunk and the list sizes are only known at the end: Close writes
// the index and patches them. A file that was never closed (crash) has the frames but no index.

// aviKeyframe is AVIIF_KEYFRAME of an idx1 entry; every JPEG is a keyframe, repeats are not.
const aviKeyframe = 0x10

// Offsets in the file of the fields Close patches.
const (
	offRIFFSize     = 4
	offTotalFrames  = 12 + 12 + 8 + 16 // RIFF hdr, LIST hdrl hdr, avih hdr, 4 fields
	offAvihBuffer   = 12 + 12 + 8 + 28
	offStrhBuffer   = 12 + 12 + 8 + 56 + 12 + 8 + 36
	offStrhLength   = 12 + 12 + 8 + 56 + 12 + 8 + 32
	offMoviListSize = headerSize - 8
	headerSize      = 12 + 12 + 8 + 56 + 12 + 8 + 56 + 8 + 40 + 12 // up to and including "movi"
	hdrlSize        = 4 + 8 + 56 + 12 + 8 + 56 + 8 + 40
	strlSize        = 4 + 8 + 56 + 8 + 40
)

// AVIWriter writes an MJPEG AVI to a seekable file.
type AVIWriter struct {
	w      io.WriteSeeker
	size   int64 // bytes written so far, the header included
	index  []byte
	frames uint32
	maxBuf uint32
	closed bool
}

// errClosed: a write after Close.
var errClosed = errors.New("capture: AVI writer closed")

// NewAVIWriter writes the headers for a width x height video at fps frames per second.
func NewAVIWriter(w io.WriteSeeker, width, height, fps int) (*AVIWriter, error) {
	if width <= 0 || height <= 0 || fps <= 0 {
		return nil, errors.New("capture: invalid AVI size or rate")
	}
	b := make([]byte, 0, headerSize)
	le := binary.LittleEndian
	u32 := func(v uint32) { b = le.AppendUint32(b, v) }
	u16 := func(v uint16) { b = le.AppendUint16(b, v) }
	fcc := func(s string) { b = append(b, s...) }

	fcc("RIFF")
	u32(0) // patched
	fcc("AVI ")
	fcc("LIST")
	u32(hdrlSize)
	fcc("hdrl")

	fcc("avih")
	u32(56)
	u32(uint32(1000000 / fps))            // dwMicroSecPerFrame
	u32(uint32(width * height * 3 * fps)) // dwMaxBytesPerSec, an upper bound
	u32(0)                                // dwPaddingGranularity
	u32(0x10)                             // dwFlags: AVIF_HASINDEX
	u32(0)                                // dwTotalFrames, patched
	u32(0)                                // dwInitialFrames
	u32(1)                                // dwStreams
	u32(0)                                // dwSuggestedBufferSize, patched
	u32(uint32(width))                    // dwWidth
	u32(uint32(height))                   // dwHeight
	u32(0)                                // dwReserved[4]
	u32(0)
	u32(0)
	u32(0)

	fcc("LIST")
	u32(strlSize)
	fcc("strl")
	fcc("strh")
	u32(56)
	fcc("vids")
	fcc("MJPG")
	u32(0)           // dwFlags
	u16(0)           // wPriority
	u16(0)           // wLanguage
	u32(0)           // dwInitialFrames
	u32(1)           // dwScale
	u32(uint32(fps)) // dwRate: fps frames per second
	u32(0)           // dwStart
	u32(0)           // dwLength, patched
	u32(0)           // dwSuggestedBufferSize, patched
	u32(0xFFFFFFFF)  // dwQuality: default
	u32(0)           // dwSampleSize
	u16(0)           // rcFrame
	u16(0)
	u16(uint16(width))
	u16(uint16(height))

	fcc("strf")
	u32(40)
	u32(40)                         // biSize
	u32(uint32(width))              // biWidth
	u32(uint32(height))             // biHeight
	u16(1)                          // biPlanes
	u16(24)                         // biBitCount
	fcc("MJPG")                     // biCompression
	u32(uint32(width * height * 3)) // biSizeImage
	u32(0)                          // biXPelsPerMeter
	u32(0)                          // biYPelsPerMeter
	u32(0)                          // biClrUsed
	u32(0)                          // biClrImportant

	fcc("LIST")
	u32(0) // movi size, patched
	fcc("movi")
	if len(b) != headerSize {
		panic("capture: AVI header size")
	}
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	return &AVIWriter{w: w, size: headerSize}, nil
}

// Size is the current file size; after Close it includes the index.
func (a *AVIWriter) Size() int64 { return a.size }

// Frames is the number of frame slots written, repeats included.
func (a *AVIWriter) Frames() int { return int(a.frames) }

// WriteFrame appends one JPEG as the next frame.
func (a *AVIWriter) WriteFrame(jpeg []byte) error { return a.chunk(jpeg) }

// WriteRepeat appends an empty chunk: players show the previous frame again for this slot
// (the "drop frame" of VirtualDub and ffmpeg), which keeps the timeline without a second copy.
func (a *AVIWriter) WriteRepeat() error { return a.chunk(nil) }

// Projected is the size of the closed file after one more chunk of n bytes.
func (a *AVIWriter) Projected(n int) int64 { return a.size + int64(len(a.index)) + 8 + chunkCost(n) }

// chunkCost is what a chunk of n bytes adds to the closed file: header, data, pad and its
// index entry.
func chunkCost(n int) int64 { return int64(8 + n + n%2 + 16) }

func (a *AVIWriter) chunk(data []byte) error {
	if a.closed {
		return errClosed
	}
	off := uint32(a.size - (headerSize - 4)) // relative to the "movi" list type
	hdr := make([]byte, 8)
	copy(hdr, "00dc")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(data)))
	if _, err := a.w.Write(hdr); err != nil {
		return err
	}
	if _, err := a.w.Write(data); err != nil {
		return err
	}
	n := int64(8 + len(data))
	if len(data)%2 == 1 {
		if _, err := a.w.Write([]byte{0}); err != nil {
			return err
		}
		n++
	}
	a.size += n
	var flags uint32
	if len(data) > 0 {
		flags = aviKeyframe
	}
	le := binary.LittleEndian
	a.index = append(a.index, "00dc"...)
	a.index = le.AppendUint32(a.index, flags)
	a.index = le.AppendUint32(a.index, off)
	a.index = le.AppendUint32(a.index, uint32(len(data)))
	a.frames++
	a.maxBuf = max(a.maxBuf, uint32(len(data)))
	return nil
}

// Close writes the index and the final sizes and counts into the header. It does not close
// the underlying file.
func (a *AVIWriter) Close() error {
	if a.closed {
		return nil
	}
	a.closed = true
	moviSize := uint32(a.size - (headerSize - 4))
	idx := make([]byte, 8, 8+len(a.index))
	copy(idx, "idx1")
	binary.LittleEndian.PutUint32(idx[4:], uint32(len(a.index)))
	idx = append(idx, a.index...)
	if _, err := a.w.Write(idx); err != nil {
		return err
	}
	a.size += int64(len(idx))
	patch := []struct {
		off int64
		v   uint32
	}{
		{offRIFFSize, uint32(a.size - 8)},
		{offTotalFrames, a.frames},
		{offAvihBuffer, a.maxBuf},
		{offStrhLength, a.frames},
		{offStrhBuffer, a.maxBuf},
		{offMoviListSize, moviSize},
	}
	var v [4]byte
	for _, p := range patch {
		if _, err := a.w.Seek(p.off, io.SeekStart); err != nil {
			return err
		}
		binary.LittleEndian.PutUint32(v[:], p.v)
		if _, err := a.w.Write(v[:]); err != nil {
			return err
		}
	}
	_, err := a.w.Seek(0, io.SeekEnd)
	return err
}
