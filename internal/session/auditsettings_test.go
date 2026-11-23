package session

import (
	"context"
	"errors"
	"testing"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"

	"github.com/Srv-02/openlamt/internal/amt"
)

func openAuditFake(t *testing.T) (*Session, *Fake) {
	t.Helper()
	f := &Fake{}
	s, _, err := Open(context.Background(), amt.Config{Host: "device.example.com"}, nil, ConnectFake(f))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestAuditSettingsThroughSession(t *testing.T) {
	s, f := openAuditFake(t)
	f.AuditState().Settings = amt.AuditSettings{Recording: true, Records: 3, Policy: []amt.AuditEvent{{App: 17, Event: 0}}}
	got, err := s.AuditSettings()
	if err != nil || !got.Recording || got.Records != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	off := false
	change := amt.AuditChange{Recording: &off}
	if err := s.SetAuditSettings(change, amt.Confirmation{}); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("recording off without confirmation: %v", err)
	}
	c, _ := amt.ConfirmChecked(amt.OpAuditSettings, "device.example.com", true)
	if err := s.SetAuditSettings(change, c); err != nil || f.AuditState().Settings.Recording {
		t.Fatalf("%v, recording %v", err, f.AuditState().Settings.Recording)
	}
	f.Audit = []amt.AuditLogEntry{{Event: "x"}}
	if err := s.ClearAuditLog(c); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("clear with the settings confirmation: %v", err)
	}
	cc, _ := amt.ConfirmChecked(amt.OpAuditClear, "device.example.com", true)
	if err := s.ClearAuditLog(cc); err != nil || f.Audit != nil || f.AuditState().Clears != 1 {
		t.Fatalf("clear: %v", err)
	}
	f.AuditState().ReadErr = amterror.NewAMTError("b:AccessDenied", "x", "")
	if _, err := s.AuditSettings(); !errors.Is(err, amt.ErrAccessDenied) {
		t.Fatalf("classified: %v", err)
	}
}

func TestForwardingThroughSession(t *testing.T) {
	s, f := openAuditFake(t)
	f.Forwarding().Data.Filters = []amt.EventFilter{{ID: "Intel(r) AMT:All", Name: "All"}}
	spec := amt.TargetSpec{Address: "http://192.0.2.5/events", Delivery: amt.DeliveryPush, FilterID: "Intel(r) AMT:All"}
	if err := s.AddForwardingTarget(spec); err != nil {
		t.Fatal(err)
	}
	got, err := s.EventForwarding()
	if err != nil || len(got.Targets) != 1 || got.Targets[0].FilterName != "All" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.DeleteForwardingTarget(got.Targets[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.EventForwarding(); len(got.Targets) != 0 {
		t.Fatalf("not deleted: %+v", got)
	}
	if err := s.AddForwardingTarget(amt.TargetSpec{Address: "x"}); !errors.Is(err, amt.ErrTargetAddress) {
		t.Fatalf("validated: %v", err)
	}
}

func TestAuditAndForwardingUnsupported(t *testing.T) {
	s := &Session{dev: bare{}}
	if _, err := s.AuditSettings(); !errors.Is(err, ErrUnsupported) {
		t.Error(err)
	}
	if _, err := s.EventForwarding(); !errors.Is(err, ErrUnsupported) {
		t.Error(err)
	}
}
