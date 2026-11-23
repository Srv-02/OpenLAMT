package session

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeCertificates is the certificate state of a Fake beyond FakeConfig.Certificates (the
// certificates themselves, shared with the TLS settings): the key pairs and the errors. Writes
// check the values and the confirmation like *amt.Device and record their method in
// FakeConfig.Writes.
type FakeCertificates struct {
	Keys     []amt.KeyPair
	ReadErr  error // CertificateStore fails with it
	Full     bool  // imports fail with amt.ErrCertificateStoreFull
	WriteErr error // every write fails with it after the checks
	next     int
}

// Certs returns the certificate state of f.
func (f *Fake) Certs() *FakeCertificates { return fakeState[FakeCertificates](f, "certificates") }

var _ CertificateDevice = (*Fake)(nil)

func (f *Fake) CertificateStore() (amt.CertificateStore, error) {
	var s amt.CertificateStore
	var err error
	f.cfgRead(func() {
		c := f.Certs()
		switch {
		case c.ReadErr != nil:
			err = c.ReadErr
		case f.Cfg.CertsErr != nil:
			err = f.Cfg.CertsErr
		default:
			s = amt.CertificateStore{Certificates: slices.Clone(f.Cfg.Certificates), Keys: slices.Clone(c.Keys)}
		}
	})
	return s, err
}

// fakeCertificate describes der as the device would list it.
func fakeCertificate(id string, der []byte, x *x509.Certificate) amt.Certificate {
	sum := sha256.Sum256(der)
	return amt.Certificate{ID: id, Subject: x.Subject.String(), Issuer: x.Issuer.String(),
		NotBefore: x.NotBefore.UTC(), NotAfter: x.NotAfter.UTC(), Fingerprint: hex.EncodeToString(sum[:]), DER: der}
}

// certImport runs an import: the checks of *amt.Device, then Full and WriteErr, then apply.
func (f *Fake) certImport(method string, der []byte, key *rsa.PrivateKey, withKey bool) (string, error) {
	x, err := x509.ParseCertificate(der)
	if err != nil {
		return "", &amt.ValidationError{Field: "Certificate", Reason: "an X.509 certificate in DER form"}
	}
	if withKey {
		pub, ok := x.PublicKey.(*rsa.PublicKey)
		if key == nil || !ok || !pub.Equal(&key.PublicKey) {
			return "", &amt.ValidationError{Field: "Key", Reason: "the RSA private key of the certificate"}
		}
	}
	var id string
	f.cfgRead(func() {
		c := f.Certs()
		switch {
		case c.Full:
			err = fmt.Errorf("%w: %w", amt.ErrCertificateStoreFull, &amt.MethodError{Method: method, ReturnValue: 23})
			return
		case c.WriteErr != nil:
			err = c.WriteErr
			return
		}
		c.next++
		id = fmt.Sprintf("Intel(r) AMT Certificate: Handle: %d", 100+c.next)
		cert := fakeCertificate(id, der, x)
		cert.TrustedRoot = !withKey
		if withKey {
			cert.HasPrivateKey = true
			pub := x509.MarshalPKCS1PublicKey(&key.PublicKey)
			sum := sha256.Sum256(pub)
			c.Keys = append(c.Keys, amt.KeyPair{ID: fmt.Sprintf("Intel(r) AMT Key: Handle: %d", 100+c.next), Algorithm: "RSA",
				Bits: key.N.BitLen(), Fingerprint: hex.EncodeToString(sum[:]), Certificate: id})
		}
		f.Cfg.Certificates = append(f.Cfg.Certificates, cert)
		f.Cfg.Writes = append(f.Cfg.Writes, method)
	})
	return id, err
}

func (f *Fake) AddTrustedRootCertificate(der []byte) (string, error) {
	return f.certImport("AddTrustedRootCertificate", der, nil, false)
}

func (f *Fake) AddCertificateWithKey(der []byte, key *rsa.PrivateKey) (string, error) {
	return f.certImport("AddCertificateWithKey", der, key, true)
}

// confirmedAny is confirmed for the first of ops that c allows.
func (f *Fake) confirmedAny(c amt.Confirmation, ops []amt.Operation, field string) error {
	for _, op := range ops {
		if f.confirmed(c, op, nil) == nil {
			return nil
		}
	}
	return &amt.RiskyChangeError{Op: ops[0], Fields: []string{field}}
}

// DeleteCertificate checks like *amt.Device; the TLS certificate is refused as the device does.
func (f *Fake) DeleteCertificate(id string, confirm amt.Confirmation) error {
	var err error
	f.cfgRead(func() {
		i := slices.IndexFunc(f.Cfg.Certificates, func(c amt.Certificate) bool { return c.ID == id })
		if i < 0 {
			err = &amt.ValidationError{Field: "Certificate", Reason: "not in the certificate store"}
			return
		}
		cert := f.Cfg.Certificates[i]
		if err = f.confirmedAny(confirm, amt.CertificateConfirmations(cert), id); err != nil {
			return
		}
		switch c := f.Certs(); {
		case c.WriteErr != nil:
			err = c.WriteErr
		case cert.TLS:
			err = fmt.Errorf("delete certificate: %w", amt.ErrCertificateInUse)
		default:
			f.Cfg.Certificates = slices.Delete(f.Cfg.Certificates, i, i+1)
			for k := range c.Keys {
				if c.Keys[k].Certificate == id {
					c.Keys[k].Certificate = ""
				}
			}
			f.Cfg.Writes = append(f.Cfg.Writes, "DeleteCertificate")
		}
	})
	return err
}

// DeleteKeyPair checks like *amt.Device; the key of the TLS certificate is refused.
func (f *Fake) DeleteKeyPair(id string, confirm amt.Confirmation) error {
	var err error
	f.cfgRead(func() {
		c := f.Certs()
		i := slices.IndexFunc(c.Keys, func(k amt.KeyPair) bool { return k.ID == id })
		if i < 0 {
			err = &amt.ValidationError{Field: "Key", Reason: "not in the certificate store"}
			return
		}
		if err = f.confirmedAny(confirm, []amt.Operation{amt.OpCertDelete}, id); err != nil {
			return
		}
		linked := c.Keys[i].Certificate
		inUse := linked != "" && slices.ContainsFunc(f.Cfg.Certificates, func(x amt.Certificate) bool { return x.ID == linked && x.TLS })
		switch {
		case c.WriteErr != nil:
			err = c.WriteErr
		case inUse:
			err = fmt.Errorf("delete key pair: %w", amt.ErrCertificateInUse)
		default:
			c.Keys = slices.Delete(c.Keys, i, i+1)
			for j := range f.Cfg.Certificates {
				if f.Cfg.Certificates[j].ID == linked {
					f.Cfg.Certificates[j].HasPrivateKey = false
				}
			}
			f.Cfg.Writes = append(f.Cfg.Writes, "DeleteKeyPair")
		}
	})
	return err
}
