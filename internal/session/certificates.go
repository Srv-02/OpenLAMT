package session

import (
	"crypto/rsa"

	"github.com/Srv-02/openlamt/internal/amt"
)

// CertificateDevice is the capability of certificate management (issue #16): the certificate
// store, import and delete. One Click Recovery (#21) uses AddTrustedRootCertificate and
// DeleteCertificate for its temporary root certificate.
type CertificateDevice interface {
	CertificateStore() (amt.CertificateStore, error)
	AddTrustedRootCertificate(der []byte) (string, error)
	AddCertificateWithKey(der []byte, key *rsa.PrivateKey) (string, error)
	DeleteCertificate(id string, confirm amt.Confirmation) error
	DeleteKeyPair(id string, confirm amt.Confirmation) error
}

var _ CertificateDevice = (*amt.Device)(nil)

// CertificateStore reads the certificates and key pairs of the device.
func (s *Session) CertificateStore() (amt.CertificateStore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[CertificateDevice](s)
	if err != nil {
		return amt.CertificateStore{}, err
	}
	st, err := d.CertificateStore()
	return st, classify(err)
}

// AddTrustedRootCertificate adds der (one X.509 certificate) as trusted root and returns its ID.
func (s *Session) AddTrustedRootCertificate(der []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[CertificateDevice](s)
	if err != nil {
		return "", err
	}
	id, err := d.AddTrustedRootCertificate(der)
	return id, classify(err)
}

// AddCertificateWithKey adds the private key and the certificate der and returns the
// certificate's ID. The key is only sent to the device; send it over TLS.
func (s *Session) AddCertificateWithKey(der []byte, key *rsa.PrivateKey) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[CertificateDevice](s)
	if err != nil {
		return "", err
	}
	id, err := d.AddCertificateWithKey(der, key)
	return id, classify(err)
}

// DeleteCertificate removes certificate id: a confirmation for amt.OpCertDelete, for a trusted
// root for amt.OpRootCertDelete (or amt.OpBootFile). The TLS certificate fails with an error that
// wraps amt.ErrCertificateInUse.
func (s *Session) DeleteCertificate(id string, confirm amt.Confirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[CertificateDevice](s)
	if err != nil {
		return err
	}
	return classify(d.DeleteCertificate(id, confirm))
}

// DeleteKeyPair removes key pair id; it needs a confirmation for amt.OpCertDelete.
func (s *Session) DeleteKeyPair(id string, confirm amt.Confirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[CertificateDevice](s)
	if err != nil {
		return err
	}
	return classify(d.DeleteKeyPair(id, confirm))
}
