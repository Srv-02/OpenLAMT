package session

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/ider/idertest"
)

type sessionImage struct{ closed atomic.Int32 }

func (m *sessionImage) Size() int64                           { return 4096 }
func (m *sessionImage) ReadAt(p []byte, _ int64) (int, error) { return len(p), nil }
func (m *sessionImage) Close() error                          { m.closed.Add(1); return nil }

func iderSession(t *testing.T, dev *Fake) (*Session, *idertest.Server) {
	t.Helper()
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	dev.IDERAddr, dev.IDERUser, dev.IDERPassword = srv.Addr, "admin", "pw"
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com", Username: "admin"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	return s, srv
}

func TestOpenIDERDialsAndOwnsImage(t *testing.T) {
	dev := &Fake{}
	s, srv := iderSession(t, dev)
	go srv.AcceptSession(t)
	img := &sessionImage{}
	c, err := s.OpenIDER(ctx, img, ider.StartOnReboot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if img.closed.Load() != 0 {
		t.Fatal("image closed on success")
	}
	c.Close()
	if img.closed.Load() != 1 {
		t.Fatalf("closed = %d", img.closed.Load())
	}
	if st := dev.IDERStarts(); len(st) != 1 || st[0] != ider.StartOnReboot {
		t.Fatalf("starts %v", st)
	}
}

func TestOpenIDERErrorKeepsImage(t *testing.T) {
	boom := errors.New("boom")
	dev := &Fake{IDERErr: boom}
	s, _ := iderSession(t, dev)
	img := &sessionImage{}
	if _, err := s.OpenIDER(ctx, img, ider.StartNow, nil); !errors.Is(err, boom) || img.closed.Load() != 0 {
		t.Fatalf("err=%v closed=%d", err, img.closed.Load())
	}
}

func TestOpenIDERConsentErrors(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s, _ := iderSession(t, dev)
	img := &sessionImage{}
	if _, err := s.OpenIDER(ctx, img, ider.StartNow, nil); !errors.Is(err, amt.ErrConsentRequired) {
		t.Fatalf("nil prompt: %v", err)
	}
	var ce *amt.ConsentError
	if _, err := s.OpenIDER(ctx, img, ider.StartNow, codeFunc(func() (string, error) { return "654321", nil })); !errors.As(err, &ce) {
		t.Fatalf("wrong code: %v", err)
	}
}

func TestOpenIDERHoldsMutexWhilePromptIsOpen(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s, srv := iderSession(t, dev)
	go srv.AcceptSession(t)
	asked := make(chan struct{})
	answer := make(chan string)
	prompt := codeFunc(func() (string, error) { close(asked); return <-answer, nil })
	done := make(chan error, 1)
	go func() {
		c, err := s.OpenIDER(ctx, &sessionImage{}, ider.StartNow, prompt)
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	<-asked
	statusDone := make(chan struct{})
	go func() { s.Status(); close(statusDone) }()
	select {
	case <-statusDone:
		t.Fatal("status read ran while the consent prompt was open")
	case <-time.After(50 * time.Millisecond):
	}
	answer <- "123456"
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-statusDone
	if dev.MaxBusy() != 1 {
		t.Fatalf("max concurrent device calls = %d", dev.MaxBusy())
	}
}

func TestISOBootMethods(t *testing.T) {
	boom := errors.New("boom")
	dev := &Fake{}
	s, _ := iderSession(t, dev)
	if err := s.NextBootFromISO(); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearISOBoot(); err != nil {
		t.Fatal(err)
	}
	if n, c := dev.ISOCalls(); n != 1 || c != 1 {
		t.Fatalf("calls %d %d", n, c)
	}
	dev.NextBootErr, dev.ClearISOBootErr = boom, boom
	if err := s.NextBootFromISO(); !errors.Is(err, boom) {
		t.Fatalf("next: %v", err)
	}
	if err := s.ClearISOBoot(); !errors.Is(err, boom) {
		t.Fatalf("clear: %v", err)
	}
}
