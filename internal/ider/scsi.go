// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/AMTIDER.ts, IDERDataProcessor.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920,
// cross-checked against MeshCommander (github.com/Ylianst/MeshCommander, amt-ider-ws-0.0.1.js).
// Copyright (c) Intel Corporation 2023. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package ider

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// Wire behavior of the original (spec R1): sendCommandEndResponse(error=true, ...) sends the
// variant with ATAPI status 0x50, i.e. GOOD without sense data, and error=false sends status
// 0x51 with sense data, i.e. CHECK CONDITION. The names are the other way round in MeshCommander
// and ui-toolkit; the bytes are what MeshCommander and ui-toolkit have run with in practice, so
// every call site keeps the original's choice. The comments name the original's call:
// "TS good" = sendCommandEndResponse(true, ...), "TS check" = sendCommandEndResponse(false, ...).

// sendGood sends SCSI status GOOD without sense data (AMTIDER.ts:138-141).
func (c *Client) sendGood(dev byte) error {
	return c.send(cmdCommandEnd, true, false, make([]byte, 12),
		[]byte{0xc5, 0, 3, 0, 0, 0, dev, 0x50, 0, 0, 0})
}

// sendCheckCondition sends CHECK CONDITION with sense key, ASC and ASCQ (AMTIDER.ts:143-168).
func (c *Client) sendCheckCondition(dev, sense, asc, ascq byte) error {
	return c.send(cmdCommandEnd, true, false, make([]byte, 12),
		[]byte{0x87, sense << 4, 3, 0, 0, 0, dev, 0x51, sense, asc, ascq})
}

// sendDataToHost sends SCSI data (AMTIDER.ts sendDataToHost). dma is featureRegister & 1. The
// original passes dma as a number, so the DMA attribute bit of the header is always set for 0x54
// (even for dma = 0): attributes are 3 for the completed chunk and 1 for the others.
func (c *Client) sendDataToHost(dev byte, completed bool, data []byte, dma bool) error {
	var dmalen uint16
	if !dma {
		dmalen = uint16(len(data))
	}
	h := make([]byte, 26)
	h[1], h[2] = byte(len(data)), byte(len(data)>>8)
	h[4] = 0xb5
	if dma {
		h[4] = 0xb4
	}
	h[6] = 2
	h[8], h[9] = byte(dmalen), byte(dmalen>>8)
	h[10], h[11] = dev, 0x58
	if completed {
		h[12], h[14] = 0x85, 3
		h[18], h[19] = dev, 0x50
	}
	return c.send(cmdDataToHost, completed, true, h, data)
}

// mediaBlocks is the number of 2048-byte blocks of the CD; the last block may be a partial
// sector that is padded with zeros. Floppy: no medium, 0.
func (c *Client) mediaBlocks(dev byte) uint64 {
	if dev == deviceFlopp {
		return 0
	}
	return uint64((c.size + sectorSize - 1) / sectorSize)
}

// handleFloppy answers commands for the floppy drive (0xA0), which never has a medium (ISO only).
// Deliberate deviation from the original (ui-toolkit and MeshCommander with floppy == null): it
// reports GOOD for TEST_UNIT_READY, for reads (without any data) and for several other commands,
// so the drive looks ready but delivers nothing. A Linux host then reset the drive in a loop
// (about 2.5 RESET_OCCURRED per second, each right after a floppy READ_CAPACITY). Here every
// medium command gets CHECK CONDITION NOT READY / MEDIUM NOT PRESENT (02/3A/00); only START_STOP
// and ALLOW_MEDIUM_REMOVAL succeed, and GET_EVENT_STATUS_NOTIFICATION reports "no medium".
func (c *Client) handleFloppy(cdb []byte) error {
	switch cdb[0] {
	case 0x1b, 0x1e: // START_STOP, ALLOW_MEDIUM_REMOVAL
		return c.sendGood(deviceFlopp)
	}
	return c.sendCheckCondition(deviceFlopp, 0x02, 0x3a, 0x00)
}

// handleWritten processes COMMAND_WRITTEN (IDERDataProcessor.ts:152-166); b is the body without
// the 8-byte header, so original offsets are reduced by 8.
func (c *Client) handleWritten(b []byte) error {
	flags := b[6]
	dev := byte(deviceFlopp)
	if flags&0x10 != 0 {
		dev = deviceCD
	}
	return c.handleSCSI(b[8:20], dev, b[1]&1 != 0, flags)
}

// handleSCSI dispatches one CDB (IDERDataProcessor.ts handleSCSI). dma is featureRegister & 1.
func (c *Client) handleSCSI(cdb []byte, dev byte, dma bool, flags byte) error {
	if dev == deviceFlopp && cdb[0] != 0x4a { // GET_EVENT_STATUS_NOTIFICATION reports "no medium" below
		return c.handleFloppy(cdb)
	}
	cd := dev == deviceCD
	switch cdb[0] {
	case 0x00: // TEST_UNIT_READY, :215-243
		if !c.cdReady {
			c.cdReady = true
			return c.sendGood(dev) // TS good 06/28/00 (unit attention): GOOD on the wire
		}
		return c.sendGood(dev) // TS good: ready
	case 0x08: // READ_6, :246-256
		lba := uint32(cdb[1]&0x1f)<<16 + uint32(cdb[2])<<8 + uint32(cdb[3])
		n := uint64(cdb[4])
		if n == 0 {
			n = 256
		}
		return c.readDisk(dev, lba, n, dma)
	case 0x0a: // WRITE_6, :259-268
		return c.sendGood(dev) // TS good 02/3A/00
	case 0x1a: // MODE_SENSE_6, :271-304
		if cdb[2] == 0x3f && cdb[3] == 0 {
			return c.sendDataToHost(dev, true, []byte{0, 0x05, 0x80, 0}, dma)
		}
		return c.sendGood(dev) // TS good 05/24/00
	case 0x1b: // START_STOP, :309-314
		return c.sendGood(dev) // TS good
	case 0x1e: // ALLOW_MEDIUM_REMOVAL, :317-329
		return c.sendGood(dev) // TS good (02/3A/00 for the floppy)
	case 0x23: // READ_FORMAT_CAPACITIES, :332-360
		if !cd || c.size == 0 {
			return c.sendCheckCondition(dev, 0x05, 0x24, 0x00) // TS check
		}
		return c.sendDataToHost(dev, true, []byte{0, 0, 0, 8, 0x00, 0x00, 0x0b, 0x40, 0x02, 0x00, 0x02, 0x00}, dma)
	case 0x25: // READ_CAPACITY, :363-395
		if !cd || c.size == 0 {
			return c.sendCheckCondition(dev, 0x02, 0x3a, 0x00) // TS check
		}
		last := uint32(c.mediaBlocks(dev) - 1)
		d := make([]byte, 8)
		binary.BigEndian.PutUint32(d, last)
		d[6] = 0x08 // block length 2048
		// The original passes deviceFlags (cdb byte 14 of the message) as the device byte.
		return c.sendDataToHost(flags, true, d, dma)
	case 0x28: // READ_10, :398-403
		return c.readDisk(dev, binary.BigEndian.Uint32(cdb[2:]), uint64(binary.BigEndian.Uint16(cdb[7:])), dma)
	case 0xa8: // READ_12: not in the original, same as READ_10 with a 32-bit length
		return c.readDisk(dev, binary.BigEndian.Uint32(cdb[2:]), uint64(binary.BigEndian.Uint32(cdb[6:])), dma)
	case 0x2a, 0x2e: // WRITE_10, WRITE_AND_VERIFY, :406-412
		// Deliberate deviation (spec R1): the original asks for data (0x52), which the medium
		// cannot take; CHECK CONDITION 07/27/00 write protected right away.
		return c.sendCheckCondition(dev, 0x07, 0x27, 0x00)
	case 0x43: // READ_TOC, :415-499
		return c.handleReadTOC(cdb, dev, dma)
	case 0x46: // GET_CONFIGURATION, :502-552
		return c.handleGetConfiguration(cdb, dev, dma)
	case 0x4a: // GET_EVENT_STATUS_NOTIFICATION, :555-567
		if cdb[1] != 0x01 && cdb[4] != 0x10 {
			return c.sendGood(dev) // TS good 05/26/01
		}
		var present byte // a floppy is never present
		if cd {
			present = 0x02
		}
		return c.sendDataToHost(dev, true, []byte{0, present, 0x80, 0}, dma)
	case 0x4c: // SEND_CUE_SHEET, :195-203: sense byte 0x50 and device 0xb0 as in the original
		return c.send(cmdCommandEnd, true, false, make([]byte, 12),
			[]byte{0x87, 0x50, 0x03, 0x00, 0x00, 0x00, 0xb0, 0x51, 0x05, 0x20, 0x00})
	case 0x51: // READ_DISC_INFO, :570-574
		return c.sendCheckCondition(dev, 0x05, 0x20, 0x00) // TS check
	case 0x55: // MODE_SELECT_10, :577-581
		return c.sendGood(dev) // TS good 05/20/00
	case 0x5a: // MODE_SENSE_10, :584-657
		return c.handleModeSense10(cdb, dev, dma)
	case 0xac: // GET_PERFORMANCE, :213-215
		return c.sendDataToHost(dev, true, rdPerformance, dma)
	}
	return c.sendCheckCondition(dev, 0x05, 0x20, 0x00) // TS check: unknown opcode, :219-222
}

func (c *Client) handleReadTOC(cdb []byte, dev byte, dma bool) error {
	msf := cdb[1]&0x02 != 0
	format := cdb[2] & 0x07
	if format == 0 {
		format = cdb[9] >> 6
	}
	if dev == deviceFlopp {
		return c.sendGood(dev) // TS good 05/20/00: not implemented
	}
	switch format {
	case 1:
		return c.sendDataToHost(dev, true, []byte{0x00, 0x0a, 0x01, 0x01, 0x00, 0x14, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}, dma)
	case 0:
		if msf {
			return c.sendDataToHost(dev, true, []byte{0x00, 0x12, 0x01, 0x01, 0x00, 0x14, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00,
				0x00, 0x14, 0xaa, 0x00, 0x00, 0x00, 0x34, 0x13}, dma)
		}
		return c.sendDataToHost(dev, true, []byte{0x00, 0x12, 0x01, 0x01, 0x00, 0x14, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x14, 0xaa, 0x00, 0x00, 0x00, 0x00, 0x00}, dma)
	}
	// Other formats (2..7): deliberate deviation from the original, which sends no reply at all and
	// leaves the host waiting for a command timeout. Reply CHECK CONDITION 05/24/00 (invalid field in CDB).
	return c.sendCheckCondition(dev, 0x05, 0x24, 0x00)
}

func (c *Client) handleGetConfiguration(cdb []byte, dev byte, dma bool) error {
	sendall := cdb[1] != 2
	firstcode := binary.BigEndian.Uint16(cdb[2:])
	buflen := int(binary.BigEndian.Uint16(cdb[7:]))
	if buflen == 0 {
		return c.sendDataToHost(dev, true, []byte{0, 0, 0, 0x3c, 0, 0, 0, 0x08}, dma)
	}
	r := []byte{0, 0, 0, 0x08}
	if firstcode == 0 {
		r = append(r, cdConfigProfileList...)
	}
	if firstcode == 0x1 || (sendall && firstcode < 0x1) {
		r = append(r, cdConfigCore...)
	}
	if firstcode == 0x2 || (sendall && firstcode < 0x2) {
		r = append(r, cdMorphing...)
	}
	if firstcode == 0x3 || (sendall && firstcode < 0x3) {
		r = append(r, cdConfigRemovable...)
	}
	if firstcode == 0x10 || (sendall && firstcode < 0x10) {
		r = append(r, cdConfigRandom...)
	}
	if firstcode == 0x1e || (sendall && firstcode < 0x1e) {
		r = append(r, cdRead...)
	}
	if firstcode == 0x100 || (sendall && firstcode < 0x100) {
		r = append(r, cdPowerManagement...)
	}
	if firstcode == 0x105 || (sendall && firstcode < 0x105) {
		r = append(r, cdTimeout...)
	}
	out := make([]byte, 4, 4+len(r))
	binary.BigEndian.PutUint32(out, uint32(len(r)))
	out = append(out, r...)
	if len(out) > buflen {
		out = out[:buflen]
	}
	return c.sendDataToHost(dev, true, out, dma)
}

func (c *Client) handleModeSense10(cdb []byte, dev byte, dma bool) error {
	if binary.BigEndian.Uint16(cdb[7:]) == 0 {
		return c.sendDataToHost(dev, true, []byte{0, 0, 0, 0x3c, 0, 0, 0, 0x08}, dma)
	}
	floppy := dev == deviceFlopp // no floppy image: sectorCount 0 selects the small-floppy pages
	var r []byte
	switch cdb[2] & 0x3f {
	case 0x01:
		if floppy {
			r = modeSenseRecoveryFloppyError
		} else {
			r = modeSenseRecoveryCDError
		}
	case 0x05:
		if floppy {
			r = modeSenseMSFloppyDiskPage
		}
	case 0x3f:
		if floppy {
			r = modeSenseMS3FFloppy
		} else {
			r = modeSenseMS3FCD
		}
	case 0x1a:
		if !floppy {
			r = modeSenseMSCD1A
		}
	case 0x1d:
		if !floppy {
			r = modeSenseMSCD1D
		}
	case 0x2a:
		if !floppy {
			r = modeSenseMSCD2A
		}
	}
	if r == nil {
		return c.sendCheckCondition(dev, 0x05, 0x20, 0x00) // TS check
	}
	return c.sendDataToHost(dev, true, r, dma)
}

// readDisk serves READ_6/READ_10/READ_12 (AMTIDER.ts sendDiskData and sendDiskDataEx). The read
// is synchronous: the image is read in chunks of at most readbfr bytes, each sent as one 0x54
// message; the last one carries "completed".
func (c *Client) readDisk(dev byte, lba uint32, blocks uint64, dma bool) error {
	if uint64(lba)+blocks > c.mediaBlocks(dev) {
		return c.sendGood(dev) // TS good 05/21/00: out of range
	}
	if blocks == 0 {
		return c.sendGood(dev) // TS good 00/00/00
	}
	if c.readBuf <= 0 { // dispatch rejects reads before OPEN_SESSION; never loop on an empty buffer
		return fmt.Errorf("%w: read before OPEN_SESSION reply", errProtocol)
	}
	off := int64(lba) * sectorSize
	remaining := int64(blocks) * sectorSize
	buf := make([]byte, min(int64(c.readBuf), remaining))
	for remaining > 0 {
		n := min(int64(len(buf)), remaining)
		chunk := buf[:n]
		if err := c.readImage(chunk, off); err != nil {
			// Deliberate deviation (spec R1): CHECK CONDITION MEDIUM ERROR 03/11/00, session stays.
			return c.sendCheckCondition(dev, 0x03, 0x11, 0x00)
		}
		off += n
		remaining -= n
		if err := c.sendDataToHost(dev, remaining == 0, chunk, dma); err != nil {
			return err
		}
		c.bytesRead.Add(n)
	}
	c.reads.Add(1)
	c.lastRead.Store(time.Now().UnixNano())
	return nil
}

// readImage fills buf from the image at off; bytes behind the end of the image (the padded last
// sector) are zero.
func (c *Client) readImage(buf []byte, off int64) error {
	n, err := c.img.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == io.EOF && off+int64(n) >= c.size {
		clear(buf[n:])
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return err
}
