package amt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// cfgResp is one canned answer of cfgWSMan: a testdata file, an inline body or an error.
type cfgResp struct {
	file string
	body string
	err  error
}

// cfgWSMan answers per route with a sequence (the last answer repeats) and records requests.
type cfgWSMan struct {
	fakeWSMan
	seq   map[string][]cfgResp
	calls map[string]int
}

func (f *cfgWSMan) Post(msg string) ([]byte, error) {
	f.requests = append(f.requests, msg)
	key := routeKey(msg)
	list, ok := f.seq[key]
	if !ok {
		return nil, fmt.Errorf("fake: no route for %q", key)
	}
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	i := min(f.calls[key], len(list)-1)
	f.calls[key]++
	r := list[i]
	switch {
	case r.err != nil:
		return nil, r.err
	case r.body != "":
		return []byte(r.body), nil
	}
	return os.ReadFile(filepath.Join("testdata", r.file))
}

// requestsFor returns the bodies of all requests with the given route.
func (f *cfgWSMan) requestsFor(route string) []string {
	var out []string
	for _, r := range f.requests {
		if routeKey(r) == route {
			out = append(out, r)
		}
	}
	return out
}

// withProp returns the fixture with the first value of property name replaced (or, with
// value "-", the property removed).
func withProp(t *testing.T, file, name, value string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	open, closeTag := "<h:"+name+">", "</h:"+name+">"
	i := strings.Index(s, open)
	if i < 0 {
		t.Fatalf("%s has no %s", file, name)
	}
	j := strings.Index(s[i:], closeTag) + i + len(closeTag)
	if value == "-" {
		return s[:i] + s[j:]
	}
	return s[:i] + open + value + closeTag + s[j:]
}

func settingsSeq() map[string][]cfgResp {
	return map[string][]cfgResp{
		"Get|AMT_GeneralSettings":                                {{file: "cfg_general_get.xml"}},
		"Enumerate|AMT_EthernetPortSettings":                     {{file: "enumerate.xml"}},
		"Pull|AMT_EthernetPortSettings":                          {{file: "cfg_ethernet_pull.xml"}},
		"Get|AMT_RedirectionService":                             {{file: "cfg_redirection_get.xml"}},
		"Get|CIM_KVMRedirectionSAP":                              {{file: "cfg_kvmsap_get.xml"}},
		"Get|IPS_OptInService":                                   {{file: "cfg_optin_get.xml"}},
		"Enumerate|AMT_TLSSettingData":                           {{file: "enumerate.xml"}},
		"Pull|AMT_TLSSettingData":                                {{file: "cfg_tls_pull.xml"}},
		"GetLowAccuracyTimeSynch|AMT_TimeSynchronizationService": {{file: "cfg_timesync_getlow.xml"}},
		"Get|AMT_EnvironmentDetectionSettingData":                {{file: "cfg_envdetect_get.xml"}},
	}
}

// isWriteRoute reports a request that changes device state.
func isWriteRoute(route string) bool {
	action, _, _ := strings.Cut(route, "|")
	switch action {
	case "Put", "Create", "Delete", "RequestStateChange":
		return true
	}
	for _, p := range []string{"Set", "Add", "Remove", "Update", "Commit"} {
		if strings.HasPrefix(action, p) {
			return true
		}
	}
	return false
}

func assertNoWrites(t *testing.T, f *cfgWSMan) {
	t.Helper()
	for _, r := range f.requests {
		if isWriteRoute(routeKey(r)) {
			t.Errorf("unexpected write request %s", routeKey(r))
		}
	}
}

func cfgDevice(seq map[string][]cfgResp) (*Device, *cfgWSMan) {
	f := &cfgWSMan{seq: seq}
	d := newDevice(f, "")
	d.cfg.Username = "admin"
	d.cfg.Host = testHost
	return d, f
}

// fixedNow sets timeNow for one test.
func fixedNow(t *testing.T, at time.Time) {
	t.Helper()
	old := timeNow
	timeNow = func() time.Time { return at }
	t.Cleanup(func() { timeNow = old })
}

func TestSettingsFull(t *testing.T) {
	fixedNow(t, time.Unix(1767225590, 0)) // device clock (Ta0 1767225600) is 10 s ahead
	d, f := cfgDevice(settingsSeq())
	got, err := d.Settings()
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{
		General: GeneralSettings{
			HostName: "amt-test", DomainName: "example.com", SharedFQDN: false,
			HostOSFQDN: "host-test.example.com", DigestRealm: "Digest:00000000000000000000000000000000",
			PingResponse: true, RMCPPingResponse: true,
			DynamicDNS: DDNSByDHCPServer, DDNSIntervalMinutes: 1440, DDNSTTLSeconds: 900,
			NetworkEnabled: true, NetworkInterfaceEnabled: true, WSManOnly: false,
			IdleWakeTimeoutMinutes: 65535, PrivacyLevel: 0, PowerSource: 0,
		},
		Network: NetworkSettings{
			Available: true, DHCP: true, IPSync: false,
			IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0", Gateway: "192.0.2.1",
			PrimaryDNS: "192.0.2.53", SecondaryDNS: "192.0.2.54",
			MACAddress: "00-00-5e-00-53-01", LinkUp: true, SharedMAC: true,
		},
		Features: Features{Redirection: true, SOL: true, IDER: true, KVM: true, KVMAvailable: true},
		Consent: ConsentSettings{Available: true, Policy: ConsentNone, CanModify: true,
			CodeTimeoutSeconds: 120, DisplayTimeoutSeconds: 300},
		TLS: TLSSettings{
			Remote: TLSInterface{Available: true, Enabled: true},
			Local:  TLSInterface{Available: true, AcceptNonSecure: true, NonSecureSupported: true, TrustedCNs: []string{"ca.example.com"}},
		},
		Clock:       ClockInfo{Available: true, Time: time.Unix(1767225600, 0).UTC(), Offset: 10 * time.Second},
		Environment: EnvironmentDetection{Available: true, Domains: []string{"intranet.example.com"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Settings:\n got %+v\nwant %+v", got, want)
	}
	assertNoWrites(t, f)
}

func TestSettingsDecodesStates(t *testing.T) {
	tests := []struct {
		name string
		mod  func(t *testing.T, seq map[string][]cfgResp)
		chk  func(t *testing.T, s Settings)
	}{
		{"redirection only IDE-R, KVM disabled, listener off", func(t *testing.T, seq map[string][]cfgResp) {
			r := withProp(t, "cfg_redirection_get.xml", "EnabledState", "32769")
			seq["Get|AMT_RedirectionService"] = []cfgResp{{body: strings.Replace(r, "<h:ListenerEnabled>true", "<h:ListenerEnabled>false", 1)}}
			seq["Get|CIM_KVMRedirectionSAP"] = []cfgResp{{body: withProp(t, "cfg_kvmsap_get.xml", "EnabledState", "3")}}
		}, func(t *testing.T, s Settings) {
			if want := (Features{IDER: true, KVMAvailable: true}); s.Features != want {
				t.Errorf("Features = %+v", s.Features)
			}
		}},
		{"KVM SAP absent", func(t *testing.T, seq map[string][]cfgResp) {
			seq["Get|CIM_KVMRedirectionSAP"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
		}, func(t *testing.T, s Settings) {
			if s.Features.KVMAvailable || s.Features.KVM || !s.Features.SOL {
				t.Errorf("Features = %+v", s.Features)
			}
		}},
		{"consent all and KVM", func(t *testing.T, seq map[string][]cfgResp) {
			seq["Get|IPS_OptInService"] = []cfgResp{{body: withProp(t, "cfg_optin_get.xml", "OptInRequired", "4294967295")}}
		}, func(t *testing.T, s Settings) {
			if s.Consent.Policy != ConsentAll {
				t.Errorf("Policy = %v", s.Consent.Policy)
			}
		}},
		{"consent KVM, not modifiable", func(t *testing.T, seq map[string][]cfgResp) {
			o := withProp(t, "cfg_optin_get.xml", "OptInRequired", "1")
			seq["Get|IPS_OptInService"] = []cfgResp{{body: strings.Replace(o, "<h:CanModifyOptInPolicy>1", "<h:CanModifyOptInPolicy>0", 1)}}
		}, func(t *testing.T, s Settings) {
			if s.Consent.Policy != ConsentKVM || s.Consent.CanModify {
				t.Errorf("Consent = %+v", s.Consent)
			}
		}},
		{"DDNS client", func(t *testing.T, seq map[string][]cfgResp) {
			seq["Get|AMT_GeneralSettings"] = []cfgResp{{body: withProp(t, "cfg_general_get.xml", "DDNSUpdateEnabled", "true")}}
		}, func(t *testing.T, s Settings) {
			if s.General.DynamicDNS != DDNSClient {
				t.Errorf("DynamicDNS = %v", s.General.DynamicDNS)
			}
		}},
		{"environment detection without domains", func(t *testing.T, seq map[string][]cfgResp) {
			seq["Get|AMT_EnvironmentDetectionSettingData"] = []cfgResp{{body: withProp(t, "cfg_envdetect_get.xml", "DetectionStrings", "-")}}
		}, func(t *testing.T, s Settings) {
			if !s.Environment.Available || s.Environment.Domains != nil {
				t.Errorf("Environment = %+v", s.Environment)
			}
		}},
		{"clock method fails", func(t *testing.T, seq map[string][]cfgResp) {
			seq["GetLowAccuracyTimeSynch|AMT_TimeSynchronizationService"] = []cfgResp{{body: withProp(t, "cfg_timesync_getlow.xml", "ReturnValue", "1")}}
		}, func(t *testing.T, s Settings) {
			if s.Clock.Available {
				t.Errorf("Clock = %+v", s.Clock)
			}
		}},
		{"no wired port", func(t *testing.T, seq map[string][]cfgResp) {
			seq["Pull|AMT_EthernetPortSettings"] = []cfgResp{{body: withProp(t, "cfg_ethernet_pull.xml", "PhysicalConnectionType", "3")}}
		}, func(t *testing.T, s Settings) {
			if s.Network.Available {
				t.Errorf("Network = %+v", s.Network)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seq := settingsSeq()
			tt.mod(t, seq)
			d, _ := cfgDevice(seq)
			s, err := d.Settings()
			if err != nil {
				t.Fatal(err)
			}
			tt.chk(t, s)
		})
	}
}

func TestSettingsErrors(t *testing.T) {
	t.Run("access denied", func(t *testing.T) {
		seq := settingsSeq()
		seq["Get|IPS_OptInService"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
		d, _ := cfgDevice(seq)
		if _, err := d.Settings(); !errors.Is(err, ErrAccessDenied) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("general settings fault is fatal", func(t *testing.T) {
		seq := settingsSeq()
		seq["Get|AMT_GeneralSettings"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
		d, _ := cfgDevice(seq)
		if _, err := d.Settings(); err == nil || !strings.Contains(err.Error(), "AMT_GeneralSettings") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("transport error names the class", func(t *testing.T) {
		seq := settingsSeq()
		seq["Get|AMT_RedirectionService"] = []cfgResp{{err: errors.New("connection reset")}}
		d, _ := cfgDevice(seq)
		if _, err := d.Settings(); err == nil || !strings.Contains(err.Error(), "AMT_RedirectionService") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("missing optional class leaves the section empty", func(t *testing.T) {
		seq := settingsSeq()
		seq["Get|AMT_EnvironmentDetectionSettingData"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
		seq["Enumerate|AMT_TLSSettingData"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
		d, _ := cfgDevice(seq)
		s, err := d.Settings()
		if err != nil || s.Environment.Available || s.TLS.Remote.Available || !s.Network.Available {
			t.Errorf("s = %+v, err %v", s, err)
		}
	})
}
