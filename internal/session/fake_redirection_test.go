package session

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
)

func TestFakeRedirectionHonoursNeverEnable(t *testing.T) {
	open := func(dev *Fake, never bool) *Session {
		t.Helper()
		s, _, err := Open(ctx, amt.Config{Host: "device.example.com", Username: "admin", NeverEnable: never}, nil, ConnectFake(dev))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	calls := map[string]func(s *Session) error{
		"KVM":   func(s *Session) error { _, err := s.OpenKVM(ctx, 0, nil); return err },
		"SOL":   func(s *Session) error { _, err := s.OpenSOL(ctx, nil); return err },
		"IDE-R": func(s *Session) error { _, err := s.OpenIDER(ctx, nil, ider.StartMode(0), nil); return err },
	}
	for feature, call := range calls {
		// NeverEnable: the listener is refused first, then the feature itself; nothing changes.
		dev := &Fake{}
		dev.Redirection().ListenerOff = true
		var re *amt.RedirectionOffError
		if err := call(open(dev, true)); !errors.As(err, &re) || re.Feature != "redirection" {
			t.Fatalf("%s listener: err = %v", feature, err)
		}
		dev = &Fake{}
		switch feature {
		case "KVM":
			dev.Redirection().KVMOff = true
		case "SOL":
			dev.Redirection().SOLOff = true
		default:
			dev.Redirection().IDEROff = true
		}
		if err := call(open(dev, true)); !errors.As(err, &re) || re.Feature != feature {
			t.Fatalf("%s: err = %v", feature, err)
		}
		if got := dev.RedirectionEnabled(); len(got) != 0 {
			t.Fatalf("%s: enabled %v despite NeverEnable", feature, got)
		}
		// Without NeverEnable the Fake switches the listener, then the feature on, once.
		dev = &Fake{}
		dev.Redirection().ListenerOff = true
		switch feature {
		case "KVM":
			dev.Redirection().KVMOff = true
		case "SOL":
			dev.Redirection().SOLOff = true
		default:
			dev.Redirection().IDEROff = true
		}
		_ = call(open(dev, false)) // the dial fails (no server); the enabling came first
		if got, want := dev.RedirectionEnabled(), []string{"redirection", feature}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: enabled %v, want %v", feature, got, want)
		}
	}
	// The zero value has everything on: NeverEnable changes nothing.
	dev := &Fake{}
	_ = calls["KVM"](open(dev, true))
	if got := dev.RedirectionEnabled(); len(got) != 0 {
		t.Fatalf("enabled %v", got)
	}
}
