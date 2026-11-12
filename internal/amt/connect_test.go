package amt

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

type promptFunc func(host, fp string) (bool, error)

func (p promptFunc) ConfirmFingerprint(host, fp string) (bool, error) { return p(host, fp) }

func noPrompt(t *testing.T) promptFunc {
	return func(host, fp string) (bool, error) {
		t.Helper()
		t.Fatal("prompt must not be called")
		return false, nil
	}
}

func TestConnect_TOFUThenStatus(t *testing.T) {
	ts := newAMTServer(t, true, "admin", "secret-1", statusRoutes())
	calls := 0
	prompt := promptFunc(func(host, fp string) (bool, error) {
		calls++
		if host != "device.example.com" || fp != fingerprintOf(ts.Certificate().Raw) {
			t.Fatalf("prompt %s %s", host, fp)
		}
		return true, nil
	})
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true, dialContext: dialTo(ts)}
	d, err := Connect(context.Background(), cfg, prompt)
	if err != nil || calls != 1 || d.Fingerprint() != fingerprintOf(ts.Certificate().Raw) {
		t.Fatalf("%v calls=%d", err, calls)
	}
	st, err := d.Status()
	if err != nil || st.AMTVersion != "16.1.25" {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestConnect_PinnedSilently(t *testing.T) {
	ts := newAMTServer(t, true, "admin", "secret-1", statusRoutes())
	real := fingerprintOf(ts.Certificate().Raw)
	for _, pin := range []string{real, FormatFingerprint(real), strings.ToUpper(real)} {
		cfg := Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true, Fingerprint: pin, dialContext: dialTo(ts)}
		d, err := Connect(context.Background(), cfg, noPrompt(t))
		if err != nil {
			t.Fatal(err)
		}
		if d.Fingerprint() != real {
			t.Fatalf("fingerprint %q not normalized", d.Fingerprint())
		}
		if _, err := d.Status(); err != nil {
			t.Fatalf("pin %q: %v", pin, err)
		}
	}
}

func TestConnect_FingerprintMismatch(t *testing.T) {
	ts := newAMTServer(t, true, "admin", "secret-1", statusRoutes())
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true,
		Fingerprint: strings.Repeat("00", 32), dialContext: dialTo(ts)}
	d, err := Connect(context.Background(), cfg, noPrompt(t))
	var mm *FingerprintMismatchError
	if !errors.As(err, &mm) || mm.Actual != fingerprintOf(ts.Certificate().Raw) || d != nil {
		t.Fatalf("want mismatch error from Connect, got %v", err)
	}
}

func TestConnect_TrustDeclined(t *testing.T) {
	ts := newAMTServer(t, true, "admin", "secret-1", statusRoutes())
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true, dialContext: dialTo(ts)}
	d, err := Connect(context.Background(), cfg, promptFunc(func(string, string) (bool, error) { return false, nil }))
	if !errors.Is(err, ErrTrustDeclined) || d != nil {
		t.Fatalf("%v %v", d, err)
	}
	d, err = Connect(context.Background(), cfg, nil)
	if !errors.Is(err, ErrTrustDeclined) || d != nil {
		t.Fatalf("nil prompt: %v %v", d, err)
	}
}

func TestConnect_PromptErrorWins(t *testing.T) {
	ts := newAMTServer(t, true, "admin", "secret-1", statusRoutes())
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true, dialContext: dialTo(ts)}
	want := errors.New("dialog closed")
	d, err := Connect(context.Background(), cfg, promptFunc(func(string, string) (bool, error) { return true, want }))
	if !errors.Is(err, want) || d != nil {
		t.Fatalf("%v %v", d, err)
	}
}

func TestConnect_WrongPassword(t *testing.T) {
	ts := newAMTServer(t, true, "admin", "secret-1", statusRoutes())
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "Geheim-42", UseTLS: true,
		Fingerprint: fingerprintOf(ts.Certificate().Raw), dialContext: dialTo(ts)}
	d, err := Connect(context.Background(), cfg, noPrompt(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Status()
	if err == nil || strings.Contains(err.Error(), "Geheim-42") {
		t.Fatalf("want error without password, got %v", err)
	}
}

func TestConnect_PlainHTTP(t *testing.T) {
	ts := newAMTServer(t, false, "admin", "secret-1", statusRoutes())
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: false,
		Fingerprint: strings.Repeat("00", 32), dialContext: dialTo(ts)}
	d, err := Connect(context.Background(), cfg, noPrompt(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Status(); err != nil {
		t.Fatal(err)
	}
	if d.Fingerprint() != "" {
		t.Fatalf("fingerprint %q", d.Fingerprint())
	}
}

func TestConnect_BlankFingerprintMeansUnpinned(t *testing.T) {
	ts := newAMTServer(t, true, "admin", "secret-1", statusRoutes())
	calls := 0
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true, Fingerprint: "  ", dialContext: dialTo(ts)}
	_, err := Connect(context.Background(), cfg, promptFunc(func(string, string) (bool, error) { calls++; return true, nil }))
	if err != nil || calls != 1 {
		t.Fatalf("%v calls=%d", err, calls)
	}
}

func TestConnect_InvalidFingerprint(t *testing.T) {
	cfg := Config{Host: "device.example.com", UseTLS: true, Fingerprint: "xyz"}
	if d, err := Connect(context.Background(), cfg, noPrompt(t)); err == nil || d != nil {
		t.Fatalf("%v %v", d, err)
	}
}

func TestConnect_RejectsIPv6Literal(t *testing.T) {
	dialed := false
	cfg := Config{Host: "2001:db8::10", UseTLS: true,
		dialContext: func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, errors.New("no") }}
	_, err := Connect(context.Background(), cfg, noPrompt(t))
	if !errors.Is(err, ErrIPv6Unsupported) || dialed {
		t.Fatalf("%v dialed=%v", err, dialed)
	}
}

func TestConnect_TransportIdleTimeout(t *testing.T) {
	d, err := Connect(context.Background(), Config{Host: "device.example.com", Username: "admin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.transport.IdleConnTimeout != IdleConnTimeout || IdleConnTimeout <= 0 {
		t.Fatalf("IdleConnTimeout = %v", d.transport.IdleConnTimeout)
	}
}

func limitedRoutes() map[string]string {
	r := statusRoutes()
	r["Enumerate|CIM_ServiceAvailableToElement"] = "400:unreachable_fault.xml"
	r["Enumerate|CIM_SoftwareIdentity"] = "400:unreachable_fault.xml"
	r["Get|AMT_SetupAndConfigurationService"] = "400:access_denied_fault.xml"
	return r
}

func TestConnect_LimitedAccountStatus(t *testing.T) {
	ts := newAMTServer(t, true, "kvm", "secret-1", limitedRoutes())
	cfg := Config{Host: "device.example.com", Username: "kvm", Password: "secret-1", UseTLS: true,
		Fingerprint: fingerprintOf(ts.Certificate().Raw), dialContext: dialTo(ts)}
	d, err := Connect(context.Background(), cfg, noPrompt(t))
	if err != nil {
		t.Fatal(err)
	}
	st, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	if want := StatusPower | StatusVersion | StatusControlMode; st.Denied != want {
		t.Fatalf("Denied = %b, want %b", st.Denied, want)
	}
}

func TestConnect_LimitedAccountWrongPassword(t *testing.T) {
	ts := newAMTServer(t, true, "kvm", "secret-1", limitedRoutes())
	cfg := Config{Host: "device.example.com", Username: "kvm", Password: "Geheim-42", UseTLS: true,
		Fingerprint: fingerprintOf(ts.Certificate().Raw), dialContext: dialTo(ts)}
	d, err := Connect(context.Background(), cfg, noPrompt(t))
	if err != nil {
		t.Fatal(err)
	}
	st, err := d.Status()
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want a 401 error, got %+v %v", st, err)
	}
	if coreClassDenied(err) {
		t.Fatal("a 401 must never count as a permission fault")
	}
}
