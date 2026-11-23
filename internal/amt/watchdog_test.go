package amt

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const agentID = "5f1c2a9e-0d3b-4e57-9a61-2c8b7e4d1f03"

// watchdogSeq is defenseSeq plus two watchdogs: backup-agent (running; on stop an event and
// "activate", on start "deactivate") and one without name and actions; the agent policy is
// Isolate.
func watchdogSeq() map[string][]cfgResp {
	seq := defenseSeq()
	for k, v := range map[string][]cfgResp{
		"Enumerate|AMT_AgentPresenceWatchdog":        {{file: "enumerate.xml"}},
		"Pull|AMT_AgentPresenceWatchdog":             {{file: "wd_pull.xml"}},
		"Enumerate|AMT_StateTransitionCondition":     {{file: "enumerate.xml"}},
		"Pull|AMT_StateTransitionCondition":          {{file: "wd_transition_pull.xml"}},
		"Enumerate|AMT_AgentPresenceWatchdogAction":  {{file: "enumerate.xml"}},
		"Pull|AMT_AgentPresenceWatchdogAction":       {{file: "wd_action_pull.xml"}},
		"Enumerate|CIM_ConcreteDependency":           {{file: "enumerate.xml"}},
		"Pull|CIM_ConcreteDependency":                {{file: "wd_dependency_pull.xml"}},
		"Enumerate|AMT_AgentPresenceInterfacePolicy": {{file: "enumerate.xml"}},
		"Pull|AMT_AgentPresenceInterfacePolicy":      {{file: "wd_agentpolicy_pull.xml"}},
		"Create|AMT_AgentPresenceWatchdog":           {{file: "create_ok.xml"}},
		"Delete|AMT_AgentPresenceWatchdog":           {{file: "delete_ok.xml"}},
		"AddAction|AMT_AgentPresenceWatchdog":        {{file: "wd_addaction_ok.xml"}},
		"Create|AMT_AgentPresenceInterfacePolicy":    {{file: "create_ok.xml"}},
		"Delete|AMT_AgentPresenceInterfacePolicy":    {{file: "delete_ok.xml"}},
	} {
		seq[k] = v
	}
	return seq
}

func TestGuidTextRoundTrip(t *testing.T) {
	b, ok := guidBytes(agentID)
	if !ok {
		t.Fatal("not a GUID")
	}
	// the layout of a Windows GUID in memory, as MeshCommander sends it
	if got := b[:4]; !reflect.DeepEqual(got, []byte{0x9e, 0x2a, 0x1c, 0x5f}) {
		t.Errorf("first field % x", got)
	}
	if got := guidText(b); got != agentID {
		t.Errorf("round trip %q", got)
	}
	if _, ok := guidBytes("{5F1C2A9E0D3B4E579A612C8B7E4D1F03}"); !ok {
		t.Error("braces and upper case refused")
	}
	for _, bad := range []string{"", "5f1c2a9e", "zz1c2a9e-0d3b-4e57-9a61-2c8b7e4d1f03"} {
		if ValidAgentID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	a, b2 := NewAgentID(), NewAgentID()
	if !ValidAgentID(a) || a == b2 || a[14] != '4' {
		t.Errorf("NewAgentID %q, %q", a, b2)
	}
}

func TestGetWatchdogsReadsActions(t *testing.T) {
	d, f := cfgDevice(watchdogSeq())
	w, err := d.GetWatchdogs()
	if err != nil {
		t.Fatal(err)
	}
	if !w.Offered || w.Policy != handleIsolate || len(w.List) != 2 {
		t.Fatalf("%+v", w)
	}
	want := Watchdog{ID: agentID, Name: "backup-agent", Startup: 120, Heartbeat: 30, State: WatchdogRunning,
		OnStop:  WatchdogActions{Event: true, Policy: PolicyActivate},
		OnStart: WatchdogActions{Policy: PolicyDeactivate}, deviceID: "niocXzsNV06aYSyLfk0fAw=="}
	if w.List[0] != want {
		t.Errorf("watchdog\n got %+v\nwant %+v", w.List[0], want)
	}
	second := w.List[1]
	if second.ID != "03020100-0504-0706-0809-0a0b0c0d0e0f" || second.State != WatchdogExpired || second.OnStop.any() || second.OnStart.any() {
		t.Errorf("second %+v", second)
	}
	assertNoWrites(t, f)
}

func TestGetWatchdogsEmptyReadsNoActions(t *testing.T) {
	seq := watchdogSeq()
	seq["Pull|AMT_AgentPresenceWatchdog"] = []cfgResp{{file: "wd_pull_empty.xml"}}
	d, f := cfgDevice(seq)
	w, err := d.GetWatchdogs()
	if err != nil || !w.Offered || len(w.List) != 0 {
		t.Fatalf("%+v, err %v", w, err)
	}
	if n := len(f.requestsFor("Enumerate|CIM_ConcreteDependency")); n != 0 {
		t.Errorf("%d dependency reads without a watchdog", n)
	}
}

func TestGetWatchdogsMissingClassAndDenied(t *testing.T) {
	seq := watchdogSeq()
	seq["Enumerate|AMT_AgentPresenceWatchdog"] = []cfgResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, _ := cfgDevice(seq)
	if w, err := d.GetWatchdogs(); err != nil || w.Offered {
		t.Fatalf("%+v, err %v", w, err)
	}
	seq = watchdogSeq()
	seq["Enumerate|AMT_AgentPresenceWatchdog"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, _ = cfgDevice(seq)
	if _, err := d.GetWatchdogs(); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err %v", err)
	}
}

func goodWatchdog() WatchdogSpec {
	return WatchdogSpec{Name: "av-agent", ID: "11111111-2222-4333-8444-555555555555", Startup: 120, Heartbeat: 30}
}

func TestWatchdogSpecValidate(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*WatchdogSpec)
		ok   bool
	}{
		{"valid", func(*WatchdogSpec) {}, true},
		{"no name", func(s *WatchdogSpec) { s.Name = "" }, true},
		{"name of 17 characters", func(s *WatchdogSpec) { s.Name = strings.Repeat("n", 17) }, false},
		{"control character", func(s *WatchdogSpec) { s.Name = "a\x00" }, false},
		{"bad ID", func(s *WatchdogSpec) { s.ID = "agent" }, false},
		{"startup 0", func(s *WatchdogSpec) { s.Startup = 0 }, false},
		{"startup 65536", func(s *WatchdogSpec) { s.Startup = 65536 }, false},
		{"heartbeat 65535", func(s *WatchdogSpec) { s.Heartbeat = 65535 }, true},
		{"heartbeat 0", func(s *WatchdogSpec) { s.Heartbeat = 0 }, false},
		{"policy action without policy", func(s *WatchdogSpec) { s.OnStop.Policy = PolicyActivate }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := goodWatchdog()
			c.mod(&s)
			if err := s.Validate(); c.ok != (err == nil) {
				t.Fatalf("err %v, want ok %v", err, c.ok)
			}
		})
	}
}

func TestAddWatchdogWithEventsOnly(t *testing.T) {
	d, f := cfgDevice(watchdogSeq())
	s := goodWatchdog()
	s.OnStop.Event, s.OnStart.Event = true, true
	if err := d.AddWatchdog(s, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	create := onePut(t, f, "Create|AMT_AgentPresenceWatchdog")
	assertContains(t, create, "<h:CreationClassName>AMT_AgentPresenceWatchdog</h:CreationClassName>",
		"<h:DeviceID>ERERESIiM0OERFVVVVVVVQ==</h:DeviceID>", "<h:StartupInterval>120</h:StartupInterval>",
		"<h:SystemCreationClassName>CIM_ComputerSystem</h:SystemCreationClassName><h:SystemName>Intel(r) AMT</h:SystemName>",
		"<h:TimeoutInterval>30</h:TimeoutInterval><h:MonitoredEntityDescription>av-agent</h:MonitoredEntityDescription>")
	acts := f.requestsFor("AddAction|AMT_AgentPresenceWatchdog")
	if len(acts) != 2 {
		t.Fatalf("%d actions", len(acts))
	}
	assertContains(t, acts[0], `<w:Selector Name="DeviceID">ERERESIiM0OERFVVVVVVVQ==</w:Selector>`,
		"<h:OldState>5</h:OldState><h:NewState>8</h:NewState><h:EventOnTransition>true</h:EventOnTransition>")
	assertContains(t, acts[1], "<h:OldState>11</h:OldState><h:NewState>4</h:NewState><h:EventOnTransition>true</h:EventOnTransition>")
	for _, a := range acts {
		assertLacks(t, a, "ActionSd")
	}
	if n := len(f.requestsFor("Create|AMT_AgentPresenceInterfacePolicy")) + len(f.requestsFor("Delete|AMT_AgentPresenceInterfacePolicy")); n != 0 {
		t.Errorf("%d agent policy writes without a policy action", n)
	}
}

func TestAddWatchdogWithoutActions(t *testing.T) {
	d, f := cfgDevice(watchdogSeq())
	if err := d.AddWatchdog(goodWatchdog(), Confirmation{}); err != nil {
		t.Fatal(err)
	}
	onePut(t, f, "Create|AMT_AgentPresenceWatchdog")
	if n := len(f.requestsFor("AddAction|AMT_AgentPresenceWatchdog")); n != 0 {
		t.Errorf("%d actions", n)
	}
}

func TestAddWatchdogActivatingDropPolicyNeedsConfirmation(t *testing.T) {
	s := goodWatchdog()
	s.OnStop = WatchdogActions{Event: true, Policy: PolicyActivate}
	s.OnStart = WatchdogActions{Policy: PolicyDeactivate}
	s.Policy = handleIsolate

	d, f := cfgDevice(watchdogSeq())
	var risky *RiskyChangeError
	if err := d.AddWatchdog(s, Confirmation{}); !errors.As(err, &risky) || risky.Op != OpDefenseDrop {
		t.Fatalf("err %v", err)
	}
	assertNoWrites(t, f)

	if err := d.AddWatchdog(s, mustConfirm(t, OpDefenseDrop)); err != nil {
		t.Fatal(err)
	}
	acts := f.requestsFor("AddAction|AMT_AgentPresenceWatchdog")
	if len(acts) != 2 {
		t.Fatalf("%d actions", len(acts))
	}
	assertContains(t, acts[0], "<h:NewState>8</h:NewState><h:EventOnTransition>true</h:EventOnTransition><h:ActionSd>0</h:ActionSd>")
	assertContains(t, acts[1], "<h:NewState>4</h:NewState><h:EventOnTransition>false</h:EventOnTransition><h:ActionSd>1</h:ActionSd>")
	// Isolate is the agent policy already: no link writes
	if n := len(f.requestsFor("Create|AMT_AgentPresenceInterfacePolicy")); n != 0 {
		t.Errorf("%d agent policy links created", n)
	}
}

func TestAddWatchdogAllowPolicyNeedsNoConfirmationAndLinksIt(t *testing.T) {
	seq := watchdogSeq()
	seq["Pull|AMT_AgentPresenceWatchdog"] = []cfgResp{{file: "wd_pull_empty.xml"}}
	d, f := cfgDevice(seq)
	s := goodWatchdog()
	s.OnStop.Policy, s.Policy = PolicyActivate, handleAllow
	if err := d.AddWatchdog(s, Confirmation{}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Delete|AMT_AgentPresenceInterfacePolicy"), "Intel(r) AMT:Handle:2")
	assertContains(t, onePut(t, f, "Create|AMT_AgentPresenceInterfacePolicy"), "Intel(r) AMT:Handle:1", "Intel(r) AMT Ethernet Port 0")
	if routeIndex(f, "Create|AMT_AgentPresenceInterfacePolicy") > routeIndex(f, "Create|AMT_AgentPresenceWatchdog") {
		t.Error("the watchdog is created before its policy is linked")
	}
}

func TestAddWatchdogRefusals(t *testing.T) {
	t.Run("agent ID in use", func(t *testing.T) {
		d, f := cfgDevice(watchdogSeq())
		s := goodWatchdog()
		s.ID = strings.ToUpper(agentID)
		if err := d.AddWatchdog(s, Confirmation{}); !errors.Is(err, ErrWatchdogExists) {
			t.Fatalf("err %v", err)
		}
		assertNoWrites(t, f)
	})
	t.Run("another policy than the shared one", func(t *testing.T) {
		d, f := cfgDevice(watchdogSeq())
		s := goodWatchdog()
		s.OnStop.Policy, s.Policy = PolicyActivate, handleAllow
		var shared *WatchdogPolicySharedError
		if err := d.AddWatchdog(s, Confirmation{}); !errors.As(err, &shared) || shared.Policy != "Isolate" {
			t.Fatalf("err %v", err)
		}
		assertNoWrites(t, f)
	})
	t.Run("unknown policy", func(t *testing.T) {
		d, _ := cfgDevice(watchdogSeq())
		s := goodWatchdog()
		s.OnStop.Policy, s.Policy = PolicyActivate, "nope"
		if err := d.AddWatchdog(s, Confirmation{}); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("action limit deletes the new watchdog", func(t *testing.T) {
		seq := watchdogSeq()
		seq["AddAction|AMT_AgentPresenceWatchdog"] = []cfgResp{{file: "wd_addaction_full.xml"}}
		d, f := cfgDevice(seq)
		s := goodWatchdog()
		s.OnStop.Event = true
		var me *MethodError
		if err := d.AddWatchdog(s, Confirmation{}); !errors.As(err, &me) || me.ReturnValue != 23 {
			t.Fatalf("err %v", err)
		}
		assertContains(t, onePut(t, f, "Delete|AMT_AgentPresenceWatchdog"), `<w:Selector Name="DeviceID">ERERESIiM0OERFVVVVVVVQ==</w:Selector>`)
	})
	t.Run("the device refuses the create", func(t *testing.T) {
		seq := watchdogSeq()
		seq["Create|AMT_AgentPresenceWatchdog"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
		d, f := cfgDevice(seq)
		s := goodWatchdog()
		s.OnStop.Event = true
		if err := d.AddWatchdog(s, Confirmation{}); !errors.Is(err, ErrAccessDenied) {
			t.Fatalf("err %v", err)
		}
		if n := len(f.requestsFor("AddAction|AMT_AgentPresenceWatchdog")); n != 0 {
			t.Errorf("%d actions after a refused create", n)
		}
	})
}

func TestDeleteWatchdog(t *testing.T) {
	d, f := cfgDevice(watchdogSeq())
	if err := d.DeleteWatchdog(strings.ToUpper(agentID)); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Delete|AMT_AgentPresenceWatchdog"), `<w:Selector Name="DeviceID">niocXzsNV06aYSyLfk0fAw==</w:Selector>`)
	if err := d.DeleteWatchdog("11111111-2222-4333-8444-555555555555"); !errors.Is(err, ErrWatchdogNotFound) {
		t.Fatalf("err %v", err)
	}
}

// routeIndex is the position of the first request with route, -1 for none.
func routeIndex(f *cfgWSMan, route string) int {
	for i, r := range f.requests {
		if routeKey(r) == route {
			return i
		}
	}
	return -1
}
