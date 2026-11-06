package amt

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrTrustDeclined is returned when the user rejects an unknown certificate.
var ErrTrustDeclined = errors.New("certificate fingerprint not trusted")

// TrustPrompt asks the user whether to pin a certificate on first contact.
type TrustPrompt interface {
	// ConfirmFingerprint is called once on first TLS contact; fp is normalized.
	ConfirmFingerprint(host, fp string) (bool, error)
}

type dialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// FingerprintMismatchError reports a certificate that differs from the pinned one.
type FingerprintMismatchError struct{ Host, Expected, Actual string }

func (e *FingerprintMismatchError) Error() string {
	return fmt.Sprintf("certificate of %s does not match pinned fingerprint: expected %s, got %s",
		e.Host, FormatFingerprint(e.Expected), FormatFingerprint(e.Actual))
}

// NormalizeFingerprint accepts "ab:cd:…", "ABCD…" or "abcd…" and returns 64 lowercase hex chars.
func NormalizeFingerprint(s string) (string, error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return "", errors.New("invalid SHA-256 fingerprint: expected 64 hex characters")
	}
	return s, nil
}

// FormatFingerprint renders a normalized fingerprint as "AB:CD:…" for humans.
func FormatFingerprint(fp string) string {
	fp = strings.ToUpper(fp)
	parts := make([]string, 0, len(fp)/2+1)
	for i := 0; i < len(fp); i += 2 {
		parts = append(parts, fp[i:min(i+2, len(fp))])
	}
	return strings.Join(parts, ":")
}

func fingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// pinVerifier is used as tls.Config.VerifyConnection (runs on every connection, also resumed ones).
func pinVerifier(host, expected string) func(cs tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("no peer certificate presented")
		}
		actual := fingerprintOf(cs.PeerCertificates[0].Raw)
		if actual != expected {
			return &FingerprintMismatchError{Host: host, Expected: expected, Actual: actual}
		}
		return nil
	}
}

// fetchFingerprint honours ctx for dial and handshake; callers pass a deadline.
func fetchFingerprint(ctx context.Context, addr string, dial dialFunc) (string, error) {
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	// Certificate is only read, never trusted; the caller pins it after confirmation.
	tc := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if err := tc.HandshakeContext(ctx); err != nil {
		return "", err
	}
	certs := tc.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("no peer certificate presented")
	}
	return fingerprintOf(certs[0].Raw), nil
}
