package session

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
)

func defenseSession(t *testing.T) (*Session, *Fake) {
	t.Helper()
	f := &Fake{}
	st := f.DefensePage()
	st.Defense = amt.SystemDefense{Offered: true, Active: "allow",
		Policies: []amt.DefensePolicy{{ID: "allow", Name: "Allow all", Precedence: 2}, {ID: "iso", Name: "Isolate", Precedence: 1, DropIn: true, DropOut: true, Filters: []string{"dns"}}},
		Filters:  []amt.DefenseFilter{{ID: "dns", Name: "allow-dns", Kind: amt.FilterIPv4, Direction: amt.Outbound, Protocol: amt.ProtocolUDP, PortStart: 53, PortEnd: 53}}}
	st.Watchdogs = amt.Watchdogs{Offered: true}
	s, _, err := Open(context.Background(), amt.Config{Host: "device.example.com"}, nil, ConnectFake(f))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestSessionSystemDefense(t *testing.T) {
	s, f := defenseSession(t)
	sd, err := s.SystemDefense()
	if err != nil || !sd.Offered || len(sd.Policies) != 2 || !sd.Policies[0].Active {
		t.Fatalf("%+v, %v", sd, err)
	}
	var risky *amt.RiskyChangeError
	if err := s.ActivateDefensePolicy("iso", amt.Confirmation{}); !errors.As(err, &risky) || risky.Op != amt.OpDefenseDrop {
		t.Fatalf("drop policy without confirmation: %v", err)
	}
	c, _ := amt.ConfirmChecked(amt.OpDefenseDrop, "device.example.com", true)
	if err := s.ActivateDefensePolicy("iso", c); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDefensePolicy("iso"); !errors.Is(err, amt.ErrDefensePolicyActive) {
		t.Fatalf("active policy deleted: %v", err)
	}
	var inUse *amt.DefenseFilterInUseError
	if err := s.DeleteDefenseFilter("dns"); !errors.As(err, &inUse) {
		t.Fatalf("filter in use: %v", err)
	}
	if err := s.DeactivateDefensePolicy(); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDefenseFilter(amt.FilterSpec{Name: "ssh", Kind: amt.FilterIPv4, Direction: amt.Inbound, Protocol: amt.ProtocolTCP,
		PortStart: 22, PortEnd: 22, Remote: netip.MustParsePrefix("192.0.2.0/24")}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDefensePolicy(amt.PolicySpec{Name: "SSH only", Precedence: 3, Filters: []string{"dns"}, Drop: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDefensePolicy("iso"); err != nil {
		t.Fatal(err)
	}
	want := []string{"ActivateDefensePolicy", "DeactivateDefensePolicy", "AddDefenseFilter", "AddDefensePolicy", "DeleteDefensePolicy"}
	if got := f.DefensePage().Written(); !slices.Equal(got, want) {
		t.Errorf("writes %v, want %v", got, want)
	}
}

func TestSessionWatchdogs(t *testing.T) {
	s, f := defenseSession(t)
	spec := amt.WatchdogSpec{Name: "backup-agent", ID: amt.NewAgentID(), Startup: 120, Heartbeat: 30,
		OnStop: amt.WatchdogActions{Event: true, Policy: amt.PolicyActivate}, OnStart: amt.WatchdogActions{Policy: amt.PolicyDeactivate}, Policy: "iso"}
	var risky *amt.RiskyChangeError
	if err := s.AddWatchdog(spec, amt.Confirmation{}); !errors.As(err, &risky) {
		t.Fatalf("watchdog with a drop policy without confirmation: %v", err)
	}
	c, _ := amt.ConfirmChecked(amt.OpDefenseDrop, "device.example.com", true)
	if err := s.AddWatchdog(spec, c); err != nil {
		t.Fatal(err)
	}
	if err := s.AddWatchdog(spec, c); !errors.Is(err, amt.ErrWatchdogExists) {
		t.Fatalf("same agent ID: %v", err)
	}
	other := spec
	other.ID, other.Policy = amt.NewAgentID(), "allow"
	var shared *amt.WatchdogPolicySharedError
	if err := s.AddWatchdog(other, c); !errors.As(err, &shared) || shared.Policy != "Isolate" {
		t.Fatalf("second policy: %v", err)
	}
	w, err := s.Watchdogs()
	if err != nil || len(w.List) != 1 || w.Policy != "iso" {
		t.Fatalf("%+v, %v", w, err)
	}
	if err := s.DeleteWatchdog(spec.ID); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	f.DefensePage().WatchdogErr = boom
	if _, err := s.Watchdogs(); !errors.Is(err, boom) {
		t.Fatalf("read error: %v", err)
	}
	if _, err := s.SystemDefense(); err != nil {
		t.Fatalf("WatchdogErr reached System Defense: %v", err)
	}
}
