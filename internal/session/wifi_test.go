package session

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
)

func wifiFake() *Fake {
	f := &Fake{}
	f.WiFiFake().State = amt.WiFiSettings{Available: true, State: amt.WiFiS0Sx, SyncKnown: true, UEFIShareKnown: true,
		LinkUp: true, IPAddress: "192.0.2.61",
		Profiles: []amt.WiFiProfile{{InstanceID: "Intel(r) AMT:WiFi Endpoint Settings Office", Name: "Office", SSID: "ACME-Lab", Priority: 1, Security: amt.WiFiWPA2Personal}},
	}
	return f
}

func TestWiFiThroughSession(t *testing.T) {
	f := wifiFake()
	s := openAs(t, f, "admin", "pw")
	w, err := s.WiFi()
	if err != nil || !w.Available || len(w.Profiles) != 1 {
		t.Fatalf("WiFi() = %+v, %v", w, err)
	}
	spec := amt.WiFiProfileSpec{Name: "Lab", SSID: "ACME-Lab2", Priority: 2, Security: amt.WiFiWPA3Personal, Passphrase: "correct-horse"}
	if err := s.AddWiFiProfile(spec); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateWiFiProfile("Intel(r) AMT:WiFi Endpoint Settings Office", amt.WiFiProfileSpec{Name: "Office", SSID: "ACME-Lab", Priority: 3, Security: amt.WiFiWPA2Personal}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWiFiSync(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWiFiUEFIShare(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWiFiState(amt.WiFiS0, amt.Confirmation{}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWiFiProfile("Intel(r) AMT:WiFi Endpoint Settings Lab", amt.Confirmation{}); err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, c := range f.WiFiCalls() {
		methods = append(methods, c.Method)
	}
	want := []string{"AddWiFiProfile", "UpdateWiFiProfile", "SetWiFiSync", "SetWiFiUEFIShare", "SetWiFiState", "DeleteWiFiProfile"}
	if !reflect.DeepEqual(methods, want) {
		t.Errorf("calls %v, want %v", methods, want)
	}
	w, _ = s.WiFi()
	if len(w.Profiles) != 1 || w.Profiles[0].Priority != 3 || w.State != amt.WiFiS0 || !w.Sync || !w.UEFIShare {
		t.Errorf("state after the writes: %+v", w)
	}
}

func TestWiFiLockoutNeedsConfirmation(t *testing.T) {
	f := wifiFake()
	f.WiFiFake().State.OverWiFi = true
	s := openAs(t, f, "admin", "pw")
	const id = "Intel(r) AMT:WiFi Endpoint Settings Office"
	if err := s.DeleteWiFiProfile(id, amt.Confirmation{}); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("delete without confirmation: %v", err)
	}
	if err := s.SetWiFiState(amt.WiFiOff, amt.Confirmation{}); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("off without confirmation: %v", err)
	}
	if len(f.WiFiCalls()) != 0 {
		t.Fatalf("written: %v", f.WiFiCalls())
	}
	c, err := amt.ConfirmChecked(amt.OpWiFiLockout, s.Config().Host, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetWiFiState(amt.WiFiOff, c); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWiFiProfile(id, c); err != nil {
		t.Fatal(err)
	}
}

func TestWiFiUnsupportedDevice(t *testing.T) {
	s := &Session{dev: bare{}}
	if _, err := s.WiFi(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("WiFi: %v", err)
	}
	if err := s.DeleteWiFiProfile("x", amt.Confirmation{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Delete: %v", err)
	}
}
