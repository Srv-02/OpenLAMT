package ui

import (
	"errors"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// fakeFavorites is the device window's favoriteStore with one optional entry.
type fakeFavorites struct {
	fav   *favorites.Favorite
	block chan struct{} // Find waits until it is closed (nil: no wait)
}

func (f fakeFavorites) Find(host, user string) (favorites.Favorite, bool, error) {
	if f.block != nil {
		<-f.block
	}
	if f.fav == nil {
		return favorites.Favorite{}, false, nil
	}
	return *f.fav, true, nil
}
func (f fakeFavorites) SetQuality(host, user, q string) error { return nil }
func (f fakeFavorites) SetLayout(host, user, l string) error  { return nil }

// autoDevice builds a KVM device window like newKVMDevice, but configures the global setting
// and the favorite before Show, because Show starts the automatic connect.
func autoDevice(t *testing.T, dev *sessionFake, global bool, fav *favorites.Favorite) (*kvmHarness, chan struct{}) {
	t.Helper()
	return autoDeviceErr(t, dev, global, fav, nil)
}

// autoDeviceErr makes every status read after the connect fail with statusErr.
func autoDeviceErr(t *testing.T, dev *sessionFake, global bool, fav *favorites.Favorite, statusErr error, noStore ...bool) (*kvmHarness, chan struct{}) {
	t.Helper()
	h, loaded := autoDeviceSetup(t, dev, global, fakeFavorites{fav: fav}, statusErr, noStore...)
	recv(t, loaded)
	return h, loaded
}

// autoDeviceSetup calls the real Show (without its poller) and returns before the favorite's
// read has been waited for; loaded signals that read.
func autoDeviceSetup(t *testing.T, dev *sessionFake, global bool, store fakeFavorites, statusErr error, noStore ...bool) (*kvmHarness, chan struct{}) {
	t.Helper()
	englishUI(t)
	a := test.NewTempApp(t)
	srv := kvmtest.Start(t, kvmtest.Options{Updates: [][]byte{redUpdate(128, 64)}, Username: "admin", Password: "pw"})
	dev.FP, dev.KVMAddr, dev.KVMUser, dev.KVMPassword = fp, srv.Addr, "admin", "pw"
	h := &kvmHarness{deviceHarness: newDevice(t, a, dev, true), srv: srv,
		changed: make(chan struct{}, 8), frames: make(chan struct{}, 16), asked: make(chan func(string, bool), 4)}
	h.d.goos = "linux"
	h.d.view.mapper = newKeyMapper("linux", func() bool { return true })
	h.d.onKVMChanged = func() { h.changed <- struct{}{} }
	h.d.wake = nil
	h.d.view.onFrame = func() { h.frames <- struct{}{} }
	h.d.askConsent = func(answer func(string, bool)) func() { h.asked <- answer; return func() {} }
	if statusErr != nil { // the last read before Show failed
		h.d.apply(amt.Status{}, statusErr)
	}
	t.Cleanup(func() { // end a running session and wait for its events before the window closes
		drain := func() {
			for {
				select {
				case <-h.changed:
				case <-time.After(300 * time.Millisecond):
					return
				}
			}
		}
		drain()
		if h.d.kvmClient != nil {
			tapKVM(h.d)
			drain()
		}
	})
	h.d.settings.SetKVMAutoConnect(global)
	if len(noStore) == 0 {
		h.d.layouts = store
	}
	loaded := make(chan struct{}, 1)
	if len(noStore) == 0 {
		h.d.onQualityLoaded = func() { loaded <- struct{}{} }
	} else {
		loaded <- struct{}{} // loadQuality returns early without a store
	}
	// Show itself, but without its poller: the test driver runs fyne.Do on the calling
	// goroutine, so a running poller would race with the favorite's goroutine on the window
	// state. Status reads are fed through apply instead.
	h.d.startPoller = func() {}
	h.d.Show()
	return h, loaded
}

var powerOn = amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25"}

func wantConnected(t *testing.T, h *kvmHarness) {
	t.Helper()
	recv(t, h.changed)
	recv(t, h.frames)
	if h.d.kvmClient == nil || h.srv.Connections() != 1 {
		t.Fatalf("not connected: busy=%v conns=%d", h.d.kvmBusy, h.srv.Connections())
	}
}

func wantIdle(t *testing.T, h *kvmHarness) {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	if h.d.kvmClient != nil || h.d.kvmBusy || h.srv.Connections() != 0 || len(h.errs) != 0 {
		t.Fatalf("connected or connecting: busy=%v conns=%d", h.d.kvmBusy, h.srv.Connections())
	}
}

func TestAutoConnectFollowsGlobalSetting(t *testing.T) {
	h, _ := autoDevice(t, &sessionFake{St: powerOn}, true, nil) // quick connect: no favorite
	wantConnected(t, h)
	h2, _ := autoDevice(t, &sessionFake{St: powerOn}, false, nil)
	wantIdle(t, h2)
}

func TestAutoConnectFavoriteOverride(t *testing.T) {
	for _, c := range []struct {
		global   bool
		override string
		want     bool
	}{
		{false, favorites.AutoConnectOn, true}, {true, favorites.AutoConnectOff, false},
		{true, "", true}, {false, "", false},
	} {
		fav := &favorites.Favorite{Host: "device.example.com", Username: "admin", AutoConnect: c.override}
		h, _ := autoDevice(t, &sessionFake{St: powerOn}, c.global, fav)
		if c.want {
			wantConnected(t, h)
		} else {
			wantIdle(t, h)
		}
	}
}

// The favorite's quality is applied before the automatic connect.
func TestAutoConnectUsesFavoriteQuality(t *testing.T) {
	setReconnect(t, time.Hour, 10*time.Millisecond) // a dropped Low session ends for good
	dev := &sessionFake{St: powerOn}
	fav := &favorites.Favorite{Host: "device.example.com", Username: "admin", Quality: favorites.QualityLow, AutoConnect: favorites.AutoConnectOn}
	h, _ := autoDevice(t, dev, false, fav)
	recv(t, h.changed) // the session opened (the fake server may drop it after a Low update)
	if q := dev.Qualities(); len(q) == 0 || q[0] != kvm.QualityLow {
		t.Fatalf("qualities %v", q)
	}
}

// A device that is off gets no attempt, and the single chance is used up: powering on later
// does not connect.
func TestAutoConnectNotWhenDeviceOffAndOnlyOnce(t *testing.T) {
	h, _ := autoDevice(t, &sessionFake{St: amt.Status{PowerState: models.PowerStateOffSoft}}, true, nil)
	wantIdle(t, h)
	h.d.apply(powerOn, nil)
	wantIdle(t, h)
}

// A failed status read is no attempt; the first successful read with the device on connects,
// once.
func TestAutoConnectWaitsForFirstSuccessfulStatus(t *testing.T) {
	dev := &sessionFake{St: powerOn}
	h, _ := autoDeviceErr(t, dev, true, nil, errors.New("unreachable"))
	wantIdle(t, h)
	h.d.apply(powerOn, nil)
	wantConnected(t, h)
	// Disconnect: the chance is used up, a further status does not reconnect.
	tapKVM(h.d)
	recv(t, h.changed)
	h.d.apply(powerOn, nil)
	time.Sleep(150 * time.Millisecond)
	if h.srv.Connections() != 1 || h.d.kvmClient != nil {
		t.Fatalf("reconnected: conns=%d", h.srv.Connections())
	}
}

// Without a favorites store (not wired) the global setting decides at the first status read.
func TestAutoConnectWithoutFavoritesStore(t *testing.T) {
	h, _ := autoDeviceErr(t, &sessionFake{St: powerOn}, true, nil, nil, true)
	wantIdle(t, h) // nothing read yet
	h.d.apply(powerOn, nil)
	wantConnected(t, h)
}

// A click on KVM before the favorite has been read uses up the automatic chance: cancelling
// the consent dialog does not let the automatic connect fire when the read returns.
func TestAutoConnectNotAfterManualClickBeforeQualityLoaded(t *testing.T) {
	block := make(chan struct{})
	dev := &sessionFake{St: powerOn, Consent: true, ConsentCode: "123456"}
	h, loaded := autoDeviceSetup(t, dev, true, fakeFavorites{block: block}, nil)
	h.d.apply(powerOn, nil)
	tapKVM(h.d)
	answer := <-h.asked
	answer("", false)  // cancel the consent dialog
	recv(t, h.changed) // kvmOpened with the cancelled connect: the channel orders its writes before the reads below
	close(block)
	recv(t, loaded)
	wantIdle(t, h)
	select {
	case <-h.asked:
		t.Fatal("automatic connect fired after the manual click")
	default:
	}
}

// A quality changed in the form while the first connect is still waiting for consent is
// checked when the session has opened: it reconnects with the new quality.
func TestAdoptQualityDuringConnectReconnectsAfterOpen(t *testing.T) {
	setReconnect(t, time.Hour, 10*time.Millisecond)
	dev := &sessionFake{Consent: true, ConsentCode: "123456"}
	h := newKVMDevice(t, dev, kvmtest.Options{})
	tapKVM(h.d)
	answer := <-h.asked
	h.d.adoptQuality(kvm.QualityLow)
	if !h.d.adoptPending || h.d.kvmQuality != kvm.QualityLow {
		t.Fatalf("pending=%v quality=%v", h.d.adoptPending, h.d.kvmQuality)
	}
	answer("123456", true)
	recv(t, h.changed) // the first session opened
	again := <-h.asked // the reconnect asks for consent as well
	again("123456", true)
	recv(t, h.changed) // reconnect started
	recv(t, h.changed) // reconnect finished
	if q := dev.Qualities(); len(q) != 2 || q[0] != kvm.QualityHigh || q[1] != kvm.QualityLow {
		t.Fatalf("qualities %v", q)
	}
	tapKVM(h.d) // end the session and wait for its end, so nothing runs when the window closes
	recv(t, h.changed)
}
