package amt

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

const (
	handleAllow   = "Intel(r) AMT:Handle:1"
	handleIsolate = "Intel(r) AMT:Handle:2"
	handleNoARP   = "Intel(r) AMT:Handle:3"
)

// defenseSeq is a device with three policies ("Allow all" active), four IP filters and one
// Ethernet filter, and statistics of the active policy.
func defenseSeq() map[string][]cfgResp {
	return map[string][]cfgResp{
		"Enumerate|AMT_SystemDefensePolicy":            {{file: "enumerate.xml"}},
		"Pull|AMT_SystemDefensePolicy":                 {{file: "sd_policy_pull.xml"}},
		"Enumerate|AMT_IPHeadersFilter":                {{file: "enumerate.xml"}},
		"Pull|AMT_IPHeadersFilter":                     {{file: "sd_ipfilter_pull.xml"}},
		"Enumerate|AMT_Hdr8021Filter":                  {{file: "enumerate.xml"}},
		"Pull|AMT_Hdr8021Filter":                       {{file: "sd_ethfilter_pull.xml"}},
		"Enumerate|AMT_NetworkPortSystemDefensePolicy": {{file: "enumerate.xml"}},
		"Pull|AMT_NetworkPortSystemDefensePolicy":      {{file: "sd_portpolicy_pull.xml"}},
		"UpdateStatistics|AMT_SystemDefensePolicy":     {{file: "sd_updatestats_ok.xml"}},
		"Enumerate|AMT_ActiveFilterStatistics":         {{file: "enumerate.xml"}},
		"Pull|AMT_ActiveFilterStatistics":              {{file: "sd_stats_pull.xml"}},
		"Create|AMT_IPHeadersFilter":                   {{file: "create_ok.xml"}},
		"Create|AMT_Hdr8021Filter":                     {{file: "create_ok.xml"}},
		"Create|AMT_SystemDefensePolicy":               {{file: "create_ok.xml"}},
		"Create|AMT_NetworkPortSystemDefensePolicy":    {{file: "create_ok.xml"}},
		"Delete|AMT_IPHeadersFilter":                   {{file: "delete_ok.xml"}},
		"Delete|AMT_SystemDefensePolicy":               {{file: "delete_ok.xml"}},
		"Delete|AMT_NetworkPortSystemDefensePolicy":    {{file: "delete_ok.xml"}},
	}
}

func TestGetSystemDefenseReadsPoliciesFiltersAndActive(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	sd, err := d.GetSystemDefense()
	if err != nil {
		t.Fatal(err)
	}
	if !sd.Offered || sd.Active != handleAllow {
		t.Fatalf("offered %v, active %q", sd.Offered, sd.Active)
	}
	wantPolicies := []DefensePolicy{ // higher precedence first
		{ID: handleAllow, Name: "Allow all", Precedence: 2, Active: true},
		{ID: handleIsolate, Name: "Isolate", Precedence: 1, DropIn: true, DropOut: true, Filters: []string{"1", "2"}},
		{ID: handleNoARP, Name: "No ARP", Precedence: 0, Filters: []string{"5"}},
	}
	if !reflect.DeepEqual(sd.Policies, wantPolicies) {
		t.Errorf("policies\n got %+v\nwant %+v", sd.Policies, wantPolicies)
	}
	wantFilters := []DefenseFilter{
		{ID: "1", Name: "allow-dhcp", Kind: FilterIPv4, Direction: Outbound, Protocol: ProtocolUDP, PortStart: 67, PortEnd: 68, class: classIPFilter},
		{ID: "2", Name: "allow-dns", Kind: FilterIPv4, Direction: Outbound, Protocol: ProtocolUDP, PortStart: 53, PortEnd: 53,
			Remote: netip.MustParsePrefix("192.0.2.1/32"), Count: true, Matches: 12, HasMatches: true, class: classIPFilter},
		{ID: "3", Name: "allow-ssh", Kind: FilterIPv4, Direction: Inbound, Protocol: ProtocolTCP, PortStart: 22, PortEnd: 22,
			Remote: netip.MustParsePrefix("192.0.2.0/24"), Event: true, class: classIPFilter},
		{ID: "4", Name: "block-v6", Kind: FilterIPv6, Direction: Inbound, Remote: netip.MustParsePrefix("2001:db8::/32"),
			Drop: true, Count: true, Matches: 3, HasMatches: true, class: classIPFilter},
		{ID: "5", Name: "drop-arp", Kind: FilterEthernet, Direction: Inbound, Protocol: EtherTypeARP, RateLimit: 100, class: classEthFilter},
	}
	if !reflect.DeepEqual(sd.Filters, wantFilters) {
		t.Errorf("filters\n got %+v\nwant %+v", sd.Filters, wantFilters)
	}
	upd := onePut(t, f, "UpdateStatistics|AMT_SystemDefensePolicy")
	assertContains(t, upd, `<w:Selector Name="InstanceID">Intel(r) AMT:Handle:1</w:Selector>`,
		"Intel(r) AMT Ethernet Port 0", "<h:ResetOnRead>false</h:ResetOnRead>")
	for _, r := range f.requests {
		if route := routeKey(r); isWriteRoute(route) && route != "UpdateStatistics|AMT_SystemDefensePolicy" {
			t.Errorf("unexpected write request %s", route)
		}
	}
}

func TestSystemDefenseDrops(t *testing.T) {
	d, _ := cfgDevice(defenseSeq())
	sd, err := d.GetSystemDefense()
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{handleAllow: false, handleIsolate: true, handleNoARP: true, "unknown": false} {
		if got := sd.Drops(id); got != want {
			t.Errorf("Drops(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestGetSystemDefenseWithoutActivePolicyReadsNoStatistics(t *testing.T) {
	seq := defenseSeq()
	seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{file: "pull_empty.xml"}}
	d, f := cfgDevice(seq)
	sd, err := d.GetSystemDefense()
	if err != nil {
		t.Fatal(err)
	}
	if sd.Active != "" || sd.Policies[0].Active {
		t.Fatalf("active %q", sd.Active)
	}
	if n := len(f.requestsFor("UpdateStatistics|AMT_SystemDefensePolicy")); n != 0 {
		t.Errorf("%d UpdateStatistics without an active policy", n)
	}
}

func TestGetSystemDefenseStatisticsAreOptional(t *testing.T) {
	seq := defenseSeq()
	seq["UpdateStatistics|AMT_SystemDefensePolicy"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, _ := cfgDevice(seq)
	sd, err := d.GetSystemDefense()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sd.Filters {
		if f.HasMatches {
			t.Errorf("%s has matches without statistics", f.Name)
		}
	}
}

func TestGetSystemDefenseMissingClassIsNotOffered(t *testing.T) {
	seq := defenseSeq()
	seq["Enumerate|AMT_SystemDefensePolicy"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, f := cfgDevice(seq)
	sd, err := d.GetSystemDefense()
	if err != nil || sd.Offered {
		t.Fatalf("offered %v, err %v", sd.Offered, err)
	}
	if len(f.requests) != 1 {
		t.Errorf("%d requests after the missing class", len(f.requests))
	}
}

func TestGetSystemDefenseMissingEthernetFilterClass(t *testing.T) {
	seq := defenseSeq()
	seq["Enumerate|AMT_Hdr8021Filter"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, _ := cfgDevice(seq)
	sd, err := d.GetSystemDefense()
	if err != nil || len(sd.Filters) != 4 {
		t.Fatalf("%d filters, err %v", len(sd.Filters), err)
	}
}

func TestGetSystemDefenseAccessDenied(t *testing.T) {
	seq := defenseSeq()
	seq["Enumerate|AMT_SystemDefensePolicy"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, _ := cfgDevice(seq)
	if _, err := d.GetSystemDefense(); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err %v, want ErrAccessDenied", err)
	}
	seq = defenseSeq()
	seq["Pull|AMT_IPHeadersFilter"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, _ = cfgDevice(seq)
	if _, err := d.GetSystemDefense(); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("pull fault: err %v, want ErrAccessDenied", err)
	}
}

func TestPrefixOfEncodings(t *testing.T) {
	for _, c := range []struct {
		addr, mask []string
		want       string
	}{
		{[]string{"C0000201"}, nil, "192.0.2.1/32"},
		{[]string{"c0000200"}, []string{"ffffff00"}, "192.0.2.0/24"},
		{[]string{"wAACAA=="}, []string{"////AA=="}, "192.0.2.0/24"},
		{[]string{"192", "0", "2", "7"}, []string{"255", "255", "255", "255"}, "192.0.2.7/32"},
		{[]string{"192.0.2.9"}, nil, "192.0.2.9/32"},
		{[]string{"IAENuAAAAAAAAAAAAAAAAA=="}, []string{"/////wAAAAAAAAAAAAAAAA=="}, "2001:db8::/32"},
		{[]string{"garbage"}, nil, "invalid Prefix"},
		{nil, nil, "invalid Prefix"},
	} {
		if got := prefixOf(c.addr, c.mask).String(); got != c.want {
			t.Errorf("prefixOf(%v, %v) = %s, want %s", c.addr, c.mask, got, c.want)
		}
	}
}

func goodFilter() FilterSpec {
	return FilterSpec{Name: "allow-web", Kind: FilterIPv4, Direction: Inbound, Protocol: ProtocolTCP, PortStart: 80, PortEnd: 443,
		Remote: netip.MustParsePrefix("192.0.2.0/24")}
}

func TestFilterSpecValidate(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*FilterSpec)
		ok   bool
	}{
		{"valid", func(*FilterSpec) {}, true},
		{"name of 16 characters", func(s *FilterSpec) { s.Name = strings.Repeat("a", 16) }, true},
		{"empty name", func(s *FilterSpec) { s.Name = " " }, false},
		{"name of 17 characters", func(s *FilterSpec) { s.Name = strings.Repeat("a", 17) }, false},
		{"control character", func(s *FilterSpec) { s.Name = "a\tb" }, false},
		{"no direction", func(s *FilterSpec) { s.Direction = 0 }, false},
		{"no kind", func(s *FilterSpec) { s.Kind = 0 }, false},
		{"read-only kind", func(s *FilterSpec) { s.Kind = FilterIP }, false},
		{"ports for ICMP", func(s *FilterSpec) { s.Protocol = ProtocolICMP }, false},
		{"any protocol without ports", func(s *FilterSpec) { s.Protocol, s.PortStart, s.PortEnd = ProtocolAny, 0, 0 }, true},
		{"port 0", func(s *FilterSpec) { s.PortStart = 0 }, false},
		{"reversed range", func(s *FilterSpec) { s.PortStart, s.PortEnd = 443, 80 }, false},
		{"port above 65535", func(s *FilterSpec) { s.PortEnd = 65536 }, false},
		{"IPv6 address in IPv4 filter", func(s *FilterSpec) { s.Remote = netip.MustParsePrefix("2001:db8::/32") }, false},
		{"IPv6 filter", func(s *FilterSpec) { s.Kind, s.Remote = FilterIPv6, netip.MustParsePrefix("2001:db8::/32") }, true},
		{"any address", func(s *FilterSpec) { s.Remote = netip.Prefix{} }, true},
		{"Ethernet ARP", func(s *FilterSpec) {
			*s = FilterSpec{Name: "arp", Kind: FilterEthernet, Direction: Inbound, Protocol: EtherTypeARP}
		}, true},
		{"Ethernet with ports", func(s *FilterSpec) { s.Kind, s.Protocol, s.Remote = FilterEthernet, EtherTypeIP, netip.Prefix{} }, false},
		{"Ethernet below 0x0600", func(s *FilterSpec) { *s = FilterSpec{Name: "x", Kind: FilterEthernet, Direction: Inbound, Protocol: 6} }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := goodFilter()
			c.mod(&s)
			err := s.Validate()
			if c.ok != (err == nil) || err != nil && !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("err %v, want ok %v", err, c.ok)
			}
		})
	}
}

func TestAddDefenseFilterIPv4Inbound(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	s := goodFilter()
	s.Name, s.Drop, s.Count, s.Event = "web<&>", true, true, true
	if err := d.AddDefenseFilter(s); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Create|AMT_IPHeadersFilter")
	assertContains(t, body,
		`<h:AMT_IPHeadersFilter xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_IPHeadersFilter">`,
		"<h:InstanceID>0</h:InstanceID><h:Name>web&lt;&amp;&gt;</h:Name><h:CreationClassName>AMT_IPHeadersFilter</h:CreationClassName>",
		"<h:SystemName>Intel(r) AMT</h:SystemName><h:SystemCreationClassName>CIM_ComputerSystem</h:SystemCreationClassName>",
		"<h:HdrIPVersion>4</h:HdrIPVersion>",
		"<h:HdrSrcAddress>C0000200</h:HdrSrcAddress><h:HdrSrcMask>FFFFFF00</h:HdrSrcMask>",
		"<h:HdrProtocolID>6</h:HdrProtocolID><h:HdrDestPortStart>80</h:HdrDestPortStart><h:HdrDestPortEnd>443</h:HdrDestPortEnd>",
		"<h:FilterProfile>1</h:FilterProfile><h:FilterDirection>1</h:FilterDirection><h:ActionEventOnMatch>true</h:ActionEventOnMatch>")
	assertLacks(t, body, "HdrDestAddress", "web<&>")
}

func TestAddDefenseFilterOutboundAnyAndIPv6(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.AddDefenseFilter(FilterSpec{Name: "to-dns", Kind: FilterIPv4, Direction: Outbound, Protocol: ProtocolAny,
		Remote: netip.MustParsePrefix("192.0.2.77/32")}); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Create|AMT_IPHeadersFilter")
	assertContains(t, body, "<h:HdrDestAddress>C000024D</h:HdrDestAddress><h:HdrDestMask>FFFFFFFF</h:HdrDestMask>",
		"<h:FilterProfile>3</h:FilterProfile><h:FilterDirection>0</h:FilterDirection>")
	assertLacks(t, body, "HdrProtocolID", "HdrDestPort", "HdrSrc")

	d, f = cfgDevice(defenseSeq())
	if err := d.AddDefenseFilter(FilterSpec{Name: "v6", Kind: FilterIPv6, Direction: Inbound, Protocol: ProtocolICMPv6,
		Remote: netip.MustParsePrefix("2001:db8:1:2::5/64"), Drop: true}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Create|AMT_IPHeadersFilter"), "<h:HdrIPVersion>6</h:HdrIPVersion>",
		"<h:HdrSrcAddress>20010DB8000100020000000000000000</h:HdrSrcAddress>",
		"<h:HdrSrcMask>FFFFFFFFFFFFFFFF0000000000000000</h:HdrSrcMask>", "<h:HdrProtocolID>58</h:HdrProtocolID>",
		"<h:FilterProfile>4</h:FilterProfile>")
}

func TestAddDefenseFilterEthernet(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.AddDefenseFilter(FilterSpec{Name: "arp", Kind: FilterEthernet, Direction: Outbound, Protocol: EtherTypeARP, Count: true}); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Create|AMT_Hdr8021Filter")
	assertContains(t, body, "<h:CreationClassName>AMT_Hdr8021Filter</h:CreationClassName>", "<h:HdrProtocolID8021>2054</h:HdrProtocolID8021>",
		"<h:FilterProfile>0</h:FilterProfile><h:FilterDirection>0</h:FilterDirection><h:ActionEventOnMatch>false</h:ActionEventOnMatch>")
	assertLacks(t, body, "HdrIPVersion")
}

func TestAddDefenseFilterRefusals(t *testing.T) {
	t.Run("name in use", func(t *testing.T) {
		d, f := cfgDevice(defenseSeq())
		s := goodFilter()
		s.Name = "ALLOW-DNS"
		if err := d.AddDefenseFilter(s); !errors.Is(err, ErrDefenseNameExists) {
			t.Fatalf("err %v", err)
		}
		assertNoWrites(t, f)
	})
	t.Run("invalid spec sends nothing", func(t *testing.T) {
		d, f := cfgDevice(defenseSeq())
		s := goodFilter()
		s.Name = ""
		if err := d.AddDefenseFilter(s); !errors.Is(err, ErrInvalidValue) || len(f.requests) != 0 {
			t.Fatalf("err %v, %d requests", err, len(f.requests))
		}
	})
	t.Run("the device refuses", func(t *testing.T) {
		seq := defenseSeq()
		seq["Create|AMT_IPHeadersFilter"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
		d, _ := cfgDevice(seq)
		if err := d.AddDefenseFilter(goodFilter()); !errors.Is(err, ErrAccessDenied) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("no system defense", func(t *testing.T) {
		seq := defenseSeq()
		seq["Enumerate|AMT_SystemDefensePolicy"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
		d, f := cfgDevice(seq)
		if err := d.AddDefenseFilter(goodFilter()); !errors.Is(err, ErrNotModifiable) {
			t.Fatalf("err %v", err)
		}
		assertNoWrites(t, f)
	})
}

func TestDeleteDefenseFilter(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.DeleteDefenseFilter("3"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Delete|AMT_IPHeadersFilter"), `<w:Selector Name="InstanceID">3</w:Selector>`)

	d, f = cfgDevice(defenseSeq())
	var inUse *DefenseFilterInUseError
	if err := d.DeleteDefenseFilter("2"); !errors.As(err, &inUse) || inUse.Policy != "Isolate" {
		t.Fatalf("err %v", err)
	}
	assertNoWrites(t, f)

	d, _ = cfgDevice(defenseSeq())
	if err := d.DeleteDefenseFilter("99"); !errors.Is(err, ErrDefenseNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestPolicySpecValidate(t *testing.T) {
	for _, c := range []struct {
		s  PolicySpec
		ok bool
	}{
		{PolicySpec{Name: "Isolate", Precedence: 9, Filters: []string{"1"}, Drop: true}, true},
		{PolicySpec{Name: "", Precedence: 1}, false},
		{PolicySpec{Name: strings.Repeat("p", 17)}, false},
		{PolicySpec{Name: "x", Precedence: -1}, false},
		{PolicySpec{Name: "x", Precedence: 4294967295}, true},
		{PolicySpec{Name: "x", Precedence: 4294967296}, false},
		{PolicySpec{Name: "x", Filters: make([]string, 63)}, false},
	} {
		if err := c.s.Validate(); c.ok != (err == nil) {
			t.Errorf("%+v: err %v, want ok %v", c.s, err, c.ok)
		}
	}
}

func TestAddDefensePolicy(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.AddDefensePolicy(PolicySpec{Name: "Lockdown", Precedence: 5, Filters: []string{"1", "3"}, Drop: true}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Create|AMT_SystemDefensePolicy"),
		"<h:InstanceID>0</h:InstanceID><h:PolicyName>Lockdown</h:PolicyName><h:PolicyPrecedence>5</h:PolicyPrecedence>",
		"<h:TxDefaultCount>false</h:TxDefaultCount><h:TxDefaultDrop>true</h:TxDefaultDrop><h:TxDefaultMatchEvent>false</h:TxDefaultMatchEvent>",
		"<h:RxDefaultCount>false</h:RxDefaultCount><h:RxDefaultDrop>true</h:RxDefaultDrop><h:RxDefaultMatchEvent>false</h:RxDefaultMatchEvent>",
		"<h:FilterCreationHandles>1</h:FilterCreationHandles><h:FilterCreationHandles>3</h:FilterCreationHandles>")

	d, f = cfgDevice(defenseSeq())
	if err := d.AddDefensePolicy(PolicySpec{Name: "Open", Precedence: 0}); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Create|AMT_SystemDefensePolicy")
	assertContains(t, body, "<h:TxDefaultDrop>false</h:TxDefaultDrop>", "<h:RxDefaultDrop>false</h:RxDefaultDrop>")
	assertLacks(t, body, "FilterCreationHandles")
}

func TestAddDefensePolicyRefusals(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.AddDefensePolicy(PolicySpec{Name: "isolate", Precedence: 3}); !errors.Is(err, ErrDefenseNameExists) {
		t.Fatalf("name in use: err %v", err)
	}
	if err := d.AddDefensePolicy(PolicySpec{Name: "New", Filters: []string{"42"}}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("unknown filter: err %v", err)
	}
	assertNoWrites(t, f)
}

func TestDeleteDefensePolicy(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.DeleteDefensePolicy(handleIsolate); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Delete|AMT_SystemDefensePolicy"), `<w:Selector Name="InstanceID">Intel(r) AMT:Handle:2</w:Selector>`)

	d, f = cfgDevice(defenseSeq())
	if err := d.DeleteDefensePolicy(handleAllow); !errors.Is(err, ErrDefensePolicyActive) {
		t.Fatalf("active policy: err %v", err)
	}
	assertNoWrites(t, f)
}

func TestActivateDropPolicyNeedsConfirmation(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	var risky *RiskyChangeError
	if err := d.ActivateDefensePolicy(handleIsolate, Confirmation{}); !errors.As(err, &risky) || risky.Op != OpDefenseDrop {
		t.Fatalf("err %v, want RiskyChangeError for OpDefenseDrop", err)
	}
	wrong, _ := ConfirmChecked(OpDefenseDelete, testHost, true)
	if err := d.ActivateDefensePolicy(handleIsolate, wrong); !errors.As(err, &risky) {
		t.Fatalf("wrong operation: err %v", err)
	}
	assertNoWrites(t, f)

	c, err := ConfirmChecked(OpDefenseDrop, testHost, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ActivateDefensePolicy(handleIsolate, c); err != nil {
		t.Fatal(err)
	}
	del := onePut(t, f, "Delete|AMT_NetworkPortSystemDefensePolicy")
	assertContains(t, del, `<w:Selector Name="Antecedent"><a:EndpointReference>`, "CIM_EthernetPort</w:ResourceURI>",
		`<w:Selector Name="DeviceID">Intel(r) AMT Ethernet Port 0</w:Selector>`, `<w:Selector Name="Dependent">`,
		`<w:Selector Name="InstanceID">Intel(r) AMT:Handle:1</w:Selector>`)
	create := onePut(t, f, "Create|AMT_NetworkPortSystemDefensePolicy")
	assertContains(t, create, "<h:Antecedent><Address", `<Selector Name="DeviceID">Intel(r) AMT Ethernet Port 0</Selector>`,
		"<h:Dependent><Address", `<Selector Name="InstanceID">Intel(r) AMT:Handle:2</Selector>`)
	if i, j := routeIndex(f, "Delete|AMT_NetworkPortSystemDefensePolicy"), routeIndex(f, "Create|AMT_NetworkPortSystemDefensePolicy"); i < 0 || j < i {
		t.Error("the old link is not deleted before the new one is created")
	}
}

func TestActivateAllowPolicyNeedsNoConfirmation(t *testing.T) {
	seq := defenseSeq()
	seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{file: "sd_portpolicy_pull_isolate.xml"}}
	d, f := cfgDevice(seq)
	if err := d.ActivateDefensePolicy(handleAllow, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Delete|AMT_NetworkPortSystemDefensePolicy"), "Intel(r) AMT:Handle:2")
	assertContains(t, onePut(t, f, "Create|AMT_NetworkPortSystemDefensePolicy"), "Intel(r) AMT:Handle:1")
}

func TestActivateWithoutActivePolicyOnlyCreates(t *testing.T) {
	seq := defenseSeq()
	seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{file: "pull_empty.xml"}}
	d, f := cfgDevice(seq)
	c, _ := ConfirmChecked(OpDefenseDrop, testHost, true)
	if err := d.ActivateDefensePolicy(handleNoARP, c); err != nil {
		t.Fatal(err)
	}
	if n := len(f.requestsFor("Delete|AMT_NetworkPortSystemDefensePolicy")); n != 0 {
		t.Errorf("%d deletes", n)
	}
	onePut(t, f, "Create|AMT_NetworkPortSystemDefensePolicy")
}

func TestActivateTheActivePolicyWritesNothing(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.ActivateDefensePolicy(handleAllow, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)
	if err := d.ActivateDefensePolicy("nope", Confirmation{}); !errors.Is(err, ErrDefenseNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestDeactivateDefensePolicy(t *testing.T) {
	d, f := cfgDevice(defenseSeq())
	if err := d.DeactivateDefensePolicy(); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Delete|AMT_NetworkPortSystemDefensePolicy"), "Intel(r) AMT:Handle:1")

	seq := defenseSeq()
	seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{file: "pull_empty.xml"}}
	d, f = cfgDevice(seq)
	if err := d.DeactivateDefensePolicy(); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, f)
}

func TestDefenseLinkWriteRefused(t *testing.T) {
	seq := defenseSeq()
	seq["Delete|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, f := cfgDevice(seq)
	if err := d.ActivateDefensePolicy(handleNoARP, mustConfirm(t, OpDefenseDrop)); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err %v", err)
	}
	if n := len(f.requestsFor("Create|AMT_NetworkPortSystemDefensePolicy")); n != 0 {
		t.Error("created the new link after the old one could not be deleted")
	}
}

func mustConfirm(t *testing.T, op Operation) Confirmation {
	t.Helper()
	c, err := ConfirmChecked(op, testHost, true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDefenseConfirmationLevels(t *testing.T) {
	for op, want := range map[Operation]Level{OpDefenseDelete: LevelSimple, OpWatchdogDelete: LevelSimple, OpDefenseDrop: LevelOrange} {
		if got := RiskOf(op).Level; got != want {
			t.Errorf("%s level %d, want %d", op, got, want)
		}
	}
}

func TestDefenseDeletesRefusedByTheDevice(t *testing.T) {
	seq := watchdogSeq()
	for _, r := range []string{"Delete|AMT_SystemDefensePolicy", "Delete|AMT_IPHeadersFilter", "Delete|AMT_AgentPresenceWatchdog", "Create|AMT_SystemDefensePolicy"} {
		seq[r] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	}
	d, _ := cfgDevice(seq)
	for name, err := range map[string]error{
		"policy":   d.DeleteDefensePolicy(handleIsolate),
		"filter":   d.DeleteDefenseFilter("3"),
		"watchdog": d.DeleteWatchdog(agentID),
		"create":   d.AddDefensePolicy(PolicySpec{Name: "New"}),
	} {
		if !errors.Is(err, ErrAccessDenied) {
			t.Errorf("%s: err %v, want ErrAccessDenied", name, err)
		}
	}
}
