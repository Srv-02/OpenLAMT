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

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

var allSettingsParts = []SettingsPart{SettingsGeneral, SettingsNetwork, SettingsFeatures, SettingsKVM,
	SettingsConsent, SettingsTLS, SettingsClock, SettingsEnvironment}

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

// fullSettings is what Settings decodes from settingsSeq at fixedNow(1767225590).
func fullSettings() Settings {
	return Settings{
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
}

// TestSettingsFull is the admin case: every section is read and none is marked unreadable.
func TestSettingsFull(t *testing.T) {
	fixedNow(t, time.Unix(1767225590, 0)) // device clock (Ta0 1767225600) is 10 s ahead
	d, f := cfgDevice(settingsSeq())
	got, err := d.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if want := fullSettings(); !reflect.DeepEqual(got, want) {
		t.Errorf("Settings:\n got %+v\nwant %+v", got, want)
	}
	for _, p := range allSettingsParts {
		if !got.Readable(p) {
			t.Errorf("part %b unreadable for an admin account", p)
		}
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

// otherFault is a SOAP fault that is neither AccessDenied nor DestinationUnreachable.
func otherFault(t *testing.T) error {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "unreachable_fault.xml"))
	if err != nil {
		t.Fatal(err)
	}
	return amterror.DecodeAMTErrorString(strings.Replace(string(b), "DestinationUnreachable", "InternalError", 1))
}

func TestSettingsErrors(t *testing.T) {
	t.Run("general settings fault other than a refusal is fatal", func(t *testing.T) {
		seq := settingsSeq()
		seq["Get|AMT_GeneralSettings"] = []cfgResp{{err: otherFault(t)}}
		d, _ := cfgDevice(seq)
		if _, err := d.Settings(); err == nil || !strings.Contains(err.Error(), "AMT_GeneralSettings") || errors.Is(err, ErrAccessDenied) {
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
		d, _ := cfgDevice(seq)
		s, err := d.Settings()
		if err != nil || s.Environment.Available || !s.Network.Available || !s.TLS.Remote.Available || s.Unreadable != 0 {
			t.Errorf("s = %+v, err %v", s, err)
		}
	})
}

// Issue #58: a section the account may not read is marked unreadable; the other sections keep
// their values instead of failing the whole read.
func TestSettingsUnreadableSection(t *testing.T) {
	denied := func(t *testing.T) error { return faultErr(t, "access_denied_fault.xml") }
	forbidden := func(*testing.T) error { return errors.New("wsman.Client post received: 403 Forbidden\n<html/>") }
	unreachable := func(t *testing.T) error { return faultErr(t, "unreachable_fault.xml") }
	tests := []struct {
		name  string
		route string
		err   func(*testing.T) error
		part  SettingsPart
		clear func(*Settings) // the values that are no longer read
	}{
		{"general denied", "Get|AMT_GeneralSettings", denied, SettingsGeneral, func(s *Settings) { s.General = GeneralSettings{} }},
		{"general forbidden", "Get|AMT_GeneralSettings", forbidden, SettingsGeneral, func(s *Settings) { s.General = GeneralSettings{} }},
		// a class every firmware has: DestinationUnreachable is the refusal, as in Status
		{"general unreachable", "Get|AMT_GeneralSettings", unreachable, SettingsGeneral, func(s *Settings) { s.General = GeneralSettings{} }},
		{"network denied", "Enumerate|AMT_EthernetPortSettings", denied, SettingsNetwork, func(s *Settings) { s.Network = NetworkSettings{} }},
		{"network unreachable on Enumerate", "Enumerate|AMT_EthernetPortSettings", unreachable, SettingsNetwork, func(s *Settings) { s.Network = NetworkSettings{} }},
		{"network fault on Pull", "Pull|AMT_EthernetPortSettings", unreachable, SettingsNetwork, func(s *Settings) { s.Network = NetworkSettings{} }},
		{"network denied on Pull", "Pull|AMT_EthernetPortSettings", denied, SettingsNetwork, func(s *Settings) { s.Network = NetworkSettings{} }},
		{"redirection denied", "Get|AMT_RedirectionService", denied, SettingsFeatures, func(s *Settings) {
			s.Features.Redirection, s.Features.SOL, s.Features.IDER = false, false, false
		}},
		{"KVM SAP denied", "Get|CIM_KVMRedirectionSAP", denied, SettingsKVM, func(s *Settings) {
			s.Features.KVM, s.Features.KVMAvailable = false, false
		}},
		{"consent denied", "Get|IPS_OptInService", denied, SettingsConsent, func(s *Settings) { s.Consent = ConsentSettings{} }},
		{"TLS denied", "Enumerate|AMT_TLSSettingData", denied, SettingsTLS, func(s *Settings) { s.TLS = TLSSettings{} }},
		{"TLS unreachable on Enumerate", "Enumerate|AMT_TLSSettingData", unreachable, SettingsTLS, func(s *Settings) { s.TLS = TLSSettings{} }},
		{"TLS fault on Pull", "Pull|AMT_TLSSettingData", unreachable, SettingsTLS, func(s *Settings) { s.TLS = TLSSettings{} }},
		{"clock denied", "GetLowAccuracyTimeSynch|AMT_TimeSynchronizationService", denied, SettingsClock, func(s *Settings) { s.Clock = ClockInfo{} }},
		{"environment denied", "Get|AMT_EnvironmentDetectionSettingData", denied, SettingsEnvironment, func(s *Settings) { s.Environment = EnvironmentDetection{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixedNow(t, time.Unix(1767225590, 0))
			seq := settingsSeq()
			seq[tt.route] = []cfgResp{{err: tt.err(t)}}
			d, f := cfgDevice(seq)
			got, err := d.Settings()
			if err != nil {
				t.Fatalf("err = %v, want the other sections", err)
			}
			want := fullSettings()
			tt.clear(&want)
			want.Unreadable = tt.part
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Settings:\n got %+v\nwant %+v", got, want)
			}
			if got.Readable(tt.part) {
				t.Errorf("Readable(%b) = true", tt.part)
			}
			assertNoWrites(t, f)
		})
	}
}

// The account of issue #58 (Redirection realm only, measured on AMT 16): General settings are
// refused, the network and the redirection services are readable.
func TestSettingsRedirectionOnlyAccount(t *testing.T) {
	fixedNow(t, time.Unix(1767225590, 0))
	seq := settingsSeq()
	for _, route := range []string{"Get|AMT_GeneralSettings", "Enumerate|AMT_TLSSettingData",
		"GetLowAccuracyTimeSynch|AMT_TimeSynchronizationService", "Get|AMT_EnvironmentDetectionSettingData"} {
		seq[route] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	}
	d, _ := cfgDevice(seq)
	s, err := d.Settings()
	if err != nil {
		t.Fatal(err)
	}
	want := SettingsGeneral | SettingsTLS | SettingsClock | SettingsEnvironment
	if s.Unreadable != want || !s.Network.Available || s.Network.IPAddress != "192.0.2.10" || !s.Features.SOL || !s.Consent.Available {
		t.Errorf("Unreadable %b, want %b; %+v", s.Unreadable, want, s)
	}
}

// When no section at all can be read, Settings fails with the refusal as before.
func TestSettingsNothingReadable(t *testing.T) {
	for _, missingEnv := range []bool{false, true} {
		seq := settingsSeq()
		for route := range seq {
			if !strings.HasPrefix(route, "Pull|") {
				seq[route] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
			}
		}
		if missingEnv { // a missing class is no readable section
			seq["Get|AMT_EnvironmentDetectionSettingData"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
		}
		d, _ := cfgDevice(seq)
		if s, err := d.Settings(); !errors.Is(err, ErrAccessDenied) || !strings.Contains(err.Error(), "AMT_GeneralSettings") {
			t.Errorf("missing env %v: err = %v, s = %+v", missingEnv, err, s)
		}
	}
}

// A section read with an empty result (no wired port, clock method error) counts as read.
func TestSettingsReadButEmptySectionIsReadable(t *testing.T) {
	seq := settingsSeq()
	seq["Pull|AMT_EthernetPortSettings"] = []cfgResp{{body: withProp(t, "cfg_ethernet_pull.xml", "PhysicalConnectionType", "3")}}
	seq["GetLowAccuracyTimeSynch|AMT_TimeSynchronizationService"] = []cfgResp{{body: withProp(t, "cfg_timesync_getlow.xml", "ReturnValue", "1")}}
	d, _ := cfgDevice(seq)
	s, err := d.Settings()
	if err != nil || s.Unreadable != 0 || s.Network.Available || s.Clock.Available {
		t.Errorf("s = %+v, err %v", s, err)
	}
}
