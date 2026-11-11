package ider

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/ider/idertest"
	"github.com/Srv-02/openlamt/internal/redir"
)

// testImage is an in-memory image that records Close and can fail reads.
type testImage struct {
	data    []byte
	closed  atomic.Int32
	failAt  int64 // reads touching offsets >= failAt fail (0 = never)
	readErr error
}

func newImage(n int) *testImage {
	d := make([]byte, n)
	for i := range d {
		d[i] = byte(i*7 + i>>8 + 1)
	}
	return &testImage{data: d}
}

func (m *testImage) Size() int64 { return int64(len(m.data)) }
func (m *testImage) Close() error {
	m.closed.Add(1)
	return nil
}
func (m *testImage) ReadAt(p []byte, off int64) (int, error) {
	if m.failAt > 0 && off+int64(len(p)) > m.failAt {
		return 0, m.readErr
	}
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// patternImage is a sparse image of a given size with a deterministic content, for LBAs that
// overflow 32-bit byte offsets.
type patternImage struct {
	size   int64
	closed atomic.Int32
}

func pat(off int64) byte { return byte(off*31 + off>>11) }

func (m *patternImage) Size() int64 { return m.size }
func (m *patternImage) Close() error {
	m.closed.Add(1)
	return nil
}
func (m *patternImage) ReadAt(p []byte, off int64) (int, error) {
	for i := range p {
		if off+int64(i) >= m.size {
			return i, io.EOF
		}
		p[i] = pat(off + int64(i))
	}
	return len(p), nil
}

type fixture struct {
	c   *Client
	p   *idertest.Peer
	srv *idertest.Server
}

type fixtureOpts struct {
	readbfr uint16
	start   StartMode
	tls     bool
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "idertest"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// openSessionWire is OPEN_SESSION (0x40, seq 0, rx 30000, tx 0, heartbeat 20000, version 1).
var openSessionWire = []byte{0x40, 0, 0, 0, 0, 0, 0, 0, 0x30, 0x75, 0, 0, 0x20, 0x4e, 1, 0, 0, 0}

func toggleWire(seq byte, value byte) []byte {
	return []byte{0x48, 0, 0, 0, seq, 0, 0, 0, 3, value, 0, 0, 0}
}

func wire(m idertest.Msg) []byte {
	b := []byte{m.Cmd, 0, 0, m.Attr, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[4:], m.Seq)
	return append(b, m.Payload...)
}

// establish starts a fake server, dials and plays the device side of the session setup:
// OPEN_SESSION, reply, toggle, STATUS_DATA type 3.
func establish(t *testing.T, img Image, o fixtureOpts) *fixture {
	t.Helper()
	if o.readbfr == 0 {
		o.readbfr = 8192
	}
	so := idertest.Options{Username: "admin", Password: "pw"}
	cfg := Config{Username: "admin", Password: "pw", Image: img, Start: o.start}
	if o.tls {
		so.TLS = &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}}
		cfg.TLS = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	}
	srv := idertest.Start(t, so)
	cfg.Addr = srv.Addr
	type res struct {
		c   *Client
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := Dial(context.Background(), cfg)
		ch <- res{c, err}
	}()
	p := srv.Accept(t)
	m := p.Read(t)
	if !bytes.Equal(wire(m), openSessionWire) {
		t.Fatalf("OPEN_SESSION = %x, want %x", wire(m), openSessionWire)
	}
	p.OpenSessionReply(t, o.readbfr, 8192, 0)
	m = p.Read(t)
	value := byte(0x19)
	if o.start == StartOnReboot {
		value = 0x09
	}
	if !bytes.Equal(wire(m), toggleWire(1, value)) {
		t.Fatalf("toggle = %x, want %x", wire(m), toggleWire(1, value))
	}
	p.StatusData(t, 3, 1)
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	t.Cleanup(func() { r.c.Close() })
	return &fixture{c: r.c, p: p, srv: srv}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitDone(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("session did not end")
	}
}

// Expected wire payloads, written out from AMTIDER.ts.

func zeros(n int) []byte { return make([]byte, n) }

// good is the 0x51 payload sendCommandEndResponse(true, ...) produces: status 0x50, no sense.
func good(dev byte) []byte {
	return append(zeros(12), 0xc5, 0, 3, 0, 0, 0, dev, 0x50, 0, 0, 0)
}

// check is the 0x51 payload sendCommandEndResponse(false, ...) produces: CHECK CONDITION.
func check(dev, sense, asc, ascq byte) []byte {
	return append(zeros(12), 0x87, sense<<4, 3, 0, 0, 0, dev, 0x51, sense, asc, ascq)
}

// dataHdr is the 26-byte header of a 0x54 chunk with n data bytes.
func dataHdr(dev byte, dma, completed bool, n int) []byte {
	h := make([]byte, 26)
	h[1], h[2] = byte(n), byte(n>>8)
	h[4], h[6] = 0xb5, 2
	if dma {
		h[4] = 0xb4
	} else {
		h[8], h[9] = byte(n), byte(n>>8)
	}
	h[10], h[11] = dev, 0x58
	if completed {
		h[12], h[14], h[18], h[19] = 0x85, 3, dev, 0x50
	}
	return h
}

// head limits a payload for failure messages.
func head(b []byte) []byte { return b[:min(len(b), 48)] }

func (f *fixture) expectEnd(t *testing.T, want []byte) {
	t.Helper()
	m := f.p.Read(t)
	if m.Cmd != 0x51 || m.Attr != 2 || !bytes.Equal(m.Payload, want) {
		t.Fatalf("got cmd %#x attr %d payload %x\nwant cmd 0x51 attr 2 payload %x", m.Cmd, m.Attr, head(m.Payload), head(want))
	}
}

// expectData reads one completed 0x54 message and compares it with the original's bytes.
func (f *fixture) expectData(t *testing.T, dev byte, dma bool, data []byte) {
	t.Helper()
	m := f.p.Read(t)
	want := append(dataHdr(dev, dma, true, len(data)), data...)
	if m.Cmd != 0x54 || m.Attr != 3 || !bytes.Equal(m.Payload, want) {
		t.Fatalf("got cmd %#x attr %d payload %x\nwant cmd 0x54 attr 3 payload %x", m.Cmd, m.Attr, head(m.Payload), head(want))
	}
}

// readTransfer reads the chunks of one transfer, checking every header, and returns the data.
func (f *fixture) readTransfer(t *testing.T, dev byte, dma bool, chunk int, total int) []byte {
	t.Helper()
	var out []byte
	for len(out) < total {
		m := f.p.Read(t)
		n := min(chunk, total-len(out))
		last := len(out)+n == total
		wantAttr := byte(1)
		if last {
			wantAttr = 3
		}
		if m.Cmd != 0x54 || m.Attr != wantAttr || len(m.Payload) != 26+n || !bytes.Equal(m.Payload[:26], dataHdr(dev, dma, last, n)) {
			t.Fatalf("chunk at %d: cmd %#x attr %d, header %x (len %d), want chunk %d attr %d", len(out), m.Cmd, m.Attr, m.Payload[:min(26, len(m.Payload))], len(m.Payload), n, wantAttr)
		}
		out = append(out, m.Payload[26:]...)
	}
	return out
}

func cdb(b ...byte) []byte { return append(b, make([]byte, 12-len(b))...) }

func read10(lba uint32, n uint16) []byte {
	c := cdb(0x28)
	binary.BigEndian.PutUint32(c[2:], lba)
	binary.BigEndian.PutUint16(c[7:], n)
	return c
}

func read12(lba, n uint32) []byte {
	c := cdb(0xa8)
	binary.BigEndian.PutUint32(c[2:], lba)
	binary.BigEndian.PutUint32(c[6:], n)
	return c
}

const (
	cdFlags     = 0x10 // message byte 14: bit 4 selects the CD
	floppyFlags = 0x00
)

func TestDial_SessionSetupWire(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	if f.c.Err() != nil || img.closed.Load() != 0 {
		t.Fatalf("err %v closed %d", f.c.Err(), img.closed.Load())
	}
	if s := f.c.Stats(); s.ImageSize != 4096 || s.Started.IsZero() || s.Reads != 0 || !s.LastRead.IsZero() {
		t.Fatalf("stats %+v", s)
	}
}

func TestDial_StartOnRebootToggle(t *testing.T) {
	establish(t, newImage(4096), fixtureOpts{start: StartOnReboot}) // checks 0x09 on the wire
}

func TestDial_RetoggleOnRegsAvail(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	f.p.StatusData(t, 1, 1) // REGS_AVAIL bit 0
	m := f.p.Read(t)
	if !bytes.Equal(wire(m), toggleWire(2, 0x19)) {
		t.Fatalf("re-toggle = %x", wire(m))
	}
	f.p.StatusData(t, 1, 0) // bit 0 clear: nothing
	f.p.Ping(t)
	if m := f.p.Read(t); m.Cmd != 0x45 || m.Seq != 3 {
		t.Fatalf("got %x", wire(m))
	}
}

func TestDial_RetoggleBeforeToggleReply(t *testing.T) {
	img := newImage(4096)
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	ch := make(chan error, 1)
	go func() {
		c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Image: img})
		if c != nil {
			t.Cleanup(func() { c.Close() })
		}
		ch <- err
	}()
	p := srv.Accept(t)
	p.Read(t)
	p.OpenSessionReply(t, 8192, 8192, 0)
	p.Read(t) // toggle
	p.StatusData(t, 1, 1)
	if m := p.Read(t); !bytes.Equal(wire(m), toggleWire(2, 0x19)) {
		t.Fatalf("re-toggle = %x", wire(m))
	}
	p.StatusData(t, 3, 1)
	if err := <-ch; err != nil {
		t.Fatal(err)
	}
}

func dialFail(t *testing.T, img Image, script func(t *testing.T, p *idertest.Peer)) error {
	t.Helper()
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	ch := make(chan error, 1)
	go func() {
		_, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Image: img})
		ch <- err
	}()
	p := srv.Accept(t)
	p.Read(t)
	script(t, p)
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Dial did not return")
		return nil
	}
}

func TestDial_ToggleFailure(t *testing.T) {
	img := newImage(4096)
	err := dialFail(t, img, func(t *testing.T, p *idertest.Peer) {
		p.OpenSessionReply(t, 8192, 8192, 0)
		p.Read(t)
		p.StatusData(t, 3, 0)
	})
	if !errors.Is(err, ErrToggleFailed) {
		t.Fatalf("err = %v", err)
	}
	if img.closed.Load() != 0 {
		t.Fatal("Dial closed the image on error")
	}
}

func TestDial_ToggleTimeout(t *testing.T) {
	old := toggleTimeout
	toggleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { toggleTimeout = old })
	img := newImage(4096)
	start := time.Now()
	err := dialFail(t, img, func(t *testing.T, p *idertest.Peer) {
		p.OpenSessionReply(t, 8192, 8192, 0)
		p.Read(t) // toggle, never answered
	})
	if !errors.Is(err, ErrToggleFailed) || time.Since(start) > 3*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
	if img.closed.Load() != 0 {
		t.Fatal("Dial closed the image on error")
	}
}

func TestDial_BadOpenSessionReply(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rd, wr       uint16
		proto        byte
		wantNoToggle bool
	}{
		{"readbfr 0", 0, 8192, 0, true},
		{"readbfr too big", 8193, 8192, 0, true},
		{"writebfr too big", 8192, 8193, 0, true},
		{"proto", 8192, 8192, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := newImage(4096)
			err := dialFail(t, img, func(t *testing.T, p *idertest.Peer) {
				p.OpenSessionReply(t, tc.rd, tc.wr, tc.proto)
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if img.closed.Load() != 0 {
				t.Fatal("Dial closed the image on error")
			}
		})
	}
}

func TestDial_FirstMessageNotOpenSession(t *testing.T) {
	img := newImage(4096)
	err := dialFail(t, img, func(t *testing.T, p *idertest.Peer) { p.Send(t, 0x77, 0, nil) })
	if err == nil || img.closed.Load() != 0 {
		t.Fatalf("err %v closed %d", err, img.closed.Load())
	}
}

// A peer that sends device commands before the OPEN_SESSION reply (readbfr still unknown) must
// get a protocol error instead of a read loop with an empty buffer that never advances.
func TestDial_CommandsBeforeOpenSession(t *testing.T) {
	read10 := []byte{0x28, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0}
	for _, tc := range []struct {
		name   string
		script func(t *testing.T, p *idertest.Peer)
	}{
		{"read10", func(t *testing.T, p *idertest.Peer) { p.Written(t, read10, 0x10, 0) }},
		{"reset", func(t *testing.T, p *idertest.Peer) { p.Reset(t) }},
		{"data from host", func(t *testing.T, p *idertest.Peer) { p.Send(t, 0x53, 0, make([]byte, 6)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := newImage(4096)
			err := dialFail(t, img, tc.script)
			if !errors.Is(err, errProtocol) || img.closed.Load() != 0 {
				t.Fatalf("err %v closed %d", err, img.closed.Load())
			}
		})
	}
}

func TestDial_ContextTimeout(t *testing.T) {
	img := newImage(4096)
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := Dial(ctx, Config{Addr: srv.Addr, Username: "admin", Password: "pw", Image: img})
	if !errors.Is(err, context.DeadlineExceeded) || img.closed.Load() != 0 {
		t.Fatalf("err %v closed %d", err, img.closed.Load())
	}
}

func TestDial_WrongPasswordKeepsImage(t *testing.T) {
	img := newImage(4096)
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	_, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "nope", Image: img})
	var re *redir.RedirectionError
	if !errors.As(err, &re) || re.Stage != "auth" {
		t.Fatalf("err = %v", err)
	}
	if img.closed.Load() != 0 {
		t.Fatal("Dial closed the image on error")
	}
}

func TestDial_InvalidConfig(t *testing.T) {
	if _, err := Dial(context.Background(), Config{Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("nil image accepted")
	}
	img := newImage(4096)
	if _, err := Dial(context.Background(), Config{Addr: "127.0.0.1:1", Image: img, Start: StartMode(9)}); err == nil || img.closed.Load() != 0 {
		t.Fatalf("err %v closed %d", err, img.closed.Load())
	}
}

func TestTestUnitReady(t *testing.T) {
	f := establish(t, newImage(4096), fixtureOpts{})
	// First TUR: the original calls sendCommandEndResponse(true, 6, ...) (unit attention) which
	// puts GOOD on the wire; second: ready, also GOOD.
	for range 2 {
		f.p.Written(t, cdb(0x00), cdFlags, 0)
		f.expectEnd(t, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xc5, 0, 3, 0, 0, 0, 0xb0, 0x50, 0, 0, 0})
	}
}

func TestSCSI_Table(t *testing.T) {
	img := newImage(5000) // 3 blocks, the last one padded
	f := establish(t, img, fixtureOpts{})
	for _, tc := range []struct {
		name    string
		cdb     []byte
		flags   byte
		feature byte
		end     []byte // expected 0x51 payload
		dev     byte   // device byte of the expected data reply
		data    []byte // expected 0x54 data; nil = expect end
		dma     bool
	}{
		// CD (0xB0)
		{name: "WRITE_6", cdb: cdb(0x0a), flags: cdFlags, end: good(0xb0)},
		{name: "START_STOP", cdb: cdb(0x1b, 0, 0, 0, 2), flags: cdFlags, end: good(0xb0)},
		{name: "ALLOW_MEDIUM_REMOVAL", cdb: cdb(0x1e), flags: cdFlags, end: good(0xb0)},
		{name: "MODE_SELECT_10", cdb: cdb(0x55), flags: cdFlags, end: good(0xb0)},
		{name: "MODE_SENSE_6 all pages", cdb: cdb(0x1a, 0, 0x3f, 0), flags: cdFlags, dev: 0xb0, data: []byte{0, 5, 0x80, 0}},
		{name: "MODE_SENSE_6 dma", cdb: cdb(0x1a, 0, 0x3f, 0), flags: cdFlags, feature: 1, dev: 0xb0, dma: true, data: []byte{0, 5, 0x80, 0}},
		{name: "MODE_SENSE_6 other page", cdb: cdb(0x1a, 0, 0x01, 0), flags: cdFlags, end: good(0xb0)},
		{name: "READ_FORMAT_CAPACITIES", cdb: cdb(0x23), flags: cdFlags, dev: 0xb0,
			data: []byte{0, 0, 0, 8, 0, 0, 0x0b, 0x40, 0x02, 0, 0x02, 0}},
		{name: "READ_CAPACITY", cdb: cdb(0x25), flags: 0x50, dev: 0x50, // deviceFlags is the device byte
			data: []byte{0, 0, 0, 2, 0, 0, 8, 0}},
		{name: "READ_TOC format 1", cdb: cdb(0x43, 0, 1), flags: cdFlags, dev: 0xb0,
			data: []byte{0x00, 0x0a, 0x01, 0x01, 0x00, 0x14, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{name: "READ_TOC format 0", cdb: cdb(0x43), flags: cdFlags, dev: 0xb0,
			data: []byte{0x00, 0x12, 0x01, 0x01, 0x00, 0x14, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x14, 0xaa, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{name: "READ_TOC format 0 msf", cdb: cdb(0x43, 0x02), flags: cdFlags, dev: 0xb0,
			data: []byte{0x00, 0x12, 0x01, 0x01, 0x00, 0x14, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x14, 0xaa, 0x00, 0x00, 0x00, 0x34, 0x13}},
		{name: "READ_TOC format from byte 9", cdb: cdb(0x43, 0, 0, 0, 0, 0, 0, 0, 0, 0x40), flags: cdFlags, dev: 0xb0,
			data: []byte{0x00, 0x0a, 0x01, 0x01, 0x00, 0x14, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{name: "GET_EVENT_STATUS_NOTIFICATION", cdb: cdb(0x4a, 0x01), flags: cdFlags, dev: 0xb0, data: []byte{0, 2, 0x80, 0}},
		{name: "GET_EVENT_STATUS_NOTIFICATION byte 4", cdb: cdb(0x4a, 0, 0, 0, 0x10), flags: cdFlags, dev: 0xb0, data: []byte{0, 2, 0x80, 0}},
		{name: "GET_EVENT_STATUS_NOTIFICATION invalid", cdb: cdb(0x4a), flags: cdFlags, end: good(0xb0)},
		{name: "SEND_CUE_SHEET", cdb: cdb(0x4c), flags: cdFlags,
			end: append(zeros(12), 0x87, 0x50, 0x03, 0, 0, 0, 0xb0, 0x51, 0x05, 0x20, 0x00)},
		// READ_DISC_INFO and READ_TRACK_INFO: deliberate deviation from the original (CHECK
		// CONDITION 05/20/00), which made the host reset the device over USB-R.
		{name: "READ_DISC_INFO", cdb: cdb(0x51, 0, 0, 0, 0, 0, 0, 0, 0x22), flags: cdFlags, dev: 0xb0,
			data: append([]byte{0x00, 0x20, 0x0e, 0x01, 0x01, 0x01, 0x01}, zeros(27)...)},
		{name: "READ_DISC_INFO allocation length 2", cdb: cdb(0x51, 0, 0, 0, 0, 0, 0, 0, 0x02), flags: cdFlags, dev: 0xb0,
			data: []byte{0x00, 0x20}},
		{name: "READ_DISC_INFO allocation length 0", cdb: cdb(0x51), flags: cdFlags, dev: 0xb0,
			data: append([]byte{0x00, 0x20, 0x0e, 0x01, 0x01, 0x01, 0x01}, zeros(27)...)},
		{name: "READ_TRACK_INFO", cdb: cdb(0x52, 0x01, 0, 0, 0, 0x01, 0, 0, 0x24), flags: cdFlags, dev: 0xb0,
			data: append(append([]byte{0x00, 0x22, 0x01, 0x01, 0x00, 0x04, 0x01}, zeros(17)...), 0, 0, 0, 3, 0, 0, 0, 2, 0, 0, 0, 0)},
		{name: "READ_TRACK_INFO allocation length 8", cdb: cdb(0x52, 0x01, 0, 0, 0, 0x01, 0, 0, 0x08), flags: cdFlags, dev: 0xb0,
			data: []byte{0x00, 0x22, 0x01, 0x01, 0x00, 0x04, 0x01, 0x00}},
		{name: "GET_PERFORMANCE", cdb: cdb(0xac), flags: cdFlags, dev: 0xb0, data: []byte{0, 0, 0, 4, 2, 0, 0, 0}},
		{name: "MODE_SENSE_10 buflen 0", cdb: cdb(0x5a, 0, 0x3f), flags: cdFlags, dev: 0xb0, data: []byte{0, 0, 0, 0x3c, 0, 0, 0, 8}},
		{name: "MODE_SENSE_10 CD error recovery", cdb: mode10(0x01), flags: cdFlags, dev: 0xb0,
			data: []byte{0x00, 0x0e, 0x01, 0x80, 0x00, 0x00, 0x00, 0x00, 0x01, 0x06, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00}},
		{name: "MODE_SENSE_10 page 5 on CD", cdb: mode10(0x05), flags: cdFlags, end: check(0xb0, 5, 0x20, 0)},
		{name: "MODE_SENSE_10 0x3f CD", cdb: mode10(0x3f), flags: cdFlags, dev: 0xb0, data: modeSenseMS3FCD},
		{name: "MODE_SENSE_10 0x1a", cdb: mode10(0x1a), flags: cdFlags, dev: 0xb0, data: modeSenseMSCD1A},
		{name: "MODE_SENSE_10 0x1d", cdb: mode10(0x1d), flags: cdFlags, dev: 0xb0, data: modeSenseMSCD1D},
		{name: "MODE_SENSE_10 0x2a", cdb: mode10(0x2a), flags: cdFlags, dev: 0xb0, data: modeSenseMSCD2A},
		{name: "MODE_SENSE_10 unknown page", cdb: mode10(0x2b), flags: cdFlags, end: check(0xb0, 5, 0x20, 0)},
		{name: "WRITE_10 on CD", cdb: cdb(0x2a, 0, 0, 0, 0, 0, 0, 0, 1), flags: cdFlags, end: check(0xb0, 7, 0x27, 0)},
		{name: "WRITE_AND_VERIFY on CD", cdb: cdb(0x2e, 0, 0, 0, 0, 0, 0, 0, 1), flags: cdFlags, end: check(0xb0, 7, 0x27, 0)},
		{name: "unknown opcode", cdb: cdb(0xff), flags: cdFlags, end: check(0xb0, 5, 0x20, 0)},
		{name: "READ_10 out of range", cdb: read10(2, 2), flags: cdFlags, end: good(0xb0)},
		{name: "READ_10 length 0", cdb: read10(1, 0), flags: cdFlags, end: good(0xb0)},
		{name: "READ_10 length 0 at end", cdb: read10(3, 0), flags: cdFlags, end: good(0xb0)},
		{name: "READ_10 lba past end", cdb: read10(7, 1), flags: cdFlags, end: good(0xb0)},
		{name: "READ_12 out of range", cdb: read12(0, 4), flags: cdFlags, end: good(0xb0)},
		{name: "READ_6 out of range", cdb: cdb(0x08, 0, 0, 2, 2), flags: cdFlags, end: good(0xb0)},
		// Floppy (0xA0): no medium, answered like the original with floppy == null (GOOD, reads
		// without data). Deliberate deviation: READ_CAPACITY and READ_FORMAT_CAPACITIES answer GOOD
		// with an empty capacity instead of CHECK CONDITION, which made the host reset the device.
		{name: "floppy TUR", cdb: cdb(0x00), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy WRITE_6", cdb: cdb(0x0a), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy WRITE_10", cdb: cdb(0x2a, 0, 0, 0, 0, 0, 0, 0, 1), flags: floppyFlags, end: check(0xa0, 7, 0x27, 0)},
		{name: "floppy MODE_SENSE_6", cdb: cdb(0x1a, 0, 0x3f, 0), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy ALLOW_MEDIUM_REMOVAL", cdb: cdb(0x1e), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy START_STOP", cdb: cdb(0x1b), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy READ_FORMAT_CAPACITIES", cdb: cdb(0x23, 0, 0, 0, 0, 0, 0, 0, 0xfc), flags: floppyFlags, dev: 0xa0,
			data: []byte{0, 0, 0, 8, 0, 0, 0, 0, 0x03, 0, 0x02, 0}},
		{name: "floppy READ_FORMAT_CAPACITIES allocation length 4", cdb: cdb(0x23, 0, 0, 0, 0, 0, 0, 0, 0x04), flags: floppyFlags, dev: 0xa0,
			data: []byte{0, 0, 0, 8}},
		{name: "floppy READ_CAPACITY", cdb: cdb(0x25), flags: floppyFlags, dev: 0xa0, data: []byte{0, 0, 0, 0, 0, 0, 0x02, 0}},
		{name: "floppy READ_CAPACITY dma", cdb: cdb(0x25), flags: floppyFlags, feature: 1, dev: 0xa0, dma: true,
			data: []byte{0, 0, 0, 0, 0, 0, 0x02, 0}},
		{name: "floppy READ_10", cdb: read10(0, 1), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy READ_12", cdb: read12(0, 1), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy READ_6", cdb: cdb(0x08, 0, 0, 0, 1), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy READ_TOC", cdb: cdb(0x43, 0, 1), flags: floppyFlags, end: good(0xa0)},
		{name: "floppy GET_EVENT_STATUS_NOTIFICATION", cdb: cdb(0x4a, 0x01), flags: floppyFlags, dev: 0xa0, data: []byte{0, 0, 0x80, 0}},
		{name: "floppy READ_DISC_INFO", cdb: cdb(0x51), flags: floppyFlags, end: check(0xa0, 5, 0x20, 0)},
		{name: "floppy READ_TRACK_INFO", cdb: cdb(0x52), flags: floppyFlags, end: check(0xa0, 5, 0x20, 0)},
		{name: "floppy unknown opcode", cdb: cdb(0xff), flags: floppyFlags, end: check(0xa0, 5, 0x20, 0)},
		{name: "floppy MODE_SENSE_10 recovery", cdb: mode10(0x01), flags: floppyFlags, dev: 0xa0,
			data: []byte{0x00, 0x12, 0x24, 0x80, 0x00, 0x00, 0x00, 0x00, 0x01, 0x0a, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}},
		{name: "floppy MODE_SENSE_10 page 5", cdb: mode10(0x05), flags: floppyFlags, dev: 0xa0, data: modeSenseMSFloppyDiskPage},
		{name: "floppy MODE_SENSE_10 0x3f", cdb: mode10(0x3f), flags: floppyFlags, dev: 0xa0, data: modeSenseMS3FFloppy},
		{name: "floppy MODE_SENSE_10 0x1a", cdb: mode10(0x1a), flags: floppyFlags, end: check(0xa0, 5, 0x20, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.p.Written(t, tc.cdb, tc.flags, tc.feature)
			if tc.data != nil {
				f.expectData(t, tc.dev, tc.dma, tc.data)
			} else {
				f.expectEnd(t, tc.end)
			}
		})
	}
	if f.c.Err() != nil {
		t.Fatal(f.c.Err())
	}
}

func mode10(page byte) []byte { return cdb(0x5a, 0, page, 0, 0, 0, 0, 0, 0xff) }

func TestModeSense10Literals(t *testing.T) {
	// The page data comes from Constants.ts; spot-check lengths and first bytes of each.
	for name, tc := range map[string]struct {
		got  []byte
		n    int
		head []byte
	}{
		"MSFloppyDiskPage": {modeSenseMSFloppyDiskPage, 40, []byte{0x00, 0x26, 0x24, 0x80}},
		"MS3FFloppy":       {modeSenseMS3FFloppy, 94, []byte{0x00, 0x5c, 0x24, 0x80}},
		"MS3FCD":           {modeSenseMS3FCD, 42, []byte{0x00, 0x28, 0x01, 0x80}},
		"MSCD1A":           {modeSenseMSCD1A, 20, nil},
		"MSCD1D":           {modeSenseMSCD1D, 20, nil},
		"MSCD2A":           {modeSenseMSCD2A, 34, nil},
	} {
		if len(tc.got) != tc.n || (tc.head != nil && !bytes.HasPrefix(tc.got, tc.head)) {
			t.Errorf("%s: len %d head %x", name, len(tc.got), tc.got[:4])
		}
	}
}

func TestReadTOC_UnsupportedFormatIsInvalidField(t *testing.T) {
	// Deviation from the original (no reply): formats 2..7 get CHECK CONDITION 05/24/00.
	f := establish(t, newImage(4096), fixtureOpts{})
	for format := byte(2); format <= 7; format++ {
		f.p.Written(t, cdb(0x43, 0, format), cdFlags, 0)
		f.expectEnd(t, check(0xb0, 5, 0x24, 0))
	}
	if f.c.Err() != nil {
		t.Fatal(f.c.Err())
	}
}

func TestGetConfiguration(t *testing.T) {
	f := establish(t, newImage(4096), fixtureOpts{})
	gc := func(sendall bool, first uint16, buflen uint16) []byte {
		c := cdb(0x46, 0)
		if !sendall {
			c[1] = 2
		}
		binary.BigEndian.PutUint16(c[2:], first)
		binary.BigEndian.PutUint16(c[7:], buflen)
		return c
	}
	profile := []byte{0, 0, 3, 4, 0, 8, 1, 0}
	core := []byte{0, 1, 3, 4, 0, 0, 0, 2}
	morph := []byte{0, 2, 3, 4, 0, 0, 0, 0}
	removable := []byte{0, 3, 3, 4, 0x29, 0, 0, 2}
	random := []byte{0, 0x10, 1, 8, 0, 0, 8, 0, 0, 1, 0, 0}
	read := []byte{0, 0x1e, 3, 0}
	power := []byte{1, 0, 3, 0}
	timeout := []byte{1, 5, 3, 0}
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	all := cat(profile, core, morph, removable, random, read, power, timeout)
	full := cat([]byte{0, 0, 0, byte(4 + len(all))}, []byte{0, 0, 0, 8}, all)

	f.p.Written(t, gc(true, 0, 0), cdFlags, 0)
	f.expectData(t, 0xb0, false, []byte{0, 0, 0, 0x3c, 0, 0, 0, 8})
	f.p.Written(t, gc(true, 0, 0xffff), cdFlags, 0)
	f.expectData(t, 0xb0, false, full)
	f.p.Written(t, gc(true, 0, 8), cdFlags, 1) // cut to buflen, DMA
	f.expectData(t, 0xb0, true, full[:8])
	f.p.Written(t, gc(false, 0x10, 0xffff), cdFlags, 0) // only the requested feature
	f.expectData(t, 0xb0, false, cat([]byte{0, 0, 0, 16}, []byte{0, 0, 0, 8}, random))
	f.p.Written(t, gc(true, 0x3, 0xffff), cdFlags, 0) // 0x3 and everything above
	f.expectData(t, 0xb0, false, cat([]byte{0, 0, 0, 4 + 8 + 12 + 4 + 4 + 4}, []byte{0, 0, 0, 8}, removable, random, read, power, timeout))
}

func TestRead_Chunking(t *testing.T) {
	img := newImage(6 * 2048)
	for _, tc := range []struct {
		name    string
		readbfr uint16
		dma     bool
		lba, n  uint32
		call    func(lba, n uint32) []byte
	}{
		{"READ_10 chunk 4096", 4096, false, 1, 4, func(l, n uint32) []byte { return read10(l, uint16(n)) }},
		{"READ_10 chunk 3000 dma", 3000, true, 0, 6, func(l, n uint32) []byte { return read10(l, uint16(n)) }},
		{"READ_12 chunk 8192", 8192, false, 2, 4, read12},
		{"READ_10 one sector chunk 2048", 2048, false, 5, 1, func(l, n uint32) []byte { return read10(l, uint16(n)) }},
		{"READ_6", 8192, false, 3, 2, func(l, n uint32) []byte { return cdb(0x08, 0, byte(l>>8), byte(l), byte(n)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := establish(t, img, fixtureOpts{readbfr: tc.readbfr})
			feature := byte(0)
			if tc.dma {
				feature = 1
			}
			f.p.Written(t, tc.call(tc.lba, tc.n), cdFlags, feature)
			total := int(tc.n) * 2048
			got := f.readTransfer(t, 0xb0, tc.dma, int(tc.readbfr), total)
			if !bytes.Equal(got, img.data[int(tc.lba)*2048:int(tc.lba)*2048+total]) {
				t.Fatal("data differs")
			}
			waitFor(t, func() bool { return f.c.Stats().Reads == 1 })
			if s := f.c.Stats(); s.BytesRead != int64(total) || s.LastRead.IsZero() {
				t.Fatalf("stats %+v", s)
			}
		})
	}
}

func TestRead6_LengthZeroMeans256(t *testing.T) {
	img := newImage(300 * 2048)
	f := establish(t, img, fixtureOpts{})
	f.p.Written(t, cdb(0x08, 0, 0, 10, 0), cdFlags, 0)
	got := f.readTransfer(t, 0xb0, false, 8192, 256*2048)
	if !bytes.Equal(got, img.data[10*2048:266*2048]) {
		t.Fatal("data differs")
	}
	// lba 5-bit high part: ((cdb[1] & 0x1f) << 16); high bits of cdb[1] are ignored.
	f.p.Written(t, cdb(0x08, 0xe0, 0, 0, 1), cdFlags, 0)
	f.expectData(t, 0xb0, false, img.data[:2048])
}

func TestRead_OddImageSizePadded(t *testing.T) {
	img := newImage(5000)
	f := establish(t, img, fixtureOpts{})
	f.p.Written(t, cdb(0x25), cdFlags, 0)
	f.expectData(t, cdFlags, false, []byte{0, 0, 0, 2, 0, 0, 8, 0}) // device byte = deviceFlags
	f.p.Written(t, read10(0, 3), cdFlags, 0)
	want := append(append([]byte(nil), img.data...), make([]byte, 3*2048-5000)...)
	got := f.readTransfer(t, 0xb0, false, 8192, 3*2048)
	if !bytes.Equal(got, want) {
		t.Fatal("padded data differs")
	}
	f.p.Written(t, read10(2, 1), cdFlags, 0) // last, partial block alone
	f.expectData(t, 0xb0, false, append(append([]byte(nil), img.data[4096:]...), make([]byte, 2048-(5000-4096))...))
}

func TestRead_ExactBlockSizeNoPadding(t *testing.T) {
	img := newImage(2 * 2048)
	f := establish(t, img, fixtureOpts{})
	f.p.Written(t, cdb(0x25), cdFlags, 0)
	f.expectData(t, cdFlags, false, []byte{0, 0, 0, 1, 0, 0, 8, 0})
}

func TestRead_LBAAbove2Pow20SparseImage(t *testing.T) {
	const blocks = 5_000_000 // ~10 GB, offsets beyond 32 bit
	img := &patternImage{size: blocks * 2048}
	f := establish(t, img, fixtureOpts{})
	f.p.Written(t, cdb(0x25), cdFlags, 0)
	last := uint32(blocks - 1)
	f.expectData(t, cdFlags, false, []byte{byte(last >> 24), byte(last >> 16), byte(last >> 8), byte(last), 0, 0, 8, 0})
	for _, tc := range []struct {
		name string
		cdb  []byte
		lba  int64
		n    int
	}{
		{"READ_10 lba 3000000", read10(3_000_000, 2), 3_000_000, 2},
		{"READ_12 lba 4194304", read12(4_194_304, 3), 4_194_304, 3},
		{"READ_10 last block", read10(blocks-1, 1), blocks - 1, 1},
		{"READ_12 big length", read12(blocks-40000, 40000), blocks - 40000, 40000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.p.Written(t, tc.cdb, cdFlags, 0)
			total := tc.n * 2048
			got := f.readTransfer(t, 0xb0, false, 8192, total)
			for _, i := range []int{0, 1, 2047, 2048, total / 2, total - 1} {
				if i >= total {
					continue
				}
				if got[i] != pat(tc.lba*2048+int64(i)) {
					t.Fatalf("byte %d differs", i)
				}
			}
		})
	}
	f.p.Written(t, read12(blocks, 1), cdFlags, 0) // behind the end
	f.expectEnd(t, good(0xb0))
	f.p.Written(t, read12(blocks-1, 2), cdFlags, 0)
	f.expectEnd(t, good(0xb0))
}

func TestRead_ImageErrorIsMediumError(t *testing.T) {
	img := newImage(8 * 2048)
	img.failAt, img.readErr = 4*2048, errors.New("disk gone")
	f := establish(t, img, fixtureOpts{})
	f.p.Written(t, read10(5, 1), cdFlags, 0)
	f.expectEnd(t, check(0xb0, 3, 0x11, 0))
	f.p.Written(t, cdb(0x00), cdFlags, 0) // the session stays
	f.expectEnd(t, good(0xb0))
	if f.c.Err() != nil {
		t.Fatal(f.c.Err())
	}
	if s := f.c.Stats(); s.Reads != 0 || s.BytesRead != 0 {
		t.Fatalf("stats %+v", s)
	}
}

func TestRead_ErrorMidTransferEndsWithMediumError(t *testing.T) {
	img := newImage(8 * 2048)
	img.failAt, img.readErr = 6144, io.ErrUnexpectedEOF // the second chunk [4096, 8192) fails
	f := establish(t, img, fixtureOpts{readbfr: 4096})
	f.p.Written(t, read10(0, 8), cdFlags, 0)
	m := f.p.Read(t)
	if m.Cmd != 0x54 || m.Attr != 1 || len(m.Payload) != 26+4096 {
		t.Fatalf("first chunk: cmd %#x attr %d len %d", m.Cmd, m.Attr, len(m.Payload))
	}
	f.expectEnd(t, check(0xb0, 3, 0x11, 0))
	if f.c.Err() != nil {
		t.Fatal(f.c.Err())
	}
}

func TestPingPongAndReset(t *testing.T) {
	f := establish(t, newImage(4096), fixtureOpts{})
	f.p.Heartbeat(t) // 0x4B and 0x45 consume sequence numbers but are not answered
	f.p.Pong(t)
	f.p.Send(t, 0x4a, 0, []byte{1, 0, 0}) // ERROR OCCURRED: only logged by the original
	f.p.Ping(t)
	if m := f.p.Read(t); m.Cmd != 0x45 || m.Attr != 0 || len(m.Payload) != 0 || m.Seq != 2 {
		t.Fatalf("PONG = %x", wire(m))
	}
	f.p.Reset(t)
	if m := f.p.Read(t); m.Cmd != 0x47 || m.Attr != 0 || len(m.Payload) != 0 || m.Seq != 3 {
		t.Fatalf("reset reply = %x", wire(m))
	}
	if f.c.Err() != nil {
		t.Fatal(f.c.Err())
	}
}

func TestStats(t *testing.T) {
	img := newImage(10 * 2048)
	f := establish(t, img, fixtureOpts{})
	f.p.StatusData(t, 2, 2) // REGS_STATUS is accepted and ignored
	f.p.Written(t, read10(0, 4), cdFlags, 0)
	f.readTransfer(t, 0xb0, false, 8192, 4*2048)
	f.p.Written(t, read10(4, 1), cdFlags, 0)
	f.readTransfer(t, 0xb0, false, 8192, 2048)
	waitFor(t, func() bool { return f.c.Stats().Reads == 2 })
	before := time.Now()
	s := f.c.Stats()
	if s.BytesRead != 5*2048 || s.ImageSize != int64(len(img.data)) || s.LastRead.IsZero() || s.LastRead.After(before) {
		t.Fatalf("stats %+v", s)
	}
	if f.c.Err() != nil {
		t.Fatal(f.c.Err())
	}
}

func TestClose(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	if err := f.c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.c.Done():
	default:
		t.Fatal("Done not closed after Close")
	}
	if !errors.Is(f.c.Err(), ErrClosed) || img.closed.Load() != 1 {
		t.Fatalf("err %v closed %d", f.c.Err(), img.closed.Load())
	}
	if err := f.c.Close(); err != nil || img.closed.Load() != 1 {
		t.Fatalf("second Close: err %v closed %d", err, img.closed.Load())
	}
	// The peer sees the connection close.
	if _, err := f.p.TryRead(2 * time.Second); err == nil {
		t.Fatal("peer still got a message")
	}
}

func TestClose_Concurrent(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	done := make(chan struct{})
	for range 4 {
		go func() { f.c.Close(); done <- struct{}{} }()
	}
	for range 4 {
		<-done
	}
	if img.closed.Load() != 1 || !errors.Is(f.c.Err(), ErrClosed) {
		t.Fatalf("closed %d err %v", img.closed.Load(), f.c.Err())
	}
}

// slowImage blocks a read until released, to test Close during a transfer.
type slowImage struct {
	*testImage
	entered chan struct{}
	release chan struct{}
}

func (s *slowImage) ReadAt(p []byte, off int64) (int, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return s.testImage.ReadAt(p, off)
}

func TestClose_DuringTransferWaitsForReadThenClosesImage(t *testing.T) {
	img := &slowImage{testImage: newImage(64 * 2048), entered: make(chan struct{}, 1), release: make(chan struct{})}
	f := establish(t, img, fixtureOpts{})
	f.p.Written(t, read10(0, 32), cdFlags, 0)
	<-img.entered
	closed := make(chan error, 1)
	go func() { closed <- f.c.Close() }()
	time.Sleep(50 * time.Millisecond)
	if img.closed.Load() != 0 {
		t.Fatal("image closed while a read is running")
	}
	close(img.release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if img.closed.Load() != 1 || !errors.Is(f.c.Err(), ErrClosed) {
		t.Fatalf("closed %d err %v", img.closed.Load(), f.c.Err())
	}
}

func TestPeerDisconnect(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	f.p.Disconnect()
	waitDone(t, f.c)
	err := f.c.Err()
	if err == nil || errors.Is(err, ErrClosed) || errors.Is(err, ErrClosedByDevice) || img.closed.Load() != 1 {
		t.Fatalf("err %v closed %d", err, img.closed.Load())
	}
	first := err
	f.c.Close() // keeps the first error
	if f.c.Err() != first || img.closed.Load() != 1 {
		t.Fatalf("err %v closed %d", f.c.Err(), img.closed.Load())
	}
}

func TestCloseByDevice(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	f.p.Close(t)
	waitDone(t, f.c)
	if !errors.Is(f.c.Err(), ErrClosedByDevice) || img.closed.Load() != 1 {
		t.Fatalf("err %v closed %d", f.c.Err(), img.closed.Load())
	}
}

func TestSequenceError(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	f.p.SendSeq(t, 0x44, 0, 99, nil) // PING with a wrong sequence number
	waitDone(t, f.c)
	if err := f.c.Err(); err == nil || !errors.Is(err, errProtocol) || img.closed.Load() != 1 {
		t.Fatalf("err %v closed %d", f.c.Err(), img.closed.Load())
	}
	if m, err := f.p.TryRead(500 * time.Millisecond); err == nil {
		t.Fatalf("a reply was sent for the out-of-sequence message: %x", wire(m))
	}
}

func TestUnknownCommand(t *testing.T) {
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	f.p.Send(t, 0x77, 0, nil)
	waitDone(t, f.c)
	if err := f.c.Err(); !errors.Is(err, errProtocol) || img.closed.Load() != 1 {
		t.Fatalf("err %v closed %d", err, img.closed.Load())
	}
}

func TestReadTimeoutEndsDeadSession(t *testing.T) {
	old := readTimeout
	readTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readTimeout = old })
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	// Heartbeats renew the deadline: the session outlives several timeouts.
	for range 6 {
		time.Sleep(100 * time.Millisecond)
		f.p.Heartbeat(t)
	}
	if f.c.Err() != nil {
		t.Fatalf("session ended despite heartbeats: %v", f.c.Err())
	}
	waitDone(t, f.c) // silence: the read deadline ends it
	err := f.c.Err()
	if err == nil || errors.Is(err, ErrClosed) || !errors.Is(err, os.ErrDeadlineExceeded) || img.closed.Load() != 1 {
		t.Fatalf("err %v closed %d", err, img.closed.Load())
	}
}

func TestTLS(t *testing.T) {
	img := newImage(3 * 2048)
	f := establish(t, img, fixtureOpts{tls: true})
	f.p.Written(t, read10(1, 2), cdFlags, 0)
	got := f.readTransfer(t, 0xb0, false, 8192, 2*2048)
	if !bytes.Equal(got, img.data[2048:]) {
		t.Fatal("data differs")
	}
	if err := f.c.Close(); err != nil || !errors.Is(f.c.Err(), ErrClosed) || img.closed.Load() != 1 {
		t.Fatalf("err %v / %v closed %d", err, f.c.Err(), img.closed.Load())
	}
}

func TestOpenISO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.iso")
	if err := os.WriteFile(path, []byte("hello iso"), 0o600); err != nil {
		t.Fatal(err)
	}
	img, err := OpenISO(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if img.Size() != 9 {
		t.Fatalf("size %d", img.Size())
	}
	if n, err := img.ReadAt(buf, 6); n != 3 || (err != nil && err != io.EOF) {
		t.Fatalf("n %d err %v", n, err)
	}
	if err := img.Close(); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.iso")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"directory": dir, "empty": empty, "missing": filepath.Join(dir, "none.iso")} {
		if img, err := OpenISO(p); err == nil {
			img.Close()
			t.Errorf("%s: expected error", name)
		}
	}
}
