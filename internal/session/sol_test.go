package session

import (
	"errors"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/sol/soltest"
)

func solSession(t *testing.T, dev *Fake) (*Session, *soltest.Server) {
	t.Helper()
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	dev.SOL.Addr, dev.SOL.User, dev.SOL.Password = srv.Addr, "admin", "pw"
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com", Username: "admin"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	return s, srv
}

func TestOpenSOLDials(t *testing.T) {
	dev := &Fake{}
	s, srv := solSession(t, dev)
	go srv.AcceptSession(t)
	c, err := s.OpenSOL(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if dev.SOL.Calls() != 1 {
		t.Fatalf("calls %d", dev.SOL.Calls())
	}
}

func TestOpenSOLErrors(t *testing.T) {
	boom := errors.New("boom")
	dev := &Fake{}
	dev.SOL.Err = boom
	s, _ := solSession(t, dev)
	if _, err := s.OpenSOL(ctx, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	dev = &Fake{Consent: true, ConsentCode: "123456"}
	s, _ = solSession(t, dev)
	if _, err := s.OpenSOL(ctx, nil); !errors.Is(err, amt.ErrConsentRequired) {
		t.Fatalf("nil prompt: %v", err)
	}
	var ce *amt.ConsentError
	if _, err := s.OpenSOL(ctx, codeFunc(func() (string, error) { return "654321", nil })); !errors.As(err, &ce) {
		t.Fatalf("wrong code: %v", err)
	}
}

func TestOpenSOLHoldsMutexWhilePromptIsOpen(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s, srv := solSession(t, dev)
	go srv.AcceptSession(t)
	asked := make(chan struct{})
	answer := make(chan string)
	prompt := codeFunc(func() (string, error) { close(asked); return <-answer, nil })
	done := make(chan error, 1)
	go func() {
		c, err := s.OpenSOL(ctx, prompt)
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
