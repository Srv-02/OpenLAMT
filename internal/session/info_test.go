package session

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

func openInfoSession(t *testing.T, dev *Fake) *Session {
	t.Helper()
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSessionInfoPassesValuesThrough(t *testing.T) {
	dev := &Fake{
		HW:     amt.HardwareInfo{System: amt.SystemInfo{Manufacturer: "Example Corp", Model: "EX-1000"}},
		Events: []amt.EventLogEntry{{Time: time.Unix(100, 0).UTC(), Description: "one"}, {Description: "two"}},
		Audit:  []amt.AuditLogEntry{{AppID: 16, EventID: 2, App: "App"}},
	}
	s := openInfoSession(t, dev)

	hw, err := s.HardwareInfo()
	if err != nil || !reflect.DeepEqual(hw, dev.HW) {
		t.Fatalf("hw %+v %v", hw, err)
	}
	ev, err := s.EventLog()
	if err != nil || !reflect.DeepEqual(ev, dev.Events) {
		t.Fatalf("events %+v %v", ev, err)
	}
	au, err := s.AuditLog()
	if err != nil || !reflect.DeepEqual(au, dev.Audit) {
		t.Fatalf("audit %+v %v", au, err)
	}
	// The fake returns copies: changing the result must not touch the fake.
	ev[0].Description = "changed"
	if dev.Events[0].Description != "one" {
		t.Fatal("fake leaked its slice")
	}
}

func TestSessionInfoClassify401(t *testing.T) {
	libErr := fmt.Errorf("%w: %v\n%v", errors.New("wsman.Client post received"), "401 Unauthorized", "<html>secret body</html>")
	dev := &Fake{HWErr: libErr, EventsErr: libErr, AuditErr: libErr}
	s := openInfoSession(t, dev)

	hw, err := s.HardwareInfo()
	if !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret body") || !reflect.DeepEqual(hw, amt.HardwareInfo{}) {
		t.Fatalf("hw %+v %v", hw, err)
	}
	ev, err := s.EventLog()
	if !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret body") || ev != nil {
		t.Fatalf("events %+v %v", ev, err)
	}
	au, err := s.AuditLog()
	if !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret body") || au != nil {
		t.Fatalf("audit %+v %v", au, err)
	}
}

func TestSessionAuditLogKeepsAccessDenied(t *testing.T) {
	dev := &Fake{AuditErr: fmt.Errorf("read audit log: %w", amt.ErrAccessDenied)}
	s := openInfoSession(t, dev)
	if _, err := s.AuditLog(); !errors.Is(err, amt.ErrAccessDenied) || errors.Is(err, ErrAuth) {
		t.Fatalf("%v", err)
	}
}

func TestSessionStatusAndAuditLogAreSerialized(t *testing.T) {
	dev := &Fake{Audit: []amt.AuditLogEntry{{AppID: 1}}}
	s := openInfoSession(t, dev)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); s.Status() }()
		go func() { defer wg.Done(); s.AuditLog() }()
		go func() { defer wg.Done(); s.HardwareInfo() }()
	}
	wg.Wait()
	if dev.MaxBusy() != 1 {
		t.Fatalf("max concurrent device calls = %d", dev.MaxBusy())
	}
}
