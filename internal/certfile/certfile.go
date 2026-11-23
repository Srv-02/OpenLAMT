// Package certfile reads the certificate files OpenLAMT imports into Intel AMT (issue #16): a
// certificate as PEM or DER (.pem, .cer, .crt) and a certificate with its private key as
// PKCS#12 (.p12, .pfx). It also writes a certificate as PEM. Nothing here touches the disk: the
// caller reads and writes the files, and a private key lives only in memory.
package certfile

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"

	"software.sslmate.com/src/go-pkcs12"
)

// ErrNotCertificate: the data is no certificate (or PKCS#12 file) OpenLAMT can read.
var ErrNotCertificate = errors.New("the file is not a certificate")

// ErrWrongPassword: the password does not open the PKCS#12 file.
var ErrWrongPassword = errors.New("wrong password for the PKCS#12 file")

// ErrUnsupportedKey: the PKCS#12 file holds a key Intel AMT does not take (AMT takes RSA only).
var ErrUnsupportedKey = errors.New("the private key is not an RSA key")

// MaxSize is the largest file the import reads; certificate files are a few KiB.
const MaxSize = 1 << 20

// Certificate returns the first certificate of data (DER): the first CERTIFICATE block of a PEM
// file, or data itself as DER.
func Certificate(data []byte) ([]byte, error) {
	if len(data) > MaxSize {
		return nil, ErrNotCertificate
	}
	rest := data
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type == "CERTIFICATE" {
			if _, err := x509.ParseCertificate(b.Bytes); err != nil {
				return nil, ErrNotCertificate
			}
			return b.Bytes, nil
		}
	}
	// DER as it is first: its last byte can be one TrimSpace would cut (0x20, 0x0A, ...)
	for _, der := range [][]byte{data, bytes.TrimSpace(data)} {
		if _, err := x509.ParseCertificate(der); err == nil {
			return der, nil
		}
	}
	return nil, ErrNotCertificate
}

// PKCS12 opens a .p12 or .pfx file with password and returns its certificate (DER) and RSA
// private key. Further CA certificates in the file are ignored.
func PKCS12(data []byte, password string) ([]byte, *rsa.PrivateKey, error) {
	if len(data) > MaxSize {
		return nil, nil, ErrNotCertificate
	}
	key, cert, _, err := pkcs12.DecodeChain(data, password)
	switch {
	case errors.Is(err, pkcs12.ErrIncorrectPassword):
		return nil, nil, ErrWrongPassword
	case err != nil || cert == nil:
		return nil, nil, ErrNotCertificate
	}
	k, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, ErrUnsupportedKey
	}
	return cert.Raw, k, nil
}

// PEM encodes the certificate der as a PEM CERTIFICATE block.
func PEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
