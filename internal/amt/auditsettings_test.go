package amt

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

func auditSeq() map[string][]cfgResp {
	return map[string][]cfgResp{
		"Get|AMT_AuditLog":                           {{file: "audit_log_get.xml"}},
		"Put|AMT_AuditLog":                           {{file: "audit_put_ok.xml"}},
		"Enumerate|AMT_AuditPolicyRule":              {{file: "enumerate.xml"}},
		"Pull|AMT_AuditPolicyRule":                   {{file: "audit_policy_pull.xml"}},
		"RequestStateChange|AMT_AuditLog":            {{file: "audit_rsc_ok.xml"}},
		"SetAuditPolicyBulk|AMT_AuditPolicyRule":     {{file: "audit_bulk_ok.xml"}},
		"SetAuditLock|AMT_AuditLog":                  {{file: "audit_lock_ok.xml"}, {file: "audit_unlock_ok.xml"}},
		"ClearLog|AMT_AuditLog":                      {{file: "audit_clear_ok.xml"}},
		"Enumerate|CIM_FilterCollection":             {{file: "enumerate.xml"}},
		"Enumerate|CIM_ListenerDestination":          {{file: "enumerate.xml"}},
		"Enumerate|CIM_FilterCollectionSubscription": {{file: "enumerate.xml"}},
	}
}

func TestAuditSettingsReadsLogAndPolicy(t *testing.T) {
	d, f := cfgDevice(auditSeq())
	s, err := d.AuditSettings()
	if err != nil {
		t.Fatal(err)
	}
	want := AuditSettings{Recording: true, Storage: AuditOverwriteOldest, Records: 34, PercentUsed: 9,
		Policy: []AuditEvent{{17, 0}, {17, 4}, {20, 0}, {29, 8}}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("got %+v\nwant %+v", s, want)
	}
	if !s.Recorded(AuditEvent{29, 8}) || s.Recorded(AuditEvent{29, 0}) {
		t.Error("Recorded")
	}
	// the policy is enumerated without selectors, like the library does it
	assertContains(t, onePut(t, f, "Pull|AMT_AuditPolicyRule"), "<EnumerationContext>DD020000-0000-0000-0000-000000000000</EnumerationContext>")
}

func TestAuditSettingsRestrictedAndDisabled(t *testing.T) {
	seq := auditSeq()
	seq["Get|AMT_AuditLog"] = []cfgResp{{file: "audit_log_get_restricted.xml"}}
	d, _ := cfgDevice(seq)
	s, err := d.AuditSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Recording || s.Storage != AuditOverwriteAfterDays || s.MinDaysToKeep != 7 || s.Records != 120 || s.PercentUsed != 40 {
		t.Fatalf("got %+v", s)
	}
}

func TestAuditSettingsWithoutStoragePolicyIsRollOver(t *testing.T) {
	seq := auditSeq()
	seq["Get|AMT_AuditLog"] = []cfgResp{{body: withProp(t, "audit_log_get.xml", "StoragePolicy", "-")}}
	d, _ := cfgDevice(seq)
	if s, err := d.AuditSettings(); err != nil || s.Storage != AuditOverwriteOldest {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestAuditSettingsAccessDenied(t *testing.T) {
	for _, route := range []string{"Get|AMT_AuditLog", "Enumerate|AMT_AuditPolicyRule"} {
		for _, sub := range []string{"b:AccessDenied", "b:DestinationUnreachable"} {
			seq := auditSeq()
			seq[route] = []cfgResp{{err: amterror.NewAMTError(sub, "refused", "")}}
			d, _ := cfgDevice(seq)
			if _, err := d.AuditSettings(); !errors.Is(err, ErrAccessDenied) {
				t.Errorf("%s %s: %v", route, sub, err)
			}
		}
	}
	seq := auditSeq()
	seq["Get|AMT_AuditLog"] = []cfgResp{{err: errors.New("network down")}}
	d, _ := cfgDevice(seq)
	if _, err := d.AuditSettings(); err == nil || errors.Is(err, ErrAccessDenied) {
		t.Errorf("other error: %v", err)
	}
}

func TestDecodeAuditEventID(t *testing.T) {
	for v, want := range map[uint32]AuditEvent{
		17<<16 | 4:     {17, 4},
		29<<24 | 8:     {29, 8},
		16<<16 | 0x13:  {16, 19},
		33<<24 | 0x102: {33, 258},
	} {
		if got := decodeAuditEventID(v); got != want {
			t.Errorf("%#x: %+v, want %+v", v, got, want)
		}
	}
}

func TestKnownAuditEvents(t *testing.T) {
	got := KnownAuditEvents(30)
	if want := []AuditEvent{{30, 0}, {30, 1}, {30, 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if len(KnownAuditEvents(16)) != 20 || KnownAuditEvents(31) != nil {
		t.Error("Security Admin has 20 events, 31 none")
	}
}

func TestSetAuditSettingsWritesOnlyWhatChanged(t *testing.T) {
	d, f := cfgDevice(auditSeq())
	if err := d.SetAuditSettings(AuditChange{}, Confirmation{}); err != nil || len(f.requests) != 0 {
		t.Fatalf("empty change: %v, %d requests", err, len(f.requests))
	}
	on := true
	err := d.SetAuditSettings(AuditChange{Recording: &on, Enable: []AuditEvent{{29, 0}, {29, 1}}, Disable: []AuditEvent{{17, 4}}}, Confirmation{})
	if err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "SetAuditPolicyBulk|AMT_AuditPolicyRule")
	assertContains(t, body,
		`<a:Action>http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuditPolicyRule/SetAuditPolicyBulk</a:Action>`,
		`<h:SetAuditPolicyBulk_INPUT xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuditPolicyRule">`+
			`<h:Enable>true</h:Enable><h:Enable>true</h:Enable><h:Enable>false</h:Enable>`+
			`<h:AuditedAppID>29</h:AuditedAppID><h:AuditedAppID>29</h:AuditedAppID><h:AuditedAppID>17</h:AuditedAppID>`+
			`<h:EventID>0</h:EventID><h:EventID>1</h:EventID><h:EventID>4</h:EventID>`+
			`<h:PolicyType>0</h:PolicyType><h:PolicyType>0</h:PolicyType><h:PolicyType>0</h:PolicyType></h:SetAuditPolicyBulk_INPUT>`)
	rsc := onePut(t, f, "RequestStateChange|AMT_AuditLog")
	assertContains(t, rsc, `<h:RequestStateChange_INPUT xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuditLog"><h:RequestedState>2</h:RequestedState></h:RequestStateChange_INPUT>`)
	if len(f.requestsFor("Put|AMT_AuditLog")) != 0 {
		t.Error("storage written without a change")
	}
	// order: policy before the state
	if routeKey(f.requests[0]) != "SetAuditPolicyBulk|AMT_AuditPolicyRule" || routeKey(f.requests[1]) != "RequestStateChange|AMT_AuditLog" {
		t.Errorf("order %s, %s", routeKey(f.requests[0]), routeKey(f.requests[1]))
	}
}

func TestSetAuditPolicySplitsLargeChanges(t *testing.T) {
	d, f := cfgDevice(auditSeq())
	var many []AuditEvent
	for i := range 45 {
		many = append(many, AuditEvent{16, i})
	}
	if err := d.SetAuditSettings(AuditChange{Enable: many}, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	reqs := f.requestsFor("SetAuditPolicyBulk|AMT_AuditPolicyRule")
	if len(reqs) != 3 || strings.Count(reqs[0], "<h:Enable>") != 20 || strings.Count(reqs[2], "<h:Enable>") != 5 {
		t.Fatalf("%d requests", len(reqs))
	}
}

func TestSetAuditStoragePutsPolicyAndDays(t *testing.T) {
	d, f := cfgDevice(auditSeq())
	st, days := AuditOverwriteAfterDays, 14
	if err := d.SetAuditSettings(AuditChange{Storage: &st, MinDaysToKeep: &days}, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Put|AMT_AuditLog")
	assertContains(t, body, `<h:AMT_AuditLog xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuditLog">`,
		"<h:StoragePolicy>2</h:StoragePolicy>", "<h:MinDaysToKeep>14</h:MinDaysToKeep>", "<h:Name>Intel(r) AMT:Audit Log</h:Name>")
	assertLacks(t, body, "TimeOfLastRecord", "Datetime")

	// back to roll over: MinDaysToKeep goes
	seq := auditSeq()
	seq["Get|AMT_AuditLog"] = []cfgResp{{file: "audit_log_get_restricted.xml"}}
	d, f = cfgDevice(seq)
	st = AuditStopWhenFull
	if err := d.SetAuditSettings(AuditChange{Storage: &st}, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	body = onePut(t, f, "Put|AMT_AuditLog")
	assertContains(t, body, "<h:StoragePolicy>0</h:StoragePolicy>")
	assertLacks(t, body, "MinDaysToKeep")

	// the same policy and days: nothing is written
	d, f = cfgDevice(seq)
	st, days = AuditOverwriteAfterDays, 7
	if err := d.SetAuditSettings(AuditChange{Storage: &st, MinDaysToKeep: &days}, Confirmation{}); err != nil || len(f.requestsFor("Put|AMT_AuditLog")) != 0 {
		t.Fatalf("%v, %d puts", err, len(f.requestsFor("Put|AMT_AuditLog")))
	}
}

func TestSetAuditSettingsValidates(t *testing.T) {
	d, f := cfgDevice(auditSeq())
	st, bad := AuditOverwriteAfterDays, 0
	for _, c := range []AuditChange{
		{Storage: &st},
		{Storage: &st, MinDaysToKeep: &bad},
		{Storage: &st, MinDaysToKeep: ptr(101)},
		{Storage: ptr(AuditStorage(3))},
	} {
		if err := d.SetAuditSettings(c, Confirmation{}); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if len(f.requests) != 0 {
		t.Errorf("%d requests", len(f.requests))
	}
}

// Switching the recording off is orange (OpAuditSettings): without its confirmation nothing is
// written; with it the state goes to Disabled (3).
func TestSetAuditSettingsRecordingOffNeedsConfirmation(t *testing.T) {
	d, f := cfgDevice(auditSeq())
	off := false
	c := AuditChange{Recording: &off, Disable: []AuditEvent{{17, 0}}}
	var re *RiskyChangeError
	if err := d.SetAuditSettings(c, confirmed(OpAuditClear)); !errors.As(err, &re) || re.Op != OpAuditSettings || len(f.requests) != 0 {
		t.Fatalf("%v, %d requests", err, len(f.requests))
	}
	if err := d.SetAuditSettings(c, confirmed(OpAuditSettings)); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "RequestStateChange|AMT_AuditLog"), "<h:RequestedState>3</h:RequestedState>")
	if !c.Risky() || (AuditChange{Disable: []AuditEvent{{17, 0}}}).Risky() {
		t.Error("only Recording off is risky")
	}
}

func TestSetAuditSettingsMethodErrors(t *testing.T) {
	on := true
	for _, tc := range []struct {
		route, file, method string
		rv                  int
	}{
		{"SetAuditPolicyBulk|AMT_AuditPolicyRule", "audit_bulk_fail.xml", "AMT_AuditPolicyRule.SetAuditPolicyBulk", 36},
		{"RequestStateChange|AMT_AuditLog", "audit_rsc_fail.xml", "AMT_AuditLog.RequestStateChange", 16},
	} {
		seq := auditSeq()
		seq[tc.route] = []cfgResp{{file: tc.file}}
		d, f := cfgDevice(seq)
		err := d.SetAuditSettings(AuditChange{Recording: &on, Enable: []AuditEvent{{29, 0}}}, Confirmation{})
		var me *MethodError
		if !errors.As(err, &me) || me.Method != tc.method || me.ReturnValue != tc.rv {
			t.Errorf("%s: %v", tc.route, err)
		}
		if tc.route == "SetAuditPolicyBulk|AMT_AuditPolicyRule" && len(f.requestsFor("RequestStateChange|AMT_AuditLog")) != 0 {
			t.Error("the state was written after a failed policy")
		}
	}
	seq := auditSeq()
	seq["RequestStateChange|AMT_AuditLog"] = []cfgResp{{err: amterror.NewAMTError("b:AccessDenied", "x", "")}}
	d, _ := cfgDevice(seq)
	if err := d.SetAuditSettings(AuditChange{Recording: &on}, Confirmation{}); !errors.Is(err, ErrAccessDenied) {
		t.Errorf("denied: %v", err)
	}
	seq["RequestStateChange|AMT_AuditLog"] = []cfgResp{{file: "empty_body.xml"}}
	d, _ = cfgDevice(seq)
	if err := d.SetAuditSettings(AuditChange{Recording: &on}, Confirmation{}); !errors.Is(err, ErrIncompleteResponse) {
		t.Errorf("no output: %v", err)
	}
}

// ClearAuditLog is red (OpAuditClear) and runs lock, ClearLog, unlock with the lock's handle.
func TestClearAuditLogLocksClearsUnlocks(t *testing.T) {
	d, f := cfgDevice(auditSeq())
	if err := d.ClearAuditLog(confirmed(OpAuditSettings)); !errors.Is(err, ErrRiskyChange) || len(f.requests) != 0 {
		t.Fatalf("without its confirmation: %v, %d requests", err, len(f.requests))
	}
	if err := d.ClearAuditLog(confirmed(OpAuditClear)); err != nil {
		t.Fatal(err)
	}
	var routes []string
	for _, r := range f.requests {
		routes = append(routes, routeKey(r))
	}
	if want := []string{"SetAuditLock|AMT_AuditLog", "ClearLog|AMT_AuditLog", "SetAuditLock|AMT_AuditLog"}; !reflect.DeepEqual(routes, want) {
		t.Fatalf("routes %v", routes)
	}
	assertContains(t, f.requests[0], "<h:LockTimeoutInSeconds>30</h:LockTimeoutInSeconds><h:Flag>0</h:Flag><h:Handle>0</h:Handle>")
	assertContains(t, f.requests[1], `<h:ClearLog_INPUT xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuditLog"></h:ClearLog_INPUT>`)
	assertContains(t, f.requests[2], "<h:Flag>2</h:Flag><h:Handle>7</h:Handle>")
}

func TestClearAuditLogErrors(t *testing.T) {
	// a refused lock: nothing is cleared
	seq := auditSeq()
	seq["SetAuditLock|AMT_AuditLog"] = []cfgResp{{file: "audit_lock_fail.xml"}}
	d, f := cfgDevice(seq)
	var me *MethodError
	if err := d.ClearAuditLog(confirmed(OpAuditClear)); !errors.As(err, &me) || me.Method != "AMT_AuditLog.SetAuditLock" || me.ReturnValue != 16 {
		t.Fatalf("lock: %v", err)
	}
	if len(f.requestsFor("ClearLog|AMT_AuditLog")) != 0 {
		t.Error("cleared without a lock")
	}
	// a refused clear: still unlocked, the clear error is returned
	seq = auditSeq()
	seq["ClearLog|AMT_AuditLog"] = []cfgResp{{file: "audit_clear_fail.xml"}}
	d, f = cfgDevice(seq)
	if err := d.ClearAuditLog(confirmed(OpAuditClear)); !errors.As(err, &me) || me.Method != "AMT_AuditLog.ClearLog" {
		t.Fatalf("clear: %v", err)
	}
	if len(f.requestsFor("SetAuditLock|AMT_AuditLog")) != 2 {
		t.Error("not unlocked after a failed clear")
	}
	// a wrong output element
	seq["ClearLog|AMT_AuditLog"] = []cfgResp{{file: "audit_clear_wrong.xml"}}
	d, _ = cfgDevice(seq)
	if err := d.ClearAuditLog(confirmed(OpAuditClear)); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("wrong output: %v", err)
	}
	// a failed unlock after a clear is not an error: the lock ends by itself
	seq = auditSeq()
	seq["SetAuditLock|AMT_AuditLog"] = []cfgResp{{file: "audit_lock_ok.xml"}, {file: "audit_lock_fail.xml"}}
	d, _ = cfgDevice(seq)
	if err := d.ClearAuditLog(confirmed(OpAuditClear)); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	// access denied
	seq = auditSeq()
	seq["SetAuditLock|AMT_AuditLog"] = []cfgResp{{err: amterror.NewAMTError("b:AccessDenied", "x", "")}}
	d, _ = cfgDevice(seq)
	if err := d.ClearAuditLog(confirmed(OpAuditClear)); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied: %v", err)
	}
}

func TestAuditConfirmationLevels(t *testing.T) {
	if RiskOf(OpAuditSettings).Level != LevelOrange || RiskOf(OpAuditClear).Level != LevelRed || RiskOf(OpSubscriptionDelete).Level != LevelSimple {
		t.Fatal("levels of the design: settings orange, clear red, target delete simple")
	}
}
