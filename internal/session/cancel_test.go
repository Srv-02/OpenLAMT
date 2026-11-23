package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/kvm"
)

// A cancel that arrives while the open waits for the session lock must not reach the device.
func TestOpenCancelledWhileWaitingForLockWritesNothing(t *testing.T) {
	dev := &Fake{}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com", Username: "admin"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	type res struct{ err error }
	out := make(chan res, 3)
	go func() { _, err := s.OpenKVM(cctx, kvm.Quality(0), nil); out <- res{err} }()
	go func() { _, err := s.OpenSOL(cctx, nil); out <- res{err} }()
	img := &sessionImage{}
	go func() { _, err := s.OpenIDER(cctx, img, ider.StartNow, nil); out <- res{err} }()
	cancel()
	s.mu.Unlock()
	for range 3 {
		if r := <-out; !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err %v, want context.Canceled", r.err)
		}
	}
	if n := len(dev.Qualities()) + dev.SOL.Calls() + len(dev.IDERStarts()); n != 0 {
		t.Fatalf("%d device calls after cancel", n)
	}
}
