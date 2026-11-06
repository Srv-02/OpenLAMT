package amt

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// ErrIPv6Unsupported is returned for IPv6 literal hosts (the WS-Man library builds URLs without brackets).
var ErrIPv6Unsupported = errors.New("IPv6 literal hosts are not supported")

// ConnectTimeout bounds TCP connect and TLS handshake, so an unreachable device fails fast
// instead of waiting for the operating system's connect timeout (about 75 s on macOS).
const ConnectTimeout = 10 * time.Second

// IdleConnTimeout closes kept-alive WS-Man sockets that nobody used for this long. It is the
// safety net for sockets that return to the pool after the window closed (a running power action).
const IdleConnTimeout = 90 * time.Second

// Connect builds a Device. With UseTLS and an empty Fingerprint it fetches the server
// fingerprint (ConnectTimeout) and asks prompt once; declining returns ErrTrustDeclined.
// With a configured pin the server certificate is checked right away (same deadline), so a
// changed certificate fails here with *FingerprintMismatchError, before any action is confirmed.
// No WS-Man call is made here.
func Connect(ctx context.Context, cfg Config, prompt TrustPrompt) (*Device, error) {
	if strings.Contains(cfg.Host, ":") {
		return nil, ErrIPv6Unsupported
	}

	dial := cfg.dialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: ConnectTimeout}).DialContext
	}
	transport := &http.Transport{DialContext: dial, TLSHandshakeTimeout: ConnectTimeout,
		IdleConnTimeout: IdleConnTimeout}
	fp := ""
	var tlsCfg *tls.Config
	if cfg.UseTLS {
		var err error
		if fp, err = resolvePin(ctx, cfg, prompt); err != nil {
			return nil, err
		}
		// AMT certificates cannot be checked against system CAs; the pin replaces chain verification.
		tlsCfg = tlsConfigFor(cfg.Host, fp)
		transport.TLSClientConfig = tlsCfg
	}

	c := client.NewWsman(client.Parameters{
		Target:    cfg.Host,
		Username:  cfg.Username,
		Password:  cfg.Password,
		UseDigest: true,
		UseTLS:    cfg.UseTLS,
		Transport: transport,
	})
	dev := newDevice(c, fp)
	dev.cfg = cfg
	dev.tlsCfg = tlsCfg
	dev.transport = transport
	return dev, nil
}

// tlsConfigFor is the pinned TLS config Connect uses for WS-Man and OpenKVM for redirection.
func tlsConfigFor(host, fp string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection:   pinVerifier(host, fp),
	}
}

// resolvePin returns the normalized pin: the configured one, or the one the user confirms (TOFU).
func resolvePin(ctx context.Context, cfg Config, prompt TrustPrompt) (string, error) {
	pinned := strings.TrimSpace(cfg.Fingerprint) != ""
	var pin string
	if pinned {
		var err error
		if pin, err = NormalizeFingerprint(cfg.Fingerprint); err != nil {
			return "", err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()
	fp, err := fetchFingerprint(ctx, cfg.Address(), cfg.dialContext)
	if err != nil {
		return "", err
	}
	if pinned {
		if fp != pin {
			return "", &FingerprintMismatchError{Host: cfg.Host, Expected: pin, Actual: fp}
		}
		return pin, nil
	}
	if prompt == nil {
		return "", ErrTrustDeclined
	}
	ok, err := prompt.ConfirmFingerprint(cfg.Host, fp)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrTrustDeclined
	}
	return fp, nil
}
