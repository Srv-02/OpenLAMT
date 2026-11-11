package ui

import (
	"bytes"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// keysPerConnection returns the key messages (type 4) the server received, grouped by
// connection: every session starts with SetEncodings (type 2).
func keysPerConnection(srv *kvmtest.Server) [][][]byte {
	var out [][][]byte
	for _, m := range srv.ClientMessages() {
		switch m[0] {
		case 2:
			out = append(out, nil)
		case 4:
			if len(out) > 0 {
				out[len(out)-1] = append(out[len(out)-1], m)
			}
		}
	}
	return out
}

// setWakeRepeat sets the pause between the two wake keys for one test.
func setWakeRepeat(t *testing.T, d time.Duration) {
	t.Helper()
	old := wakeRepeat
	wakeRepeat = d
	t.Cleanup(func() { wakeRepeat = old })
}

func isWakePair(keys [][]byte) bool {
	return len(keys) == 2 && bytes.Equal(keys[0], keyMsg(0xffcc, true)) && bytes.Equal(keys[1], keyMsg(0xffcc, false))
}

// isWakeTwice reports two wake keys, each pressed and released.
func isWakeTwice(keys [][]byte) bool {
	return len(keys) == 4 && isWakePair(keys[:2]) && isWakePair(keys[2:])
}

// "Connect KVM" presses and releases the wake key at the first frame and once more wakeRepeat
// later, to wake a sleeping host screen; that is no local input and leaves the idle timeout
// alone.
func TestKVMConnectWakesScreenTwice(t *testing.T) {
	setWakeRepeat(t, 20*time.Millisecond)
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.d.wake = wakeScreen
	h.d.idleTimeout = func() time.Duration { return time.Hour }
	h.connect(t, true)
	last := h.d.lastInput
	waitInput(t, h.srv, 4)
	time.Sleep(100 * time.Millisecond)
	if keys := keysPerConnection(h.srv); len(keys) != 1 || !isWakeTwice(keys[0]) {
		t.Fatalf("keys %x", keys)
	}
	if n := len(inputMessages(h.srv)); n != 4 {
		t.Fatalf("%d input messages", n)
	}
	if !h.d.lastInput.Equal(last) {
		t.Fatal("the wake counted as input for the idle timeout")
	}
	h.disconnect(t)
}

// A session that ends before wakeRepeat is over gets no second wake key.
func TestKVMDisconnectBeforeSecondWakeSendsNoMore(t *testing.T) {
	setWakeRepeat(t, 200*time.Millisecond)
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	h.d.wake = wakeScreen
	h.connect(t, true)
	h.disconnect(t)
	time.Sleep(300 * time.Millisecond)
	if keys := keysPerConnection(h.srv); len(keys) != 1 || (len(keys[0]) != 0 && !isWakePair(keys[0])) {
		t.Fatalf("keys %x", keys)
	}
}

// Automatic reconnects (AMT drops a black session after about 31 s) send no wake: otherwise
// the host would never fall asleep again while the window is open.
func TestKVMReconnectDoesNotWakeScreen(t *testing.T) {
	setReconnect(t, 50*time.Millisecond, 10*time.Millisecond)
	setWakeRepeat(t, 20*time.Millisecond)
	h := newKVMDevice(t, &sessionFake{St: amt.Status{AMTVersion: "16.1.25"}},
		kvmtest.Options{UpdatesPerConn: true, CloseAfter: 150 * time.Millisecond, CloseConns: 2})
	h.d.wake = wakeScreen
	h.d.idleTimeout = func() time.Duration { return time.Hour }
	snaps := snapshots(h)
	tapKVM(h.d)
	next(t, snaps) // opened
	recv(t, h.frames)
	for i := 1; i <= 2; i++ {
		if s := next(t, snaps); !s.reconnecting {
			t.Fatalf("drop %d: %+v", i, s)
		}
		if s := next(t, snaps); s.client == nil {
			t.Fatalf("reconnect %d: %+v", i, s)
		}
		recv(t, h.frames) // every reconnected session gets its first frame
	}
	time.Sleep(100 * time.Millisecond)
	keys := keysPerConnection(h.srv)
	if len(keys) != 3 || !isWakeTwice(keys[0]) || len(keys[1]) != 0 || len(keys[2]) != 0 {
		t.Fatalf("keys per connection %x", keys)
	}
	tapKVM(h.d)
	next(t, snaps)
}
