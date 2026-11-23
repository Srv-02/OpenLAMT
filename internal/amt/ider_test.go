package amt

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/ider/idertest"
)

type testImage struct {
	closed atomic.Int32
}

func (m *testImage) Size() int64                           { return 4096 }
func (m *testImage) ReadAt(p []byte, _ int64) (int, error) { return len(p), nil }
func (m *testImage) Close() error                          { m.closed.Add(1); return nil }

func iderRoutes() map[string]string {
	return map[string]string{
		"Get|AMT_BootCapabilities":   "boot_capabilities_ider_true.xml",
		"Get|AMT_RedirectionService": "redirection_get.xml",
		"Get|IPS_OptInService":       "optin_get_none.xml",
	}
}

func TestEnableIDER_States(t *testing.T) {
	cases := []struct {
		file string
		want string // RequestedState, empty = no change
	}{
		{"redirection_get_32768.xml", "32769"},
		{"redirection_get_32769.xml", ""},
		{"redirection_get_32770.xml", "32771"},
		{"redirection_get.xml", ""},
	}
	for _, c := range cases {
		routes := map[string]string{"Get|AMT_RedirectionService": c.file, "RequestStateChange|AMT_RedirectionService": "kvm_requeststatechange.xml"}
		f := &fakeWSMan{routes: routes}
		if err := newDevice(f, "").EnableIDER(); err != nil {
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

func TestEnableIDER_UnknownState(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{"Get|AMT_RedirectionService": "redirection_get_3.xml"}}
	if err := newDevice(f, "").EnableIDER(); err == nil || len(f.requests) != 1 {
		t.Fatalf("err=%v requests=%d", err, len(f.requests))
	}
}

func TestEnableIDER_EnablesListener(t *testing.T) {
	routes := map[string]string{"Get|AMT_RedirectionService": "redirection_get_nolistener.xml", "Put|AMT_RedirectionService": "redirection_put.xml"}
	f := &fakeWSMan{routes: routes}
	if err := newDevice(f, "").EnableIDER(); err != nil {
		t.Fatal(err)
	}
	if got := routeKeys(f); !reflect.DeepEqual(got, []string{"Get|AMT_RedirectionService", "Put|AMT_RedirectionService"}) {
		t.Fatalf("%v", got)
	}
}

func TestEnableIDER_ReturnValueError(t *testing.T) {
	routes := map[string]string{"Get|AMT_RedirectionService": "redirection_get_32768.xml", "RequestStateChange|AMT_RedirectionService": "kvm_requeststatechange_fail.xml"}
	err := newDevice(&fakeWSMan{routes: routes}, "").EnableIDER()
	var ie *IDERError
	if !errors.As(err, &ie) || ie.ReturnValue != 2 || ie.Step == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestEnableIDER_EmptyResponses(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{"Get|AMT_RedirectionService": "empty_body.xml"}}
	if err := newDevice(f, "").EnableIDER(); err == nil || len(f.requests) != 1 {
		t.Fatalf("err=%v", err)
	}
	routes := map[string]string{"Get|AMT_RedirectionService": "redirection_get_32768.xml", "RequestStateChange|AMT_RedirectionService": "empty_body.xml"}
	if err := newDevice(&fakeWSMan{routes: routes}, "").EnableIDER(); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("err = %v", err)
	}
}

func TestIDERConsentRule(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		kvm     bool // UserConsent (KVM) instead of IDE-R
		prompts int
	}{
		{"ider, none", "optin_get_none.xml", false, 0},
		{"ider, kvm only", "optin_get_kvm_notstarted.xml", false, 0},
		{"ider, all", "optin_get_all_notstarted.xml", false, 1},
		{"ider, all in session", "optin_get_all_insession.xml", false, 0},
		{"kvm, kvm only", "optin_get_kvm_notstarted.xml", true, 1},
	}
	for _, c := range cases {
		routes := map[string]string{"Get|IPS_OptInService": c.file, "StartOptIn|IPS_OptInService": "optin_start.xml", "SendOptInCode|IPS_OptInService": "optin_send.xml"}
		asked := 0
		prompt := consentFunc(func() (string, error) { asked++; return "123456", nil })
		d := newDevice(&fakeWSMan{routes: routes}, "")
		var err error
		if c.kvm {
			err = d.UserConsent(prompt)
		} else {
			err = d.userConsent(prompt, true)
		}
		if err != nil || asked != c.prompts {
			t.Fatalf("%s: err=%v asked=%d", c.name, err, asked)
		}
	}
}

func TestOpenIDER_Dials(t *testing.T) {
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "pw"})
	f := &fakeWSMan{routes: iderRoutes()}
	d := newDevice(f, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: dialToAddr(srv.Addr)}
	go srv.AcceptSession(t)
	img := &testImage{}
	c, err := d.OpenIDER(context.Background(), img, ider.StartNow, nil)
	if err != nil {
		t.Fatal(err)
	}
	if img.closed.Load() != 0 {
		t.Fatal("image closed on success")
	}
	if err := c.Close(); err != nil || img.closed.Load() != 1 {
		t.Fatalf("close: %v closed=%d", err, img.closed.Load())
	}
	if got := routeKeys(f); !reflect.DeepEqual(got, []string{"Get|AMT_BootCapabilities", "Get|AMT_RedirectionService", "Get|IPS_OptInService"}) {
		t.Fatalf("%v", got)
	}
}

func TestOpenIDER_Unsupported(t *testing.T) {
	routes := iderRoutes()
	routes["Get|AMT_BootCapabilities"] = "boot_capabilities_ider_false.xml"
	f := &fakeWSMan{routes: routes}
	img := &testImage{}
	_, err := newDevice(f, "").OpenIDER(context.Background(), img, ider.StartNow, nil)
	if !errors.Is(err, ErrIDERUnsupported) || len(f.requests) != 1 || img.closed.Load() != 0 {
		t.Fatalf("err=%v requests=%d closed=%d", err, len(f.requests), img.closed.Load())
	}
}

func TestOpenIDER_ConsentFailureKeepsImage(t *testing.T) {
	routes := iderRoutes()
	routes["Get|IPS_OptInService"] = "optin_get_all_notstarted.xml"
	dials := 0
	d := newDevice(&fakeWSMan{routes: routes}, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("must not dial")
	}}
	img := &testImage{}
	_, err := d.OpenIDER(context.Background(), img, ider.StartNow, nil)
	if !errors.Is(err, ErrConsentRequired) || dials != 0 || img.closed.Load() != 0 {
		t.Fatalf("err=%v dials=%d closed=%d", err, dials, img.closed.Load())
	}
}

func TestOpenIDER_DialFailureKeepsImage(t *testing.T) {
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "other"})
	d := newDevice(&fakeWSMan{routes: iderRoutes()}, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: dialToAddr(srv.Addr)}
	img := &testImage{}
	if _, err := d.OpenIDER(context.Background(), img, ider.StartNow, nil); err == nil || img.closed.Load() != 0 {
		t.Fatalf("err=%v closed=%d", err, img.closed.Load())
	}
}

func TestOpenIDER_TLSWithoutPinnedConfigFails(t *testing.T) {
	dials := 0
	d := newDevice(&fakeWSMan{routes: iderRoutes()}, "")
	d.cfg = Config{Host: "device.example.com", Username: "admin", Password: "pw", UseTLS: true,
		dialContext: func(context.Context, string, string) (net.Conn, error) { dials++; return nil, errors.New("no") }}
	img := &testImage{}
	if _, err := d.OpenIDER(context.Background(), img, ider.StartNow, nil); err == nil || dials != 0 || img.closed.Load() != 0 {
		t.Fatalf("err=%v dials=%d", err, dials)
	}
}

func isoRoutes(pull, get string) map[string]string {
	r := biosRoutes(pull)
	r["Get|AMT_BootSettingData"] = get
	return r
}

func TestBootToISO(t *testing.T) {
	for _, tc := range []struct{ pull, state string }{
		{"power_state_pull.xml", "10"}, {"power_state_pull_off.xml", "2"},
	} {
		f := &fakeWSMan{routes: isoRoutes(tc.pull, "boot_settingdata_get_ider.xml")}
		if err := newDevice(f, "").Power(BootToISO); err != nil {
			t.Fatal(err)
		}
		if keys := requestKeys(f); !reflect.DeepEqual(keys, biosSequence) {
			t.Fatalf("sequence %v", keys)
		}
		put := f.requests[4]
		for _, s := range []string{"<h:UseIDER>true</h:UseIDER>", "<h:IDERBootDevice>1</h:IDERBootDevice>", "<h:BIOSSetup>false</h:BIOSSetup>",
			"<h:EnforceSecureBoot>true</h:EnforceSecureBoot>", "<h:InstanceID>Intel(r) AMT:BootSettingData 0</h:InstanceID>"} {
			if !strings.Contains(put, s) {
				t.Fatalf("put lacks %s: %s", s, put)
			}
		}
		if !strings.Contains(f.requests[7], "<h:PowerState>"+tc.state+"</h:PowerState>") {
			t.Fatalf("final action: %s", f.requests[7])
		}
	}
}

func TestBootToISOSetBootConfigRoleFailure(t *testing.T) {
	routes := isoRoutes("power_state_pull.xml", "boot_settingdata_get.xml")
	routes["SetBootConfigRole|CIM_BootService"] = "setbootconfigrole_fail.xml"
	f := &fakeWSMan{routes: routes}
	err := newDevice(f, "").Power(BootToISO)
	var pe *PowerError
	if !errors.As(err, &pe) || pe.ReturnValue != 2 || pe.Action != BootToISO || len(f.requests) != 8 { // with the undo
		t.Fatalf("err=%v requests=%d", err, len(f.requests))
	}
}

func TestNextBootFromISOHasNoPowerCommand(t *testing.T) {
	f := &fakeWSMan{routes: isoRoutes("power_state_pull.xml", "boot_settingdata_get.xml")}
	if err := newDevice(f, "").NextBootFromISO(); err != nil {
		t.Fatal(err)
	}
	want := []string{"Get|AMT_BootSettingData", "ChangeBootOrder|CIM_BootConfigSetting", "Put|AMT_BootSettingData",
		"SetBootConfigRole|CIM_BootService", "ChangeBootOrder|CIM_BootConfigSetting"}
	if keys := requestKeys(f); !reflect.DeepEqual(keys, want) {
		t.Fatalf("sequence %v", keys)
	}
	if !strings.Contains(f.requests[2], "<h:UseIDER>true</h:UseIDER>") || !strings.Contains(f.requests[2], "<h:IDERBootDevice>1</h:IDERBootDevice>") {
		t.Fatalf("put: %s", f.requests[2])
	}
}

func TestClearISOBoot(t *testing.T) {
	f := &fakeWSMan{routes: isoRoutes("power_state_pull.xml", "boot_settingdata_get_ider.xml")}
	if err := newDevice(f, "").ClearISOBoot(); err != nil {
		t.Fatal(err)
	}
	if keys := requestKeys(f); !reflect.DeepEqual(keys, []string{"Get|AMT_BootSettingData", "Put|AMT_BootSettingData"}) {
		t.Fatalf("sequence %v", keys)
	}
	if put := f.requests[1]; !strings.Contains(put, "<h:UseIDER>false</h:UseIDER>") || !strings.Contains(put, "<h:IDERBootDevice>0</h:IDERBootDevice>") {
		t.Fatalf("put: %s", put)
	}
}

func TestClearISOBootWritesNothingWhenNotSet(t *testing.T) {
	f := &fakeWSMan{routes: isoRoutes("power_state_pull.xml", "boot_settingdata_get.xml")}
	if err := newDevice(f, "").ClearISOBoot(); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests %v", requestKeys(f))
	}
}

// Issue #57: an account limited to the Redirection realm may not read AMT_BootCapabilities.
// OpenIDER then tries the session anyway instead of reporting the missing permission.
func TestOpenIDER_BootCapabilitiesRefusedTriesAnyway(t *testing.T) {
	for name, refusal := range map[string]func(*testing.T) error{
		"AccessDenied fault": func(t *testing.T) error { return faultErr(t, "access_denied_fault.xml") },
		"HTTP 403":           func(*testing.T) error { return errors.New("wsman.Client post received: 403 Forbidden\n<html/>") },
	} {
		t.Run(name, func(t *testing.T) {
			srv := idertest.Start(t, idertest.Options{Username: "kvm", Password: "pw"})
			seq := map[string][]cfgResp{"Get|AMT_BootCapabilities": {{err: refusal(t)}}}
			for route, file := range iderRoutes() {
				if _, ok := seq[route]; !ok {
					seq[route] = []cfgResp{{file: file}}
				}
			}
			d, f := cfgDevice(seq)
			d.cfg = Config{Host: "192.0.2.10", Username: "kvm", Password: "pw", dialContext: dialToAddr(srv.Addr)}
			go srv.AcceptSession(t)
			img := &testImage{}
			c, err := d.OpenIDER(context.Background(), img, ider.StartNow, nil)
			if err != nil {
				t.Fatalf("err = %v, want a session", err)
			}
			defer c.Close()
			if got := routeKeys(&f.fakeWSMan); !reflect.DeepEqual(got, []string{"Get|AMT_BootCapabilities", "Get|AMT_RedirectionService", "Get|IPS_OptInService"}) {
				t.Fatalf("%v", got)
			}
		})
	}
}

// Admin regression: any other failure of the BootCapabilities read still stops OpenIDER.
func TestOpenIDER_BootCapabilitiesErrorStops(t *testing.T) {
	seq := map[string][]cfgResp{"Get|AMT_BootCapabilities": {{err: errors.New("connection reset")}}}
	d, f := cfgDevice(seq)
	img := &testImage{}
	if _, err := d.OpenIDER(context.Background(), img, ider.StartNow, nil); err == nil || !strings.Contains(err.Error(), "connection reset") || len(f.requests) != 1 {
		t.Fatalf("err = %v, requests %d", err, len(f.requests))
	}
}

func TestNeverEnableRefusesWithoutWriting(t *testing.T) {
	enableKVM := func(d *Device) error { return d.EnableKVM() }
	enableSOL := func(d *Device) error { return d.EnableSOL() }
	enableIDER := func(d *Device) error { return d.EnableIDER() }
	cases := []struct {
		name    string
		redir   string
		kvm     string
		enable  func(*Device) error
		feature string
	}{
		{"listener off KVM", "redirection_get_nolistener.xml", "kvm_get_enabled.xml", enableKVM, "redirection"},
		{"listener off SOL", "redirection_get_nolistener.xml", "", enableSOL, "redirection"},
		{"listener off IDE-R", "redirection_get_nolistener.xml", "", enableIDER, "redirection"},
		{"KVM off", "redirection_get.xml", "kvm_get_disabled.xml", enableKVM, "KVM"},
		{"SOL off", "redirection_get_32769.xml", "", enableSOL, "SOL"},
		{"IDE-R off", "redirection_get_32770.xml", "", enableIDER, "IDE-R"},
	}
	for _, c := range cases {
		routes := map[string]string{"Get|AMT_RedirectionService": c.redir}
		if c.kvm != "" {
			routes["Get|CIM_KVMRedirectionSAP"] = c.kvm
		}
		f := &fakeWSMan{routes: routes}
		d := newDevice(f, "")
		d.cfg.NeverEnable = true
		err := c.enable(d)
		var re *RedirectionOffError
		if !errors.As(err, &re) || re.Feature != c.feature {
			t.Fatalf("%s: err = %v", c.name, err)
		}
		for _, k := range routeKeys(f) {
			if !strings.HasPrefix(k, "Get|") {
				t.Fatalf("%s: wrote to the device: %v", c.name, routeKeys(f))
			}
		}
	}
	// Everything on: no write needed, so NeverEnable changes nothing.
	for name, enable := range map[string]func(*Device) error{"KVM": enableKVM, "SOL": enableSOL, "IDE-R": enableIDER} {
		f := &fakeWSMan{routes: map[string]string{"Get|AMT_RedirectionService": "redirection_get.xml", "Get|CIM_KVMRedirectionSAP": "kvm_get_enabled.xml"}}
		d := newDevice(f, "")
		d.cfg.NeverEnable = true
		if err := enable(d); err != nil {
			t.Fatalf("%s on: %v", name, err)
		}
		for _, k := range routeKeys(f) {
			if !strings.HasPrefix(k, "Get|") {
				t.Fatalf("%s on: wrote: %v", name, routeKeys(f))
			}
		}
	}
}
