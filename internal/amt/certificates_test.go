package amt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

// pkmsBody is the answer of an AMT_PublicKeyManagementService method: ReturnValue rv and, with
// created, the reference element (CreatedCertificate or CreatedKey) with InstanceID id.
func pkmsBody(method, rv, created, id string) string {
	ref := ""
	if created != "" {
		ref = `<g:` + created + `><b:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</b:Address><b:ReferenceParameters><c:ResourceURI>http://intel.com/wbem/wscim/1/amt-schema/1/AMT_PublicKeyCertificate</c:ResourceURI><c:SelectorSet><c:Selector Name="InstanceID">` +
			id + `</c:Selector></c:SelectorSet></b:ReferenceParameters></g:` + created + `>`
	}
	return `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:b="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:c="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns:g="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_PublicKeyManagementService"><a:Header/><a:Body><g:` +
		method + `_OUTPUT>` + ref + `<g:ReturnValue>` + rv + `</g:ReturnValue></g:` + method + `_OUTPUT></a:Body></a:Envelope>`
}

// certSeq is tlsSeq with a trusted root, the management service and the Delete calls.
func certSeq(t *testing.T) map[string][]cfgResp {
	seq := tlsSeq(t, true)
	seq["AddTrustedRootCertificate|AMT_PublicKeyManagementService"] = []cfgResp{{body: pkmsBody("AddTrustedRootCertificate", "0", "CreatedCertificate", "Intel(r) AMT Certificate: Handle: 7")}}
	seq["AddKey|AMT_PublicKeyManagementService"] = []cfgResp{{body: pkmsBody("AddKey", "0", "CreatedKey", "Intel(r) AMT Key: Handle: 8")}}
	seq["AddCertificate|AMT_PublicKeyManagementService"] = []cfgResp{{body: pkmsBody("AddCertificate", "0", "CreatedCertificate", "Intel(r) AMT Certificate: Handle: 9")}}
	seq["Delete|AMT_PublicKeyCertificate"] = []cfgResp{{file: "empty_body.xml"}}
	seq["Delete|AMT_PublicPrivateKeyPair"] = []cfgResp{{file: "empty_body.xml"}}
	return seq
}

// routes returns the route keys of the recorded requests.
func routes(f *cfgWSMan) []string {
	out := make([]string, len(f.requests))
	for i, r := range f.requests {
		out[i] = routeKey(r)
	}
	return out
}

// requestsTo returns the bodies of the requests with route key.
func requestsTo(f *cfgWSMan, key string) []string {
	var out []string
	for _, r := range f.requests {
		if routeKey(r) == key {
			out = append(out, r)
		}
	}
	return out
}

func TestCertificateStoreReadsKeysAndLinksThem(t *testing.T) {
	d, _ := tlsDevice(t, certSeq(t))
	s, err := d.CertificateStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Certificates) != 4 || len(s.Keys) != 2 {
		t.Fatalf("%d certificates, %d keys", len(s.Certificates), len(s.Keys))
	}
	if string(s.Certificates[0].DER) != string(certA.der) || !s.Certificates[3].TrustedRoot || !s.Certificates[0].TLS {
		t.Errorf("certificates %+v", s.Certificates)
	}
	a, b := s.Keys[0], s.Keys[1]
	if a.ID != "Intel(r) AMT Key: Handle: 0" || a.Algorithm != "ECDSA" || a.Bits != 256 || a.Certificate != certA.id {
		t.Errorf("SubjectPublicKeyInfo key %+v", a)
	}
	if b.Algorithm != "RSA" || b.Bits != 1024 || b.Certificate != certB.id {
		t.Errorf("PKCS#1 key %+v", b)
	}
	if len(a.Fingerprint) != 64 || a.Fingerprint == b.Fingerprint {
		t.Errorf("fingerprints %q %q", a.Fingerprint, b.Fingerprint)
	}
}

func TestCertificateStoreKeyWithoutCertificate(t *testing.T) {
	seq := certSeq(t)
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	seq["Pull|AMT_PublicPrivateKeyPair"] = []cfgResp{{body: pullBody(classKeyPair, keyItem("Intel(r) AMT Key: Handle: 5", x509.MarshalPKCS1PublicKey(&k.PublicKey)))}}
	d, _ := tlsDevice(t, seq)
	s, err := d.CertificateStore()
	if err != nil {
		t.Fatal(err)
	}
	if s.Keys[0].Certificate != "" || s.Certificates[0].HasPrivateKey {
		t.Errorf("unlinked key %+v, certificate A %+v", s.Keys[0], s.Certificates[0])
	}
}

func TestAddTrustedRootCertificateSendsTheCertificate(t *testing.T) {
	d, f := tlsDevice(t, certSeq(t))
	id, err := d.AddTrustedRootCertificate(certRoot.der)
	if err != nil || id != "Intel(r) AMT Certificate: Handle: 7" {
		t.Fatalf("id %q, %v", id, err)
	}
	req := requestsTo(f, "AddTrustedRootCertificate|AMT_PublicKeyManagementService")
	if len(req) != 1 || !strings.Contains(req[0], base64.StdEncoding.EncodeToString(certRoot.der)) {
		t.Fatalf("request %v", req)
	}
}

func TestAddTrustedRootCertificateRejectsOtherData(t *testing.T) {
	d, f := tlsDevice(t, certSeq(t))
	if _, err := d.AddTrustedRootCertificate([]byte("not a certificate")); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("got %v", err)
	}
	if len(f.requests) != 0 {
		t.Fatalf("requests %v", routes(f))
	}
}

func TestAddTrustedRootCertificateErrors(t *testing.T) {
	testCerts(t)
	for name, c := range map[string]struct {
		resp   cfgResp
		full   bool
		method int
	}{
		"store full":   {cfgResp{body: pkmsBody("AddTrustedRootCertificate", "23", "", "")}, true, 23},
		"duplicate":    {cfgResp{body: pkmsBody("AddTrustedRootCertificate", "2058", "", "")}, false, 2058},
		"invalid cert": {cfgResp{body: pkmsBody("AddTrustedRootCertificate", "2063", "", "")}, false, 2063},
		"fault":        {cfgResp{err: amterror.NewAMTError("a:InternalError", "x", "")}, false, 0},
		"no output":    {cfgResp{file: "empty_body.xml"}, false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			seq := certSeq(t)
			seq["AddTrustedRootCertificate|AMT_PublicKeyManagementService"] = []cfgResp{c.resp}
			d, _ := tlsDevice(t, seq)
			_, err := d.AddTrustedRootCertificate(certRoot.der)
			if err == nil {
				t.Fatal("no error")
			}
			if errors.Is(err, ErrCertificateStoreFull) != c.full {
				t.Errorf("store full %v: %v", c.full, err)
			}
			var me *MethodError
			if got := errors.As(err, &me); got != (c.method != 0) || got && me.ReturnValue != c.method {
				t.Errorf("method error %v, want %d", err, c.method)
			}
			if name == "no output" && !errors.Is(err, ErrIncompleteResponse) {
				t.Errorf("no output: %v", err)
			}
		})
	}
}

// rsaCert is a certificate with an RSA key that is not on the device yet.
func rsaCert(t *testing.T) (testCert, *rsa.PrivateKey) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return makeCert(t, "new", "new.example.com", k, &k.PublicKey, false), k
}

func TestAddCertificateWithKeyAddsKeyThenCertificate(t *testing.T) {
	c, k := rsaCert(t)
	d, f := tlsDevice(t, certSeq(t))
	id, err := d.AddCertificateWithKey(c.der, k)
	if err != nil || id != "Intel(r) AMT Certificate: Handle: 9" {
		t.Fatalf("id %q, %v", id, err)
	}
	got := routes(f)
	if len(got) != 2 || got[0] != "AddKey|AMT_PublicKeyManagementService" || got[1] != "AddCertificate|AMT_PublicKeyManagementService" {
		t.Fatalf("requests %v", got)
	}
	assertContains(t, f.requests[0], base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(k)))
	assertContains(t, f.requests[1], base64.StdEncoding.EncodeToString(c.der))
}

func TestAddCertificateWithKeyKeepsAKeyAlreadyOnTheDevice(t *testing.T) {
	c, k := rsaCert(t)
	seq := certSeq(t)
	seq["AddKey|AMT_PublicKeyManagementService"] = []cfgResp{{body: pkmsBody("AddKey", "2058", "", "")}}
	d, f := tlsDevice(t, seq)
	if _, err := d.AddCertificateWithKey(c.der, k); err != nil {
		t.Fatal(err)
	}
	if n := len(requestsTo(f, "AddCertificate|AMT_PublicKeyManagementService")); n != 1 {
		t.Fatalf("AddCertificate %d times", n)
	}
}

func TestAddCertificateWithKeyRemovesTheKeyWhenTheCertificateFails(t *testing.T) {
	c, k := rsaCert(t)
	seq := certSeq(t)
	seq["AddCertificate|AMT_PublicKeyManagementService"] = []cfgResp{{body: pkmsBody("AddCertificate", "2063", "", "")}}
	d, f := tlsDevice(t, seq)
	_, err := d.AddCertificateWithKey(c.der, k)
	var me *MethodError
	if !errors.As(err, &me) || me.Method != "AddCertificate" || me.ReturnValue != 2063 {
		t.Fatalf("got %v", err)
	}
	del := requestsTo(f, "Delete|AMT_PublicPrivateKeyPair")
	if len(del) != 1 || !strings.Contains(del[0], "Intel(r) AMT Key: Handle: 8") {
		t.Fatalf("rollback %v", routes(f))
	}
	key := base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(k))
	if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), key[:40]) {
		t.Fatal("the error carries the private key")
	}
}

func TestAddCertificateWithKeyStoreFull(t *testing.T) {
	c, k := rsaCert(t)
	seq := certSeq(t)
	seq["AddKey|AMT_PublicKeyManagementService"] = []cfgResp{{body: pkmsBody("AddKey", "23", "", "")}}
	d, f := tlsDevice(t, seq)
	if _, err := d.AddCertificateWithKey(c.der, k); !errors.Is(err, ErrCertificateStoreFull) {
		t.Fatalf("got %v", err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests %v", routes(f))
	}
}

func TestAddCertificateWithKeyNeedsTheMatchingRSAKey(t *testing.T) {
	c, _ := rsaCert(t)
	_, other := rsaCert(t)
	testCerts(t)
	d, f := tlsDevice(t, certSeq(t))
	for name, in := range map[string]struct {
		der []byte
		key *rsa.PrivateKey
	}{
		"other key":   {c.der, other},
		"no key":      {c.der, nil},
		"ECDSA cert":  {certA.der, other},
		"not a cert":  {[]byte("x"), other},
		"empty input": {nil, other},
	} {
		if _, err := d.AddCertificateWithKey(in.der, in.key); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(f.requests) != 0 {
		t.Fatalf("requests %v", routes(f))
	}
}

func TestDeleteCertificateConfirmationLevels(t *testing.T) {
	testCerts(t)
	cases := []struct {
		name    string
		id      string
		confirm Confirmation
		ok      bool
		op      Operation
	}{
		{"certificate, none", certNoKey.id, Confirmation{}, false, OpCertDelete},
		{"certificate, simple", certNoKey.id, confirmed(OpCertDelete), true, ""},
		{"certificate, root op", certNoKey.id, confirmed(OpRootCertDelete), false, OpCertDelete},
		{"root, simple", certRoot.id, confirmed(OpCertDelete), false, OpRootCertDelete},
		{"root, orange", certRoot.id, confirmed(OpRootCertDelete), true, ""},
		{"root, One Click Recovery", certRoot.id, confirmed(OpBootFile), true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, f := tlsDevice(t, certSeq(t))
			err := d.DeleteCertificate(c.id, c.confirm)
			dels := requestsTo(f, "Delete|AMT_PublicKeyCertificate")
			if c.ok {
				if err != nil || len(dels) != 1 || !strings.Contains(dels[0], c.id) {
					t.Fatalf("err %v, deletes %d", err, len(dels))
				}
				return
			}
			var re *RiskyChangeError
			if !errors.As(err, &re) || re.Op != c.op || len(dels) != 0 {
				t.Fatalf("err %v, deletes %d", err, len(dels))
			}
		})
	}
}

func TestDeleteCertificateInUseByTLS(t *testing.T) {
	testCerts(t)
	fault := amterror.NewAMTError("e:InternalError", "in use", "")
	for name, c := range map[string]struct {
		id    string
		inUse bool
	}{"TLS certificate": {certA.id, true}, "other certificate": {certNoKey.id, false}} {
		t.Run(name, func(t *testing.T) {
			seq := certSeq(t)
			seq["Delete|AMT_PublicKeyCertificate"] = []cfgResp{{err: fault}}
			d, _ := tlsDevice(t, seq)
			err := d.DeleteCertificate(c.id, confirmed(OpCertDelete))
			if err == nil || errors.Is(err, ErrCertificateInUse) != c.inUse {
				t.Fatalf("in use %v: %v", c.inUse, err)
			}
		})
	}
}

func TestDeleteCertificateUnknownID(t *testing.T) {
	d, f := tlsDevice(t, certSeq(t))
	if err := d.DeleteCertificate("nope", confirmed(OpCertDelete)); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("got %v", err)
	}
	if len(requestsTo(f, "Delete|AMT_PublicKeyCertificate")) != 0 {
		t.Fatal("deleted")
	}
}

func TestDeleteKeyPair(t *testing.T) {
	d, f := tlsDevice(t, certSeq(t))
	var re *RiskyChangeError
	if err := d.DeleteKeyPair("Intel(r) AMT Key: Handle: 1", Confirmation{}); !errors.As(err, &re) || re.Op != OpCertDelete {
		t.Fatalf("without confirmation: %v", err)
	}
	if err := d.DeleteKeyPair("Intel(r) AMT Key: Handle: 1", confirmed(OpCertDelete)); err != nil {
		t.Fatal(err)
	}
	dels := requestsTo(f, "Delete|AMT_PublicPrivateKeyPair")
	if len(dels) != 1 || !strings.Contains(dels[0], "Intel(r) AMT Key: Handle: 1") {
		t.Fatalf("deletes %v", dels)
	}
	if err := d.DeleteKeyPair("nope", confirmed(OpCertDelete)); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestDeleteKeyPairOfTheTLSCertificateIsInUse(t *testing.T) {
	seq := certSeq(t)
	seq["Delete|AMT_PublicPrivateKeyPair"] = []cfgResp{{err: amterror.NewAMTError("e:InternalError", "in use", "")}}
	d, _ := tlsDevice(t, seq)
	if err := d.DeleteKeyPair("Intel(r) AMT Key: Handle: 0", confirmed(OpCertDelete)); !errors.Is(err, ErrCertificateInUse) {
		t.Fatalf("TLS key: %v", err)
	}
	if err := d.DeleteKeyPair("Intel(r) AMT Key: Handle: 1", confirmed(OpCertDelete)); err == nil || errors.Is(err, ErrCertificateInUse) {
		t.Fatalf("other key: %v", err)
	}
}

func TestCertificateConfirmations(t *testing.T) {
	if got := CertificateConfirmations(Certificate{TrustedRoot: true}); len(got) != 2 || got[0] != OpRootCertDelete || got[1] != OpBootFile {
		t.Errorf("root: %v", got)
	}
	if got := CertificateConfirmations(Certificate{}); len(got) != 1 || got[0] != OpCertDelete {
		t.Errorf("certificate: %v", got)
	}
	if RiskOf(OpCertDelete).Level != LevelSimple || RiskOf(OpRootCertDelete).Level != LevelOrange {
		t.Error("design levels")
	}
}
