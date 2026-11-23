package amt

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"
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
	if i, j := routeIndex(f, "Create|AMT_NetworkPortSystemDefensePolicy"), routeIndex(f, "Delete|AMT_NetworkPortSystemDefensePolicy"); i < 0 || j < i {
		t.Error("the new link is not created before the old one is deleted")
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
	seq["Create|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, f := cfgDevice(seq)
	if err := d.ActivateDefensePolicy(handleNoARP, mustConfirm(t, OpDefenseDrop)); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err %v", err)
	}
	if n := len(f.requestsFor("Delete|AMT_NetworkPortSystemDefensePolicy")); n != 0 {
		t.Error("deleted the old link after the new one was denied")
	}
}

// linkWrites lists the Create and Delete requests of the port link with the policy handle each
// one names, in order, e.g. "Create Intel(r) AMT:Handle:3".
func linkWrites(f *cfgWSMan) []string {
	var out []string
	for _, r := range f.requests {
		var verb string
		switch routeKey(r) {
		case "Create|AMT_NetworkPortSystemDefensePolicy":
			verb = "Create"
		case "Delete|AMT_NetworkPortSystemDefensePolicy":
			verb = "Delete"
		default:
			continue
		}
		for _, h := range []string{handleAllow, handleIsolate, handleNoARP} {
			if strings.Contains(r, ">"+h+"<") {
				out = append(out, verb+" "+h)
			}
		}
	}
	return out
}

// Issue #161: the port keeps a policy whatever step of the switch fails.
func TestActivateSwitchOrder(t *testing.T) {
	refused := func(t *testing.T) cfgResp { return cfgResp{err: faultErr(t, "already_exists_fault.xml")} }
	ok := cfgResp{file: "create_ok.xml"}
	cases := []struct {
		name    string
		creates func(t *testing.T) []cfgResp
		deletes func(t *testing.T) []cfgResp
		writes  []string
		wantErr bool
		lost    bool
	}{
		{"create first, then delete the old link",
			func(*testing.T) []cfgResp { return []cfgResp{ok} }, nil,
			[]string{"Create " + handleNoARP, "Delete " + handleAllow}, false, false},
		{"old link already gone",
			func(*testing.T) []cfgResp { return []cfgResp{ok} },
			func(t *testing.T) []cfgResp { return []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}} },
			[]string{"Create " + handleNoARP, "Delete " + handleAllow}, false, false},
		{"no answer to the create keeps the old link",
			func(*testing.T) []cfgResp { return []cfgResp{{err: errors.New("connection reset")}} }, nil,
			[]string{"Create " + handleNoARP}, true, false},
		{"refused create falls back to delete first",
			func(t *testing.T) []cfgResp { return []cfgResp{refused(t), ok} }, nil,
			[]string{"Create " + handleNoARP, "Delete " + handleAllow, "Create " + handleNoARP}, false, false},
		{"failed fallback links the old policy again",
			func(t *testing.T) []cfgResp { return []cfgResp{refused(t), refused(t), ok} }, nil,
			[]string{"Create " + handleNoARP, "Delete " + handleAllow, "Create " + handleNoARP, "Create " + handleAllow}, true, false},
		{"failed relink reports a port without policy",
			func(t *testing.T) []cfgResp {
				return []cfgResp{refused(t), refused(t), {err: errors.New("connection reset")}}
			}, nil,
			[]string{"Create " + handleNoARP, "Delete " + handleAllow, "Create " + handleNoARP, "Create " + handleAllow}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seq := defenseSeq()
			seq["Create|AMT_NetworkPortSystemDefensePolicy"] = c.creates(t)
			if c.deletes != nil {
				seq["Delete|AMT_NetworkPortSystemDefensePolicy"] = c.deletes(t)
			}
			d, f := cfgDevice(seq)
			err := d.ActivateDefensePolicy(handleNoARP, mustConfirm(t, OpDefenseDrop))
			if (err != nil) != c.wantErr || errors.Is(err, ErrDefensePolicyLost) != c.lost {
				t.Fatalf("err %v", err)
			}
			if got := linkWrites(f); !slices.Equal(got, c.writes) {
				t.Fatalf("writes %q, want %q", got, c.writes)
			}
		})
	}
}

// Issue #167: after a failed or unanswered step of the switch the links are read once and
// settled. The old link is "Allow all", the new one "No ARP".
func TestActivateSettlesLinks(t *testing.T) {
	reset := cfgResp{err: errors.New("connection reset")}
	refused := func(t *testing.T) cfgResp { return cfgResp{err: faultErr(t, "already_exists_fault.xml")} }
	ok, deleted := cfgResp{file: "create_ok.xml"}, cfgResp{file: "delete_ok.xml"}
	onlyOld, onlyNew := cfgResp{file: "sd_portpolicy_pull.xml"}, cfgResp{file: "sd_portpolicy_pull_noarp.xml"}
	none, both := cfgResp{file: "pull_empty.xml"}, cfgResp{file: "sd_portpolicy_pull_two.xml"}
	create, del := "Create "+handleNoARP, "Delete "+handleAllow
	cases := []struct {
		name    string
		creates func(t *testing.T) []cfgResp
		deletes []cfgResp
		read    cfgResp // the links after the failed step
		writes  []string
		want    func(error) bool
	}{
		{"only the new link: success",
			func(*testing.T) []cfgResp { return []cfgResp{ok} }, []cfgResp{reset}, onlyNew,
			[]string{create, del}, func(err error) bool { return err == nil }},
		{"unanswered create, only the old link: plain error",
			func(*testing.T) []cfgResp { return []cfgResp{reset} }, nil, onlyOld,
			[]string{create}, plainSwitchError},
		{"unanswered fallback delete, only the old link: plain error, no relink",
			func(t *testing.T) []cfgResp { return []cfgResp{refused(t)} }, []cfgResp{reset}, onlyOld,
			[]string{create, del}, plainSwitchError},
		{"no link: the new one is linked once",
			func(t *testing.T) []cfgResp { return []cfgResp{refused(t), ok} }, []cfgResp{reset}, none,
			[]string{create, del, create}, func(err error) bool { return err == nil }},
		{"no link and the link fails: port may have no policy",
			func(t *testing.T) []cfgResp { return []cfgResp{refused(t), reset} }, []cfgResp{reset}, none,
			[]string{create, del, create}, func(err error) bool { return errors.Is(err, ErrDefensePolicyLost) }},
		{"unanswered fallback create, no link: the new one is linked once",
			func(t *testing.T) []cfgResp { return []cfgResp{refused(t), reset, ok} }, nil, none,
			[]string{create, del, create, create}, func(err error) bool { return err == nil }},
		{"both links: the old one is deleted once",
			func(*testing.T) []cfgResp { return []cfgResp{ok} }, []cfgResp{reset, deleted}, both,
			[]string{create, del, del}, func(err error) bool { return err == nil }},
		{"both links and the delete fails: the error names both",
			func(*testing.T) []cfgResp { return []cfgResp{ok} }, []cfgResp{reset}, both,
			[]string{create, del, del}, func(err error) bool {
				var le *DefenseLinksError
				return errors.As(err, &le) && slices.Equal(le.Policies, []string{"Allow all", "No ARP"})
			}},
		{"unanswered create, both links: the old one is deleted once",
			func(*testing.T) []cfgResp { return []cfgResp{reset} }, nil, both,
			[]string{create, del}, func(err error) bool { return err == nil }},
		{"read fails after the fallback delete: port may have no policy",
			func(t *testing.T) []cfgResp { return []cfgResp{refused(t)} }, []cfgResp{reset}, reset,
			[]string{create, del}, func(err error) bool { return errors.Is(err, ErrDefensePolicyLost) }},
		{"read fails while a link is sure: plain error",
			func(*testing.T) []cfgResp { return []cfgResp{ok} }, []cfgResp{reset}, reset,
			[]string{create, del}, plainSwitchError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seq := defenseSeq()
			seq["Create|AMT_NetworkPortSystemDefensePolicy"] = c.creates(t)
			if c.deletes != nil {
				seq["Delete|AMT_NetworkPortSystemDefensePolicy"] = c.deletes
			}
			seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{onlyOld, c.read}
			d, f := cfgDevice(seq)
			err := d.ActivateDefensePolicy(handleNoARP, mustConfirm(t, OpDefenseDrop))
			if !c.want(err) {
				t.Fatalf("err %v", err)
			}
			if got := linkWrites(f); !slices.Equal(got, c.writes) {
				t.Fatalf("writes %q, want %q", got, c.writes)
			}
		})
	}
}

// plainSwitchError is a failed switch without the "no policy" or "two policies" warning.
func plainSwitchError(err error) bool {
	var le *DefenseLinksError
	return err != nil && !errors.Is(err, ErrDefensePolicyLost) && !errors.As(err, &le)
}

// Issue #167: two links are not shown as one active policy.
func TestGetSystemDefenseTwoLinksPicksNone(t *testing.T) {
	seq := defenseSeq()
	seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{file: "sd_portpolicy_pull_two.xml"}}
	d, f := cfgDevice(seq)
	sd, err := d.GetSystemDefense()
	if err != nil {
		t.Fatal(err)
	}
	if sd.Active != "" || !slices.Equal(sd.Linked, []string{handleAllow, handleNoARP}) {
		t.Fatalf("active %q, linked %q", sd.Active, sd.Linked)
	}
	for _, p := range sd.Policies {
		if p.Active != (p.ID == handleAllow || p.ID == handleNoARP) {
			t.Errorf("%s active %v", p.Name, p.Active)
		}
	}
	var le *DefenseLinksError
	if err := sd.LinkError(); !errors.As(err, &le) || !slices.Equal(le.Policies, []string{"Allow all", "No ARP"}) {
		t.Errorf("link error %v", err)
	}
	if n := len(f.requestsFor("UpdateStatistics|AMT_SystemDefensePolicy")); n != 0 {
		t.Errorf("%d UpdateStatistics with two links", n)
	}
	if err := d.DeleteDefensePolicy(handleNoARP); !errors.Is(err, ErrDefensePolicyActive) {
		t.Errorf("delete of a linked policy: %v", err)
	}
	seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{file: "sd_portpolicy_pull.xml"}}
	d, _ = cfgDevice(seq)
	if sd, _ := d.GetSystemDefense(); sd.LinkError() != nil || sd.Active != handleAllow {
		t.Errorf("one link: active %q, %v", sd.Active, sd.LinkError())
	}
}

// Issue #167: with two links, activating one of them deletes the other, another policy
// replaces both, and Turn off deletes both.
func TestDefenseWritesWithTwoLinks(t *testing.T) {
	cases := []struct {
		name   string
		write  func(d *Device) error
		writes []string
	}{
		{"activate a linked policy", func(d *Device) error { return d.ActivateDefensePolicy(handleNoARP, mustConfirm(t, OpDefenseDrop)) },
			[]string{"Delete " + handleAllow}},
		{"activate another policy", func(d *Device) error { return d.ActivateDefensePolicy(handleIsolate, mustConfirm(t, OpDefenseDrop)) },
			[]string{"Create " + handleIsolate, "Delete " + handleAllow, "Delete " + handleNoARP}},
		{"turn off", (*Device).DeactivateDefensePolicy,
			[]string{"Delete " + handleAllow, "Delete " + handleNoARP}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seq := defenseSeq()
			seq["Pull|AMT_NetworkPortSystemDefensePolicy"] = []cfgResp{{file: "sd_portpolicy_pull_two.xml"}}
			d, f := cfgDevice(seq)
			if err := c.write(d); err != nil {
				t.Fatal(err)
			}
			if got := linkWrites(f); !slices.Equal(got, c.writes) {
				t.Fatalf("writes %q, want %q", got, c.writes)
			}
		})
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
