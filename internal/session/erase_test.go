package session

import (
	"errors"
	"slices"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
)

func eraseSession(t *testing.T, dev *Fake, tls bool) *Session {
	t.Helper()
	cfg := amt.Config{Host: bootHost, UseTLS: tls}
	if tls {
		cfg.Fingerprint = "AA"
	}
	s, _, err := Open(ctx, cfg, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var rpeSSDs = amt.EraseOptions{Platform: true, Actions: amt.EraseSSDs}

// TestEraseRunsConsentFirst: Erase asks for the consent before it arms the erase; a wrong code,
// a missing prompt and a cancelled prompt write nothing (the Boot pattern).
func TestEraseRunsConsentFirst(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s := eraseSession(t, dev, true)
	ok, _ := amt.ConfirmChecked(amt.OpErase, bootHost, true)
	var ce *amt.ConsentError
	if err := s.Erase(codePrompt("654321"), rpeSSDs, ok); !errors.As(err, &ce) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := s.Erase(nil, rpeSSDs, ok); !errors.Is(err, amt.ErrConsentRequired) {
		t.Fatalf("no prompt: %v", err)
	}
	if n := len(dev.BootFake().Erases()); n != 0 {
		t.Fatalf("%d erases without consent", n)
	}
	if err := s.Erase(codePrompt("123456"), rpeSSDs, ok); err != nil {
		t.Fatal(err)
	}
	if got := dev.BootFake().Erases(); len(got) != 1 || got[0] != rpeSSDs || dev.BootConsents() != 3 {
		t.Fatalf("erases %v consents %d", got, dev.BootConsents())
	}
}

// Without the red confirmation, and for Remote Platform Erase without TLS, not even the
// consent starts.
func TestEraseChecksBeforeConsent(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s := eraseSession(t, dev, true)
	other, _ := amt.ConfirmChecked(amt.OpErase, "other.example.com", true)
	for _, c := range []amt.Confirmation{{}, other} {
		if err := s.Erase(codePrompt("123456"), rpeSSDs, c); !errors.Is(err, amt.ErrRiskyChange) {
			t.Fatalf("unconfirmed: %v", err)
		}
	}
	plain := &Fake{Consent: true, ConsentCode: "123456"}
	s = eraseSession(t, plain, false)
	ok, _ := amt.ConfirmChecked(amt.OpErase, bootHost, true)
	if err := s.Erase(codePrompt("123456"), rpeSSDs, ok); !errors.Is(err, amt.ErrEraseNeedsTLS) {
		t.Fatalf("without TLS: %v", err)
	}
	if dev.BootConsents()+plain.BootConsents() != 0 || len(dev.BootFake().Erases())+len(plain.BootFake().Erases()) != 0 {
		t.Fatal("consent or erase before the checks")
	}
	// Remote Secure Erase needs no TLS.
	if err := s.Erase(codePrompt("123456"), amt.EraseOptions{Password: "x"}, ok); err != nil {
		t.Fatal(err)
	}
}

func TestSetRPE(t *testing.T) {
	dev := &Fake{}
	s := eraseSession(t, dev, true)
	if err := s.SetRPE(true); err != nil {
		t.Fatal(err)
	}
	info, err := s.BootInfo()
	if err != nil || !info.Erase.Enabled || !slices.Equal(dev.BootFake().RPEWrites(), []bool{true}) {
		t.Fatalf("info %+v, %v", info.Erase, err)
	}
}
