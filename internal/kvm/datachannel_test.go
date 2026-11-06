package kvm

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// Byte strings MeshCommander sent to an AMT 16 device (plaintext capture, client to server).
var (
	meshKvmExtCmd40 = mustHex("060000000000000d004b766d457874436d64000400")
	meshKvmExtCmd3  = mustHex("060000000000000c004b766d457874436d640003")
	meshPresent     = append(mustHex("060000000000002c004b766d446174614368616e6e656c00"), `{"action":"present","ver":1}`...)
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// inBandSession dials the fake server with amtMajor; the first update carries the data channel
// ack and KvmExtCmd 4 (as the device sends them) in front of a frame.
func inBandSession(t *testing.T, amtMajor int) *kvmtest.Server {
	t.Helper()
	z := kvmtest.NewZlibStream()
	first := kvmtest.Concat(
		kvmtest.ServerCutText("\x00KvmDataChannel\x00"),
		kvmtest.ServerCutText("\x00KvmExtCmd\x00\x04\x00"),
		solidUpdate(z, []byte{0x1f, 0xf8}, 128, 64),
	)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: [][]byte{first}})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", AMTMajor: amtMajor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.WaitFrame(waitCtx(t), 0); err != nil {
		t.Fatal(err)
	}
	return srv
}

// wantMessages waits for len(want) client messages, checks that no further one follows (no
// periodic keepalive) and compares them byte by byte.
func wantMessages(t *testing.T, srv *kvmtest.Server, want ...[]byte) {
	t.Helper()
	waitMessages(t, srv, len(want))
	time.Sleep(100 * time.Millisecond)
	got := srv.ClientMessages()
	if len(got) != len(want) {
		t.Fatalf("%d client messages, want %d: %x", len(got), len(want), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d = %x, want %x", i, got[i], want[i])
		}
	}
}

// AMT 16: the order MeshCommander uses after ServerInit (amt-desktop-0.0.2.js state 3):
// SetEncodings, KvmExtCmd 4,0, incremental request; "present" only after the device's data
// channel ack; KvmExtCmd 4 from the device is answered with KvmExtCmd 3.
func TestDial_AMT16InBandSequenceLikeMeshCommander(t *testing.T) {
	srv := inBandSession(t, 16)
	full := framebufferUpdateRequest(true, 0, 0, 128, 64)
	wantMessages(t, srv, setEncodings, meshKvmExtCmd40, full, meshPresent, meshKvmExtCmd3, full)
}

// AMT 12 to 15: "present" but no KvmExtCmd 4,0 (MeshCommander: kvmExtChanged only for AMT > 15).
func TestDial_AMT12SendsPresentWithoutKvmExtCmd(t *testing.T) {
	srv := inBandSession(t, 12)
	full := framebufferUpdateRequest(true, 0, 0, 128, 64)
	wantMessages(t, srv, setEncodings, full, meshPresent, meshKvmExtCmd3, full)
}

// Unknown version: no "present", no KvmExtCmd 4,0; the device's KvmExtCmd 4 is still answered.
func TestDial_UnknownAMTVersionSendsNoPresent(t *testing.T) {
	srv := inBandSession(t, 0)
	full := framebufferUpdateRequest(true, 0, 0, 128, 64)
	wantMessages(t, srv, setEncodings, full, meshKvmExtCmd3, full)
}

func TestDataChannel(t *testing.T) {
	var sent [][]byte
	send := func(b []byte) error { sent = append(sent, b); return nil }
	d := newDataChannel()
	if err := d.sendData(presentMessage, send); err != nil || len(sent) != 0 {
		t.Fatalf("sent before ack: %x", sent)
	}
	// A clipboard text and the informational ExtCmds 1 and 3 change nothing.
	for _, m := range []string{"hello", "\x00KvmExtCmd\x00\x03\x00", "\x00KvmExtCmd\x00\x01\x00"} {
		if err := d.serverCutText([]byte(m), send); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 0 {
		t.Fatalf("sent %x", sent)
	}
	_ = d.serverCutText([]byte(kvmDataChannelPrefix), send) // ack
	if len(sent) != 1 || !bytes.Equal(sent[0], meshPresent) {
		t.Fatalf("after ack %x", sent)
	}
	// A second bare 16 byte message is no ack any more (onKvmDataAck is false, not -1).
	_ = d.sendData("x", send)
	_ = d.serverCutText([]byte(kvmDataChannelPrefix), send)
	if len(sent) != 1 {
		t.Fatalf("sent without ack %x", sent)
	}
	// Byte 15 not zero acknowledges.
	_ = d.serverCutText([]byte("\x00KvmDataChannel\x01"), send)
	if len(sent) != 2 || !bytes.Equal(sent[1], append(mustHex("0600000000000011"), kvmDataChannelPrefix+"x"...)) {
		t.Fatalf("second %x", sent)
	}
	_ = d.serverCutText([]byte("\x00KvmExtCmd\x00\x02\x00"), send)
	if len(sent) != 3 || !bytes.Equal(sent[2], mustHex("060000000000000c004b766d457874436d640001")) {
		t.Fatalf("ExtCmd 2 reply %x", sent)
	}
}
