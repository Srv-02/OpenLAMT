// Ported from MeshCommander (github.com/Ylianst/MeshCommander), amt-desktop-0.0.2.js
// (handleServerCutText, sendKvmExtCmd, sendKvmData, ProcessData state 3) and index.html
// (onDesktopStateChange state 3: "present" for AMT 12 and later; kvmExtChanged for AMT > 15).
// MeshCommander by Ylian Saint-Hilaire and contributors. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

import (
	"bytes"
	"encoding/binary"
)

// Intel's in-band KVM messages travel as ClientCutText / ServerCutText.
const (
	kvmDataChannelPrefix = "\x00KvmDataChannel\x00"
	kvmExtCmdPrefix      = "\x00KvmExtCmd\x00"

	// maxCutText bounds the ServerCutText that is inspected for in-band messages; longer
	// texts are discarded unread, as before.
	maxCutText = 4096
)

// presentMessage is the soft-KVM presence indicator MeshCommander sends once per session on
// AMT 12 and later (index.html onDesktopStateChange, state 3).
const presentMessage = `{"action":"present","ver":1}`

// clientCutText frames payload as ClientCutText: type 6, three padding bytes, BE32 length.
func clientCutText(payload string) []byte {
	b := binary.BigEndian.AppendUint32([]byte{msgClientCutText, 0, 0, 0}, uint32(len(payload)))
	return append(b, payload...)
}

// kvmExtCmd builds MeshCommander's sendKvmExtCmd(cmd, val); without val only the command byte
// follows the prefix.
func kvmExtCmd(cmd byte, val ...byte) []byte {
	return clientCutText(kvmExtCmdPrefix + string(cmd) + string(val))
}

// dataChannel is MeshCommander's KVM data channel state (onKvmDataAck, onKvmDataPending):
// data waits until the device acknowledges the channel, then one message goes out per ack.
type dataChannel struct {
	ack     int8 // -1 unknown (onKvmDataAck == -1), 0 waiting, 1 acknowledged
	pending []string
}

func newDataChannel() *dataChannel { return &dataChannel{ack: -1} }

// sendData is sendKvmData: queue until acknowledged, otherwise send and wait for the next ack.
func (d *dataChannel) sendData(x string, send func([]byte) error) error {
	if d.ack != 1 {
		d.pending = append(d.pending, x)
		return nil
	}
	d.ack = 0
	return send(clientCutText(kvmDataChannelPrefix + x))
}

// serverCutText is the in-band part of handleServerCutText: a data channel message
// acknowledges and releases one pending message; KvmExtCmd 2 and 4 are answered with 1 and 3.
// Everything else (clipboard text, KvmExtCmd 1 and 3) is ignored.
func (d *dataChannel) serverCutText(t []byte, send func([]byte) error) error {
	switch {
	case len(t) >= 16 && bytes.HasPrefix(t, []byte(kvmDataChannelPrefix[:15])):
		if (d.ack == -1 && len(t) == 16) || t[15] != 0 {
			d.ack = 1
		}
		if d.ack == 1 && len(d.pending) > 0 {
			x := d.pending[0]
			d.pending = d.pending[1:]
			return d.sendData(x, send)
		}
	case len(t) >= 13 && bytes.HasPrefix(t, []byte(kvmExtCmdPrefix)):
		switch t[11] {
		case 2:
			return send(kvmExtCmd(1))
		case 4:
			return send(kvmExtCmd(3))
		}
	}
	return nil
}
