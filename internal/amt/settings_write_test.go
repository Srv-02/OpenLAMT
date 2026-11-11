package amt

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// onePut returns the body of the only request with route, failing otherwise.
func onePut(t *testing.T, f *cfgWSMan, route string) string {
	t.Helper()
	reqs := f.requestsFor(route)
	if len(reqs) != 1 {
		t.Fatalf("%d %s requests, want 1", len(reqs), route)
	}
	return reqs[0]
}

func assertContains(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(body, p) {
			t.Errorf("request lacks %s", p)
		}
	}
}

func assertLacks(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(body, p) {
			t.Errorf("request must not contain %s", p)
		}
	}
}

// propCount counts the properties of the Put body element (h: elements minus the wrapper).
func propCount(body string) int {
	_, b, _ := strings.Cut(body, "<Body>")
	return strings.Count(b, "</h:") - 1
}

func writeSeq() map[string][]cfgResp {
	seq := settingsSeq()
	for _, class := range []string{classGeneral, classEthernet, classRedirection, classOptIn, classEnvDetect} {
		seq["Put|"+class] = []cfgResp{{file: "cfg_put_ok.xml"}}
	}
	seq["RequestStateChange|AMT_RedirectionService"] = []cfgResp{{file: "cfg_redirection_rsc.xml"}}
	seq["RequestStateChange|CIM_KVMRedirectionSAP"] = []cfgResp{{file: "kvm_requeststatechange.xml"}}
	seq["SetHighAccuracyTimeSynch|AMT_TimeSynchronizationService"] = []cfgResp{{file: "cfg_timesync_set.xml"}}
	return seq
}

func TestSetGeneralWritesCompleteObjectWithChangedValues(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	err := d.SetGeneral(GeneralChange{HostName: ptr("new-name"), PingResponse: ptr(false), SharedFQDN: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Put|AMT_GeneralSettings")
	assertContains(t, body,
		`<h:AMT_GeneralSettings xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_GeneralSettings">`,
		"<h:HostName>new-name</h:HostName>",
		"<h:PingResponseEnabled>false</h:PingResponseEnabled>",
		"<h:RmcpPingResponseEnabled>true</h:RmcpPingResponseEnabled>",
		"<h:SharedFQDN>false</h:SharedFQDN>",
		"<h:WsmanOnlyMode>false</h:WsmanOnlyMode>",
		"<h:DigestRealm>Digest:00000000000000000000000000000000</h:DigestRealm>",
		"<h:DomainName>example.com</h:DomainName>")
	assertLacks(t, body, "SelectorSet")
	if n := propCount(body); n != 25 {
		t.Errorf("Put has %d properties, want all 25 that were read", n)
	}
}

func TestSetGeneralDynamicDNS(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	if err := d.SetGeneral(GeneralChange{DynamicDNS: ptr(DDNSClient), DDNSIntervalMinutes: ptr(60)}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Put|AMT_GeneralSettings"),
		"<h:DDNSUpdateEnabled>true</h:DDNSUpdateEnabled>",
		"<h:DDNSUpdateByDHCPServerEnabled>false</h:DDNSUpdateByDHCPServerEnabled>",
		"<h:DDNSPeriodicUpdateInterval>60</h:DDNSPeriodicUpdateInterval>",
		"<h:DDNSTTL>900</h:DDNSTTL>")
}

func TestSetGeneralWithoutDifferenceDoesNotWrite(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	err := d.SetGeneral(GeneralChange{HostName: ptr("amt-test"), PingResponse: ptr(true), DynamicDNS: ptr(DDNSByDHCPServer)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)
	if len(f.requestsFor("Get|AMT_GeneralSettings")) != 1 {
		t.Error("the current state must be read before deciding")
	}
}

func TestSetGeneralValidation(t *testing.T) {
	for name, c := range map[string]GeneralChange{
		"host name with underscore": {HostName: ptr("bad_name")},
		"host name too long":        {HostName: ptr(strings.Repeat("a", 64))},
		"host name leading hyphen":  {HostName: ptr("-x")},
		"domain empty label":        {DomainName: ptr("example..com")},
		"domain too long":           {DomainName: ptr(strings.Repeat("a.", 96) + "com")},
		"DDNS interval below 20":    {DDNSIntervalMinutes: ptr(10)},
		"DDNS TTL zero":             {DDNSTTLSeconds: ptr(0)},
		"DDNS mode unknown":         {DynamicDNS: ptr(DDNSMode(7))},
	} {
		t.Run(name, func(t *testing.T) {
			d, f := cfgDevice(writeSeq())
			err := d.SetGeneral(c)
			var ve *ValidationError
			if !errors.As(err, &ve) || !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("err = %v", err)
			}
			if len(f.requests) != 0 {
				t.Errorf("invalid input must not reach the device: %d requests", len(f.requests))
			}
		})
	}
	if err := (GeneralChange{HostName: ptr(""), DomainName: ptr(""), DDNSIntervalMinutes: ptr(0)}).Validate(); err != nil {
		t.Errorf("empty name, empty domain and interval 0 are valid: %v", err)
	}
}

func TestSetGeneralPutErrors(t *testing.T) {
	seq := writeSeq()
	seq["Put|AMT_GeneralSettings"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, _ := cfgDevice(seq)
	if err := d.SetGeneral(GeneralChange{HostName: ptr("x")}); !errors.Is(err, ErrAccessDenied) {
		t.Errorf("err = %v", err)
	}
	seq["Put|AMT_GeneralSettings"] = []cfgResp{{err: errors.New("connection reset")}}
	d, _ = cfgDevice(seq)
	if err := d.SetGeneral(GeneralChange{HostName: ptr("x")}); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("err = %v", err)
	}
}

func staticChange() NetworkChange {
	return NetworkChange{DHCP: ptr(false), IPAddress: ptr("192.0.2.20"), SubnetMask: ptr("255.255.255.0"),
		Gateway: ptr("192.0.2.1"), PrimaryDNS: ptr("192.0.2.53"), SecondaryDNS: ptr("")}
}

func TestSetNetworkNeedsConfirmation(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	err := d.SetNetwork(staticChange(), false)
	var re *RiskyChangeError
	if !errors.As(err, &re) || !errors.Is(err, ErrRiskyChange) || !reflect.DeepEqual(re.Fields, []string{RiskNetwork}) {
		t.Fatalf("err = %v", err)
	}
	assertNoWrites(t, f)
}

func TestSetNetworkStatic(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	if err := d.SetNetwork(staticChange(), true); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Put|AMT_EthernetPortSettings")
	assertContains(t, body,
		`<w:SelectorSet><w:Selector Name="InstanceID">Intel(r) AMT Ethernet Port Settings 0</w:Selector></w:SelectorSet>`,
		"<h:DHCPEnabled>false</h:DHCPEnabled>",
		"<h:IpSyncEnabled>false</h:IpSyncEnabled>",
		"<h:IPAddress>192.0.2.20</h:IPAddress>",
		"<h:SubnetMask>255.255.255.0</h:SubnetMask>",
		"<h:DefaultGateway>192.0.2.1</h:DefaultGateway>",
		"<h:PrimaryDNS>192.0.2.53</h:PrimaryDNS>",
		"<h:LinkPolicy>1</h:LinkPolicy><h:LinkPolicy>14</h:LinkPolicy>",
		"<h:MACAddress>00-00-5e-00-53-01</h:MACAddress>")
	assertLacks(t, body, "SecondaryDNS", "Port Settings 1")
}

func TestSetNetworkDHCPDropsAddresses(t *testing.T) {
	seq := writeSeq()
	seq["Pull|AMT_EthernetPortSettings"] = []cfgResp{{body: withProp(t, "cfg_ethernet_pull.xml", "DHCPEnabled", "false")}}
	d, f := cfgDevice(seq)
	if err := d.SetNetwork(NetworkChange{DHCP: ptr(true)}, true); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Put|AMT_EthernetPortSettings")
	assertContains(t, body, "<h:DHCPEnabled>true</h:DHCPEnabled>")
	assertLacks(t, body, "IPAddress", "SubnetMask", "DefaultGateway", "PrimaryDNS", "SecondaryDNS")
}

func TestSetNetworkWithoutDifference(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	// DHCP is on: address fields are what the DHCP server assigned and do not count as a change
	if err := d.SetNetwork(NetworkChange{DHCP: ptr(true), IPSync: ptr(false), IPAddress: ptr("192.0.2.99")}, false); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)
}

func TestSetNetworkValidation(t *testing.T) {
	cur := NetworkSettings{Available: true, IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0", Gateway: "192.0.2.1"}
	for name, c := range map[string]NetworkChange{
		"bad address":         {DHCP: ptr(false), IPAddress: ptr("192.0.2.300")},
		"IPv6 address":        {DHCP: ptr(false), IPAddress: ptr("2001:db8::1")},
		"non-contiguous mask": {DHCP: ptr(false), SubnetMask: ptr("255.0.255.0")},
		"empty mask":          {DHCP: ptr(false), SubnetMask: ptr("")},
		"gateway outside":     {DHCP: ptr(false), Gateway: ptr("198.51.100.1")},
		"bad DNS":             {DHCP: ptr(false), PrimaryDNS: ptr("dns.example.com")},
		"network address":     {DHCP: ptr(false), IPAddress: ptr("192.0.2.0")},
		"broadcast address":   {DHCP: ptr(false), IPAddress: ptr("192.0.2.255")},
		"gateway is own IP":   {DHCP: ptr(false), Gateway: ptr("192.0.2.10")},
		"gateway is network":  {DHCP: ptr(false), Gateway: ptr("192.0.2.0")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.Validate(cur); !errors.Is(err, ErrInvalidValue) {
				t.Errorf("err = %v", err)
			}
		})
	}
	if err := (NetworkChange{DHCP: ptr(false), Gateway: ptr("")}).Validate(cur); err != nil {
		t.Errorf("empty gateway is valid: %v", err)
	}
	if err := (NetworkChange{DHCP: ptr(true), IPAddress: ptr("junk")}).Validate(cur); err != nil {
		t.Errorf("addresses are ignored with DHCP: %v", err)
	}

	d, f := cfgDevice(writeSeq())
	if err := d.SetNetwork(NetworkChange{DHCP: ptr(false), IPAddress: ptr("x")}, true); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("SetNetwork err = %v", err)
	}
	assertNoWrites(t, f)
}

func TestSetNetworkWithoutWiredPort(t *testing.T) {
	seq := writeSeq()
	seq["Pull|AMT_EthernetPortSettings"] = []cfgResp{{body: withProp(t, "cfg_ethernet_pull.xml", "PhysicalConnectionType", "3")}}
	d, _ := cfgDevice(seq)
	if err := d.SetNetwork(staticChange(), true); !errors.Is(err, ErrNotModifiable) {
		t.Errorf("err = %v", err)
	}
}

func TestNetworkRisky(t *testing.T) {
	cur := NetworkSettings{Available: true, DHCP: true}
	if r := (NetworkChange{DHCP: ptr(true)}).Risky(cur); r != nil {
		t.Errorf("no change: %v", r)
	}
	if r := (NetworkChange{IPSync: ptr(true)}).Risky(cur); !reflect.DeepEqual(r, []string{RiskNetwork}) {
		t.Errorf("IP sync change: %v", r)
	}
}

func TestSetFeaturesDisableKVM(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	err := d.SetFeatures(FeatureChange{KVM: ptr(false)}, false)
	var re *RiskyChangeError
	if !errors.As(err, &re) || !reflect.DeepEqual(re.Fields, []string{RiskKVM}) {
		t.Fatalf("err = %v", err)
	}
	assertNoWrites(t, f)

	d, f = cfgDevice(writeSeq())
	if err := d.SetFeatures(FeatureChange{KVM: ptr(false)}, true); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "RequestStateChange|CIM_KVMRedirectionSAP"), "<h:RequestedState>3</h:RequestedState>")
	if n := len(f.requestsFor("RequestStateChange|AMT_RedirectionService")) + len(f.requestsFor("Put|AMT_RedirectionService")); n != 0 {
		t.Errorf("%d unrelated redirection writes", n)
	}
}

func TestSetFeaturesEnableSOL(t *testing.T) {
	seq := writeSeq()
	seq["Get|AMT_RedirectionService"] = []cfgResp{{body: withProp(t, "cfg_redirection_get.xml", "EnabledState", "32769")}}
	d, f := cfgDevice(seq)
	if err := d.SetFeatures(FeatureChange{SOL: ptr(true), IDER: ptr(true), KVM: ptr(true), Redirection: ptr(true)}, false); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "RequestStateChange|AMT_RedirectionService"), "<h:RequestedState>32771</h:RequestedState>")
	if n := len(f.requestsFor("RequestStateChange|CIM_KVMRedirectionSAP")) + len(f.requestsFor("Put|AMT_RedirectionService")); n != 0 {
		t.Errorf("%d unrelated writes", n)
	}
}

func TestSetFeaturesDisableListener(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	err := d.SetFeatures(FeatureChange{Redirection: ptr(false), SOL: ptr(false)}, false)
	var re *RiskyChangeError
	if !errors.As(err, &re) || !reflect.DeepEqual(re.Fields, []string{RiskRedirection, RiskSOL}) {
		t.Fatalf("err = %v", err)
	}
	d, f = cfgDevice(writeSeq())
	if err := d.SetFeatures(FeatureChange{Redirection: ptr(false), SOL: ptr(false)}, true); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "RequestStateChange|AMT_RedirectionService"), "<h:RequestedState>32769</h:RequestedState>")
	body := onePut(t, f, "Put|AMT_RedirectionService")
	assertContains(t, body,
		"<h:ListenerEnabled>false</h:ListenerEnabled>",
		"<h:EnabledState>32769</h:EnabledState>",
		"<h:AccessLog>01/01/2026, 10:00:00, 192.0.2.99:40000</h:AccessLog>",
		"<h:Name>Intel(r) AMT Redirection Service</h:Name>")
	assertLacks(t, body, "SelectorSet")
	// MeshCommander order: state change of SOL/IDE-R before the listener Put
	var order []string
	for _, r := range f.requests {
		if isWriteRoute(routeKey(r)) {
			order = append(order, routeKey(r))
		}
	}
	if want := []string{"RequestStateChange|AMT_RedirectionService", "Put|AMT_RedirectionService"}; !reflect.DeepEqual(order, want) {
		t.Errorf("write order %v", order)
	}
}

func TestSetFeaturesNoChangeAndErrors(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	if err := d.SetFeatures(FeatureChange{Redirection: ptr(true), KVM: ptr(true)}, false); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)

	seq := writeSeq()
	seq["Get|CIM_KVMRedirectionSAP"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, _ = cfgDevice(seq)
	if err := d.SetFeatures(FeatureChange{KVM: ptr(true)}, true); !errors.Is(err, ErrNotModifiable) {
		t.Errorf("KVM without SAP: %v", err)
	}

	seq = writeSeq()
	seq["RequestStateChange|CIM_KVMRedirectionSAP"] = []cfgResp{{file: "kvm_requeststatechange_fail.xml"}}
	d, _ = cfgDevice(seq)
	var me *MethodError
	if err := d.SetFeatures(FeatureChange{KVM: ptr(false)}, true); !errors.As(err, &me) || me.ReturnValue == 0 {
		t.Errorf("KVM state change failure: %v", err)
	}

	seq = writeSeq()
	seq["RequestStateChange|AMT_RedirectionService"] = []cfgResp{{body: withProp(t, "cfg_redirection_rsc.xml", "ReturnValue", "2")}}
	d, _ = cfgDevice(seq)
	if err := d.SetFeatures(FeatureChange{SOL: ptr(false)}, true); !errors.As(err, &me) || me.ReturnValue != 2 {
		t.Errorf("redirection state change failure: %v", err)
	}
}

func TestSetConsent(t *testing.T) {
	for policy, want := range map[ConsentPolicy]string{ConsentKVM: "1", ConsentAll: "4294967295"} {
		d, f := cfgDevice(writeSeq())
		if err := d.SetConsent(policy); err != nil {
			t.Fatal(err)
		}
		body := onePut(t, f, "Put|IPS_OptInService")
		assertContains(t, body,
			`<h:IPS_OptInService xmlns:h="http://intel.com/wbem/wscim/1/ips-schema/1/IPS_OptInService">`,
			"<h:OptInRequired>"+want+"</h:OptInRequired>",
			"<h:OptInDisplayTimeout>300</h:OptInDisplayTimeout>")
	}

	d, f := cfgDevice(writeSeq())
	if err := d.SetConsent(ConsentNone); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)

	seq := writeSeq()
	seq["Get|IPS_OptInService"] = []cfgResp{{body: withProp(t, "cfg_optin_get.xml", "CanModifyOptInPolicy", "0")}}
	d, f = cfgDevice(seq)
	if err := d.SetConsent(ConsentAll); !errors.Is(err, ErrNotModifiable) {
		t.Errorf("err = %v", err)
	}
	assertNoWrites(t, f)

	if err := d.SetConsent(ConsentPolicy(9)); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("invalid policy: %v", err)
	}
}

func TestSetEnvironmentDetection(t *testing.T) {
	d, f := cfgDevice(writeSeq())
	err := d.SetEnvironmentDetection([]string{"a.example.com", "b.example.com"}, false)
	var re *RiskyChangeError
	if !errors.As(err, &re) || !reflect.DeepEqual(re.Fields, []string{RiskEnvironment}) {
		t.Fatalf("err = %v", err)
	}
	assertNoWrites(t, f)

	d, f = cfgDevice(writeSeq())
	if err := d.SetEnvironmentDetection([]string{"a.example.com", "b.example.com"}, true); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Put|AMT_EnvironmentDetectionSettingData")
	assertContains(t, body,
		`<w:Selector Name="InstanceID">Intel(r) AMT Environment Detection Settings</w:Selector>`,
		"<h:DetectionAlgorithm>0</h:DetectionAlgorithm><h:DetectionStrings>a.example.com</h:DetectionStrings><h:DetectionStrings>b.example.com</h:DetectionStrings><h:ElementName>")

	d, f = cfgDevice(writeSeq())
	if err := d.SetEnvironmentDetection(nil, true); err != nil {
		t.Fatal(err)
	}
	assertLacks(t, onePut(t, f, "Put|AMT_EnvironmentDetectionSettingData"), "DetectionStrings")

	d, f = cfgDevice(writeSeq())
	if err := d.SetEnvironmentDetection([]string{"intranet.example.com"}, false); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)

	for name, domains := range map[string][]string{
		"six domains": {"a.example", "b.example", "c.example", "d.example", "e.example", "f.example"},
		"space":       {"bad domain"},
		"empty":       {""},
		"too long":    {strings.Repeat("a", 64)},
	} {
		if err := d.SetEnvironmentDetection(domains, true); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSyncClock(t *testing.T) {
	fixedNow(t, time.Unix(1767230000, 0))
	d, f := cfgDevice(writeSeq())
	if err := d.SyncClock(); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "SetHighAccuracyTimeSynch|AMT_TimeSynchronizationService"),
		"<h:Ta0>1767225600</h:Ta0>", "<h:Tm1>1767230000</h:Tm1>", "<h:Tm2>1767230000</h:Tm2>")

	seq := writeSeq()
	seq["SetHighAccuracyTimeSynch|AMT_TimeSynchronizationService"] = []cfgResp{{body: withProp(t, "cfg_timesync_set.xml", "ReturnValue", "36")}}
	d, _ = cfgDevice(seq)
	var me *MethodError
	if err := d.SyncClock(); !errors.As(err, &me) || me.ReturnValue != 36 {
		t.Errorf("err = %v", err)
	}

	seq = writeSeq()
	seq["GetLowAccuracyTimeSynch|AMT_TimeSynchronizationService"] = []cfgResp{{body: withProp(t, "cfg_timesync_getlow.xml", "ReturnValue", "1")}}
	d, f = cfgDevice(seq)
	if err := d.SyncClock(); !errors.As(err, &me) {
		t.Errorf("err = %v", err)
	}
	assertNoWrites(t, f)
}

func TestGeneralChangeApply(t *testing.T) {
	cur := GeneralSettings{HostName: "a", DomainName: "example.com", PingResponse: true, DynamicDNS: DDNSByDHCPServer, DDNSTTLSeconds: 900}
	got := GeneralChange{HostName: ptr("b"), PingResponse: ptr(false), RMCPPingResponse: ptr(true), SharedFQDN: ptr(true),
		DynamicDNS: ptr(DDNSClient), DDNSIntervalMinutes: ptr(60), DomainName: ptr("")}.Apply(cur)
	want := GeneralSettings{HostName: "b", SharedFQDN: true, RMCPPingResponse: true, DynamicDNS: DDNSClient,
		DDNSIntervalMinutes: 60, DDNSTTLSeconds: 900}
	if got != want {
		t.Errorf("Apply = %+v", got)
	}
}

func TestSetNetworkLeavingDHCPNeedsExplicitAddresses(t *testing.T) {
	cur := NetworkSettings{Available: true, DHCP: true, IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0"}
	for name, c := range map[string]NetworkChange{
		"lease kept":      {DHCP: ptr(false)},
		"gateway missing": {DHCP: ptr(false), IPAddress: ptr("192.0.2.10"), SubnetMask: ptr("255.255.255.0")},
		"mask missing":    {DHCP: ptr(false), IPAddress: ptr("192.0.2.10"), Gateway: ptr("192.0.2.1")},
	} {
		var ve *ValidationError
		if err := c.Validate(cur); !errors.As(err, &ve) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	noGateway := NetworkChange{DHCP: ptr(false), IPAddress: ptr("192.0.2.10"), SubnetMask: ptr("255.255.255.0"), Gateway: ptr("")}
	if err := noGateway.Validate(cur); err != nil {
		t.Errorf("explicitly no gateway: %v", err)
	}
	d, f := cfgDevice(writeSeq())
	if err := d.SetNetwork(NetworkChange{DHCP: ptr(false)}, true); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("SetNetwork err = %v", err)
	}
	assertNoWrites(t, f)
}

func TestSetFeaturesKVMOffWithoutKVMIsNoChange(t *testing.T) {
	seq := writeSeq()
	seq["Get|CIM_KVMRedirectionSAP"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, f := cfgDevice(seq)
	if err := d.SetFeatures(FeatureChange{Redirection: ptr(true), SOL: ptr(true), IDER: ptr(true), KVM: ptr(false)}, false); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)
}
