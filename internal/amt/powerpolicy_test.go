package amt

import (
	"errors"
	"strings"
	"testing"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
)

func policySeq() map[string][]cfgResp {
	seq := settingsSeq()
	seq["Enumerate|AMT_SystemPowerScheme"] = []cfgResp{{file: "enumerate.xml"}}
	seq["Pull|AMT_SystemPowerScheme"] = []cfgResp{{file: "scheme_pull.xml"}}
	seq["Enumerate|CIM_ElementSettingData"] = []cfgResp{{file: "settingdata_enumerate.xml"}}
	seq["Pull|CIM_ElementSettingData"] = []cfgResp{{file: "settingdata_pull.xml"}}
	seq["SetPowerScheme|AMT_SystemPowerScheme"] = []cfgResp{{file: "setscheme_ok.xml"}}
	seq["Put|AMT_GeneralSettings"] = []cfgResp{{file: "cfg_put_ok.xml"}}
	return seq
}

func TestGetPowerPolicy(t *testing.T) {
	d, _ := cfgDevice(policySeq())
	p, err := d.GetPowerPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Schemes) != 3 || p.Current != "SCHEME 1" || p.IdleTimeoutMinutes != 65535 {
		t.Fatalf("policy %+v", p)
	}
	s := p.Schemes[1]
	if s.ID != "SCHEME 1" || s.GUID != "MIAN7gnAeEOvKHhootu+Og==" || s.Description != "Desktop: ON in S0, ME Wake in S3, S4-5" || !s.KeepsRemoteStart {
		t.Errorf("scheme 1 = %+v", s)
	}
	if p.Schemes[0].KeepsRemoteStart || p.Schemes[2].KeepsRemoteStart {
		t.Errorf("packages without S4-5 keep the remote start: %+v", p.Schemes)
	}
	if cur, ok := p.CurrentScheme(); !ok || cur.ID != "SCHEME 1" {
		t.Errorf("current = %+v %v", cur, ok)
	}
}

func TestGetPowerPolicyWithoutCurrent(t *testing.T) {
	seq := policySeq()
	seq["Enumerate|CIM_ElementSettingData"] = []cfgResp{{file: "access_denied_fault.xml"}}
	d, _ := cfgDevice(seq)
	p, err := d.GetPowerPolicy()
	if err != nil || len(p.Schemes) != 3 || p.Current != "" {
		t.Fatalf("policy %+v, err %v", p, err)
	}
	if _, ok := p.CurrentScheme(); ok {
		t.Error("an unknown current package was found")
	}
}

func TestGetPowerPolicyFailsWithoutSchemes(t *testing.T) {
	seq := policySeq()
	seq["Enumerate|AMT_SystemPowerScheme"] = []cfgResp{{file: "access_denied_fault.xml"}}
	d, _ := cfgDevice(seq)
	if _, err := d.GetPowerPolicy(); err == nil {
		t.Fatal("read succeeded")
	}
}

func TestRisksRemoteStart(t *testing.T) {
	p := PowerPolicy{Current: "SCHEME 1", Schemes: []PowerScheme{
		{ID: "SCHEME 0"}, {ID: "SCHEME 1", KeepsRemoteStart: true}, {ID: "SCHEME 2", KeepsRemoteStart: true}, {ID: "SCHEME 3"}}}
	for id, want := range map[string]bool{"SCHEME 0": true, "SCHEME 2": false, "SCHEME 1": false, "SCHEME 9": false} {
		if got := p.RisksRemoteStart(id); got != want {
			t.Errorf("%s: %v, want %v", id, got, want)
		}
	}
	p.Current = "SCHEME 0" // no remote start now: no new loss
	if p.RisksRemoteStart("SCHEME 3") {
		t.Error("a switch between packages without remote start is risky")
	}
	p.Current = "" // unknown: warn
	if !p.RisksRemoteStart("SCHEME 3") {
		t.Error("an unknown current package must warn")
	}
}

func TestSetPowerSchemeSafeChangeNeedsNoConfirmation(t *testing.T) {
	seq := policySeq()
	seq["Pull|CIM_ElementSettingData"] = []cfgResp{{file: "settingdata_pull_scheme0.xml"}} // SCHEME 0 is active
	d, f := cfgDevice(seq)
	if err := d.SetPowerScheme("SCHEME 1", Confirmation{}); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "SetPowerScheme|AMT_SystemPowerScheme")
	assertContains(t, body,
		`<w:Selector Name="InstanceID">SCHEME 1</w:Selector>`,
		`<w:Selector Name="SchemeGUID">MIAN7gnAeEOvKHhootu+Og==</w:Selector>`,
		"SetPowerScheme_INPUT", "AMT_SystemPowerScheme")
}

func TestSetPowerSchemeRiskyChangeNeedsConfirmation(t *testing.T) {
	d, f := cfgDevice(policySeq()) // SCHEME 1 keeps the remote start, SCHEME 0 does not
	err := d.SetPowerScheme("SCHEME 0", Confirmation{})
	var re *RiskyChangeError
	if !errors.As(err, &re) || re.Op != OpPowerPolicy {
		t.Fatalf("err = %v", err)
	}
	if len(f.requestsFor("SetPowerScheme|AMT_SystemPowerScheme")) != 0 {
		t.Fatal("the device was written without confirmation")
	}
	wrong, _ := ConfirmChecked(OpTLS, testHost, true)
	if err := d.SetPowerScheme("SCHEME 0", wrong); !errors.As(err, &re) {
		t.Fatalf("a confirmation of another operation was accepted: %v", err)
	}
	checked, err := ConfirmChecked(OpPowerPolicy, testHost, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetPowerScheme("SCHEME 0", checked); err != nil {
		t.Fatal(err)
	}
	onePut(t, f, "SetPowerScheme|AMT_SystemPowerScheme")
}

func TestSetPowerSchemeErrors(t *testing.T) {
	d, _ := cfgDevice(policySeq())
	if err := d.SetPowerScheme("SCHEME 9", Confirmation{}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("unknown package: %v", err)
	}
	seq := policySeq()
	seq["SetPowerScheme|AMT_SystemPowerScheme"] = []cfgResp{{file: "setscheme_fail.xml"}}
	d, _ = cfgDevice(seq)
	var me *MethodError
	if err := d.SetPowerScheme("SCHEME 1", Confirmation{}); !errors.As(err, &me) || me.ReturnValue != 38 {
		t.Fatalf("err = %v", err)
	}
	seq["SetPowerScheme|AMT_SystemPowerScheme"] = []cfgResp{{file: "empty_body.xml"}}
	d, _ = cfgDevice(seq)
	if err := d.SetPowerScheme("SCHEME 1", Confirmation{}); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("empty answer: %v", err)
	}
}

func TestSetIdleTimeoutWritesCompleteObject(t *testing.T) {
	d, f := cfgDevice(policySeq())
	if err := d.SetIdleTimeout(30); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Put|AMT_GeneralSettings")
	assertContains(t, body, "<h:IdleWakeTimeout>30</h:IdleWakeTimeout>", "<h:HostName>amt-test</h:HostName>")
	for _, bad := range []int{0, -1, 65536} {
		if err := d.SetIdleTimeout(bad); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%d accepted: %v", bad, err)
		}
	}
	if err := d.SetIdleTimeout(65535); err != nil { // same as the device's value: nothing to write
		t.Fatal(err)
	}
}

func TestPowerSleepAndHibernateSendCIMCodes(t *testing.T) {
	for action, want := range map[PowerAction]string{PowerSleep: "<h:PowerState>4</h:PowerState>", PowerHibernate: "<h:PowerState>7</h:PowerState>"} {
		f := &fakeWSMan{routes: map[string]string{powerRoute: "power_action_ok.xml"}}
		if err := newDevice(f, "").Power(action); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if len(f.requests) != 1 || !strings.Contains(f.requests[0], want) {
			t.Fatalf("%s: %v", action, f.requests)
		}
	}
	if PowerSleep.String() != "sleep" || PowerHibernate.String() != "hibernate" || !PowerSleep.Destructive() {
		t.Error("names or Destructive")
	}
	if power.SleepDeep != 4 || power.Hibernate != 7 {
		t.Error("library constants changed")
	}
}

func TestSleepHibernateConfirmationLevel(t *testing.T) {
	if r := RiskOf(OpSleepHibernate); r.Level != LevelSimple {
		t.Errorf("OpSleepHibernate = %+v", r)
	}
	if r := RiskOf(OpPowerPolicy); r.Level != LevelOrange {
		t.Errorf("OpPowerPolicy = %+v", r)
	}
}
