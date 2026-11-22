package session

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
)

type codePrompt string

func (c codePrompt) ConsentCode() (string, error) { return string(c), nil }

// TestBootToBIOSRunsConsentFirst: the consent runs before the power command, and a rejected
// code sends no power command.
func TestBootToBIOSRunsConsentFirst(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	var ce *amt.ConsentError
	if err := s.BootToBIOS(codePrompt("654321")); !errors.As(err, &ce) || len(dev.Powered()) != 0 {
		t.Fatalf("wrong code: err=%v powered=%v", err, dev.Powered())
	}
	if err := s.BootToBIOS(nil); !errors.Is(err, amt.ErrConsentRequired) || len(dev.Powered()) != 0 {
		t.Fatalf("no prompt: err=%v powered=%v", err, dev.Powered())
	}
	if err := s.BootToBIOS(codePrompt("123456")); err != nil {
		t.Fatal(err)
	}
	if got := dev.Powered(); !reflect.DeepEqual(got, []amt.PowerAction{amt.BootToBIOS}) {
		t.Fatalf("powered %v", got)
	}
	if dev.BootConsents() != 3 {
		t.Fatalf("consents %d", dev.BootConsents())
	}
}

// TestBootToBIOSWithoutConsentNeedsNoPrompt: a device without consent boots with a nil prompt.
func TestBootToBIOSWithoutConsentNeedsNoPrompt(t *testing.T) {
	dev := &Fake{}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BootToBIOS(nil); err != nil || !reflect.DeepEqual(dev.Powered(), []amt.PowerAction{amt.BootToBIOS}) {
		t.Fatalf("err=%v powered=%v", err, dev.Powered())
	}
}
