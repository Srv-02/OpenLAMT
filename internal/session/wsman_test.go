package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

func TestSessionBrowseWSMan(t *testing.T) {
	f := &Fake{}
	w := f.WSMan()
	w.Results = map[string]amt.WSManResult{"AMT_GeneralSettings": {Class: "AMT_GeneralSettings",
		Instances: []amt.WSManInstance{{Class: "AMT_GeneralSettings", Props: []amt.WSManProp{{Name: "HostName", Value: "lab"}}}}}}
	w.Errs = map[string]error{
		"AMT_AuditLog":  fmt.Errorf("read AMT_AuditLog: %w", amterror.NewAMTError("e:AccessDenied", "x", "")),
		"AMT_TimeLimit": errors.New("timeout"),
	}
	s, _, err := Open(context.Background(), amt.Config{Host: "device.example.com"}, nil, ConnectFake(f))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.BrowseWSMan("AMT_GeneralSettings")
	if err != nil || len(res.Instances) != 1 || res.Instances[0].Props[0].Value != "lab" {
		t.Fatalf("%+v, %v", res, err)
	}
	if _, err := s.BrowseWSMan("AMT_AuditLog"); !errors.Is(err, amt.ErrAccessDenied) {
		t.Errorf("denied: %v", err)
	}
	var cu *amt.ClassUnsupportedError
	if _, err := s.BrowseWSMan("AMT_RedirectionService"); !errors.As(err, &cu) || cu.Class != "AMT_RedirectionService" {
		t.Errorf("missing class: %v", err)
	}
	if _, err := s.BrowseWSMan("AMT_TimeLimit"); err == nil || err.Error() != "timeout" {
		t.Errorf("other: %v", err)
	}
	if got := w.Read(); len(got) != 4 || got[0] != "AMT_GeneralSettings" {
		t.Errorf("reads %v", got)
	}
}

// A device without the capability answers ErrUnsupported.
func TestSessionBrowseWSManUnsupported(t *testing.T) {
	s := &Session{dev: bare{}}
	if _, err := s.BrowseWSMan("AMT_GeneralSettings"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v", err)
	}
}
