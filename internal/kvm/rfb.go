// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/RFBStateProcessors/HandshakeState.ts, src/core/RFBStateProcessors/SecurityOptions.ts,
// src/core/RFBStateProcessors/SecurityResponse.ts, src/core/RFBStateProcessors/ServerInit.ts,
// src/core/RFBStateProcessors/FrameBufferBellServerCutText.ts, src/core/RFBStateProcessors/Encoding.ts,
// src/core/ImageData/DataProcessor.ts, src/core/Utilities/CommsHelper.ts),
// commit e1eb6980963b1e59df45e05edf94b68dd5c36920.
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"io"
)

const (
	encodingRaw         int32 = 0
	encodingZRLE        int32 = 16
	encodingDesktopSize int32 = -223

	msgFramebufferUpdate = 0 // server to client
	msgBell              = 2
	msgServerCutText     = 3

	msgSetPixelFormat           = 0 // client to server
	msgSetEncodings             = 2
	msgClientCutText            = 6
	msgFramebufferUpdateRequest = 3
	msgKeyEvent                 = 4
	msgPointerEvent             = 5

	maxScreen   = 8192
	maxName     = 1024
	maxZRLEData = 65536
)

var (
	clientVersion     = []byte("RFB 003.008\n")
	pixelFormatRGB332 = []byte{0, 0, 0, 0, 8, 8, 0, 1, 0, 7, 0, 7, 0, 3, 5, 2, 0, 0, 0, 0}
	pixelFormatRGB565 = []byte{0, 0, 0, 0, 16, 16, 0, 1, 0, 31, 0, 63, 0, 31, 11, 5, 0, 0, 0, 0}
	// ServerInit.ts:56-: ZRLE, RAW, then the Intel KVM data channel (1092) and DesktopSize, in
	// the order MeshCommander uses (amt-desktop-0.0.2.js). ui-toolkit leaves 1092 out.
	setEncodings = []byte{msgSetEncodings, 0, 0, 4, 0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0x04, 0x44, 0xff, 0xff, 0xff, 0x21}
)

// RFBError reports a protocol violation. Stage is "security", "serverinit", "rectangle",
// "encoding" or "message".
type RFBError struct{ Stage, Reason string }

func (e *RFBError) Error() string { return fmt.Sprintf("rfb %s: %s", e.Stage, e.Reason) }

// rfbHandshake exchanges version (server bytes not checked), security (None), ClientInit (shared)
// and ServerInit, then sends SetEncodings (ZRLE, RAW, DesktopSize) and the pixel format for q:
// Low always sends pixelFormatRGB332; High sends pixelFormatRGB565 only if ServerInit's
// PIXEL_FORMAT bytes 4..16 differ from pixelFormatRGB565[4:17]. Returns width, height, name.
func rfbHandshake(r *bufio.Reader, w io.Writer, q Quality) (width, height int, name string, err error) {
	var ver [12]byte
	if _, err = io.ReadFull(r, ver[:]); err != nil {
		return
	}
	if _, err = w.Write(clientVersion); err != nil {
		return
	}
	n, err := r.ReadByte()
	if err != nil {
		return
	}
	if n == 0 {
		err = &RFBError{"security", "server offers no security type"}
		return
	}
	types := make([]byte, n)
	if _, err = io.ReadFull(r, types); err != nil {
		return
	}
	if !bytes.Contains(types, []byte{1}) {
		err = &RFBError{"security", "server does not offer security type None"}
		return
	}
	if _, err = w.Write([]byte{1}); err != nil {
		return
	}
	var res [4]byte
	if _, err = io.ReadFull(r, res[:]); err != nil {
		return
	}
	if binary.BigEndian.Uint32(res[:]) != 0 {
		err = &RFBError{"security", "security handshake failed"}
		return
	}
	if _, err = w.Write([]byte{1}); err != nil { // ClientInit: shared
		return
	}
	var si [24]byte
	if _, err = io.ReadFull(r, si[:]); err != nil {
		return
	}
	width, height = int(binary.BigEndian.Uint16(si[0:])), int(binary.BigEndian.Uint16(si[2:]))
	nameLen := binary.BigEndian.Uint32(si[20:])
	if width < 1 || width > maxScreen || height < 1 || height > maxScreen {
		err = &RFBError{"serverinit", "screen size out of range"}
		return
	}
	if nameLen > maxName {
		err = &RFBError{"serverinit", "desktop name too long"}
		return
	}
	nb := make([]byte, nameLen)
	if _, err = io.ReadFull(r, nb); err != nil {
		return
	}
	name = string(nb)

	if _, err = w.Write(setEncodings); err != nil {
		return
	}
	switch {
	case q == QualityLow:
		_, err = w.Write(pixelFormatRGB332)
	case !bytes.Equal(si[4:4+13], pixelFormatRGB565[4:17]):
		_, err = w.Write(pixelFormatRGB565)
	}
	return
}

// session decodes server messages into the framebuffer; one per connection, used by the read loop.
// send is the caller's serialised writer, used for the DesktopSize request and the in-band
// data channel replies.
type session struct {
	r    *bufio.Reader
	send func([]byte) error
	fb   *Framebuffer
	dc   *dataChannel // in-band messages in ServerCutText (datachannel.go)
	bpp  int
	inf  *inflater
	tile *image.RGBA // one 64x64 buffer
}

func newSession(r *bufio.Reader, send func([]byte) error, fb *Framebuffer, q Quality) *session {
	return &session{r: r, send: send, fb: fb, dc: newDataChannel(), bpp: q.bytesPerPixel(), inf: newInflater(),
		tile: image.NewRGBA(image.Rect(0, 0, maxTile, maxTile))}
}

// readMessage consumes one server message; frameDone is true after a complete FramebufferUpdate.
func (s *session) readMessage() (frameDone bool, err error) {
	t, err := s.r.ReadByte()
	if err != nil {
		return false, err
	}
	switch t {
	case msgFramebufferUpdate:
		var h [3]byte // padding, rectangle count
		if _, err := io.ReadFull(s.r, h[:]); err != nil {
			return false, err
		}
		for n := binary.BigEndian.Uint16(h[1:]); n > 0; n-- {
			if err := s.readRectangle(); err != nil {
				return false, err
			}
		}
		return true, nil
	case msgBell:
		return false, nil
	case msgServerCutText:
		var h [7]byte // padding, length
		if _, err := io.ReadFull(s.r, h[:]); err != nil {
			return false, err
		}
		n := binary.BigEndian.Uint32(h[3:])
		if n > maxCutText {
			_, err := io.CopyN(io.Discard, s.r, int64(n))
			return false, err
		}
		t := make([]byte, n)
		if _, err := io.ReadFull(s.r, t); err != nil {
			return false, err
		}
		return false, s.dc.serverCutText(t, s.send)
	}
	return false, &RFBError{"message", fmt.Sprintf("unknown server message type %d", t)}
}

func (s *session) readRectangle() error {
	var h [12]byte
	if _, err := io.ReadFull(s.r, h[:]); err != nil {
		return err
	}
	x, y := int(binary.BigEndian.Uint16(h[0:])), int(binary.BigEndian.Uint16(h[2:]))
	w, ht := int(binary.BigEndian.Uint16(h[4:])), int(binary.BigEndian.Uint16(h[6:]))
	enc := int32(binary.BigEndian.Uint32(h[8:]))

	if enc == encodingDesktopSize {
		if w < 1 || w > maxScreen || ht < 1 || ht > maxScreen {
			return &RFBError{"rectangle", "desktop size out of range"}
		}
		s.fb.resize(w, ht)
		return s.send(framebufferUpdateRequest(false, 0, 0, w, ht))
	}
	if enc != encodingRaw && enc != encodingZRLE {
		return &RFBError{"encoding", fmt.Sprintf("unsupported encoding %d", enc)}
	}
	if w < 1 || w > maxTile || ht < 1 || ht > maxTile {
		return &RFBError{"rectangle", "rectangle exceeds 64x64 tile"}
	}

	if enc == encodingRaw {
		buf := make([]byte, w*ht*s.bpp)
		if _, err := io.ReadFull(s.r, buf); err != nil {
			return err
		}
		if err := decodeRaw(buf, w, ht, s.bpp, s.tile); err != nil {
			return err
		}
	} else {
		var l [4]byte
		if _, err := io.ReadFull(s.r, l[:]); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(l[:])
		if n > maxZRLEData {
			return &RFBError{"rectangle", "ZRLE data too long"}
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(s.r, data); err != nil {
			return err
		}
		tileData := data
		if n > 5 && data[0] == 0 && int(binary.LittleEndian.Uint16(data[1:3])) == int(n)-5 {
			tileData = data[5:] // bare stored block, Encoding.ts:121-123
		} else {
			var err error
			if tileData, err = s.inf.inflate(data); err != nil {
				return err
			}
		}
		if _, err := decodeTile(tileData, w, ht, s.bpp, s.tile); err != nil {
			return err
		}
	}
	s.fb.draw(x, y, s.tile.SubImage(image.Rect(0, 0, w, ht)).(*image.RGBA))
	return nil
}

// framebufferUpdateRequest builds the 10 byte request (CommsHelper.ts sendRefresh).
func framebufferUpdateRequest(incremental bool, x, y, w, h int) []byte {
	b := []byte{msgFramebufferUpdateRequest, 0}
	if incremental {
		b[1] = 1
	}
	for _, v := range []int{x, y, w, h} {
		b = binary.BigEndian.AppendUint16(b, uint16(v))
	}
	return b
}
