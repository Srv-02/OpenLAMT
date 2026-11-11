package amt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func init() { tlsSettleDelay, tlsProbeWindow = 0, 0 }

// testCert is a generated certificate of the fake certificate store.
type testCert struct {
	id   string
	der  []byte
	x    *x509.Certificate
	key  any
	root bool
}

var (
	certsOnce sync.Once
	certA     testCert // bound to TLS, ECDSA key pair (SubjectPublicKeyInfo DERKey)
	certB     testCert // RSA key pair in PKCS#1 form (older AMT)
	certNoKey testCert // no key pair on the device
	certRoot  testCert // trusted root for client certificates
)

func makeCert(t *testing.T, id, cn string, key any, pub any, root bool) testCert {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(int64(len(id) + 1)),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA:         root, BasicConstraintsValid: root,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	x, _ := x509.ParseCertificate(der)
	return testCert{id: id, der: der, x: x, key: key, root: root}
}

func testCerts(t *testing.T) {
	certsOnce.Do(func() {
		ec := func() *ecdsa.PrivateKey {
			k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			return k
		}
		ka, kn, kr := ec(), ec(), ec()
		kb, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			t.Fatal(err)
		}
		certA = makeCert(t, "Intel(r) AMT Certificate: Handle: 0", "amt-a.example.com", ka, &ka.PublicKey, false)
		certB = makeCert(t, "Intel(r) AMT Certificate: Handle: 1", "amt-b.example.com", kb, &kb.PublicKey, false)
		certNoKey = makeCert(t, "Intel(r) AMT Certificate: Handle: 2", "nokey.example.com", kn, &kn.PublicKey, false)
		certRoot = makeCert(t, "Intel(r) AMT Certificate: Handle: 3", "ca.example.com", kr, &kr.PublicKey, true)
	})
}

func pullBody(class, items string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:b="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:c="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns:g="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/` +
		class + `"><a:Header/><a:Body><g:PullResponse><g:Items>` + items + `</g:Items><g:EndOfSequence></g:EndOfSequence></g:PullResponse></a:Body></a:Envelope>`
}

func certItem(c testCert) string {
	return `<h:AMT_PublicKeyCertificate><h:ElementName>Intel(r) AMT Certificate</h:ElementName><h:InstanceID>` + c.id +
		`</h:InstanceID><h:Issuer>CN=` + c.x.Issuer.CommonName + `</h:Issuer><h:ReadOnlyCertificate>false</h:ReadOnlyCertificate><h:Subject>CN=` +
		c.x.Subject.CommonName + `</h:Subject><h:TrustedRootCertficate>` + map[bool]string{true: "true", false: "false"}[c.root] +
		`</h:TrustedRootCertficate><h:X509Certificate>` + base64.StdEncoding.EncodeToString(c.der) + `</h:X509Certificate></h:AMT_PublicKeyCertificate>`
}

func keyItem(id string, der []byte) string {
	return `<h:AMT_PublicPrivateKeyPair><h:DERKey>` + base64.StdEncoding.EncodeToString(der) + `</h:DERKey><h:ElementName>Intel(r) AMT Key</h:ElementName><h:InstanceID>` + id + `</h:InstanceID></h:AMT_PublicPrivateKeyPair>`
}

func bindingItem(certID string) string {
	return `<h:AMT_TLSCredentialContext><h:ElementInContext><b:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</b:Address><b:ReferenceParameters><c:ResourceURI>http://intel.com/wbem/wscim/1/amt-schema/1/AMT_PublicKeyCertificate</c:ResourceURI><c:SelectorSet><c:Selector Name="InstanceID">` +
		certID + `</c:Selector></c:SelectorSet></b:ReferenceParameters></h:ElementInContext><h:ElementProvidingContext><b:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</b:Address><b:ReferenceParameters><c:ResourceURI>http://intel.com/wbem/wscim/1/amt-schema/1/AMT_TLSProtocolEndpointCollection</c:ResourceURI><c:SelectorSet><c:Selector Name="ElementName">TLSProtocolEndpointInstances Collection</c:Selector></c:SelectorSet></b:ReferenceParameters></h:ElementProvidingContext></h:AMT_TLSCredentialContext>`
}

func commitBody(rv string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_SetupAndConfigurationService"><a:Header/><a:Body><h:CommitChanges_OUTPUT><h:ReturnValue>` +
		rv + `</h:ReturnValue></h:CommitChanges_OUTPUT></a:Body></a:Envelope>`
}

// tlsSeq is writeSeq plus a certificate store with certA (bound), certB, certNoKey and,
// with root, certRoot.
func tlsSeq(t *testing.T, root bool) map[string][]cfgResp {
	testCerts(t)
	seq := writeSeq()
	certs := certItem(certA) + certItem(certB) + certItem(certNoKey)
	if root {
		certs += certItem(certRoot)
	}
	seq["Enumerate|AMT_PublicKeyCertificate"] = []cfgResp{{file: "enumerate.xml"}}
	seq["Pull|AMT_PublicKeyCertificate"] = []cfgResp{{body: pullBody(classPublicKeyCert, certs)}}
	seq["Enumerate|AMT_PublicPrivateKeyPair"] = []cfgResp{{file: "enumerate.xml"}}
	seq["Pull|AMT_PublicPrivateKeyPair"] = []cfgResp{{body: pullBody(classKeyPair,
		keyItem("Intel(r) AMT Key: Handle: 0", certA.x.RawSubjectPublicKeyInfo)+
			keyItem("Intel(r) AMT Key: Handle: 1", x509.MarshalPKCS1PublicKey(certB.x.PublicKey.(*rsa.PublicKey))))}}
	seq["Enumerate|AMT_TLSCredentialContext"] = []cfgResp{{file: "enumerate.xml"}}
	seq["Pull|AMT_TLSCredentialContext"] = []cfgResp{{body: pullBody(classTLSCredContext, bindingItem(certA.id))}}
	seq["Put|AMT_TLSCredentialContext"] = []cfgResp{{file: "cfg_put_ok.xml"}}
	seq["Create|AMT_TLSCredentialContext"] = []cfgResp{{file: "cfg_put_ok.xml"}}
	seq["Put|AMT_TLSSettingData"] = []cfgResp{{file: "cfg_put_ok.xml"}}
	seq["CommitChanges|AMT_SetupAndConfigurationService"] = []cfgResp{{body: commitBody("0")}}
	return seq
}

func tlsDevice(t *testing.T, seq map[string][]cfgResp) (*Device, *cfgWSMan) {
	d, f := cfgDevice(seq)
	d.cfg.UseTLS = true
	d.fingerprint = fingerprintOf(certA.der)
	return d, f
}

func TestTLSCertificates(t *testing.T) {
	d, f := tlsDevice(t, tlsSeq(t, true))
	got, err := d.TLSCertificates()
	if err != nil {
		t.Fatal(err)
	}
	want := []Certificate{
		{ID: certA.id, Subject: "CN=amt-a.example.com", Issuer: "CN=amt-a.example.com", Fingerprint: fingerprintOf(certA.der), HasPrivateKey: true, TLS: true},
		{ID: certB.id, Subject: "CN=amt-b.example.com", Issuer: "CN=amt-b.example.com", Fingerprint: fingerprintOf(certB.der), HasPrivateKey: true},
		{ID: certNoKey.id, Subject: "CN=nokey.example.com", Issuer: "CN=nokey.example.com", Fingerprint: fingerprintOf(certNoKey.der)},
		{ID: certRoot.id, Subject: "CN=ca.example.com", Issuer: "CN=ca.example.com", Fingerprint: fingerprintOf(certRoot.der), TrustedRoot: true},
	}
	for i := range want {
		want[i].NotBefore = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		want[i].NotAfter = time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("certificates:\n got %+v\nwant %+v", got, want)
	}
	if !got[0].Usable() || !got[1].Usable() || got[2].Usable() || got[3].Usable() {
		t.Error("Usable")
	}
	assertNoWrites(t, f)
}

func TestTLSCertificatesWithoutBinding(t *testing.T) {
	seq := tlsSeq(t, false)
	seq["Pull|AMT_TLSCredentialContext"] = []cfgResp{{body: pullBody(classTLSCredContext, "")}}
	d, _ := tlsDevice(t, seq)
	got, err := d.TLSCertificates()
	if err != nil || len(got) != 3 || got[0].TLS {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSetTLSNeedsConfirmation(t *testing.T) {
	for name, c := range map[string]struct {
		change TLSChange
		fields []string
	}{
		"certificate": {TLSChange{Certificate: ptr(certB.id)}, []string{RiskTLSCertificate}},
		"local":       {TLSChange{LocalEnabled: ptr(true)}, []string{RiskTLSLocalEnabled}},
		"trusted CNs": {TLSChange{TrustedCNs: &[]string{"client.example.com"}}, []string{RiskTLSTrustedCNs}},
		"mutual":      {TLSChange{MutualAuth: ptr(true), TrustedCNs: &[]string{"client.example.com"}}, []string{RiskTLSMutualAuth, RiskTLSTrustedCNs}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, conf := range []Confirmation{{}, confirmed(OpNetwork)} {
				d, f := tlsDevice(t, tlsSeq(t, true))
				err := d.SetTLS(c.change, conf)
				var re *RiskyChangeError
				if !errors.As(err, &re) || re.Op != OpTLS || !reflect.DeepEqual(re.Fields, c.fields) {
					t.Fatalf("err = %v", err)
				}
				assertNoWrites(t, f)
			}
		})
	}
}

func TestSetTLSWithoutDifferenceWritesNothing(t *testing.T) {
	d, f := tlsDevice(t, tlsSeq(t, true))
	if err := d.SetTLS(TLSChange{Certificate: ptr(certA.id), MutualAuth: ptr(false), LocalEnabled: ptr(false), TrustedCNs: &[]string{}}, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)
}

func TestSetTLSCertificate(t *testing.T) {
	d, f := tlsDevice(t, tlsSeq(t, false))
	if err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS)); err != nil {
		t.Fatal(err)
	}
	put := onePut(t, f, "Put|AMT_TLSCredentialContext")
	assertContains(t, put, `<w:Selector Name="InstanceID">`+certB.id+`</w:Selector>`, "AMT_TLSProtocolEndpointCollection")
	onePut(t, f, "CommitChanges|AMT_SetupAndConfigurationService")
	if n := len(f.requestsFor("Put|AMT_TLSSettingData")); n != 0 {
		t.Errorf("%d TLS setting writes for a certificate change", n)
	}
	if d.Fingerprint() != fingerprintOf(certB.der) {
		t.Error("the connection must pin the new certificate")
	}
	var order []string
	for _, r := range f.requests {
		if k := routeKey(r); isWriteRoute(k) {
			order = append(order, k)
		}
	}
	if want := []string{"Put|AMT_TLSCredentialContext", "CommitChanges|AMT_SetupAndConfigurationService"}; !reflect.DeepEqual(order, want) {
		t.Errorf("write order %v", order)
	}
}

func TestSetTLSCertificateCreatesMissingBinding(t *testing.T) {
	seq := tlsSeq(t, false)
	seq["Pull|AMT_TLSCredentialContext"] = []cfgResp{{body: pullBody(classTLSCredContext, "")}}
	d, f := tlsDevice(t, seq)
	if err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS)); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Create|AMT_TLSCredentialContext"), certB.id)
}

func TestSetTLSPlainSessionKeepsPin(t *testing.T) {
	d, _ := tlsDevice(t, tlsSeq(t, false))
	d.cfg.UseTLS, d.fingerprint = false, ""
	if err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS)); err != nil || d.Fingerprint() != "" {
		t.Fatalf("%v, pin %q", err, d.Fingerprint())
	}
}

func TestSetTLSActivatesStagedBinding(t *testing.T) {
	seq := tlsSeq(t, false) // an earlier failed call left certB bound but not committed
	seq["Pull|AMT_TLSCredentialContext"] = []cfgResp{{body: pullBody(classTLSCredContext, bindingItem(certB.id))}}
	d, f := tlsDevice(t, seq)
	if err := d.SetTLS(TLSChange{LocalEnabled: ptr(true)}, confirmed(OpTLS)); err != nil {
		t.Fatal(err)
	}
	onePut(t, f, "CommitChanges|AMT_SetupAndConfigurationService")
	if d.Fingerprint() != fingerprintOf(certB.der) || d.cfg.Fingerprint != fingerprintOf(certB.der) {
		t.Error("the commit activates the staged certificate: the pin must follow")
	}
}

func TestSetTLSMutualAuth(t *testing.T) {
	fixedNow(t, time.Unix(1767225600, 0))
	d, f := tlsDevice(t, tlsSeq(t, true))
	err := d.SetTLS(TLSChange{MutualAuth: ptr(true), TrustedCNs: &[]string{"client1.example.com", "client2.example.com"}}, confirmed(OpTLS))
	if err != nil {
		t.Fatal(err)
	}
	put := onePut(t, f, "Put|AMT_TLSSettingData")
	assertContains(t, put,
		`<w:Selector Name="InstanceID">Intel(r) AMT 802.3 TLS Settings</w:Selector>`,
		"<h:MutualAuthentication>true</h:MutualAuthentication>",
		"<h:AcceptNonSecureConnections>false</h:AcceptNonSecureConnections>",
		"<h:NonSecureConnectionsSupported>false</h:NonSecureConnectionsSupported>",
		"<h:TrustedCN>client1.example.com</h:TrustedCN><h:TrustedCN>client2.example.com</h:TrustedCN>")
	var order []string
	for _, r := range f.requests {
		if k := routeKey(r); isWriteRoute(k) {
			order = append(order, k)
		}
	}
	want := []string{"SetHighAccuracyTimeSynch|AMT_TimeSynchronizationService", "Put|AMT_TLSSettingData", "CommitChanges|AMT_SetupAndConfigurationService"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("write order %v", order)
	}
	if d.Fingerprint() != fingerprintOf(certA.der) {
		t.Error("settings without certificate change keep the pin")
	}
}

func TestSetTLSLocalInterface(t *testing.T) {
	d, f := tlsDevice(t, tlsSeq(t, false))
	if err := d.SetTLS(TLSChange{LocalEnabled: ptr(true)}, confirmed(OpTLS)); err != nil {
		t.Fatal(err)
	}
	put := onePut(t, f, "Put|AMT_TLSSettingData")
	assertContains(t, put, `<w:Selector Name="InstanceID">Intel(r) AMT LMS TLS Settings</w:Selector>`,
		"<h:Enabled>true</h:Enabled>", "<h:TrustedCN>ca.example.com</h:TrustedCN>", "<h:AcceptNonSecureConnections>true</h:AcceptNonSecureConnections>")
	if n := propCount(put); n != 7 {
		t.Errorf("Put has %d properties, want all 7 that were read", n)
	}
}

func TestSetTLSRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		change TLSChange
		want   error
	}{
		"unknown certificate":   {TLSChange{Certificate: ptr("Intel(r) AMT Certificate: Handle: 9")}, ErrInvalidValue},
		"certificate no key":    {TLSChange{Certificate: ptr(certNoKey.id)}, ErrInvalidValue},
		"trusted root":          {TLSChange{Certificate: ptr(certRoot.id)}, ErrInvalidValue},
		"empty certificate":     {TLSChange{Certificate: ptr(" ")}, ErrInvalidValue},
		"plain unsupported":     {TLSChange{AcceptNonSecure: ptr(true)}, ErrNotModifiable},
		"mutual without CNs":    {TLSChange{MutualAuth: ptr(true)}, ErrInvalidValue},
		"five CNs":              {TLSChange{TrustedCNs: &[]string{"a", "b", "c", "d", "e"}}, ErrInvalidValue},
		"CN with space":         {TLSChange{TrustedCNs: &[]string{"a b"}}, ErrInvalidValue},
		"mutual without a root": {TLSChange{MutualAuth: ptr(true), TrustedCNs: &[]string{"client.example.com"}}, ErrInvalidValue},
	} {
		t.Run(name, func(t *testing.T) {
			d, f := tlsDevice(t, tlsSeq(t, name != "mutual without a root"))
			if err := d.SetTLS(c.change, confirmed(OpTLS)); !errors.Is(err, c.want) {
				t.Fatalf("err = %v", err)
			}
			assertNoWrites(t, f)
		})
	}
	seq := tlsSeq(t, true)
	seq["Pull|AMT_TLSSettingData"] = []cfgResp{{body: withProp(t, "cfg_tls_pull.xml", "Enabled", "false")}}
	d, f := tlsDevice(t, seq)
	if err := d.SetTLS(TLSChange{LocalEnabled: ptr(true)}, confirmed(OpTLS)); !errors.Is(err, ErrNotModifiable) {
		t.Errorf("TLS off on the network interface: %v", err)
	}
	assertNoWrites(t, f)
}

func TestSetTLSCommitErrors(t *testing.T) {
	seq := tlsSeq(t, false)
	seq["CommitChanges|AMT_SetupAndConfigurationService"] = []cfgResp{{body: commitBody("38")}}
	d, _ := tlsDevice(t, seq)
	var me *MethodError
	if err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS)); !errors.As(err, &me) || me.Method != "CommitChanges" || me.ReturnValue != 38 {
		t.Fatalf("err = %v", err)
	}
	if d.Fingerprint() != fingerprintOf(certA.der) {
		t.Error("a failed commit must keep the pin")
	}

	seq["CommitChanges|AMT_SetupAndConfigurationService"] = []cfgResp{{err: errors.New("connection reset by peer")}}
	d, _ = tlsDevice(t, seq)
	err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS))
	var ce *CertificateUncertainError
	if !errors.As(err, &ce) || ce.Fingerprint != fingerprintOf(certB.der) || d.Fingerprint() != fingerprintOf(certA.der) {
		t.Fatalf("commit without answer and without proof: %v, pin changed %v", err, d.Fingerprint() != fingerprintOf(certA.der))
	}
	seq["CommitChanges|AMT_SetupAndConfigurationService"] = []cfgResp{{err: errors.New("connection reset by peer")}}
	d, _ = tlsDevice(t, seq)
	if err := d.SetTLS(TLSChange{LocalEnabled: ptr(true)}, confirmed(OpTLS)); err == nil || errors.Is(err, ErrCertificateUncertain) {
		t.Errorf("settings commit without answer: %v", err)
	}

	seq = tlsSeq(t, false)
	seq["Put|AMT_TLSCredentialContext"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, f := tlsDevice(t, seq)
	if err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS)); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("binding denied: %v", err)
	}
	if n := len(f.requestsFor("CommitChanges|AMT_SetupAndConfigurationService")); n != 0 {
		t.Error("nothing may be committed after a failed step")
	}
}

// certServer is a pair of TLS WS-Man test servers without authentication: cur presents certA,
// next presents certB. The store lists both with key pairs. CommitChanges switches dialing to
// next, and cur then drops every request, as AMT restarts TLS with the new certificate.
// dropCommit closes the connection instead of answering the commit.
type certServer struct {
	cur, next  *httptest.Server
	switched   atomic.Bool
	dropCommit bool
	noSwitch   bool // CommitChanges does not take effect
}

func (cs *certServer) serve(w http.ResponseWriter, r *http.Request) {
	local, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if cs.switched.Load() && local != nil && local.String() == cs.cur.Listener.Addr().String() {
		conn, _, _ := w.(http.Hijacker).Hijack() // the old TLS listener is gone
		conn.Close()
		return
	}
	b, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	file := func(name string) {
		data, _ := os.ReadFile(filepath.Join("testdata", name))
		_, _ = w.Write(data)
	}
	switch routeKey(string(b)) {
	case "Pull|AMT_TLSSettingData":
		file("cfg_tls_pull.xml")
	case "Pull|AMT_PublicKeyCertificate":
		_, _ = io.WriteString(w, pullBody(classPublicKeyCert, certItem(certA)+certItem(certB)))
	case "Pull|AMT_PublicPrivateKeyPair":
		_, _ = io.WriteString(w, pullBody(classKeyPair, keyItem("k0", certA.x.RawSubjectPublicKeyInfo)+keyItem("k1", certB.x.RawSubjectPublicKeyInfo)))
	case "Pull|AMT_TLSCredentialContext":
		id := certA.id
		if cs.switched.Load() {
			id = certB.id
		}
		_, _ = io.WriteString(w, pullBody(classTLSCredContext, bindingItem(id)))
	case "Enumerate|AMT_PublicKeyCertificate", "Enumerate|AMT_PublicPrivateKeyPair", "Enumerate|AMT_TLSCredentialContext", "Enumerate|AMT_TLSSettingData":
		file("enumerate.xml")
	case "Put|AMT_TLSCredentialContext":
		file("cfg_put_ok.xml")
	case "CommitChanges|AMT_SetupAndConfigurationService":
		cs.switched.Store(!cs.noSwitch)
		if cs.dropCommit {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		_, _ = io.WriteString(w, commitBody("0"))
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func tlsServer(t *testing.T, c testCert, h http.Handler) *httptest.Server {
	ts := httptest.NewUnstartedServer(h)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{c.der}, PrivateKey: c.key}}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func newCertServers(t *testing.T, dropCommit bool) (*certServer, Config) {
	testCerts(t)
	cs := &certServer{dropCommit: dropCommit}
	cs.cur, cs.next = tlsServer(t, certA, http.HandlerFunc(cs.serve)), tlsServer(t, certB, http.HandlerFunc(cs.serve))
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		ts := cs.cur
		if cs.switched.Load() {
			ts = cs.next
		}
		return (&net.Dialer{}).DialContext(ctx, network, ts.Listener.Addr().String())
	}
	return cs, Config{Host: testHost, Username: "admin", Password: "Secret-Pass1", UseTLS: true,
		Fingerprint: fingerprintOf(certA.der), dialContext: dial}
}

func TestSetTLSCertificateSwitchesPinOfLiveConnection(t *testing.T) {
	for _, drop := range []bool{false, true} {
		cs, cfg := newCertServers(t, drop)
		d, err := Connect(context.Background(), cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.TLSCertificates(); err != nil {
			t.Fatalf("before: %v", err)
		}
		if err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS)); err != nil {
			t.Fatalf("drop %v: %v", drop, err)
		}
		if d.Fingerprint() != fingerprintOf(certB.der) {
			t.Fatalf("drop %v: pin not switched", drop)
		}
		certs, err := d.TLSCertificates()
		if err != nil || !certs[1].TLS {
			t.Fatalf("drop %v: the connection must work with the new certificate: %v", drop, err)
		}
		conn, err := tls.Dial("tcp", cs.next.Listener.Addr().String(), d.tlsCfg)
		if err != nil {
			t.Fatalf("drop %v: new redirection connections must accept the new certificate: %v", drop, err)
		}
		conn.Close()
	}
}

func TestSetTLSCertificateNotCommitted(t *testing.T) {
	for name, prepare := range map[string]func(cs *certServer){
		"unreachable":     func(cs *certServer) { cs.next.Close() },
		"old certificate": func(cs *certServer) { cs.noSwitch = true },
	} {
		cs, cfg := newCertServers(t, true)
		prepare(cs)
		d, err := Connect(context.Background(), cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.SetTLS(TLSChange{Certificate: ptr(certB.id)}, confirmed(OpTLS)); !errors.Is(err, ErrCertificateUncertain) || d.Fingerprint() != fingerprintOf(certA.der) {
			t.Fatalf("%s: unproven commit: %v, pin switched %v", name, err, d.Fingerprint() != fingerprintOf(certA.der))
		}
	}
}

func TestTLSChangeApplyCopies(t *testing.T) {
	cur := TLSSettings{Remote: TLSInterface{Available: true, Enabled: true, TrustedCNs: []string{"a.example.com"}}}
	n := TLSChange{MutualAuth: ptr(true)}.Apply(cur)
	n.Remote.TrustedCNs[0] = "changed"
	if cur.Remote.TrustedCNs[0] != "a.example.com" || !strings.HasPrefix(n.Remote.TrustedCNs[0], "changed") || !n.Remote.MutualAuth {
		t.Error("Apply must not share the slice")
	}
}
