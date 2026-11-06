// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/ImageData/RLEDecoder.ts, src/core/Utilities/ImageHelper.ts,
// src/core/AMTDesktop.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920.
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

const maxTile = 64         // Encoding.ts:42
const maxTileBytes = 16384 // upper bound for one tile's ZRLE payload after inflate

// TileError reports a malformed ZRLE tile.
type TileError struct {
	Subencoding byte
	Reason      string
}

func (e *TileError) Error() string {
	return fmt.Sprintf("zrle tile (subencoding %d): %s", e.Subencoding, e.Reason)
}

// pixelRGB332 converts an 8 bit pixel like ImageHelper.setPixel with bpp 1.
func pixelRGB332(v uint16) color.RGBA {
	return color.RGBA{R: uint8(v & 224), G: uint8((v & 28) << 3), B: fixColor(uint8((v & 3) << 6)), A: 255}
}

// pixelRGB565 converts a 16 bit pixel like ImageHelper.setPixel with bpp 2.
func pixelRGB565(v uint16) color.RGBA {
	return color.RGBA{R: uint8((v >> 8) & 248), G: uint8((v >> 3) & 252), B: uint8((v & 31) << 3), A: 255}
}

// fixColor is ImageHelper.fixColor: lifts 128 and 192 to 160 and 224.
func fixColor(c uint8) uint8 {
	if c > 127 {
		return c + 32
	}
	return c
}

// readPixel reads one pixel of bpp bytes (little-endian for 2) and converts it.
// The caller guarantees len(data) >= bpp.
func readPixel(data []byte, bpp int) color.RGBA {
	if bpp == 1 {
		return pixelRGB332(uint16(data[0]))
	}
	return pixelRGB565(uint16(data[0]) | uint16(data[1])<<8)
}

func checkGeometry(sub byte, w, h, bpp int, dst *image.RGBA) error {
	if bpp != 1 && bpp != 2 {
		return &TileError{sub, "bytes per pixel"}
	}
	if w < 1 || h < 1 || dst == nil || w > dst.Rect.Dx() || h > dst.Rect.Dy() {
		return &TileError{sub, "tile geometry"}
	}
	return nil
}

func setPix(dst *image.RGBA, w, i int, c color.RGBA) {
	o := dst.PixOffset(dst.Rect.Min.X+i%w, dst.Rect.Min.Y+i/w)
	dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = c.R, c.G, c.B, c.A
}

// decodeRaw fills the top-left w*h of dst from w*h*bpp bytes; RFB raw encoding and ZRLE subencoding 0.
func decodeRaw(data []byte, w, h, bpp int, dst *image.RGBA) error {
	if err := checkGeometry(0, w, h, bpp, dst); err != nil {
		return err
	}
	if len(data) < w*h*bpp {
		return io.ErrUnexpectedEOF
	}
	for i := 0; i < w*h; i++ {
		setPix(dst, w, i, readPixel(data[i*bpp:], bpp))
	}
	return nil
}

// readRun reads the run length encoding of RLEDecoder.ts: 1 + sum of bytes up to the first one != 255.
func readRun(data []byte, pos int) (run, next int, err error) {
	run = 1
	for {
		if pos >= len(data) {
			return 0, pos, io.ErrUnexpectedEOF
		}
		b := data[pos]
		pos++
		run += int(b)
		if b != 255 {
			return run, pos, nil
		}
	}
}

func readPalette(data []byte, pos, size, bpp int) ([]color.RGBA, int, error) {
	if len(data)-pos < size*bpp {
		return nil, pos, io.ErrUnexpectedEOF
	}
	pal := make([]color.RGBA, size)
	for i := range pal {
		pal[i] = readPixel(data[pos:], bpp)
		pos += bpp
	}
	return pal, pos, nil
}

// decodeTile parses one ZRLE tile (subencoding byte + payload) into the top-left w*h of dst
// (dst may be larger, pixels go through dst.PixOffset) and returns the bytes consumed.
// Short data -> io.ErrUnexpectedEOF; subencoding 17..127 or 129, palette index beyond the
// palette, run beyond w*h, or trailing bytes -> *TileError.
func decodeTile(data []byte, w, h, bpp int, dst *image.RGBA) (int, error) {
	if len(data) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	sub := data[0]
	if err := checkGeometry(sub, w, h, bpp, dst); err != nil {
		return 0, err
	}
	total := w * h
	pos := 1
	switch {
	case sub == 0:
		if err := decodeRaw(data[1:], w, h, bpp, dst); err != nil {
			return 0, err
		}
		pos += total * bpp
	case sub == 1:
		if len(data)-pos < bpp {
			return 0, io.ErrUnexpectedEOF
		}
		c := readPixel(data[pos:], bpp)
		pos += bpp
		for i := 0; i < total; i++ {
			setPix(dst, w, i, c)
		}
	case sub <= 16:
		pal, p, err := readPalette(data, pos, int(sub), bpp)
		if err != nil {
			return 0, err
		}
		pos = p
		if err := decodePacked(data[pos:], sub, w, h, pal, dst); err != nil {
			return 0, err
		}
		pos = len(data)
	case sub == 128:
		for n := 0; n < total; {
			if len(data)-pos < bpp {
				return 0, io.ErrUnexpectedEOF
			}
			c := readPixel(data[pos:], bpp)
			run, p, err := readRun(data, pos+bpp)
			if err != nil {
				return 0, err
			}
			pos = p
			if run > total-n {
				return 0, &TileError{sub, "run beyond tile"}
			}
			for ; run > 0; run-- {
				setPix(dst, w, n, c)
				n++
			}
		}
	case sub > 129:
		pal, p, err := readPalette(data, pos, int(sub)-128, bpp)
		if err != nil {
			return 0, err
		}
		pos = p
		for n := 0; n < total; {
			if pos >= len(data) {
				return 0, io.ErrUnexpectedEOF
			}
			idx := data[pos]
			pos++
			if int(idx&127) >= len(pal) {
				return 0, &TileError{sub, "palette index"}
			}
			run := 1
			if idx > 127 {
				var err error
				if run, pos, err = readRun(data, pos); err != nil {
					return 0, err
				}
			}
			if run > total-n {
				return 0, &TileError{sub, "run beyond tile"}
			}
			for ; run > 0; run-- {
				setPix(dst, w, n, pal[idx&127])
				n++
			}
		}
	default:
		return 0, &TileError{sub, "unsupported subencoding"}
	}
	if pos != len(data) {
		return 0, &TileError{sub, "trailing data"}
	}
	return pos, nil
}

// decodePacked decodes a packed palette tile. rest holds everything after the palette.
// ui-toolkit reads one continuous bit stream (RLEDecoder.ts:82-87); RFC 6143 pads every row to
// a byte boundary. The length of rest decides which of the two the server sent.
func decodePacked(rest []byte, sub byte, w, h int, pal []color.RGBA, dst *image.RGBA) error {
	bits := 4
	if sub == 2 {
		bits = 1
	} else if sub <= 4 {
		bits = 2
	}
	unpadded := (w*h*bits + 7) / 8
	padded := h * ((w*bits + 7) / 8)
	rowMode := len(rest) == padded && len(rest) != unpadded
	if !rowMode && len(rest) != unpadded {
		return &TileError{sub, "packed palette length"}
	}
	mask := byte(1<<bits - 1)
	bitPos := 0
	for y := 0; y < h; y++ {
		if rowMode {
			bitPos = y * ((w*bits + 7) / 8) * 8
		}
		for x := 0; x < w; x++ {
			b := rest[bitPos/8]
			idx := int(b>>(8-bits-bitPos%8)) & int(mask)
			bitPos += bits
			if idx >= len(pal) {
				return &TileError{sub, "palette index"}
			}
			setPix(dst, w, y*w+x, pal[idx])
		}
	}
	return nil
}

const inflateWindow = 1 << 15 // inflateInit(15), AMTDesktop.ts:66

var (
	ErrZlibHeader   = errors.New("zrle: first rectangle lacks a zlib header")
	ErrTileTooLarge = errors.New("zrle: inflated tile exceeds limit")
)

// inflater keeps AMT's single zlib stream alive across ZRLE rectangles: every rectangle holds
// complete deflate blocks ending in a sync flush, so the flate reader is reset per rectangle
// with the last 32 KiB of output as dictionary. The 2-byte zlib header is expected on the
// first rectangle that goes through inflate; rectangles the session decodes raw (stored-block
// bypass) never reach it. Output is capped at maxTileBytes via io.LimitReader.
type inflater struct {
	fr      io.ReadCloser
	window  []byte
	started bool
}

func newInflater() *inflater {
	return &inflater{fr: flate.NewReaderDict(bytes.NewReader(nil), nil)}
}

// inflate returns the decompressed bytes of one rectangle. io.ErrUnexpectedEOF is the normal
// end (no final block in the stream) and is not returned.
func (z *inflater) inflate(data []byte) ([]byte, error) {
	if !z.started {
		if len(data) < 2 || data[0]&0x0f != 8 || (uint16(data[0])<<8|uint16(data[1]))%31 != 0 {
			return nil, ErrZlibHeader
		}
		data = data[2:]
		z.started = true
	}
	if err := z.fr.(flate.Resetter).Reset(bytes.NewReader(data), z.window); err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(z.fr, maxTileBytes+1))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if len(out) > maxTileBytes {
		return nil, ErrTileTooLarge
	}
	z.window = append(z.window, out...)
	if len(z.window) > inflateWindow {
		z.window = append([]byte(nil), z.window[len(z.window)-inflateWindow:]...)
	}
	return out, nil
}
