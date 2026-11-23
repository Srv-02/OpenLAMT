package session

import (
	"errors"
	"fmt"
	"testing"
	"time"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
)

// asleepOps are the session operations that set boot options after the user consent.
func asleepOps(t *testing.T) map[string]func(*Session) error {
	t.Helper()
	der, _ := selfSigned(t, "OpenLAMT recovery (temporary)", true)
	file, _ := amt.ConfirmChecked(amt.OpBootFile, bootHost, true)
	erase, _ := amt.ConfirmChecked(amt.OpErase, bootHost, true)
	return map[string]func(*Session) error{
		"boot to BIOS": func(s *Session) error { return s.BootToBIOS(codePrompt("123456")) },
		"boot":         func(s *Session) error { return s.Boot(codePrompt("123456"), pxe) },
		"boot from file": func(s *Session) error {
			_, err := s.BootFromFile(codePrompt("123456"), der, amt.BootOptions{Target: amt.BootHTTPS, URL: "https://192.0.2.50/r.efi"}, file)
			return err
		},
		"erase": func(s *Session) error { return s.Erase(codePrompt("123456"), rpeSSDs, erase) },
	}
}

// TestSleepingDeviceRefusedBeforeConsent (issue #142): a device in Sleep - Light or Deep (S3) is
// refused before the consent starts, before the temporary root of One Click Recovery is added
// and before any boot write; Hibernate (S4) starts through the BIOS and runs as before.
func TestSleepingDeviceRefusedBeforeConsent(t *testing.T) {
	for _, state := range []models.PowerState{models.PowerStateSleepLight, models.PowerStateSleepDeep, models.PowerStateHibernate} {
		for name, run := range asleepOps(t) {
			t.Run(fmt.Sprintf("%s %d", name, state), func(t *testing.T) {
				dev := &Fake{Consent: true, ConsentCode: "123456", St: amt.Status{PowerState: state}}
				s := eraseSession(t, dev, true)
				err := run(s)
				b := dev.BootFake()
				if state == models.PowerStateHibernate {
					if err != nil || dev.BootConsents() != 1 {
						t.Fatalf("hibernate: err %v, consents %d", err, dev.BootConsents())
					}
					return
				}
				if !errors.Is(err, amt.ErrAsleep) {
					t.Fatalf("err %v", err)
				}
				if dev.BootConsents() != 0 || len(dev.Powered()) != 0 || len(b.Boots()) != 0 || len(b.Erases()) != 0 ||
					len(dev.Cfg.Certificates) != 0 || len(dev.Cfg.Writes) != 0 {
					t.Fatalf("consents %d, powered %v, boots %v, erases %v, writes %v",
						dev.BootConsents(), dev.Powered(), b.Boots(), b.Erases(), dev.Cfg.Writes)
				}
			})
		}
	}
}

// TestBootFromFileReportsRootLeftBehind (issue #142): when the boot fails after the root was
// added (here the second sleep check in amt) and deleting the root fails as well, the error says
// so (ErrRootNotRemoved), so the app does not claim that nothing was changed.
func TestBootFromFileReportsRootLeftBehind(t *testing.T) {
	der, _ := selfSigned(t, "OpenLAMT recovery (temporary)", true)
	dev := &Fake{}
	b := dev.BootFake()
	b.BootErr = fmt.Errorf("boot: %w", amt.ErrAsleep)
	b.Gate = make(chan struct{})
	s := bootSession(t, dev)
	ok, _ := amt.ConfirmChecked(amt.OpBootFile, bootHost, true)
	o := amt.BootOptions{Target: amt.BootHTTPS, URL: "https://192.0.2.50/r.efi"}
	res := make(chan error, 1)
	go func() {
		_, err := s.BootFromFile(nil, der, o, ok)
		res <- err
	}()
	// Once the root is added, the delete after the failed boot is made to fail.
	for {
		dev.mu.Lock()
		if len(dev.Cfg.Certificates) == 1 {
			break
		}
		dev.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	dev.Certs().WriteErr = errors.New("connection reset")
	b.Gate <- struct{}{}
	dev.mu.Unlock()
	err := <-res
	if !errors.Is(err, amt.ErrAsleep) || !errors.Is(err, ErrRootNotRemoved) || len(dev.Cfg.Certificates) != 1 {
		t.Fatalf("err %v, certs %d", err, len(dev.Cfg.Certificates))
	}

	dev.Certs().WriteErr = nil
	close(b.Gate)
	if _, err := s.BootFromFile(nil, der, o, ok); !errors.Is(err, amt.ErrAsleep) || errors.Is(err, ErrRootNotRemoved) {
		t.Fatalf("root removed, but err %v", err)
	}
}
