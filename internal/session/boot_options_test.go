package session

import (
	"errors"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

const bootHost = "device.example.com"

func bootSession(t *testing.T, dev *Fake) *Session {
	t.Helper()
	s, _, err := Open(ctx, amt.Config{Host: bootHost}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var pxe = amt.BootOptions{Target: amt.BootPXE, LockKeyboard: true}

// TestBootRunsConsentFirst: Boot asks for the consent before it sets the boot options; a wrong
// code, a missing prompt and a cancelled prompt write nothing (issue #90 pattern).
func TestBootRunsConsentFirst(t *testing.T) {
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s := bootSession(t, dev)
	var ce *amt.ConsentError
	if err := s.Boot(codePrompt("654321"), pxe); !errors.As(err, &ce) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := s.Boot(nil, pxe); !errors.Is(err, amt.ErrConsentRequired) {
		t.Fatalf("no prompt: %v", err)
	}
	if n := len(dev.BootFake().Boots()); n != 0 {
		t.Fatalf("%d boots without consent", n)
	}
	if err := s.Boot(codePrompt("123456"), pxe); err != nil {
		t.Fatal(err)
	}
	if got := dev.BootFake().Boots(); len(got) != 1 || got[0] != pxe || dev.BootConsents() != 3 {
		t.Fatalf("boots %v consents %d", got, dev.BootConsents())
	}
}

func TestBootFromFileAddsRootAfterConsent(t *testing.T) {
	der, _ := selfSigned(t, "OpenLAMT recovery (temporary)", true)
	dev := &Fake{Consent: true, ConsentCode: "123456"}
	s := bootSession(t, dev)
	ok, err := amt.ConfirmChecked(amt.OpBootFile, bootHost, true)
	if err != nil {
		t.Fatal(err)
	}
	o := amt.BootOptions{Target: amt.BootHTTPS, URL: "https://192.0.2.50:8443/a/recovery.efi"}

	if _, err := s.BootFromFile(codePrompt("123456"), der, o, amt.Confirmation{}); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("unconfirmed: %v", err)
	}
	if _, err := s.BootFromFile(codePrompt("000000"), der, o, ok); err == nil || len(dev.Cfg.Certificates) != 0 {
		t.Fatalf("wrong code: %v, certs %d", err, len(dev.Cfg.Certificates))
	}
	id, err := s.BootFromFile(codePrompt("123456"), der, o, ok)
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Cfg.Certificates) != 1 || dev.Cfg.Certificates[0].ID != id || !dev.Cfg.Certificates[0].TrustedRoot {
		t.Fatalf("certificates %+v", dev.Cfg.Certificates)
	}
	if b := dev.BootFake().Boots(); len(b) != 1 || b[0].URL != o.URL {
		t.Fatalf("boots %v", b)
	}
	// The OpBootFile confirmation also removes the temporary root afterwards.
	if err := s.DeleteCertificate(id, ok); err != nil || len(dev.Cfg.Certificates) != 0 {
		t.Fatalf("delete: %v", err)
	}
}

// A refused boot removes the temporary root again.
func TestBootFromFileFailedBootRemovesRoot(t *testing.T) {
	der, _ := selfSigned(t, "OpenLAMT recovery (temporary)", true)
	dev := &Fake{}
	dev.BootFake().BootErr = &amt.MethodError{Method: "SetBootConfigRole", ReturnValue: 2}
	s := bootSession(t, dev)
	ok, _ := amt.ConfirmChecked(amt.OpBootFile, bootHost, true)
	_, err := s.BootFromFile(nil, der, amt.BootOptions{Target: amt.BootHTTPS, URL: "https://192.0.2.50/r.efi"}, ok)
	var me *amt.MethodError
	if !errors.As(err, &me) || len(dev.Cfg.Certificates) != 0 {
		t.Fatalf("err %v, certs %d", err, len(dev.Cfg.Certificates))
	}
	if w := dev.Cfg.Writes; len(w) != 2 || w[0] != "AddTrustedRootCertificate" || w[1] != "DeleteCertificate" {
		t.Fatalf("writes %v", w)
	}
}

func TestSetOCRAndBootInfo(t *testing.T) {
	dev := &Fake{}
	dev.BootFake().Info = amt.BootInfo{Caps: amt.BootCapabilities{HTTPSBoot: true}, OCR: amt.OCRState{Known: true}}
	s := bootSession(t, dev)
	if err := s.SetOCR(true); err != nil {
		t.Fatal(err)
	}
	info, err := s.BootInfo()
	if err != nil || !info.HTTPSBootAvailable() {
		t.Fatalf("info %+v, %v", info, err)
	}
}

// Poller.Do runs the function as a job and reads the status after it.
func TestPollerDoRunsJob(t *testing.T) {
	dev := &Fake{}
	p, results := startPoller(t, dev, time.Hour)
	done := make(chan error, 1)
	ran := false
	p.Do(func() error { ran = true; return errors.New("x") }, func(err error) { done <- err })
	select {
	case err := <-done:
		if err == nil || !ran {
			t.Fatalf("err %v ran %v", err, ran)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job not answered")
	}
	select {
	case <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("no status read after the job")
	}
	p.Stop()
	p.Do(func() error { t.Error("ran after Stop"); return nil }, func(err error) { done <- err })
	if err := <-done; !errors.Is(err, ErrStopped) {
		t.Fatalf("after stop: %v", err)
	}
}
