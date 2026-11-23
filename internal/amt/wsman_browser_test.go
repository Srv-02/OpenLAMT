package amt

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func browseSeq(pull ...cfgResp) map[string][]cfgResp {
	return map[string][]cfgResp{
		"Enumerate|AMT_GeneralSettings": {{file: "wsman_enumerate.xml"}},
		"Pull|AMT_GeneralSettings":      pull,
		"Release|AMT_GeneralSettings":   {{file: "wsman_pull_empty.xml"}},
	}
}

// pullBody builds a Pull response with n instances of AMT_GeneralSettings; more adds a context
// instead of EndOfSequence. text is the value of Name.
func browsePull(n int, more bool, text string) string {
	var b strings.Builder
	b.WriteString(`<a:Envelope xmlns:a="s" xmlns:g="e" xmlns:h="u"><a:Body><g:PullResponse><g:Items>`)
	for i := range n {
		fmt.Fprintf(&b, `<h:AMT_GeneralSettings><h:Name>%s</h:Name><h:Index>%d</h:Index></h:AMT_GeneralSettings>`, text, i)
	}
	b.WriteString(`</g:Items>`)
	if more {
		b.WriteString(`<g:EnumerationContext>next</g:EnumerationContext>`)
	} else {
		b.WriteString(`<g:EndOfSequence/>`)
	}
	b.WriteString(`</g:PullResponse></a:Body></a:Envelope>`)
	return b.String()
}

func TestBrowseClassReadsProperties(t *testing.T) {
	d, f := cfgDevice(browseSeq(cfgResp{file: "wsman_pull_general.xml"}))
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil {
		t.Fatal(err)
	}
	if res.Class != "AMT_GeneralSettings" || len(res.Instances) != 1 || res.Truncated {
		t.Fatalf("result %+v", res)
	}
	in := res.Instances[0]
	want := map[string]string{"AMTNetworkEnabled": "1", "DomainName": "example.com", "HostName": "labserver",
		"PowerSource": "0", "SharedFQDN": "a & b <c>"}
	got := map[string]string{}
	for _, p := range in.Props {
		got[p.Name] = p.Value
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["NetworkInterfaceEnabled"]; ok {
		t.Error("a property with xsi:nil is shown")
	}
	if in.Props[0].Name != "AMTNetworkEnabled" || in.Props[len(in.Props)-1].Name != "SharedFQDN" {
		t.Errorf("device order lost: %v", in.Props)
	}
	for _, s := range []string{"<AMT_GeneralSettings>", "  <HostName>labserver</HostName>", "<SharedFQDN>a &amp; b &lt;c&gt;</SharedFQDN>", "</AMT_GeneralSettings>"} {
		if !strings.Contains(in.XML, s) {
			t.Errorf("XML lacks %q:\n%s", s, in.XML)
		}
	}
	if strings.Contains(in.XML, "xmlns") || strings.Contains(in.XML, "h:") {
		t.Errorf("XML keeps namespaces:\n%s", in.XML)
	}
	if res.XML() != in.XML {
		t.Errorf("result XML %q", res.XML())
	}
	enum := f.requestsFor("Enumerate|AMT_GeneralSettings")
	if len(enum) != 1 || !strings.Contains(enum[0], "<w:ResourceURI>http://intel.com/wbem/wscim/1/amt-schema/1/AMT_GeneralSettings</w:ResourceURI>") {
		t.Errorf("enumerate request %v", enum)
	}
	pull := f.requestsFor("Pull|AMT_GeneralSettings")
	if len(pull) != 1 || !strings.Contains(pull[0], "<MaxElements>32</MaxElements>") || !strings.Contains(pull[0], "EE010000-0000-0000-0000-000000000000") {
		t.Errorf("pull request %v", pull)
	}
	if len(f.requestsFor("Release|AMT_GeneralSettings")) != 0 {
		t.Error("a finished read released its context")
	}
}

func TestBrowseClassSchemaPerPrefix(t *testing.T) {
	for class, uri := range map[string]string{
		"CIM_ComputerSystem": "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_ComputerSystem",
		"IPS_OptInService":   "http://intel.com/wbem/wscim/1/ips-schema/1/IPS_OptInService",
		"AMT_AuditLog":       "http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuditLog",
	} {
		seq := map[string][]cfgResp{
			"Enumerate|" + class: {{file: "wsman_enumerate.xml"}},
			"Pull|" + class:      {{file: "wsman_pull_empty.xml"}},
		}
		d, f := cfgDevice(seq)
		res, err := d.BrowseClass(class)
		if err != nil || len(res.Instances) != 0 || res.Truncated {
			t.Fatalf("%s: %+v, %v", class, res, err)
		}
		if reqs := f.requestsFor("Enumerate|" + class); len(reqs) != 1 || !strings.Contains(reqs[0], "<w:ResourceURI>"+uri+"</w:ResourceURI>") {
			t.Errorf("%s: enumerate request %v", class, reqs)
		}
	}
}

func TestBrowseClassNestedValuesAndDerivedClasses(t *testing.T) {
	seq := map[string][]cfgResp{
		"Enumerate|CIM_SettingData": {{file: "wsman_enumerate.xml"}},
		"Pull|CIM_SettingData":      {{file: "wsman_pull_refs.xml"}},
	}
	d, _ := cfgDevice(seq)
	res, err := d.BrowseClass("CIM_SettingData")
	if err != nil || len(res.Instances) != 2 {
		t.Fatalf("%+v, %v", res, err)
	}
	first, second := res.Instances[0], res.Instances[1]
	if first.Class != "CIM_ElementSettingData" || second.Class != "AMT_Derived" {
		t.Errorf("classes %q, %q", first.Class, second.Class)
	}
	got := map[string]string{}
	for _, p := range first.Props {
		got[p.Name] = p.Value
	}
	if got["LastUpdate"] != "2026-11-01T10:00:00Z" {
		t.Errorf("LastUpdate = %q", got["LastUpdate"])
	}
	ref := got["ManagedElement"]
	for _, s := range []string{"Address=http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous", "ResourceURI=http://intel.com/wbem/wscim/1/amt-schema/1/AMT_GeneralSettings", "InstanceID=Intel(r) AMT: General Settings"} {
		if !strings.Contains(ref, s) {
			t.Errorf("reference %q lacks %q", ref, s)
		}
	}
	if !strings.Contains(first.XML, `<Selector Name="InstanceID">Intel(r) AMT: General Settings</Selector>`) {
		t.Errorf("XML:\n%s", first.XML)
	}
}

func TestBrowseClassMultiplePulls(t *testing.T) {
	d, f := cfgDevice(browseSeq(cfgResp{body: browsePull(32, true, "x")}, cfgResp{body: browsePull(5, false, "x")}))
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil || len(res.Instances) != 37 || res.Truncated {
		t.Fatalf("%d instances, truncated %v, %v", len(res.Instances), res.Truncated, err)
	}
	pulls := f.requestsFor("Pull|AMT_GeneralSettings")
	if len(pulls) != 2 || !strings.Contains(pulls[1], "<EnumerationContext>next</EnumerationContext>") {
		t.Errorf("pulls %v", pulls)
	}
}

func TestBrowseClassCapsInstances(t *testing.T) {
	d, f := cfgDevice(browseSeq(cfgResp{body: browsePull(32, true, "x")})) // the device never ends
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Instances) != WSManMaxInstances || !res.Truncated {
		t.Fatalf("%d instances, truncated %v", len(res.Instances), res.Truncated)
	}
	if n := len(f.requestsFor("Pull|AMT_GeneralSettings")); n > 8 {
		t.Errorf("%d pulls for %d instances", n, WSManMaxInstances)
	}
	if rel := f.requestsFor("Release|AMT_GeneralSettings"); len(rel) != 1 || !strings.Contains(rel[0], "<EnumerationContext>next</EnumerationContext>") {
		t.Errorf("release requests %v", rel)
	}
}

func TestBrowseClassExactCapAtEndIsNotTruncated(t *testing.T) {
	var pulls []cfgResp
	for range WSManMaxInstances/wsmanPullSize - 1 { // 6 pulls of 32 = 192
		pulls = append(pulls, cfgResp{body: browsePull(wsmanPullSize, true, "x")})
	}
	pulls = append(pulls, cfgResp{body: browsePull(WSManMaxInstances-len(pulls)*wsmanPullSize, false, "x")})
	d, _ := cfgDevice(browseSeq(pulls...))
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil || len(res.Instances) != WSManMaxInstances || res.Truncated {
		t.Fatalf("%d instances, truncated %v, %v", len(res.Instances), res.Truncated, err)
	}
}

func TestBrowseClassCapsBytes(t *testing.T) {
	big := strings.Repeat("v", WSManMaxBytes/2+1)
	d, _ := cfgDevice(browseSeq(cfgResp{body: browsePull(4, false, big)}))
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Instances) != 1 || !res.Truncated {
		t.Fatalf("%d instances, truncated %v", len(res.Instances), res.Truncated)
	}
	total := 0
	for _, in := range res.Instances {
		total += len(in.XML)
	}
	if total > WSManMaxBytes+1<<10 {
		t.Errorf("%d bytes kept", total)
	}
}

func TestBrowseClassKeepsOneHugeInstance(t *testing.T) {
	huge := strings.Repeat("v", WSManMaxBytes+10)
	d, _ := cfgDevice(browseSeq(cfgResp{body: browsePull(1, false, huge)}))
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil || len(res.Instances) != 1 || res.Truncated {
		t.Fatalf("%d instances, truncated %v, %v", len(res.Instances), res.Truncated, err)
	}
}

func TestBrowseClassEmptyClass(t *testing.T) {
	d, _ := cfgDevice(browseSeq(cfgResp{file: "wsman_pull_empty.xml"}))
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil || len(res.Instances) != 0 || res.Truncated {
		t.Fatalf("%+v, %v", res, err)
	}
}

func TestBrowseClassEnumerateWithoutContextIsEmpty(t *testing.T) {
	seq := browseSeq()
	seq["Enumerate|AMT_GeneralSettings"] = []cfgResp{{body: `<a:Envelope xmlns:a="s" xmlns:g="e"><a:Body><g:EnumerateResponse><g:EndOfSequence/></g:EnumerateResponse></a:Body></a:Envelope>`}}
	d, f := cfgDevice(seq)
	res, err := d.BrowseClass("AMT_GeneralSettings")
	if err != nil || len(res.Instances) != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
	if len(f.requestsFor("Pull|AMT_GeneralSettings")) != 0 {
		t.Error("pulled without a context")
	}
}

func TestBrowseClassMissingClass(t *testing.T) {
	seq := browseSeq()
	seq["Enumerate|AMT_RedirectionService"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, _ := cfgDevice(seq)
	_, err := d.BrowseClass("AMT_RedirectionService")
	var cu *ClassUnsupportedError
	if !errors.As(err, &cu) || cu.Class != "AMT_RedirectionService" || cu.Fault != "DestinationUnreachable" {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, ErrAccessDenied) {
		t.Error("a missing class is not a missing permission")
	}
}

func TestBrowseClassAccessDenied(t *testing.T) {
	for _, name := range []string{"Enumerate", "Pull"} {
		seq := browseSeq(cfgResp{file: "wsman_pull_general.xml"})
		seq[name+"|AMT_GeneralSettings"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
		d, _ := cfgDevice(seq)
		_, err := d.BrowseClass("AMT_GeneralSettings")
		if !errors.Is(err, ErrAccessDenied) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestBrowseClassOtherErrors(t *testing.T) {
	seq := browseSeq()
	seq["Enumerate|AMT_GeneralSettings"] = []cfgResp{{err: errors.New("dial tcp: i/o timeout")}}
	d, _ := cfgDevice(seq)
	_, err := d.BrowseClass("AMT_GeneralSettings")
	if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "AMT_GeneralSettings") ||
		errors.Is(err, ErrAccessDenied) {
		t.Errorf("err = %v", err)
	}
	var cu *ClassUnsupportedError
	if errors.As(err, &cu) {
		t.Error("a timeout is not a missing class")
	}

	seq = browseSeq(cfgResp{body: `<not xml`})
	d, _ = cfgDevice(seq)
	if _, err := d.BrowseClass("AMT_GeneralSettings"); !errors.Is(err, ErrIncompleteResponse) {
		t.Errorf("garbage pull: %v", err)
	}
	seq = browseSeq()
	seq["Enumerate|AMT_GeneralSettings"] = []cfgResp{{body: `<a:Envelope xmlns:a="s"><a:Body/></a:Envelope>`}}
	d, _ = cfgDevice(seq)
	if _, err := d.BrowseClass("AMT_GeneralSettings"); !errors.Is(err, ErrIncompleteResponse) {
		t.Errorf("enumerate without response: %v", err)
	}
}

func TestBrowseClassRejectsBadNames(t *testing.T) {
	d, f := cfgDevice(map[string][]cfgResp{})
	for _, name := range []string{"", "AMT_", "XYZ_Thing", "AMT_A</w:ResourceURI><x>", "AMT_A B", "amt_GeneralSettings", "AMT_A/../B"} {
		if _, err := d.BrowseClass(name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
	if len(f.requests) != 0 {
		t.Errorf("%d requests for bad names", len(f.requests))
	}
}

func TestWSManClasses(t *testing.T) {
	list := WSManClasses()
	if len(list) < 200 {
		t.Fatalf("%d classes", len(list))
	}
	seen := map[string]bool{}
	rank := map[string]int{"AMT": 0, "CIM": 1, "IPS": 2}
	for i, c := range list {
		if !ValidWSManClass(c) {
			t.Errorf("%q is not a valid class name", c)
		}
		if seen[c] {
			t.Errorf("%q twice", c)
		}
		seen[c] = true
		if i > 0 {
			p, q := list[i-1], c
			if rank[p[:3]] > rank[q[:3]] || (p[:3] == q[:3] && p >= q) {
				t.Errorf("%q before %q breaks the order", p, q)
			}
		}
	}
	for _, c := range []string{"AMT_GeneralSettings", "AMT_AuditLog", "CIM_ComputerSystem", "CIM_PowerManagementService", "IPS_OptInService", "IPS_AlarmClockOccurrence", "IPS_HostBootReason"} {
		if !seen[c] {
			t.Errorf("%s missing", c)
		}
	}
	list[0] = "changed"
	if WSManClasses()[0] == "changed" {
		t.Error("WSManClasses returns the internal slice")
	}
	if !slices.IsSorted(WSManClasses()[:3]) {
		t.Error("AMT group unsorted")
	}
}

func TestWSManMeaning(t *testing.T) {
	for _, tc := range []struct{ class, prop, value, want string }{
		{"AMT_GeneralSettings", "AMTNetworkEnabled", "1", "Enabled"},
		{"AMT_GeneralSettings", "PowerSource", " 0 ", "AC"},
		{"CIM_Anything", "EnabledState", "2", "Enabled"},
		{"CIM_Anything", "HealthState", "5", "OK"},
		{"AMT_GeneralSettings", "AMTNetworkEnabled", "9", ""},
		{"AMT_Other", "PowerSource", "0", ""}, // class-specific
		{"AMT_GeneralSettings", "HostName", "1", ""},
	} {
		if got := WSManMeaning(tc.class, tc.prop, tc.value); got != tc.want {
			t.Errorf("%s.%s=%q: %q, want %q", tc.class, tc.prop, tc.value, got, tc.want)
		}
	}
}
