package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// The test driver runs fyne.Do on the calling goroutine, so the reconnect goroutines and the
// test do not share a Fyne thread. These tests therefore read window state only through
// snapshots taken inside onKVMChanged, and act only when no session event is in flight.

// setReconnect shortens the MeshCommander reconnect constants for one test.
func setReconnect(t *testing.T, minUptime, delay time.Duration) {
	t.Helper()
	oldUp, oldDelay := reconnectMinUptime, reconnectDelay
	reconnectMinUptime, reconnectDelay = minUptime, delay
	t.Cleanup(func() { reconnectMinUptime, reconnectDelay = oldUp, oldDelay })
}

type kvmSnap struct {
	client          *kvm.Client
	reconnecting    bool
	status          string
	kvmOn           bool // KVM toggle on
	cadOff, fullOff bool
	image, red      bool
	freed           bool // no client and no image buffers (kvmView.free)
	idleTimer       *time.Timer
	lastInput       time.Time
	errs            int
}

// snapshots replaces onKVMChanged with a hook that records the state at every KVM change.
func snapshots(h *kvmHarness) chan kvmSnap {
	ch := make(chan kvmSnap, 32)
	h.d.onKVMChanged = func() {
		d := h.d
		ch <- kvmSnap{client: d.kvmClient, reconnecting: d.kvmReconnecting, status: d.kvmStatus.Text,
			kvmOn: d.kvmOn(), cadOff: d.cadBtn.Disabled(), fullOff: d.fullBtn.Disabled(),
			image: d.view.img.Image != nil, red: d.view.front != nil && d.view.front.RGBAAt(1, 1).R >= 240,
			idleTimer: d.idleTimer, lastInput: d.lastInput, errs: len(h.errs),
			freed: d.view.client == nil && d.view.front == nil && d.view.back == nil && d.view.img.Image == nil}
	}
	return ch
}

func next(t *testing.T, ch chan kvmSnap) kvmSnap {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
		return kvmSnap{}
	}
}

// running checks that a snapshot shows an unchanged running session: no status, the
// KVM toggle on, enabled session buttons and the last (red) image.
func running(t *testing.T, what string, s kvmSnap) {
	t.Helper()
	if s.status != "" || !s.kvmOn || s.cadOff || s.fullOff || !s.image || !s.red || s.errs != 0 {
		t.Fatalf("%s: %+v", what, s)
	}
}

// AMT drops a session with a black screen after about 31 s; like MeshCommander the window
// reconnects without an error and without touching status, buttons, image or idle timer.
func TestKVMReconnectsWhenAMTDropsSession(t *testing.T) {
	setReconnect(t, 50*time.Millisecond, 10*time.Millisecond)
	h := newKVMDevice(t, &sessionFake{St: amt.Status{AMTVersion: "16.1.25"}},
		kvmtest.Options{CloseAfter: 150 * time.Millisecond, CloseConns: 3})
	h.d.idleTimeout = func() time.Duration { return time.Hour }
	snaps := snapshots(h)
	tapKVM(h.d)
	opened := next(t, snaps)
	recv(t, h.frames)
	if opened.client == nil || opened.idleTimer == nil {
		t.Fatalf("opened: %+v", opened)
	}
	prev := opened.client
	for i := 1; i <= 3; i++ { // three drops by the server, three reconnects
		s := next(t, snaps)
		if !s.reconnecting || s.client != nil {
			t.Fatalf("drop %d: %+v", i, s)
		}
		running(t, "reconnecting", s)
		s = next(t, snaps)
		if s.reconnecting || s.client == nil || s.client == prev {
			t.Fatalf("reconnect %d: %+v", i, s)
		}
		running(t, "reconnected", s)
		if s.idleTimer != opened.idleTimer || !s.lastInput.Equal(opened.lastInput) {
			t.Fatalf("reconnect %d restarted the idle timeout", i)
		}
		if err := prev.Err(); err == nil || errors.Is(err, kvm.ErrClosed) {
			t.Fatalf("dropped session %d: %v", i, err)
		}
		prev = s.client
	}
	if n := h.srv.Connections(); n != 4 {
		t.Fatalf("connections %d", n)
	}
	// The fourth session stays; "Disconnect KVM" ends it for good.
	tapKVM(h.d)
	s := next(t, snaps)
	if s.client != nil || s.reconnecting || s.status != "KVM session closed." || s.image || s.idleTimer != nil {
		t.Fatalf("after disconnect: %+v", s)
	}
	time.Sleep(300 * time.Millisecond)
	if h.srv.Connections() != 4 || len(snaps) != 0 {
		t.Fatal("reconnected after Disconnect KVM")
	}
}

// A session that AMT ends within the minimum uptime is an error, as in MeshCommander.
func TestKVMNoReconnectForShortSession(t *testing.T) {
	setReconnect(t, time.Hour, 10*time.Millisecond)
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{CloseAfter: 100 * time.Millisecond})
	snaps := snapshots(h)
	tapKVM(h.d)
	if s := next(t, snaps); s.client == nil {
		t.Fatalf("opened: %+v", s)
	}
	s := next(t, snaps) // ended by the server
	if s.client != nil || s.reconnecting || !strings.HasPrefix(s.status, "KVM session ended: ") || s.kvmOn || s.image {
		t.Fatalf("after drop: %+v", s)
	}
	time.Sleep(100 * time.Millisecond)
	if h.srv.Connections() != 1 {
		t.Fatalf("connections %d", h.srv.Connections())
	}
}

// "Disconnect KVM" while the reconnect waits cancels it.
func TestKVMDisconnectDuringReconnect(t *testing.T) {
	setReconnect(t, 20*time.Millisecond, time.Hour)
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{CloseAfter: 100 * time.Millisecond})
	snaps := snapshots(h)
	tapKVM(h.d)
	next(t, snaps) // opened
	recv(t, h.frames)
	s := next(t, snaps)
	if !s.reconnecting {
		t.Fatalf("no reconnect: %+v", s)
	}
	running(t, "reconnecting", s)
	tapKVM(h.d)
	s = next(t, snaps)
	if s.reconnecting || s.client != nil || s.status != "KVM session closed." || s.kvmOn || s.image || s.errs != 0 {
		t.Fatalf("after disconnect: %+v", s)
	}
	if h.srv.Connections() != 1 {
		t.Fatalf("connections %d", h.srv.Connections())
	}
}

// Closing the window while a reconnect waits cancels it and frees the view's buffers, which
// Fyne would otherwise keep reachable with the closed window (kvmView.free).
func TestKVMCloseWindowDuringReconnectFreesView(t *testing.T) {
	setReconnect(t, 20*time.Millisecond, time.Hour)
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{CloseAfter: 100 * time.Millisecond})
	snaps := snapshots(h)
	tapKVM(h.d)
	next(t, snaps) // opened
	recv(t, h.frames)
	if s := next(t, snaps); !s.reconnecting || s.freed {
		t.Fatalf("no reconnect: %+v", s)
	}
	h.d.win.Close()
	recv(t, h.closed)
	if s := next(t, snaps); s.reconnecting || !s.freed {
		t.Fatalf("after close: %+v", s)
	}
	if h.srv.Connections() != 1 {
		t.Fatalf("connections %d", h.srv.Connections())
	}
}

// Quality while connected (SessionA.html): the session reconnects with the new quality at once,
// like an automatic reconnect: no wake key, no message, the idle timeout keeps running, the
// last image stays. The choice is stored with the favorite. AMT asking for the consent code
// again is answered through the normal dialog.
func TestKVMQualitySwitchReconnects(t *testing.T) {
	setWakeRepeat(t, 10*time.Millisecond)
	dev := &sessionFake{}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	store := &fakeLayouts{fav: &favorites.Favorite{Host: "device.example.com", Username: "admin"}}
	h.d.layouts = store
	saved := make(chan struct{}, 4)
	h.d.onQualitySaved = func() { saved <- struct{}{} }
	h.d.wake = wakeScreen
	h.d.idleTimeout = func() time.Duration { return time.Hour }
	snaps := snapshots(h)
	tapKVM(h.d)
	opened := next(t, snaps)
	recv(t, h.frames)
	waitInput(t, h.srv, 4) // the two wake keys of "Connect KVM"

	choose := func(q kvm.Quality) kvmSnap {
		t.Helper()
		if h.d.quality.Disabled() {
			t.Fatal("quality locked while connected")
		}
		test.Tap(h.d.quality)
		tapMenuItem(t, h.d.qualityPopup, h.d.qualityMenu, h.d.qualityItems[q])
		recv(t, saved)
		s := next(t, snaps)
		if !s.reconnecting || s.client != nil {
			t.Fatalf("switch to %v: %+v", q, s)
		}
		running(t, "switching", s)
		return s
	}
	reopened := func(prev *kvm.Client) kvmSnap {
		t.Helper()
		s := next(t, snaps)
		if s.reconnecting || s.client == nil || s.client == prev {
			t.Fatalf("reconnect: %+v", s)
		}
		running(t, "switched", s)
		if s.idleTimer != opened.idleTimer {
			t.Fatal("switch restarted the idle timeout")
		}
		return s
	}

	choose(kvm.QualityLow)
	low := reopened(opened.client)
	if low.client.Quality() != kvm.QualityLow || store.fav.Quality != favorites.QualityLow || h.d.quality.Caption() != "Quality" {
		t.Fatalf("quality %v stored %q", low.client.Quality(), store.fav.Quality)
	}
	if err := opened.client.Err(); !errors.Is(err, kvm.ErrClosed) {
		t.Fatalf("old session: %v", err)
	}

	// The same quality again changes nothing.
	test.Tap(h.d.quality)
	tapMenuItem(t, h.d.qualityPopup, h.d.qualityMenu, h.d.qualityItems[kvm.QualityLow])
	recv(t, saved)

	// AMT asks for the consent code again: the dialog of a normal connect.
	dev.Consent, dev.ConsentCode = true, "123456"
	choose(kvm.QualityHigh)
	select {
	case answer := <-h.asked:
		answer("123456", true)
	case <-time.After(3 * time.Second):
		t.Fatal("no consent dialog")
	}
	high := reopened(low.client)
	if high.client.Quality() != kvm.QualityHigh || store.fav.Quality != favorites.QualityHigh {
		t.Fatalf("quality %v stored %q", high.client.Quality(), store.fav.Quality)
	}
	if q := dev.Qualities(); len(q) != 3 || q[0] != kvm.QualityHigh || q[1] != kvm.QualityLow || q[2] != kvm.QualityHigh {
		t.Fatalf("qualities %v", q)
	}
	time.Sleep(50 * time.Millisecond)
	if keys := keysPerConnection(h.srv); len(keys) != 3 || !isWakeTwice(keys[0]) || len(keys[1]) != 0 || len(keys[2]) != 0 {
		t.Fatalf("wake keys per connection %v", keys)
	}
	if h.srv.Connections() != 3 || len(h.errs) != 0 {
		t.Fatalf("connections=%d errs=%d", h.srv.Connections(), len(h.errs))
	}
	tapKVM(h.d)
	if s := next(t, snaps); s.client != nil || s.status != "KVM session closed." {
		t.Fatalf("after disconnect: %+v", s)
	}
}

// "Disconnect KVM" right after a quality switch ends the session; nothing reconnects.
func TestKVMDisconnectDuringQualitySwitch(t *testing.T) {
	h := newKVMDevice(t, &sessionFake{}, kvmtest.Options{})
	snaps := snapshots(h)
	// Both run on the frame loop at the first frame, as on the Fyne thread in production:
	// kvmEnded of the closed session can only follow them.
	h.d.view.onFrame = func() {
		h.d.setQuality(kvm.QualityLow) // closes the session for the switch
		h.d.disconnectKVM()
		h.frames <- struct{}{}
	}
	tapKVM(h.d)
	next(t, snaps)
	recv(t, h.frames)
	s := next(t, snaps)
	if s.client != nil || s.reconnecting || s.status != "KVM session closed." {
		t.Fatalf("after disconnect: %+v", s)
	}
	time.Sleep(100 * time.Millisecond)
	if h.srv.Connections() != 1 || len(snaps) != 0 {
		t.Fatal("reconnected after Disconnect KVM")
	}
}
