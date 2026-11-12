package amt

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// Certificate is one entry of the AMT certificate store (AMT_PublicKeyCertificate).
type Certificate struct {
	ID            string    // InstanceID; TLSChange.Certificate takes it
	Subject       string    // as AMT reports it, e.g. "C=…,O=…,CN=…"
	Issuer        string    // as AMT reports it
	NotBefore     time.Time // UTC; zero if the certificate cannot be parsed
	NotAfter      time.Time
	Fingerprint   string // SHA-256 of the DER certificate: the pin OpenLAMT stores when AMT presents it
	TrustedRoot   bool   // a CA for client certificates (mutual authentication), not a TLS server certificate
	ReadOnly      bool   // AMT does not allow deleting it
	HasPrivateKey bool   // AMT holds the matching key pair: usable as TLS server certificate
	TLS           bool   // the certificate AMT presents now
}

// Usable reports whether SetTLS accepts the certificate as server certificate.
func (c Certificate) Usable() bool { return c.HasPrivateKey && !c.TrustedRoot }

const (
	classPublicKeyCert  = "AMT_PublicKeyCertificate"
	classKeyPair        = "AMT_PublicPrivateKeyPair"
	classTLSCredContext = "AMT_TLSCredentialContext"
)

// tlsSettleDelay is the pause after CommitChanges: AMT restarts its TLS listener (MeshCommander
// waits 2 s before the next request). tlsProbeWindow bounds the test handshakes after a commit
// without answer. Replaced in tests.
var (
	tlsSettleDelay = 2 * time.Second
	tlsProbeWindow = 15 * time.Second
)

// ErrCertificateUncertain: CommitChanges of a certificate change got no answer, and within
// tlsProbeWindow no handshake presented the new certificate. The concrete error is
// *CertificateUncertainError; the connection keeps the old pin.
var ErrCertificateUncertain = errors.New("unknown whether the device switched its TLS certificate")

// CertificateUncertainError carries the fingerprint the device presents if the change was
// applied, so the UI can compare it instead of asking for blind trust on the next connection.
type CertificateUncertainError struct {
	Fingerprint string
	Err         error
}

func (e *CertificateUncertainError) Error() string {
	return fmt.Sprintf("%v (expected fingerprint %s): %v", ErrCertificateUncertain, FormatFingerprint(e.Fingerprint), e.Err)
}

func (e *CertificateUncertainError) Is(target error) bool { return target == ErrCertificateUncertain }
func (e *CertificateUncertainError) Unwrap() error        { return e.Err }

// keyMatches reports whether a key pair's DERKey (base64; PKCS#1 RSAPublicKey on older AMT,
// SubjectPublicKeyInfo on newer ones) is the public key of cert.
func keyMatches(cert *x509.Certificate, derKey string) bool {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(derKey))
	if err != nil || len(der) == 0 {
		return false
	}
	if bytes.Equal(der, cert.RawSubjectPublicKeyInfo) {
		return true
	}
	if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
		return bytes.Equal(der, x509.MarshalPKCS1PublicKey(pub))
	}
	return false
}

// tlsCertificateID returns the InstanceID of the certificate the TLS credential context binds,
// "" if there is no context. exists reports whether a context instance exists.
func tlsCertificateID(xmlOutput string) (id string, exists bool, err error) {
	var out struct {
		Items []struct {
			Selectors []struct {
				Name  string `xml:"Name,attr"`
				Value string `xml:",chardata"`
			} `xml:"ElementInContext>ReferenceParameters>SelectorSet>Selector"`
		} `xml:"Body>PullResponse>Items>AMT_TLSCredentialContext"`
	}
	if err := xml.Unmarshal([]byte(xmlOutput), &out); err != nil {
		return "", false, fmt.Errorf("%w: unreadable %s response: %v", ErrIncompleteResponse, classTLSCredContext, err)
	}
	for _, it := range out.Items {
		for _, s := range it.Selectors {
			if s.Name == "InstanceID" {
				return strings.TrimSpace(s.Value), true, nil
			}
		}
		return "", true, nil
	}
	return "", false, nil
}

// tlsBinding reads the TLS credential context: the bound certificate and whether it exists.
func (d *Device) tlsBinding() (string, bool, error) {
	var id string
	var exists bool
	_, err := enumeratePull(func() (string, error) {
		r, err := d.msgs.AMT.TLSCredentialContext.Enumerate()
		return r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) ([]struct{}, string, error) {
		r, err := d.msgs.AMT.TLSCredentialContext.Pull(ctx)
		if err != nil {
			return nil, "", err
		}
		i, ok, err := tlsCertificateID(xmlOf(r.Message))
		if ok && !exists {
			id, exists = i, true
		}
		return nil, xmlOf(r.Message), err
	})
	return id, exists, err
}

// TLSCertificates reads the AMT certificate store, marks the certificates whose key pair AMT
// holds and the one it presents for TLS. Nothing is written.
func (d *Device) TLSCertificates() ([]Certificate, error) {
	const op = "read certificates"
	certs, err := pullObjects(classPublicKeyCert, false, func() (*client.Message, string, error) {
		r, err := d.msgs.AMT.PublicKeyCertificate.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.AMT.PublicKeyCertificate.Pull(ctx)
		return r.Message, err
	})
	if err != nil {
		return nil, writeError(op, err)
	}
	keys, err := pullObjects(classKeyPair, false, func() (*client.Message, string, error) {
		r, err := d.msgs.AMT.PublicPrivateKeyPair.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.AMT.PublicPrivateKeyPair.Pull(ctx)
		return r.Message, err
	})
	if err != nil {
		return nil, writeError(op, err)
	}
	bound, _, err := d.tlsBinding()
	if err != nil {
		return nil, writeError(op, err)
	}
	out := make([]Certificate, 0, len(certs))
	for _, o := range certs {
		c := Certificate{
			ID:          o.str("InstanceID"),
			Subject:     o.str("Subject"),
			Issuer:      o.str("Issuer"),
			TrustedRoot: o.boolean("TrustedRootCertficate"), // sic, the AMT property name
			ReadOnly:    o.boolean("ReadOnlyCertificate"),
		}
		c.TLS = c.ID != "" && c.ID == bound
		if der, err := base64.StdEncoding.DecodeString(o.str("X509Certificate")); err == nil && len(der) > 0 {
			c.Fingerprint = fingerprintOf(der)
			if x, err := x509.ParseCertificate(der); err == nil {
				c.NotBefore, c.NotAfter = x.NotBefore.UTC(), x.NotAfter.UTC()
				for _, k := range keys {
					if keyMatches(x, k.str("DERKey")) {
						c.HasPrivateKey = true
						break
					}
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// Risk names of TLS changes in RiskyChangeError.Fields.
const (
	RiskTLSCertificate     = "TLS.Certificate"
	RiskTLSMutualAuth      = "TLS.MutualAuth"
	RiskTLSAcceptNonSecure = "TLS.AcceptNonSecure"
	RiskTLSTrustedCNs      = "TLS.TrustedCNs"
	RiskTLSLocalEnabled    = "TLS.LocalEnabled"
)

// TLSChange changes the TLS configuration; nil fields stay as they are. Every effective change is
// risky (OpTLS). TLS on the network interface stays on: switching it, and thereby the port
// OpenLAMT connects to, is not offered.
type TLSChange struct {
	Certificate     *string   // ID of a Usable certificate of TLSCertificates
	MutualAuth      *bool     // network interface: clients must present a certificate; OpenLAMT has none and is locked out
	AcceptNonSecure *bool     // network interface: also accept plain connections; only where NonSecureSupported
	TrustedCNs      *[]string // network interface: allowed client certificate names; 1–4 with MutualAuth
	LocalEnabled    *bool     // TLS on the local interface (LMS)
}

// Validate checks the values without device access.
func (c TLSChange) Validate() error {
	if c.Certificate != nil && strings.TrimSpace(*c.Certificate) == "" {
		return invalid("Certificate", "a certificate ID")
	}
	if c.TrustedCNs != nil {
		if len(*c.TrustedCNs) > 4 {
			return invalid("TrustedCNs", "at most 4 names")
		}
		for _, n := range *c.TrustedCNs {
			if n == "" || len(n) > 255 || strings.ContainsAny(n, " \t\r\n,<>&\"") {
				return invalid("TrustedCNs", "names without spaces, commas or < > & \"")
			}
		}
	}
	return nil
}

// Apply returns the settings the change leads to (the certificate is not part of TLSSettings).
func (c TLSChange) Apply(cur TLSSettings) TLSSettings {
	n := cur
	n.Remote.TrustedCNs = slices.Clone(cur.Remote.TrustedCNs)
	if c.MutualAuth != nil {
		n.Remote.MutualAuth = *c.MutualAuth
	}
	if c.AcceptNonSecure != nil {
		n.Remote.AcceptNonSecure = *c.AcceptNonSecure
	}
	if c.TrustedCNs != nil {
		n.Remote.TrustedCNs = slices.Clone(*c.TrustedCNs)
	}
	if c.LocalEnabled != nil {
		n.Local.Enabled = *c.LocalEnabled
	}
	return n
}

// Check validates the change against the current settings and certificate store and returns
// the risky fields it alters (nil: nothing to write). *Device.SetTLS and fakes use it.
func (c TLSChange) Check(cur TLSSettings, certs []Certificate) ([]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !cur.Remote.Available || !cur.Remote.Enabled {
		return nil, fmt.Errorf("TLS on the network interface is off: %w", ErrNotModifiable)
	}
	n := c.Apply(cur)
	var fields []string
	if c.Certificate != nil {
		i := slices.IndexFunc(certs, func(x Certificate) bool { return x.ID == *c.Certificate })
		switch {
		case i < 0:
			return nil, invalid("Certificate", "not in the certificate store")
		case !certs[i].Usable():
			return nil, invalid("Certificate", "needs a private key on the device and must not be a trusted root")
		case !certs[i].TLS:
			fields = append(fields, RiskTLSCertificate)
		}
	}
	if n.Remote.MutualAuth != cur.Remote.MutualAuth {
		fields = append(fields, RiskTLSMutualAuth)
	}
	if n.Remote.AcceptNonSecure != cur.Remote.AcceptNonSecure {
		if n.Remote.AcceptNonSecure && !cur.Remote.NonSecureSupported {
			return nil, fmt.Errorf("plain connections: %w", ErrNotModifiable)
		}
		fields = append(fields, RiskTLSAcceptNonSecure)
	}
	if !slices.Equal(n.Remote.TrustedCNs, cur.Remote.TrustedCNs) && (len(n.Remote.TrustedCNs) > 0 || len(cur.Remote.TrustedCNs) > 0) {
		fields = append(fields, RiskTLSTrustedCNs)
	}
	if n.Local.Enabled != cur.Local.Enabled {
		if !cur.Local.Available {
			return nil, fmt.Errorf("local interface: %w", ErrNotModifiable)
		}
		fields = append(fields, RiskTLSLocalEnabled)
	}
	if n.Remote.MutualAuth {
		if len(n.Remote.TrustedCNs) == 0 {
			return nil, invalid("TrustedCNs", "1 to 4 names are required with mutual authentication")
		}
		if !slices.ContainsFunc(certs, func(x Certificate) bool { return x.TrustedRoot }) {
			return nil, invalid("MutualAuth", "needs a trusted root certificate in the AMT store")
		}
	}
	return fields, nil
}

// commitOutput checks CommitChanges_OUTPUT/ReturnValue (the library reads another method's
// output for it).
func commitOutput(xmlOutput string) error {
	var out struct {
		RV *int `xml:"Body>CommitChanges_OUTPUT>ReturnValue"`
	}
	if xml.Unmarshal([]byte(xmlOutput), &out) != nil || out.RV == nil {
		return fmt.Errorf("%w: CommitChanges_OUTPUT", ErrIncompleteResponse)
	}
	if *out.RV != 0 {
		return &MethodError{Method: "CommitChanges", ReturnValue: *out.RV}
	}
	return nil
}

// SetTLS changes the TLS certificate and settings in MeshCommander's order: bind the certificate
// (AMT_TLSCredentialContext Put, or Create without binding), set the clock before mutual
// authentication, Put the changed AMT_TLSSettingData instances, CommitChanges, then wait for AMT
// to restart TLS. It needs confirm for OpTLS. The steps before CommitChanges are staged in AMT;
// after an error in between they stay staged until the next commit or AMT restart, so the caller
// reads the settings again.
//
// A new certificate changes the pin of this connection: the device switches to the fingerprint
// of the chosen certificate, which it read through the pinned connection (Fingerprint returns it;
// the session stores it). The same holds for a binding an earlier failed call left staged, which
// this commit activates. If the answer to CommitChanges is lost, a fresh TLS handshake that
// presents exactly that certificate within tlsProbeWindow counts as applied; otherwise
// *CertificateUncertainError names the fingerprint to expect.
func (d *Device) SetTLS(c TLSChange, confirm Confirmation) error {
	const op = "set TLS"
	if err := c.Validate(); err != nil {
		return err
	}
	objs, err := d.tlsObjects()
	if err != nil {
		return writeError(op, err)
	}
	cur := tlsFrom(objs)
	certs, err := d.TLSCertificates()
	if err != nil {
		return err
	}
	fields, err := c.Check(cur, certs)
	if err != nil || fields == nil {
		return err
	}
	if err := d.confirmed(confirm, OpTLS, fields...); err != nil {
		return err
	}
	n := c.Apply(cur)

	if n.Remote.MutualAuth && !cur.Remote.MutualAuth {
		if err := d.SyncClock(); err != nil { // mutual authentication checks certificate times
			return err
		}
	}
	// the certificate AMT presents after the commit: the chosen one, else the bound one (an
	// earlier call may have left a binding staged that this commit activates)
	newPin := ""
	if i := slices.IndexFunc(certs, func(x Certificate) bool { return x.TLS }); i >= 0 {
		newPin = certs[i].Fingerprint
	}
	if slices.Contains(fields, RiskTLSCertificate) {
		i := slices.IndexFunc(certs, func(x Certificate) bool { return x.ID == *c.Certificate })
		newPin = certs[i].Fingerprint
		if err := d.bindTLSCertificate(*c.Certificate); err != nil {
			return writeError(op, err)
		}
	}
	if !d.cfg.UseTLS || newPin == d.fingerprint {
		newPin = ""
	}
	for _, o := range objs {
		local := strings.Contains(o.str("InstanceID"), "LMS")
		t := changeTracker{o: o}
		if local {
			t.boolean("Enabled", &n.Local.Enabled)
		} else {
			t.boolean("MutualAuthentication", &n.Remote.MutualAuth)
			t.boolean("AcceptNonSecureConnections", &n.Remote.AcceptNonSecure)
			if !slices.Equal(o.strs("TrustedCN"), n.Remote.TrustedCNs) {
				o.setAll("TrustedCN", n.Remote.TrustedCNs)
				t.changed = true
			}
		}
		if t.changed {
			if err := d.putObject(o, []selector{{"InstanceID", o.str("InstanceID")}}); err != nil {
				return writeError(op, err)
			}
		}
	}
	r, err := d.msgs.AMT.SetupAndConfigurationService.CommitChanges()
	if err == nil {
		err = commitOutput(xmlOf(r.Message))
		var me *MethodError
		if errors.As(err, &me) {
			return err
		}
	}
	if err != nil && !d.presents(newPin) {
		if newPin != "" {
			return &CertificateUncertainError{Fingerprint: newPin, Err: writeError(op, err)}
		}
		return writeError(op, err)
	}
	if newPin != "" {
		cfg := d.cfg
		cfg.Fingerprint = newPin
		d.relogin(cfg, newPin)
	}
	time.Sleep(tlsSettleDelay)
	return nil
}

// bindTLSCertificate points the TLS credential context at certificate id (Create if AMT has none).
func (d *Device) bindTLSCertificate(id string) error {
	_, exists, err := d.tlsBinding()
	if err != nil {
		return err
	}
	if exists {
		_, err = d.msgs.AMT.TLSCredentialContext.Put(id)
	} else {
		_, err = d.msgs.AMT.TLSCredentialContext.Create(id)
	}
	return err
}

// presents reports whether a fresh TLS handshake shows the certificate with fingerprint pin
// ("" never matches). It settles a CommitChanges without answer after a certificate change.
func (d *Device) presents(pin string) bool {
	if pin == "" || !d.cfg.UseTLS || d.transport == nil { // no transport: a test client, no network
		return false
	}
	deadline := time.Now().Add(tlsProbeWindow)
	for {
		time.Sleep(tlsSettleDelay)
		ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
		fp, err := fetchFingerprint(ctx, d.cfg.Address(), d.cfg.dialContext)
		cancel()
		if err == nil && fp == pin {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
}
