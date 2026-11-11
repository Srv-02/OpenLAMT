package amt

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/Srv-02/openlamt/internal/sol/soltest"
)

func solRoutes() map[string]string {
	return map[string]string{
		"Get|AMT_RedirectionService": "redirection_get.xml",
		"Get|IPS_OptInService":       "optin_get_none.xml",
	}
}

func TestEnableSOL_States(t *testing.T) {
	cases := []struct {
		file string
		want string // RequestedState, empty = no change
	}{
		{"redirection_get_32768.xml", "32770"}, // nothing -> SOL
		{"redirection_get_32769.xml", "32771"}, // IDE-R -> IDE-R and SOL (IDE-R stays)
		{"redirection_get_32770.xml", ""},
		{"redirection_get.xml", ""}, // 32771
	}
	for _, c := range cases {
		routes := map[string]string{"Get|AMT_RedirectionService": c.file, "RequestStateChange|AMT_RedirectionService": "kvm_requeststatechange.xml"}
		f := &fakeWSMan{routes: routes}
		if err := newDevice(f, "").EnableSOL(); err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if c.want == "" {
			if len(f.requests) != 1 {
				t.Fatalf("%s: requests %v", c.file, routeKeys(f))
			}
			continue
		}
		if len(f.requests) != 2 || !strings.Contains(f.requests[1], "<h:RequestedState>"+c.want+"</h:RequestedState>") {
			t.Fatalf("%s: requests %v", c.file, f.requests)
		}
	}
}

func TestEnableSOL_UnknownState(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{"Get|AMT_RedirectionService": "redirection_get_3.xml"}}
	if err := newDevice(f, "").EnableSOL(); err == nil || len(f.requests) != 1 {
		t.Fatalf("err=%v requests=%d", err, len(f.requests))
	}
}

func TestEnableSOL_EnablesListener(t *testing.T) {
	routes := map[string]string{"Get|AMT_RedirectionService": "redirection_get_nolistener.xml", "Put|AMT_RedirectionService": "redirection_put.xml"}
	f := &fakeWSMan{routes: routes}
	if err := newDevice(f, "").EnableSOL(); err != nil {
		t.Fatal(err)
	}
	if got := routeKeys(f); !reflect.DeepEqual(got, []string{"Get|AMT_RedirectionService", "Put|AMT_RedirectionService"}) {
		t.Fatalf("%v", got)
	}
}

func TestEnableSOL_ReturnValueError(t *testing.T) {
	routes := map[string]string{"Get|AMT_RedirectionService": "redirection_get_32768.xml", "RequestStateChange|AMT_RedirectionService": "kvm_requeststatechange_fail.xml"}
	err := newDevice(&fakeWSMan{routes: routes}, "").EnableSOL()
	var se *SOLError
	if !errors.As(err, &se) || se.ReturnValue != 2 || se.Step == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestEnableSOL_EmptyResponses(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{"Get|AMT_RedirectionService": "empty_body.xml"}}
	if err := newDevice(f, "").EnableSOL(); err == nil || len(f.requests) != 1 {
		t.Fatalf("err=%v", err)
	}
	routes := map[string]string{"Get|AMT_RedirectionService": "redirection_get_32768.xml", "RequestStateChange|AMT_RedirectionService": "empty_body.xml"}
	if err := newDevice(&fakeWSMan{routes: routes}, "").EnableSOL(); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenSOL_Dials(t *testing.T) {
	srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
	f := &fakeWSMan{routes: solRoutes()}
	d := newDevice(f, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: dialToAddr(srv.Addr)}
	go srv.AcceptSession(t)
	c, err := d.OpenSOL(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if got := routeKeys(f); !reflect.DeepEqual(got, []string{"Get|AMT_RedirectionService", "Get|IPS_OptInService"}) {
		t.Fatalf("%v", got)
	}
}

func TestOpenSOL_ConsentRule(t *testing.T) {
	cases := []struct {
		file    string
		wantErr error
	}{
		{"optin_get_kvm_notstarted.xml", nil}, // consent only for KVM: SOL needs none
		{"optin_get_all_notstarted.xml", ErrConsentRequired},
	}
	for _, c := range cases {
		srv := soltest.Start(t, soltest.Options{Username: "admin", Password: "pw"})
		routes := solRoutes()
		routes["Get|IPS_OptInService"] = c.file
		d := newDevice(&fakeWSMan{routes: routes}, "")
		d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: dialToAddr(srv.Addr)}
		if c.wantErr == nil {
			go srv.AcceptSession(t)
		}
		sc, err := d.OpenSOL(context.Background(), nil)
		if !errors.Is(err, c.wantErr) {
			t.Fatalf("%s: err = %v", c.file, err)
		}
		if sc != nil {
			sc.Close()
		}
	}
}

func TestOpenSOL_EnableFailureDoesNotDial(t *testing.T) {
	routes := solRoutes()
	routes["Get|AMT_RedirectionService"] = "redirection_get_3.xml"
	dials := 0
	d := newDevice(&fakeWSMan{routes: routes}, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("must not dial")
	}}
	if _, err := d.OpenSOL(context.Background(), nil); err == nil || dials != 0 {
		t.Fatalf("err=%v dials=%d", err, dials)
	}
}

func TestOpenSOL_TLSWithoutPinnedConfigFails(t *testing.T) {
	dials := 0
	d := newDevice(&fakeWSMan{routes: solRoutes()}, "")
	d.cfg = Config{Host: "device.example.com", Username: "admin", Password: "pw", UseTLS: true,
		dialContext: func(context.Context, string, string) (net.Conn, error) { dials++; return nil, errors.New("no") }}
	if _, err := d.OpenSOL(context.Background(), nil); err == nil || dials != 0 {
		t.Fatalf("err=%v dials=%d", err, dials)
	}
}
