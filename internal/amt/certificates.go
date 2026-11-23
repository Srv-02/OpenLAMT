package amt

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// Certificate management (issue #16): the AMT certificate store (AMT_PublicKeyCertificate) and
// the key pairs whose private key AMT holds (AMT_PublicPrivateKeyPair), import through
// AMT_PublicKeyManagementService and delete. One Click Recovery (#21) reuses
// AddTrustedRootCertificate and DeleteCertificate for its temporary root certificate.

// KeyPair is one entry of AMT_PublicPrivateKeyPair. AMT never hands out the private key; the
// entry only shows its public half.
type KeyPair struct {
	ID          string // InstanceID
	Algorithm   string // "RSA" or "ECDSA"; "" if the public key cannot be read
	Bits        int    // key size; 0 if unknown
	Fingerprint string // SHA-256 of the public key as AMT reports it (DERKey)
	Certificate string // ID of the certificate with this public key; "" for none
}

// CertificateStore is the certificate store of the device.
type CertificateStore struct {
	Certificates []Certificate
	Keys         []KeyPair
}

// ErrCertificateStoreFull: the device has no room for another certificate or key
// (PT_STATUS_MAX_LIMIT_REACHED). The chain also holds the *MethodError of the call.
var ErrCertificateStoreFull = errors.New("the certificate store of the device is full")

// ErrCertificateInUse: the device refused to delete a certificate (or the key of one) that it
// uses for TLS. Another TLS certificate has to be chosen first.
var ErrCertificateInUse = errors.New("the device uses the certificate for TLS")

// returnValueMaxLimit is PT_STATUS_MAX_LIMIT_REACHED.
const returnValueMaxLimit = 23

// returnValueDuplicate is PT_STATUS_DUPLICATE: the certificate or key is already in the store.
const returnValueDuplicate = 2058

// CertificateStore reads the certificates, the key pairs and the TLS binding. Nothing is written.
func (d *Device) CertificateStore() (CertificateStore, error) {
	const op = "read certificates"
	certs, err := pullObjects(classPublicKeyCert, false, func() (*client.Message, string, error) {
		r, err := d.msgs.AMT.PublicKeyCertificate.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.AMT.PublicKeyCertificate.Pull(ctx)
		return r.Message, err
	})
	if err != nil {
		return CertificateStore{}, writeError(op, err)
	}
	keys, err := pullObjects(classKeyPair, false, func() (*client.Message, string, error) {
		r, err := d.msgs.AMT.PublicPrivateKeyPair.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.AMT.PublicPrivateKeyPair.Pull(ctx)
		return r.Message, err
	})
	if err != nil {
		return CertificateStore{}, writeError(op, err)
	}
	bound, _, err := d.tlsBinding()
	if err != nil {
		return CertificateStore{}, writeError(op, err)
	}
	return storeFrom(certs, keys, bound), nil
}

// storeFrom decodes the pulled certificates and keys; bound is the ID of the TLS certificate.
func storeFrom(certs, keys []*cimObject, bound string) CertificateStore {
	var s CertificateStore
	parsed := make([]*x509.Certificate, 0, len(certs))
	for _, o := range certs {
		c := Certificate{
			ID:          o.str("InstanceID"),
			Subject:     o.str("Subject"),
			Issuer:      o.str("Issuer"),
			TrustedRoot: o.boolean("TrustedRootCertficate"), // sic, the AMT property name
			ReadOnly:    o.boolean("ReadOnlyCertificate"),
		}
		c.TLS = c.ID != "" && c.ID == bound
		var x *x509.Certificate
		if der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(o.str("X509Certificate"))); err == nil && len(der) > 0 {
			c.DER = der
			c.Fingerprint = fingerprintOf(der)
			if p, err := x509.ParseCertificate(der); err == nil {
				x = p
				c.NotBefore, c.NotAfter = x.NotBefore.UTC(), x.NotAfter.UTC()
			}
		}
		parsed = append(parsed, x)
		s.Certificates = append(s.Certificates, c)
	}
	for _, o := range keys {
		k := KeyPair{ID: o.str("InstanceID")}
		derKey := o.str("DERKey")
		if der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(derKey)); err == nil && len(der) > 0 {
			sum := sha256.Sum256(der)
			k.Fingerprint = hex.EncodeToString(sum[:])
			k.Algorithm, k.Bits = publicKeyInfo(der)
		}
		for i, x := range parsed {
			if x != nil && keyMatches(x, derKey) {
				s.Certificates[i].HasPrivateKey = true
				if k.Certificate == "" {
					k.Certificate = s.Certificates[i].ID
				}
			}
		}
		s.Keys = append(s.Keys, k)
	}
	return s
}

// publicKeyInfo names the algorithm and size of a DERKey: PKCS#1 RSAPublicKey (older AMT) or
// SubjectPublicKeyInfo (newer AMT).
func publicKeyInfo(der []byte) (string, int) {
	if k, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return "RSA", k.N.BitLen()
	}
	switch k, _ := x509.ParsePKIXPublicKey(der); k := k.(type) {
	case *rsa.PublicKey:
		return "RSA", k.N.BitLen()
	case *ecdsa.PublicKey:
		return "ECDSA", k.Curve.Params().BitSize
	}
	return "", 0
}

// methodResult reads the ReturnValue and the InstanceID of the created object from the raw
// answer to method of AMT_PublicKeyManagementService. The library checks some return values
// too, but its error carries only text and AddKey is not checked at all. A SOAP fault (err) is
// the device's answer and returned as such.
func methodResult(method, xmlOutput string, err error) (string, error) {
	var fault *amterror.AMTError
	if errors.As(err, &fault) {
		return "", err
	}
	type selector struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:",chardata"`
	}
	var env struct {
		Body struct {
			Outputs []struct {
				XMLName xml.Name
				RV      *int `xml:"ReturnValue"`
				Refs    []struct {
					Selectors []selector `xml:"ReferenceParameters>SelectorSet>Selector"`
				} `xml:",any"`
			} `xml:",any"`
		}
	}
	if xml.Unmarshal([]byte(xmlOutput), &env) == nil {
		for _, o := range env.Body.Outputs {
			if o.XMLName.Local != method+"_OUTPUT" || o.RV == nil {
				continue
			}
			switch *o.RV {
			case 0:
			case returnValueMaxLimit:
				return "", fmt.Errorf("%w: %w", ErrCertificateStoreFull, &MethodError{Method: method, ReturnValue: *o.RV})
			default:
				return "", &MethodError{Method: method, ReturnValue: *o.RV}
			}
			for _, r := range o.Refs {
				for _, s := range r.Selectors {
					if s.Name == "InstanceID" {
						return strings.TrimSpace(s.Value), nil
					}
				}
			}
			return "", nil
		}
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("%w: %s_OUTPUT", ErrIncompleteResponse, method)
}

// parseDER checks that der is one X.509 certificate.
func parseDER(der []byte) (*x509.Certificate, error) {
	x, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, invalid("Certificate", "an X.509 certificate in DER form")
	}
	return x, nil
}

// AddTrustedRootCertificate adds der (one X.509 certificate, DER) to the store as trusted root
// and returns its InstanceID. AMT uses trusted roots to check the servers and clients it talks
// to (HTTPS boot of One Click Recovery among them). No confirmation: adding trusts more, it
// locks nothing out.
func (d *Device) AddTrustedRootCertificate(der []byte) (string, error) {
	const op = "add trusted root certificate"
	if _, err := parseDER(der); err != nil {
		return "", err
	}
	r, err := d.msgs.AMT.PublicKeyManagementService.AddTrustedRootCertificate(base64.StdEncoding.EncodeToString(der))
	id, err := methodResult("AddTrustedRootCertificate", xmlOf(r.Message), err)
	return id, writeError(op, err)
}

// AddCertificateWithKey adds the private key (AddKey, PKCS#1) and then the certificate der
// (AddCertificate) and returns the certificate's InstanceID. The certificate must carry the
// public half of key. AMT takes RSA keys only. A key that is already on the device
// (PT_STATUS_DUPLICATE) is used as it is; a key this call added is removed again when the
// certificate is refused. The key goes only to the device: it is neither stored nor part of an
// error. Send it over TLS (the UI asks first without TLS).
func (d *Device) AddCertificateWithKey(der []byte, key *rsa.PrivateKey) (string, error) {
	const op = "add certificate with key"
	x, err := parseDER(der)
	if err != nil {
		return "", err
	}
	pub, ok := x.PublicKey.(*rsa.PublicKey)
	if key == nil || !ok || !pub.Equal(&key.PublicKey) {
		return "", invalid("Key", "the RSA private key of the certificate")
	}
	blob := base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key))
	r, err := d.msgs.AMT.PublicKeyManagementService.AddKey(blob)
	keyID, err := methodResult("AddKey", xmlOf(r.Message), err)
	var me *MethodError
	if errors.As(err, &me) && me.ReturnValue == returnValueDuplicate && !errors.Is(err, ErrCertificateStoreFull) {
		keyID, err = "", nil // the key is on the device already: only the certificate is missing
	}
	if err != nil {
		return "", writeError(op, err)
	}
	rc, err := d.msgs.AMT.PublicKeyManagementService.AddCertificate(base64.StdEncoding.EncodeToString(der))
	id, err := methodResult("AddCertificate", xmlOf(rc.Message), err)
	if err != nil {
		if keyID != "" {
			_, _ = d.msgs.AMT.PublicPrivateKeyPair.Delete(keyID) // best effort; a leftover key shows in the store
		}
		return "", writeError(op, err)
	}
	return id, nil
}

// certificateOps are the operations that confirm deleting a certificate: OpCertDelete for an
// ordinary one; for a trusted root OpRootCertDelete, or OpBootFile, whose confirmation covers
// the temporary root of One Click Recovery from adding to removing it.
func certificateOps(c Certificate) []Operation {
	if c.TrustedRoot {
		return []Operation{OpRootCertDelete, OpBootFile}
	}
	return []Operation{OpCertDelete}
}

// DeleteCertificate removes certificate id from the store. It needs a confirmation for
// OpCertDelete, for a trusted root for OpRootCertDelete (or OpBootFile, see certificateOps).
// OpenLAMT does not forbid deleting the TLS certificate: AMT refuses it, and the error then
// wraps ErrCertificateInUse.
func (d *Device) DeleteCertificate(id string, confirm Confirmation) error {
	const op = "delete certificate"
	s, err := d.CertificateStore()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(s.Certificates, func(c Certificate) bool { return c.ID == id })
	if i < 0 {
		return invalid("Certificate", "not in the certificate store")
	}
	c := s.Certificates[i]
	if err := d.confirmedAny(confirm, certificateOps(c), c.ID); err != nil {
		return err
	}
	_, err = d.msgs.AMT.PublicKeyCertificate.Delete(id)
	return deleteError(op, err, c.TLS)
}

// DeleteKeyPair removes key pair id; it needs a confirmation for OpCertDelete. AMT refuses to
// delete the key of a certificate in use (the error then wraps ErrCertificateInUse for the TLS
// certificate).
func (d *Device) DeleteKeyPair(id string, confirm Confirmation) error {
	const op = "delete key pair"
	s, err := d.CertificateStore()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(s.Keys, func(k KeyPair) bool { return k.ID == id })
	if i < 0 {
		return invalid("Key", "not in the certificate store")
	}
	k := s.Keys[i]
	if err := d.confirmedAny(confirm, []Operation{OpCertDelete}, k.ID); err != nil {
		return err
	}
	inUse := k.Certificate != "" && slices.ContainsFunc(s.Certificates, func(c Certificate) bool { return c.ID == k.Certificate && c.TLS })
	_, err = d.msgs.AMT.PublicPrivateKeyPair.Delete(id)
	return deleteError(op, err, inUse)
}

// deleteError classifies the answer to a Delete: a SOAP fault for a certificate AMT uses for
// TLS (tls) wraps ErrCertificateInUse.
func deleteError(op string, err error, tls bool) error {
	if err == nil {
		return nil
	}
	var fault *amterror.AMTError
	if tls && errors.As(err, &fault) && !IsAccessDenied(err) {
		return fmt.Errorf("%s: %w: %w", op, ErrCertificateInUse, err)
	}
	return writeError(op, err)
}

// confirmedAny returns nil if c confirms one of ops on this device, else *RiskyChangeError for
// the first of them.
func (d *Device) confirmedAny(c Confirmation, ops []Operation, fields ...string) error {
	for _, op := range ops {
		if c.Allows(op, d.cfg.Host) {
			return nil
		}
	}
	return &RiskyChangeError{Op: ops[0], Fields: fields}
}

// CertificateConfirmations lists the operations that confirm deleting c (fakes use it for the
// same check as *Device).
func CertificateConfirmations(c Certificate) []Operation { return slices.Clone(certificateOps(c)) }
