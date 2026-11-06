package kvm

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// dialTest connects to a fake server; the first 3 client messages are handshake and request.
func dialTest(t *testing.T) (*Client, *kvmtest.Server) {
	t.Helper()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw"})
	c, err := Dial(context.Background(), Config{Addr: srv.Addr, Username: "admin", Password: "pw", Quality: QualityLow})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, srv
}

func TestKeyEventBytes(t *testing.T) {
	if got := keyEvent(0xffe3, true); !bytes.Equal(got, []byte{4, 1, 0, 0, 0, 0, 0xff, 0xe3}) {
		t.Fatalf("%x", got)
	}
	if got := keyEvent('a', false); !bytes.Equal(got, []byte{4, 0, 0, 0, 0, 0, 0, 0x61}) {
		t.Fatalf("%x", got)
	}
}

func TestPointerEventBytes(t *testing.T) {
	if got := pointerEvent(1023, 767, ButtonLeft|ButtonRight); !bytes.Equal(got, []byte{5, 5, 3, 0xff, 2, 0xff}) {
		t.Fatalf("%x", got)
	}
}

func TestKeyEventTracksShift(t *testing.T) {
	c, srv := dialTest(t)
	for _, step := range []struct {
		k    Key
		down bool
	}{{KeyA, true}, {KeyA, false}, {ShiftRight, true}, {KeyA, true}, {ShiftRight, false}, {KeyA, false}, {Digit1, true}} {
		if err := c.KeyEvent(step.k, step.down); err != nil {
			t.Fatal(err)
		}
	}
	msgs := waitMessages(t, srv, 3+7)[3:]
	// the second KeyA Up repeats 'A' although shift was already released
	want := [][]byte{keyEvent('a', true), keyEvent('a', false), keyEvent(0xffe2, true), keyEvent('A', true), keyEvent(0xffe2, false), keyEvent('A', false), keyEvent('1', true)}
	for i := range want {
		if !bytes.Equal(msgs[i], want[i]) {
			t.Fatalf("msg %d: %x", i, msgs[i])
		}
	}
}

func TestKeyEventUnmapped(t *testing.T) {
	c, srv := dialTest(t)
	if err := c.KeyEvent(KeyUnknown, true); !errors.Is(err, ErrUnmappedKey) {
		t.Fatalf("err = %v", err)
	}
	waitMessages(t, srv, 3)
	if n := len(srv.ClientMessages()); n != 3 {
		t.Fatalf("%d messages, want 3", n)
	}
}

func TestReleaseAll(t *testing.T) {
	c, srv := dialTest(t)
	for _, k := range []Key{ControlLeft, KeyC} {
		if err := c.KeyEvent(k, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	msgs := waitMessages(t, srv, 3+4)[3+2:]
	a, b := keyEvent(0xffe3, false), keyEvent('c', false)
	if !(bytes.Equal(msgs[0], a) && bytes.Equal(msgs[1], b)) && !(bytes.Equal(msgs[0], b) && bytes.Equal(msgs[1], a)) {
		t.Fatalf("release messages %x %x", msgs[0], msgs[1])
	}
	if err := c.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if err := c.PointerEvent(0, 0, 0); err != nil { // marker: proves the second ReleaseAll sent nothing
		t.Fatal(err)
	}
	all := waitMessages(t, srv, 3+5)
	if len(all) != 8 || all[7][0] != 5 {
		t.Fatalf("%d messages, last %x", len(all), all[len(all)-1])
	}
}

func TestKeyEventConcurrent(t *testing.T) {
	c, srv := dialTest(t)
	keys := []Key{KeyA, KeyB, KeyC, KeyD, KeyE, KeyF, KeyG, KeyH}
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if err := c.KeyEvent(k, true); err != nil {
					t.Error(err)
					return
				}
				if err := c.KeyEvent(k, false); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	waitMessages(t, srv, 3+800)
}

func TestCtrlAltDelSequence(t *testing.T) {
	c, srv := dialTest(t)
	if err := c.CtrlAltDel(); err != nil {
		t.Fatal(err)
	}
	msgs := waitMessages(t, srv, 3+6)[3:]
	syms := []uint32{0xffe3, 0xffe9, 0xffff, 0xffff, 0xffe9, 0xffe3}
	downs := []bool{true, true, true, false, false, false}
	for i := range syms {
		if !bytes.Equal(msgs[i], keyEvent(syms[i], downs[i])) {
			t.Fatalf("msg %d: %x", i, msgs[i])
		}
	}
}

func TestPointerEventClamps(t *testing.T) {
	c, srv := dialTest(t) // screen 128x64
	if err := c.PointerEvent(500, -3, 0); err != nil {
		t.Fatal(err)
	}
	got := waitMessages(t, srv, 3+1)[3]
	if want := pointerEvent(127, 0, 0); !bytes.Equal(got, want) {
		t.Fatalf("%x, want %x", got, want)
	}
}

func TestInputAfterCloseFails(t *testing.T) {
	c, _ := dialTest(t)
	c.Close()
	if err := c.KeyEvent(KeyA, true); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestKeyEventRepeatKeepsRememberedKeysym(t *testing.T) {
	c, srv := dialTest(t)
	for _, step := range []struct {
		k    Key
		down bool
	}{{KeyA, true}, {ShiftLeft, true}, {KeyA, true}} {
		if err := c.KeyEvent(step.k, step.down); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	msgs := waitMessages(t, srv, 3+5)[3:]
	if !bytes.Equal(msgs[2], keyEvent('a', true)) {
		t.Fatalf("repeat: %x", msgs[2])
	}
	for _, m := range msgs[3:] {
		if m[1] != 0 {
			t.Fatalf("release is not Up: %x", m)
		}
	}
	if !bytes.Equal(msgs[3], keyEvent('a', false)) && !bytes.Equal(msgs[4], keyEvent('a', false)) {
		t.Fatalf("'a' never released: %x %x", msgs[3], msgs[4])
	}
}

func TestKeyEventReleaseAllAtomic(t *testing.T) {
	c, srv := dialTest(t)
	const n = 200
	held := make([]bool, n)
	for i := range n {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = c.KeyEvent(KeyA, true) }()
		go func() { defer wg.Done(); _ = c.ReleaseAll() }()
		wg.Wait()
		c.inMu.Lock()
		_, held[i] = c.pressed[KeyA]
		c.inMu.Unlock()
		if err := c.PointerEvent(0, 0, 0); err != nil { // marker ends the iteration on the wire
			t.Fatal(err)
		}
		if err := c.ReleaseAll(); err != nil { // reset for the next iteration
			t.Fatal(err)
		}
	}
	// n markers plus at least 2 messages per iteration; wait until all n markers arrived.
	var msgs [][]byte
	for deadline := time.Now().Add(5 * time.Second); ; {
		markers := 0
		msgs = nil
		if all := srv.ClientMessages(); len(all) >= 3 {
			msgs = all[3:]
		}
		for _, m := range msgs {
			if m[0] == 5 {
				markers++
			}
		}
		if markers == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d markers", markers)
		}
		time.Sleep(5 * time.Millisecond)
	}
	i, last := 0, byte(0xff) // last 'a' state byte in this segment, 0xff = none
	for _, m := range msgs {
		switch m[0] {
		case 4:
			last = m[1]
		case 5:
			if want := byte(0); held[i] {
				want = 1
				if last != want {
					t.Fatalf("iteration %d: held but last 'a' event is %d", i, last)
				}
			} else if last == 1 {
				t.Fatalf("iteration %d: not held but last 'a' event is Down", i)
			}
			i++
			last = 0xff
		}
	}
}

func TestSendKeyShiftedLetter(t *testing.T) {
	c, srv := dialTest(t)
	for _, s := range []struct {
		sym  uint32
		down bool
	}{{0xffe1, true}, {'a', true}, {'a', false}, {0xffe1, false}, {'1', true}, {0xffea, true}} {
		if err := c.SendKey(s.sym, s.down); err != nil {
			t.Fatal(err)
		}
	}
	msgs := waitMessages(t, srv, 3+6)[3:]
	want := [][]byte{keyEvent(0xffe1, true), keyEvent('A', true), keyEvent('A', false), keyEvent(0xffe1, false), keyEvent('1', true), keyEvent(0xffea, true)}
	for i := range want {
		if !bytes.Equal(msgs[i], want[i]) {
			t.Fatalf("msg %d: %x, want %x", i, msgs[i], want[i])
		}
	}
}

func TestSendKeyUnmapped(t *testing.T) {
	c, srv := dialTest(t)
	if err := c.SendKey('A', true); !errors.Is(err, ErrUnmappedKey) {
		t.Fatalf("err = %v", err)
	}
	waitMessages(t, srv, 3)
	if n := len(srv.ClientMessages()); n != 3 {
		t.Fatalf("%d messages, want 3", n)
	}
}

func TestWakeScreenPressesF15Once(t *testing.T) {
	c, srv := dialTest(t)
	if err := c.WakeScreen(); err != nil {
		t.Fatal(err)
	}
	if err := c.KeyEvent(KeyA, true); err != nil { // nothing left held: plain 'a'
		t.Fatal(err)
	}
	msgs := waitMessages(t, srv, 3+3)[3:]
	want := [][]byte{keyEvent(0xffcc, true), keyEvent(0xffcc, false), keyEvent('a', true)}
	for i := range want {
		if !bytes.Equal(msgs[i], want[i]) {
			t.Fatalf("msg %d: %x, want %x", i, msgs[i], want[i])
		}
	}
}

// While the user holds a key, the wake sends nothing (no combination like Ctrl+F15).
func TestWakeScreenSkipsWhileKeyHeld(t *testing.T) {
	for _, k := range []Key{ShiftLeft, ControlLeft, KeyA} {
		c, srv := dialTest(t)
		if err := c.KeyEvent(k, true); err != nil {
			t.Fatal(err)
		}
		if err := c.WakeScreen(); err != nil {
			t.Fatal(err)
		}
		if err := c.KeyEvent(k, false); err != nil {
			t.Fatal(err)
		}
		msgs := waitMessages(t, srv, 3+2)[3:]
		for _, m := range msgs {
			if bytes.Equal(m[4:], []byte{0, 0, 0xff, 0xcc}) {
				t.Fatalf("%v: messages %x", k, msgs)
			}
		}
		if len(msgs) != 2 {
			t.Fatalf("%v: messages %x", k, msgs)
		}
	}
}
