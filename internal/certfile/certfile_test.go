package certfile

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

func makeCert(t *testing.T, key any, pub any) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "lab.example.com"},
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestCertificateReadsPEMAndDER(t *testing.T) {
	k := rsaKey(t)
	c := makeCert(t, k, &k.PublicKey)
	keyBlock := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	for name, data := range map[string][]byte{
		"PEM":                     PEM(c.Raw),
		"PEM after another block": append(keyBlock, PEM(c.Raw)...),
		"DER":                     c.Raw,
		"DER with a newline":      append(bytes.Clone(c.Raw), '\n'),
	} {
		der, err := Certificate(data)
		if err != nil || !bytes.Equal(der, c.Raw) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A DER certificate may end with a byte that counts as white space; the import must not cut
// it (the signature is random, so about one certificate in 40 does).
func TestCertificateKeepsATrailingSpaceByteOfDER(t *testing.T) {
	for range 5000 {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		c := makeCert(t, k, &k.PublicKey)
		if !bytes.ContainsAny(c.Raw[len(c.Raw)-1:], " \t\n\v\f\r") {
			continue
		}
		for _, data := range [][]byte{c.Raw, append(bytes.Clone(c.Raw), "\r\n"...), append([]byte(" \n"), c.Raw...)} {
			der, err := Certificate(data)
			if err != nil || !bytes.Equal(der, c.Raw) {
				t.Fatalf("DER ending in %#x (%d bytes given): %v", c.Raw[len(c.Raw)-1], len(data), err)
			}
		}
		return
	}
	t.Fatal("no certificate with a trailing white space byte generated")
}

func TestCertificateRejectsOtherFiles(t *testing.T) {
	k := rsaKey(t)
	for name, data := range map[string][]byte{
		"text":         []byte("hello"),
		"empty":        nil,
		"key only":     pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}),
		"broken block": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}),
		"too large":    make([]byte, MaxSize+1),
	} {
		if _, err := Certificate(data); !errors.Is(err, ErrNotCertificate) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPKCS12(t *testing.T) {
	k := rsaKey(t)
	c := makeCert(t, k, &k.PublicKey)
	for name, enc := range map[string]*pkcs12.Encoder{"AES (modern)": pkcs12.Modern, "RC2 (legacy)": pkcs12.LegacyRC2} {
		t.Run(name, func(t *testing.T) {
			pfx, err := enc.Encode(k, c, nil, "Secret-1")
			if err != nil {
				t.Fatal(err)
			}
			der, key, err := PKCS12(pfx, "Secret-1")
			if err != nil || !bytes.Equal(der, c.Raw) || !key.Equal(k) {
				t.Fatalf("decode: %v", err)
			}
			if _, _, err := PKCS12(pfx, "wrong"); !errors.Is(err, ErrWrongPassword) {
				t.Fatalf("wrong password: %v", err)
			}
		})
	}
	if _, _, err := PKCS12([]byte("not a pfx"), ""); !errors.Is(err, ErrNotCertificate) {
		t.Fatalf("garbage: %v", err)
	}
	if _, _, err := PKCS12(PEM(c.Raw), ""); !errors.Is(err, ErrNotCertificate) {
		t.Fatalf("PEM given as .p12: %v", err)
	}
}

func TestPKCS12PicksTheCertificateOfTheKey(t *testing.T) {
	leafKey, caKey := rsaKey(t), rsaKey(t)
	leaf := makeCert(t, leafKey, &leafKey.PublicKey)
	ca := makeCert(t, caKey, &caKey.PublicKey)
	// The encoder writes its cert argument first: passing the CA there puts the CA before the leaf.
	pfx, err := pkcs12.Modern.Encode(leafKey, ca, []*x509.Certificate{leaf}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	der, key, err := PKCS12(pfx, "pw")
	if err != nil || !bytes.Equal(der, leaf.Raw) || !key.Equal(leafKey) {
		t.Fatalf("CA first: %v", err)
	}
	other := rsaKey(t)
	pfx, err = pkcs12.Modern.Encode(other, ca, []*x509.Certificate{leaf}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PKCS12(pfx, "pw"); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("no matching certificate: %v", err)
	}
}

func TestPKCS12NeedsAnRSAKey(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := makeCert(t, k, &k.PublicKey)
	pfx, err := pkcs12.Modern.Encode(k, c, nil, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PKCS12(pfx, "pw"); !errors.Is(err, ErrUnsupportedKey) {
		t.Fatalf("got %v", err)
	}
}

func TestPEMRoundTrip(t *testing.T) {
	k := rsaKey(t)
	c := makeCert(t, k, &k.PublicKey)
	b, _ := pem.Decode(PEM(c.Raw))
	if b == nil || b.Type != "CERTIFICATE" || !bytes.Equal(b.Bytes, c.Raw) {
		t.Fatalf("block %+v", b)
	}
}
