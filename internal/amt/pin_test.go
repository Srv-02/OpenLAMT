package amt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeFingerprint(t *testing.T) {
	want := strings.Repeat("ab", 32)
	for _, in := range []string{want, strings.ToUpper(want), FormatFingerprint(want)} {
		got, err := NormalizeFingerprint(in)
		if err != nil || got != want {
			t.Fatalf("%q -> %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "abc", strings.Repeat("zz", 32)} {
		if _, err := NormalizeFingerprint(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestFormatFingerprint(t *testing.T) {
	if got := FormatFingerprint("abcd" + strings.Repeat("00", 30)); !strings.HasPrefix(got, "AB:CD:00:") || len(got) != 95 {
		t.Fatalf("got %q", got)
	}
}

func TestPinVerifier(t *testing.T) {
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	defer ts.Close()
	cert := ts.Certificate()
	fp := fingerprintOf(cert.Raw)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if err := pinVerifier("device.example.com", fp)(cs); err != nil {
		t.Fatal(err)
	}
	err := pinVerifier("device.example.com", strings.Repeat("00", 32))(cs)
	var mm *FingerprintMismatchError
	if !errors.As(err, &mm) || mm.Actual != fp || mm.Host != "device.example.com" {
		t.Fatalf("err = %v", err)
	}
	if err := pinVerifier("device.example.com", fp)(tls.ConnectionState{}); err == nil {
		t.Fatal("empty chain accepted")
	}
}

func TestFetchFingerprint(t *testing.T) {
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	defer ts.Close()
	got, err := fetchFingerprint(context.Background(), ts.Listener.Addr().String(), nil)
	if err != nil || got != fingerprintOf(ts.Certificate().Raw) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestFetchFingerprintTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // accepts, never speaks TLS
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = fetchFingerprint(ctx, ln.Addr().String(), nil)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}
