package session

import (
	"errors"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

type codeFunc func() (string, error)

func (f codeFunc) ConsentCode() (string, error) { return f() }

func kvmSession(t *testing.T, dev *Fake) (*Session, *kvmtest.Server) {
	t.Helper()
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw"})
	dev.KVMAddr, dev.KVMUser, dev.KVMPassword = srv.Addr, "admin", "pw"
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com", Username: "admin"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	return s, srv
}

func TestOpenKVMDialsAndRecordsQuality(t *testing.T) {
	dev := &Fake{}
	s, srv := kvmSession(t, dev)
	c, err := s.OpenKVM(ctx, kvm.QualityLow, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if srv.Connections() != 1 || c.Quality() != kvm.QualityLow {
		t.Fatalf("connections=%d quality=%v", srv.Connections(), c.Quality())
	}
	if q := dev.Qualities(); len(q) != 1 || q[0] != kvm.QualityLow {
		t.Fatalf("qualities %v", q)
	}
}

func TestOpenKVMHoldsMutexWhilePromptIsOpen(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s, _ := kvmSession(t, dev)
	asked := make(chan struct{})
	answer := make(chan string)
	prompt := codeFunc(func() (string, error) { close(asked); return <-answer, nil })
	done := make(chan error, 1)
	go func() {
		c, err := s.OpenKVM(ctx, kvm.QualityHigh, prompt)
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

func TestOpenKVMConsentErrors(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s, srv := kvmSession(t, dev)
	if _, err := s.OpenKVM(ctx, kvm.QualityHigh, nil); !errors.Is(err, amt.ErrConsentRequired) {
		t.Fatalf("nil prompt: %v", err)
	}
	if _, err := s.OpenKVM(ctx, kvm.QualityHigh, codeFunc(func() (string, error) { return "12", nil })); !errors.Is(err, amt.ErrInvalidConsentCode) {
		t.Fatalf("short code: %v", err)
	}
	var ce *amt.ConsentError
	if _, err := s.OpenKVM(ctx, kvm.QualityHigh, codeFunc(func() (string, error) { return "654321", nil })); !errors.As(err, &ce) || ce.Step != "SendOptInCode" {
		t.Fatalf("wrong code: %v", err)
	}
	cancelled := errors.New("cancelled")
	if _, err := s.OpenKVM(ctx, kvm.QualityHigh, codeFunc(func() (string, error) { return "", cancelled })); !errors.Is(err, cancelled) {
		t.Fatalf("prompt error: %v", err)
	}
	if srv.Connections() != 0 {
		t.Fatal("dialled despite consent failure")
	}
	dev.KVMErr = errors.New("boom")
	if _, err := s.OpenKVM(ctx, kvm.QualityHigh, nil); err == nil || err.Error() != "boom" {
		t.Fatalf("KVMErr: %v", err)
	}
}
