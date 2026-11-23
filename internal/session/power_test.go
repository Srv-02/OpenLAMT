package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

func powerSession(t *testing.T) (*Session, *Fake) {
	t.Helper()
	f := &Fake{}
	f.PowerPage().Policy = amt.PowerPolicy{Current: "SCHEME 1", IdleTimeoutMinutes: 1, Schemes: []amt.PowerScheme{
		{ID: "SCHEME 0"}, {ID: "SCHEME 1", KeepsRemoteStart: true}}}
	s, _, err := Open(context.Background(), amt.Config{Host: "device.example.com"}, nil, ConnectFake(f))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestSessionAlarms(t *testing.T) {
	s, f := powerSession(t)
	spec := amt.AlarmSpec{Name: "Backup", Start: time.Now().Add(time.Hour)}
	if err := s.AddAlarm(spec); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAlarm(spec); !errors.Is(err, amt.ErrAlarmExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := s.AddAlarm(amt.AlarmSpec{Name: "Past", Start: time.Now().Add(-time.Hour)}); !errors.Is(err, amt.ErrAlarmPast) {
		t.Fatalf("past: %v", err)
	}
	for i := 1; i < amt.MaxAlarms; i++ {
		spec.Name = string(rune('A' + i))
		if err := s.AddAlarm(spec); err != nil {
			t.Fatal(err)
		}
	}
	spec.Name = "Sixth"
	if err := s.AddAlarm(spec); !errors.Is(err, amt.ErrAlarmsFull) {
		t.Fatalf("sixth: %v", err)
	}
	list, err := s.Alarms()
	if err != nil || len(list) != amt.MaxAlarms {
		t.Fatalf("list %v, %v", list, err)
	}
	if err := s.DeleteAlarm("Backup"); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.Alarms(); len(list) != amt.MaxAlarms-1 {
		t.Fatalf("after delete: %d", len(list))
	}
	boom := errors.New("boom")
	f.PowerPage().Err = boom
	if _, err := s.Alarms(); !errors.Is(err, boom) {
		t.Fatalf("read error: %v", err)
	}
}

func TestSessionPowerPolicy(t *testing.T) {
	s, f := powerSession(t)
	if err := s.SetPowerScheme("SCHEME 0", amt.Confirmation{}); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("unconfirmed risky change: %v", err)
	}
	c, err := amt.ConfirmChecked(amt.OpPowerPolicy, "device.example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPowerScheme("SCHEME 0", c); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.PowerPolicy(); p.Current != "SCHEME 0" {
		t.Fatalf("current %q", p.Current)
	}
	if err := s.SetPowerScheme("SCHEME 1", amt.Confirmation{}); err != nil { // gains the remote start: no confirmation
		t.Fatal(err)
	}
	if err := s.SetIdleTimeout(30); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIdleTimeout(0); !errors.Is(err, amt.ErrInvalidValue) {
		t.Fatalf("zero: %v", err)
	}
	if p, _ := s.PowerPolicy(); p.IdleTimeoutMinutes != 30 {
		t.Fatalf("idle %d", p.IdleTimeoutMinutes)
	}
	if got := f.PowerPage().Written(); len(got) != 3 {
		t.Fatalf("writes %v", got)
	}
}

func TestSessionPowerUnsupported(t *testing.T) {
	s := &Session{dev: bare{}}
	if _, err := s.Alarms(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Alarms: %v", err)
	}
	if _, err := s.PowerPolicy(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("PowerPolicy: %v", err)
	}
	if err := s.AddAlarm(amt.AlarmSpec{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("AddAlarm: %v", err)
	}
	if err := s.DeleteAlarm("x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("DeleteAlarm: %v", err)
	}
	if err := s.SetPowerScheme("x", amt.Confirmation{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetPowerScheme: %v", err)
	}
	if err := s.SetIdleTimeout(5); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetIdleTimeout: %v", err)
	}
}
