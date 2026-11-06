package session

import (
	"errors"
	"slices"
	"testing"
	"time"

	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
)

type result struct {
	st  amt.Status
	err error
}

func startPoller(t *testing.T, dev *Fake, interval time.Duration) (*Poller, chan result) {
	t.Helper()
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan result, 256)
	p := NewPoller(s, interval, func(st amt.Status, err error) { results <- result{st, err} })
	p.Start()
	t.Cleanup(p.Stop)
	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("no initial read")
	}
	return p, results
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestPollerReadsImmediatelyThenAtInterval(t *testing.T) {
	dev := &Fake{St: amt.Status{AMTVersion: "16.1.25"}}
	_, results := startPoller(t, dev, 20*time.Millisecond)
	time.Sleep(110 * time.Millisecond)
	n := dev.Calls() - 2 // Open and the initial read
	if n < 3 || n > 7 {
		t.Fatalf("%d interval reads in 110 ms at 20 ms", n)
	}
	if r := <-results; r.err != nil || r.st.AMTVersion != "16.1.25" {
		t.Fatalf("%+v", r)
	}
}

func TestPollerPausesDuringPower(t *testing.T) {
	dev := &Fake{PowerGate: make(chan struct{})}
	p, _ := startPoller(t, dev, 10*time.Millisecond)
	done := make(chan error, 1)
	p.Power(amt.PowerReset, func(err error) { done <- err })
	waitUntil(t, dev.PowerStarted)
	before := dev.Calls()
	time.Sleep(60 * time.Millisecond)
	if dev.Calls() != before {
		t.Fatal("status read during power action")
	}
	close(dev.PowerGate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("done not called")
	}
	waitUntil(t, func() bool { return dev.Calls() > before })
	if got := dev.Powered(); len(got) != 1 || got[0] != amt.PowerReset {
		t.Fatalf("powered %v", got)
	}
}

func TestPollerRefreshAndSetInterval(t *testing.T) {
	dev := &Fake{}
	p, _ := startPoller(t, dev, time.Hour)
	p.Refresh()
	waitUntil(t, func() bool { return dev.Calls() == 3 })
	p.SetInterval(15 * time.Millisecond)
	if p.Interval() != 15*time.Millisecond {
		t.Fatalf("interval %v", p.Interval())
	}
	time.Sleep(100 * time.Millisecond)
	if dev.Calls() < 6 {
		t.Fatalf("%d calls after shortening the interval", dev.Calls())
	}
}

func TestPollerStopIsFinal(t *testing.T) {
	dev := &Fake{}
	p, _ := startPoller(t, dev, 5*time.Millisecond)
	p.Stop()
	n := dev.Calls()
	time.Sleep(30 * time.Millisecond)
	if dev.Calls() != n {
		t.Fatal("read after Stop")
	}
	p.Stop() // idempotent
	done := make(chan error, 1)
	p.Power(amt.PowerOn, func(err error) { done <- err })
	if err := <-done; !errors.Is(err, ErrStopped) {
		t.Fatalf("err = %v", err)
	}
}

func TestPollerStopWaitsForRunningAction(t *testing.T) {
	dev := &Fake{PowerGate: make(chan struct{})}
	p, _ := startPoller(t, dev, time.Hour)
	p.Power(amt.PowerOff, func(error) {})
	waitUntil(t, dev.PowerStarted)
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while the action was running")
	case <-time.After(30 * time.Millisecond):
	}
	close(dev.PowerGate)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the action")
	}
}

func TestPollerReportsErrors(t *testing.T) {
	dev := &Fake{StatusErr: errors.New("boom")}
	s, _, _ := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(&Fake{}))
	s.dev = dev // swap in the failing device after a successful login
	results := make(chan result, 8)
	p := NewPoller(s, time.Hour, func(st amt.Status, err error) { results <- result{st, err} })
	p.Start()
	t.Cleanup(p.Stop)
	if r := <-results; r.err == nil || r.err.Error() != "boom" {
		t.Fatalf("%+v", r)
	}
}

func TestPollerQueuedActionAnsweredAfterStop(t *testing.T) {
	dev := &Fake{PowerGate: make(chan struct{})}
	p, _ := startPoller(t, dev, time.Hour)
	p.Power(amt.PowerOff, func(error) {}) // runs and blocks on the gate
	waitUntil(t, dev.PowerStarted)
	queued := make(chan error, 1)
	p.Power(amt.PowerOn, func(err error) { queued <- err }) // waits in the queue
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	time.Sleep(20 * time.Millisecond)
	close(dev.PowerGate)
	select {
	case err := <-queued:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("queued action: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued action never answered")
	}
	<-stopped
	if got := dev.Powered(); len(got) != 1 || got[0] != amt.PowerOff {
		t.Fatalf("powered %v", got)
	}
}

func TestPollerStopBeforeStart(t *testing.T) {
	s, _, _ := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(&Fake{}))
	p := NewPoller(s, time.Hour, func(amt.Status, error) {})
	p.Stop()
	done := make(chan error, 1)
	p.Power(amt.PowerOn, func(err error) { done <- err })
	if err := <-done; !errors.Is(err, ErrStopped) {
		t.Fatalf("err = %v", err)
	}
	p.Start() // loop sees the closed stop channel and exits
	p.Stop()
}

func TestPollerReadsAgainAfterPower(t *testing.T) {
	dev := &Fake{}
	p, _ := startPoller(t, dev, time.Hour)
	before := dev.Calls()
	done := make(chan error, 1)
	p.Power(amt.PowerOn, func(err error) { done <- err })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return dev.Calls() == before+1 })
	time.Sleep(30 * time.Millisecond)
	if dev.Calls() != before+1 {
		t.Fatalf("calls %d, want exactly %d", dev.Calls(), before+1)
	}
}

func TestPollerNonPositiveIntervalFallsBack(t *testing.T) {
	s, _, _ := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(&Fake{}))
	p := NewPoller(s, 0, func(amt.Status, error) {})
	if p.Interval() != DefaultInterval {
		t.Fatalf("NewPoller(0): %v", p.Interval())
	}
	p.SetInterval(-time.Second)
	if p.Interval() != DefaultInterval {
		t.Fatalf("SetInterval(-1s): %v", p.Interval())
	}
}

func nextResult(t *testing.T, results chan result) result {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(time.Second):
		t.Fatal("no status delivered")
	}
	return result{}
}

func TestPollerReadsPowerStatesOnce(t *testing.T) {
	dev := &Fake{States: []power.PowerState{2, 8, 12}}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan result, 256)
	p := NewPoller(s, 5*time.Millisecond, func(st amt.Status, err error) { results <- result{st, err} })
	p.Start()
	t.Cleanup(p.Stop)
	if r := nextResult(t, results); !slices.Equal(r.st.PowerStates, []power.PowerState{2, 8, 12}) {
		t.Fatalf("first status without the power states: %+v", r)
	}
	waitUntil(t, func() bool { return dev.Calls() > 5 })
	if n := dev.StatesCalls(); n != 1 {
		t.Fatalf("%d power state reads, want 1 per connection", n)
	}
	if r := nextResult(t, results); !slices.Equal(r.st.PowerStates, []power.PowerState{2, 8, 12}) {
		t.Fatalf("later status lost the power states: %+v", r)
	}
}

func TestPollerRetriesFailedPowerStates(t *testing.T) {
	dev := &Fake{StatesErr: errors.New("boom")}
	p, results := startPoller(t, dev, time.Hour)
	dev.SetPowerStates([]power.PowerState{12}, nil)
	p.Refresh()
	if r := nextResult(t, results); !slices.Equal(r.st.PowerStates, []power.PowerState{12}) {
		t.Fatalf("no retry after a failed read: %+v", r)
	}
	if n := dev.StatesCalls(); n != 2 {
		t.Fatalf("%d power state reads, want 2", n)
	}
}

func TestPollerRefreshPowerStates(t *testing.T) {
	dev := &Fake{States: []power.PowerState{2, 8}}
	p, results := startPoller(t, dev, time.Hour)

	p.RefreshPowerStates() // unchanged: one delivery
	if r := nextResult(t, results); !slices.Equal(r.st.PowerStates, []power.PowerState{2, 8}) {
		t.Fatalf("%+v", r)
	}
	waitUntil(t, func() bool { return dev.StatesCalls() == 2 })

	dev.SetPowerStates([]power.PowerState{2, 8, 12}, nil) // LMS started
	p.RefreshPowerStates()
	if r := nextResult(t, results); !slices.Equal(r.st.PowerStates, []power.PowerState{2, 8}) {
		t.Fatalf("status was not delivered first with the known states: %+v", r)
	}
	if r := nextResult(t, results); !slices.Equal(r.st.PowerStates, []power.PowerState{2, 8, 12}) {
		t.Fatalf("changed states not delivered: %+v", r)
	}
	select {
	case r := <-results:
		t.Fatalf("extra delivery %+v", r)
	case <-time.After(20 * time.Millisecond):
	}

	dev.SetPowerStates(nil, errors.New("boom")) // failed refresh: unknown
	p.RefreshPowerStates()
	nextResult(t, results)
	if r := nextResult(t, results); r.st.PowerStates != nil {
		t.Fatalf("failed refresh kept the old states: %+v", r)
	}
}
