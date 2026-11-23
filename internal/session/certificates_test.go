package session

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

// selfSigned returns a certificate for cn with a new RSA key.
func selfSigned(t *testing.T, cn string, ca bool) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA: ca, BasicConstraintsValid: ca}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return der, k
}

func certFake() *Fake {
	return &Fake{Cfg: FakeConfig{Certificates: []amt.Certificate{
		{ID: "tls", Subject: "CN=amt.example.com", HasPrivateKey: true, TLS: true},
		{ID: "root", Subject: "CN=ACME Root", TrustedRoot: true},
		{ID: "old", Subject: "CN=old.example.com"},
	}}}
}

func TestCertificatesThroughSession(t *testing.T) {
	dev := certFake()
	dev.Certs().Keys = []amt.KeyPair{{ID: "k-tls", Algorithm: "RSA", Bits: 2048, Certificate: "tls"}, {ID: "k-free", Algorithm: "RSA", Bits: 2048}}
	s := openInfoSession(t, dev)

	st, err := s.CertificateStore()
	if err != nil || len(st.Certificates) != 3 || len(st.Keys) != 2 {
		t.Fatalf("store %+v, %v", st, err)
	}

	root, _ := selfSigned(t, "Lab Root", true)
	id, err := s.AddTrustedRootCertificate(root)
	if err != nil || id == "" {
		t.Fatalf("add root: %q %v", id, err)
	}
	der, key := selfSigned(t, "new.example.com", false)
	cid, err := s.AddCertificateWithKey(der, key)
	if err != nil {
		t.Fatalf("add with key: %v", err)
	}
	st, _ = s.CertificateStore()
	i := slices.IndexFunc(st.Certificates, func(c amt.Certificate) bool { return c.ID == cid })
	if i < 0 || !st.Certificates[i].HasPrivateKey || st.Certificates[i].TrustedRoot || len(st.Keys) != 3 || st.Keys[2].Certificate != cid {
		t.Fatalf("imported certificate %+v keys %+v", st.Certificates, st.Keys)
	}
	if j := slices.IndexFunc(st.Certificates, func(c amt.Certificate) bool { return c.ID == id }); j < 0 || !st.Certificates[j].TrustedRoot {
		t.Fatal("imported root missing")
	}

	if err := s.DeleteCertificate("root", conf(amt.OpCertDelete)); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("root with the simple confirmation: %v", err)
	}
	if err := s.DeleteCertificate("root", conf(amt.OpRootCertDelete)); err != nil {
		t.Fatalf("root: %v", err)
	}
	if err := s.DeleteCertificate("old", conf(amt.OpCertDelete)); err != nil {
		t.Fatalf("old: %v", err)
	}
	if err := s.DeleteCertificate("tls", conf(amt.OpCertDelete)); !errors.Is(err, amt.ErrCertificateInUse) {
		t.Fatalf("TLS certificate: %v", err)
	}
	if err := s.DeleteKeyPair("k-tls", conf(amt.OpCertDelete)); !errors.Is(err, amt.ErrCertificateInUse) {
		t.Fatalf("TLS key: %v", err)
	}
	if err := s.DeleteKeyPair("k-free", amt.Confirmation{}); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("key without confirmation: %v", err)
	}
	if err := s.DeleteKeyPair("k-free", conf(amt.OpCertDelete)); err != nil {
		t.Fatalf("key: %v", err)
	}
	want := []string{"AddTrustedRootCertificate", "AddCertificateWithKey", "DeleteCertificate", "DeleteCertificate", "DeleteKeyPair"}
	if got := dev.ConfigWrites(); !slices.Equal(got, want) {
		t.Fatalf("writes %v", got)
	}
}

func TestCertificateImportErrorsOfTheFake(t *testing.T) {
	dev := certFake()
	s := openInfoSession(t, dev)
	if _, err := s.AddTrustedRootCertificate([]byte("x")); !errors.Is(err, amt.ErrInvalidValue) {
		t.Fatalf("not a certificate: %v", err)
	}
	der, _ := selfSigned(t, "a.example.com", false)
	_, other := selfSigned(t, "b.example.com", false)
	if _, err := s.AddCertificateWithKey(der, other); !errors.Is(err, amt.ErrInvalidValue) {
		t.Fatalf("wrong key: %v", err)
	}
	dev.Certs().Full = true
	if _, err := s.AddTrustedRootCertificate(der); !errors.Is(err, amt.ErrCertificateStoreFull) {
		t.Fatalf("full: %v", err)
	}
	dev.Certs().Full, dev.Certs().ReadErr = false, errors.New("boom")
	if _, err := s.CertificateStore(); err == nil {
		t.Fatal("read error lost")
	}
	if len(dev.ConfigWrites()) != 0 {
		t.Fatalf("writes %v", dev.ConfigWrites())
	}
}

func TestCertificatesUnsupportedDevice(t *testing.T) {
	s := &Session{dev: bare{}}
	if _, err := s.CertificateStore(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	if err := s.DeleteCertificate("x", amt.Confirmation{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}
