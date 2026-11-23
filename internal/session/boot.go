package session

import (
	"errors"
	"fmt"

	"github.com/Srv-02/openlamt/internal/amt"
)

// Boot options (issue #13) and One Click Recovery (issue #21) as a session capability
// (capabilities.go). Every write of boot options asks for the user consent first, as
// BootToBIOS does (issue #90): a failed or cancelled consent writes nothing. A sleeping device
// (S3) is refused before the consent (issue #142).

// ErrRootNotRemoved: BootFromFile failed after it had added the temporary root certificate, and
// deleting the root failed too, so it stays on the device.
var ErrRootNotRemoved = errors.New("remove the temporary root certificate")

// BootDevice is what the Boot Options dialog and the One Click Recovery switch need.
type BootDevice interface {
	BootInfo() (amt.BootInfo, error)
	Boot(amt.BootOptions) error
	SetOCR(on bool) error
}

var _ BootDevice = (*amt.Device)(nil)

// BootInfo reads the boot capabilities and the One Click Recovery state.
func (s *Session) BootInfo() (amt.BootInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[BootDevice](s)
	if err != nil {
		return amt.BootInfo{}, err
	}
	info, err := d.BootInfo()
	return info, classify(err)
}

// Boot refuses a sleeping device, runs the user consent (prompt may be nil when no code is at
// hand) and then sets the boot options and restarts or powers on the device, all under the mutex.
func (s *Session) Boot(prompt amt.ConsentPrompt, o amt.BootOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[BootDevice](s)
	if err != nil {
		return err
	}
	if err := s.dev.RefuseAsleep("boot"); err != nil {
		return classify(err)
	}
	if err := s.dev.BootConsent(prompt); err != nil {
		return classify(err)
	}
	return classify(d.Boot(o))
}

// BootFromFile is One Click Recovery from a file of this computer (amt.OpBootFile, orange): the
// user consent, then the temporary root certificate rootDER as trusted root, then the HTTPS boot
// of o (o.URL points to the local server that rootDER signed). It returns the root's ID, which
// the caller deletes with DeleteCertificate and confirm once the device has loaded the image. A
// failed boot deletes the root again; a sleeping device or a failed consent adds nothing. When
// deleting the root fails, the error matches ErrRootNotRemoved.
func (s *Session) BootFromFile(prompt amt.ConsentPrompt, rootDER []byte, o amt.BootOptions, confirm amt.Confirmation) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !confirm.Allows(amt.OpBootFile, s.Config().Host) {
		return "", &amt.RiskyChangeError{Op: amt.OpBootFile, Fields: []string{"TrustedRootCertificate"}}
	}
	d, err := capability[BootDevice](s)
	if err != nil {
		return "", err
	}
	certs, err := capability[CertificateDevice](s)
	if err != nil {
		return "", err
	}
	if o.Target != amt.BootHTTPS {
		return "", fmt.Errorf("boot from file: %w", amt.ErrInvalidValue)
	}
	if err := s.dev.RefuseAsleep("boot"); err != nil {
		return "", classify(err)
	}
	if err := s.dev.BootConsent(prompt); err != nil {
		return "", classify(err)
	}
	id, err := certs.AddTrustedRootCertificate(rootDER)
	if err != nil {
		return "", classify(err)
	}
	if err := d.Boot(o); err != nil {
		if derr := certs.DeleteCertificate(id, confirm); derr != nil {
			// Each part on its own: classify keeps only the *amt.BootMayBeSetError of a join.
			return "", errors.Join(classify(err), fmt.Errorf("%w: %w", ErrRootNotRemoved, classify(derr)))
		}
		return "", classify(err)
	}
	return id, nil
}

// SetOCR allows or forbids One Click Recovery in the AMT boot service.
func (s *Session) SetOCR(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[BootDevice](s)
	if err != nil {
		return err
	}
	return classify(d.SetOCR(on))
}
