package amt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

type consentFunc func() (string, error)

func (f consentFunc) ConsentCode() (string, error) { return f() }

func kvmRoutes() map[string]string {
	return map[string]string{
		"Get|AMT_RedirectionService": "redirection_get.xml",
		"Get|CIM_KVMRedirectionSAP":  "kvm_get_enabled.xml",
		"Get|IPS_OptInService":       "optin_get_none.xml",
	}
}

func routeKeys(f *fakeWSMan) []string {
	var keys []string
	for _, r := range f.requests {
		keys = append(keys, routeKey(r))
	}
	return keys
}

func dialToAddr(addr string) dialFunc {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "device.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func failPrompt(t *testing.T) ConsentPrompt {
	return consentFunc(func() (string, error) {
		t.Error("prompt must not be called")
		return "", nil
	})
}

func TestEnableKVM_NothingToDo(t *testing.T) {
	f := &fakeWSMan{routes: kvmRoutes()}
	if err := newDevice(f, "").EnableKVM(); err != nil {
		t.Fatal(err)
	}
	if got := routeKeys(f); !reflect.DeepEqual(got, []string{"Get|AMT_RedirectionService", "Get|CIM_KVMRedirectionSAP"}) {
		t.Fatalf("%v", got)
	}
}

func TestEnableKVM_EnablesListenerAndSAP(t *testing.T) {
	routes := kvmRoutes()
	routes["Get|AMT_RedirectionService"] = "redirection_get_nolistener.xml"
	routes["Put|AMT_RedirectionService"] = "redirection_put.xml"
	routes["Get|CIM_KVMRedirectionSAP"] = "kvm_get_disabled.xml"
	routes["RequestStateChange|CIM_KVMRedirectionSAP"] = "kvm_requeststatechange.xml"
	f := &fakeWSMan{routes: routes}
	if err := newDevice(f, "").EnableKVM(); err != nil {
		t.Fatal(err)
	}
	want := []string{"Get|AMT_RedirectionService", "Put|AMT_RedirectionService", "Get|CIM_KVMRedirectionSAP", "RequestStateChange|CIM_KVMRedirectionSAP"}
	if got := routeKeys(f); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	put := f.requests[1]
	for _, s := range []string{"<h:ListenerEnabled>true</h:ListenerEnabled>", "<h:EnabledState>32771</h:EnabledState>", "<h:Name>Intel(r) AMT Redirection Service</h:Name>", "<h:SystemName>Intel(r) AMT</h:SystemName>"} {
		if !strings.Contains(put, s) {
			t.Fatalf("put lacks %s: %s", s, put)
		}
	}
	if !strings.Contains(f.requests[3], "<h:RequestedState>2</h:RequestedState>") {
		t.Fatalf("state change: %s", f.requests[3])
	}
}

func TestEnableKVM_RequestStateChangeFails(t *testing.T) {
	routes := kvmRoutes()
	routes["Get|CIM_KVMRedirectionSAP"] = "kvm_get_disabled.xml"
	routes["RequestStateChange|CIM_KVMRedirectionSAP"] = "kvm_requeststatechange_fail.xml"
	err := newDevice(&fakeWSMan{routes: routes}, "").EnableKVM()
	var ke *KVMError
	if !errors.As(err, &ke) || ke.ReturnValue != 2 || ke.Step != "KVMRedirectionSAP.RequestStateChange" {
		t.Fatalf("err = %v", err)
	}
}

func TestUserConsent_NotRequired(t *testing.T) {
	f := &fakeWSMan{routes: kvmRoutes()}
	if err := newDevice(f, "").UserConsent(failPrompt(t)); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d", len(f.requests))
	}
}

func TestUserConsent_InSession(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{"Get|IPS_OptInService": "optin_get_kvm_insession.xml"}}
	if err := newDevice(f, "").UserConsent(failPrompt(t)); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d", len(f.requests))
	}
}

func TestUserConsent_StartAndSend(t *testing.T) {
	routes := map[string]string{"Get|IPS_OptInService": "optin_get_kvm_notstarted.xml", "StartOptIn|IPS_OptInService": "optin_start.xml", "SendOptInCode|IPS_OptInService": "optin_send.xml"}
	f := &fakeWSMan{routes: routes}
	asked := 0
	err := newDevice(f, "").UserConsent(consentFunc(func() (string, error) { asked++; return " 012345\n", nil }))
	if err != nil || asked != 1 {
		t.Fatalf("err=%v asked=%d", err, asked)
	}
	if got := routeKeys(f); !reflect.DeepEqual(got, []string{"Get|IPS_OptInService", "StartOptIn|IPS_OptInService", "SendOptInCode|IPS_OptInService"}) {
		t.Fatalf("%v", got)
	}
	if !strings.Contains(f.requests[2], "<h:OptInCode>12345</h:OptInCode>") {
		t.Fatalf("code: %s", f.requests[2])
	}
}

func TestUserConsent_DisplayedSkipsStart(t *testing.T) {
	routes := map[string]string{"Get|IPS_OptInService": "optin_get_kvm_displayed.xml", "SendOptInCode|IPS_OptInService": "optin_send.xml"}
	f := &fakeWSMan{routes: routes}
	err := newDevice(f, "").UserConsent(consentFunc(func() (string, error) { return "123456", nil }))
	if err != nil {
		t.Fatal(err)
	}
	if got := routeKeys(f); !reflect.DeepEqual(got, []string{"Get|IPS_OptInService", "SendOptInCode|IPS_OptInService"}) {
		t.Fatalf("%v", got)
	}
}

func TestUserConsent_InvalidCodeNotSent(t *testing.T) {
	for _, code := range []string{"12345", "1234567", "12a456", "", "12 456", "+12345", "١٢٣٤٥٦"} {
		f := &fakeWSMan{routes: map[string]string{"Get|IPS_OptInService": "optin_get_kvm_displayed.xml"}}
		err := newDevice(f, "").UserConsent(consentFunc(func() (string, error) { return code, nil }))
		if !errors.Is(err, ErrInvalidConsentCode) || len(f.requests) != 1 {
			t.Fatalf("%q: err=%v requests=%d", code, err, len(f.requests))
		}
		if code != "" && strings.Contains(err.Error(), code) {
			t.Fatalf("error leaks code: %v", err)
		}
	}
}

func TestUserConsent_PromptError(t *testing.T) {
	boom := errors.New("input closed")
	f := &fakeWSMan{routes: map[string]string{"Get|IPS_OptInService": "optin_get_kvm_displayed.xml"}}
	err := newDevice(f, "").UserConsent(consentFunc(func() (string, error) { return "", boom }))
	if !errors.Is(err, boom) || len(f.requests) != 1 {
		t.Fatalf("err=%v requests=%d", err, len(f.requests))
	}
}

func TestUserConsent_NoPrompt(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{"Get|IPS_OptInService": "optin_get_kvm_notstarted.xml"}}
	err := newDevice(f, "").UserConsent(nil)
	if !errors.Is(err, ErrConsentRequired) || len(f.requests) != 1 {
		t.Fatalf("err=%v requests=%d", err, len(f.requests))
	}
}

func TestUserConsent_StartFails(t *testing.T) {
	routes := map[string]string{"Get|IPS_OptInService": "optin_get_kvm_notstarted.xml", "StartOptIn|IPS_OptInService": "optin_start_fail.xml"}
	err := newDevice(&fakeWSMan{routes: routes}, "").UserConsent(failPrompt(t))
	var ce *ConsentError
	if !errors.As(err, &ce) || ce.Step != "StartOptIn" || ce.ReturnValue != 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestUserConsent_SendFails(t *testing.T) {
	routes := map[string]string{"Get|IPS_OptInService": "optin_get_kvm_displayed.xml", "SendOptInCode|IPS_OptInService": "optin_send_fail.xml"}
	err := newDevice(&fakeWSMan{routes: routes}, "").UserConsent(consentFunc(func() (string, error) { return "012345", nil }))
	var ce *ConsentError
	if !errors.As(err, &ce) || ce.Step != "SendOptInCode" || ce.ReturnValue != 2 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "012345") {
		t.Fatalf("error leaks code: %v", err)
	}
}

func TestOpenKVM_TLSPinnedDial(t *testing.T) {
	cert := selfSignedCert(t)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{cert}}})
	fp := fingerprintOf(cert.Certificate[0])
	d := newDevice(&fakeWSMan{routes: kvmRoutes()}, fp)
	d.cfg = Config{Host: "device.example.com", Username: "admin", Password: "pw", UseTLS: true, dialContext: dialToAddr(srv.Addr)}
	d.tlsCfg = tlsConfigFor("device.example.com", fp)
	c, err := d.OpenKVM(context.Background(), kvm.QualityLow, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if w, _ := c.Size(); w != 128 || c.Quality() != kvm.QualityLow {
		t.Fatalf("width %d quality %v", w, c.Quality())
	}
}

func TestOpenKVM_WrongPinFails(t *testing.T) {
	cert := selfSignedCert(t)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{cert}}})
	d := newDevice(&fakeWSMan{routes: kvmRoutes()}, "")
	d.cfg = Config{Host: "device.example.com", Username: "admin", Password: "pw", UseTLS: true, dialContext: dialToAddr(srv.Addr)}
	d.tlsCfg = tlsConfigFor("device.example.com", strings.Repeat("00", 32))
	_, err := d.OpenKVM(context.Background(), kvm.QualityHigh, nil)
	var fe *FingerprintMismatchError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v", err)
	}
	if srv.Connections() != 0 {
		t.Fatalf("connections = %d", srv.Connections())
	}
}

func TestOpenKVM_PlainTCP(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw"})
	d := newDevice(&fakeWSMan{routes: kvmRoutes()}, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: dialToAddr(srv.Addr)}
	c, err := d.OpenKVM(context.Background(), kvm.QualityHigh, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
}

func TestOpenKVM_StopsWhenConsentFails(t *testing.T) {
	dials := 0
	routes := kvmRoutes()
	routes["Get|IPS_OptInService"] = "optin_get_kvm_notstarted.xml"
	d := newDevice(&fakeWSMan{routes: routes}, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("must not dial")
	}}
	_, err := d.OpenKVM(context.Background(), kvm.QualityHigh, nil)
	if !errors.Is(err, ErrConsentRequired) || dials != 0 {
		t.Fatalf("err=%v dials=%d", err, dials)
	}
}

func TestKVM_EmptyResponsesFailWithoutWriting(t *testing.T) {
	cases := []struct {
		name   string
		routes map[string]string
		run    func(d *Device) error
		sent   []string // exact request sequence expected
	}{
		{"redirection get empty", map[string]string{"Get|AMT_RedirectionService": "empty_body.xml"},
			func(d *Device) error { return d.EnableKVM() }, []string{"Get|AMT_RedirectionService"}},
		{"sap get empty", map[string]string{"Get|AMT_RedirectionService": "redirection_get.xml", "Get|CIM_KVMRedirectionSAP": "empty_body.xml"},
			func(d *Device) error { return d.EnableKVM() }, []string{"Get|AMT_RedirectionService", "Get|CIM_KVMRedirectionSAP"}},
		{"request state change without output", map[string]string{"Get|AMT_RedirectionService": "redirection_get.xml", "Get|CIM_KVMRedirectionSAP": "kvm_get_disabled.xml", "RequestStateChange|CIM_KVMRedirectionSAP": "empty_body.xml"},
			func(d *Device) error { return d.EnableKVM() }, []string{"Get|AMT_RedirectionService", "Get|CIM_KVMRedirectionSAP", "RequestStateChange|CIM_KVMRedirectionSAP"}},
		{"optin get empty", map[string]string{"Get|IPS_OptInService": "empty_body.xml"},
			func(d *Device) error { return d.UserConsent(failPrompt(t)) }, []string{"Get|IPS_OptInService"}},
		{"start without output", map[string]string{"Get|IPS_OptInService": "optin_get_kvm_notstarted.xml", "StartOptIn|IPS_OptInService": "empty_body.xml"},
			func(d *Device) error { return d.UserConsent(failPrompt(t)) }, []string{"Get|IPS_OptInService", "StartOptIn|IPS_OptInService"}},
		{"send without output", map[string]string{"Get|IPS_OptInService": "optin_get_kvm_displayed.xml", "SendOptInCode|IPS_OptInService": "empty_body.xml"},
			func(d *Device) error {
				return d.UserConsent(consentFunc(func() (string, error) { return "987654", nil }))
			}, []string{"Get|IPS_OptInService", "SendOptInCode|IPS_OptInService"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeWSMan{routes: tc.routes}
			err := tc.run(newDevice(f, ""))
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "987654") {
				t.Fatalf("error leaks code: %v", err)
			}
			if got := routeKeys(f); !reflect.DeepEqual(got, tc.sent) {
				t.Fatalf("requests = %v, want %v", got, tc.sent)
			}
		})
	}
}

// useFakeKVMWSMan swaps the WS-Man side of a device built by Connect for the KVM fake while
// keeping the cfg and tlsCfg that Connect set.
func useFakeKVMWSMan(d *Device) { d.msgs = newDevice(&fakeWSMan{routes: kvmRoutes()}, "").msgs }

func TestConnectTOFUThenOpenKVM_UsesConfirmedPin(t *testing.T) {
	cert := selfSignedCert(t)
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{cert}}})
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "pw", UseTLS: true, dialContext: dialToAddr(srv.Addr)}
	d, err := Connect(context.Background(), cfg, promptFunc(func(_, fp string) (bool, error) {
		return fp == fingerprintOf(cert.Certificate[0]), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	useFakeKVMWSMan(d)
	c, err := d.OpenKVM(context.Background(), kvm.QualityLow, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if srv.Connections() != 1 {
		t.Fatalf("connections = %d", srv.Connections())
	}
}

func TestConnectTOFUThenOpenKVM_OtherCertFails(t *testing.T) {
	// Connect confirms the certificate of the AMT server; the redirection listener presents another one.
	other := newAMTServer(t, true, "admin", "pw", statusRoutes())
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", TLS: &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}}})
	cfg := Config{Host: "device.example.com", Username: "admin", Password: "pw", UseTLS: true, dialContext: dialTo(other)}
	d, err := Connect(context.Background(), cfg, promptFunc(func(_, _ string) (bool, error) { return true, nil }))
	if err != nil {
		t.Fatal(err)
	}
	useFakeKVMWSMan(d)
	d.cfg.dialContext = dialToAddr(srv.Addr)
	_, err = d.OpenKVM(context.Background(), kvm.QualityLow, nil)
	var fe *FingerprintMismatchError
	if !errors.As(err, &fe) || srv.Connections() != 0 {
		t.Fatalf("err = %v connections = %d", err, srv.Connections())
	}
}

func TestOpenKVM_TLSWithoutPinnedConfigFails(t *testing.T) {
	dials := 0
	d := newDevice(&fakeWSMan{routes: kvmRoutes()}, "")
	d.cfg = Config{Host: "device.example.com", Username: "admin", Password: "pw", UseTLS: true,
		dialContext: func(context.Context, string, string) (net.Conn, error) { dials++; return nil, errors.New("no") }}
	if _, err := d.OpenKVM(context.Background(), kvm.QualityLow, nil); err == nil {
		t.Fatal("expected error")
	}
	if dials != 0 {
		t.Fatalf("dialed %d times", dials)
	}
}
