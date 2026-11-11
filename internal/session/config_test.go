package session

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
)

func ptr[T any](v T) *T { return &v }

// conf returns the confirmation of op for the host of openInfoSession.
func conf(op amt.Operation) amt.Confirmation {
	const host = "device.example.com"
	c, err := amt.Confirm(op, host, amt.ConfirmPhrase(op, host))
	if err != nil {
		panic(err)
	}
	return c
}

func staticChange() amt.NetworkChange {
	return amt.NetworkChange{DHCP: ptr(false), IPAddress: ptr("192.0.2.10"), SubnetMask: ptr("255.255.255.0"), Gateway: ptr("192.0.2.1")}
}

func configFake() *Fake {
	return &Fake{Cfg: FakeConfig{
		Settings: amt.Settings{
			General:     amt.GeneralSettings{HostName: "amt-test", DomainName: "example.com", PingResponse: true},
			Network:     amt.NetworkSettings{Available: true, DHCP: true, IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0"},
			Features:    amt.Features{Redirection: true, SOL: true, IDER: true, KVM: true, KVMAvailable: true},
			Consent:     amt.ConsentSettings{Available: true, Policy: amt.ConsentKVM, CanModify: true},
			Environment: amt.EnvironmentDetection{Available: true},
		},
		Users: amt.UserList{Admin: "admin", Users: []amt.User{
			{Handle: 1, Name: "$$OsAdmin", Realms: []amt.Realm{4}, Enabled: true, EnabledKnown: true, System: true},
			{Handle: 2, Name: "user1", Access: amt.AccessNetwork, Realms: []amt.Realm{2}, Enabled: true, EnabledKnown: true},
			{Handle: 3, Name: "operator", Access: amt.AccessNetwork, Realms: []amt.Realm{5}, Enabled: true, EnabledKnown: true, Self: true},
		}},
	}}
}

func TestSessionSettingsPassesValuesAndCopies(t *testing.T) {
	dev := configFake()
	dev.Cfg.Settings.Environment.Domains = []string{"intranet.example.com"}
	s := openInfoSession(t, dev)
	got, err := s.Settings()
	if err != nil || !reflect.DeepEqual(got, dev.Cfg.Settings) {
		t.Fatalf("settings %+v %v", got, err)
	}
	got.Environment.Domains[0] = "changed"
	if dev.Cfg.Settings.Environment.Domains[0] != "intranet.example.com" {
		t.Fatal("fake leaked its slice")
	}
	users, err := s.Users()
	if err != nil || !reflect.DeepEqual(users, dev.Cfg.Users) {
		t.Fatalf("users %+v %v", users, err)
	}
}

func TestSessionConfigClassifiesErrors(t *testing.T) {
	libErr := fmt.Errorf("%w: %v\n%v", errors.New("wsman.Client post received"), "401 Unauthorized", "<html>secret body</html>")
	dev := configFake()
	dev.Cfg.SettingsErr, dev.Cfg.UsersErr, dev.Cfg.WriteErr = libErr, libErr, libErr
	s := openInfoSession(t, dev)

	if st, err := s.Settings(); !errors.Is(err, ErrAuth) || !reflect.DeepEqual(st, amt.Settings{}) {
		t.Errorf("Settings %+v %v", st, err)
	}
	if u, err := s.Users(); !errors.Is(err, ErrAuth) || u.Users != nil {
		t.Errorf("Users %+v %v", u, err)
	}
	writes := map[string]func() error{
		"SetGeneral":              func() error { return s.SetGeneral(amt.GeneralChange{HostName: ptr("x")}, conf(amt.OpHostName)) },
		"SetNetwork":              func() error { return s.SetNetwork(staticChange(), conf(amt.OpNetwork)) },
		"SetFeatures":             func() error { return s.SetFeatures(amt.FeatureChange{SOL: ptr(false)}, conf(amt.OpFeaturesOff)) },
		"SetConsent":              func() error { return s.SetConsent(amt.ConsentAll) },
		"SetEnvironmentDetection": func() error { return s.SetEnvironmentDetection([]string{"a.example.com"}, conf(amt.OpEnvironment)) },
		"SyncClock":               s.SyncClock,
		"AddUser": func() error {
			_, err := s.AddUser(amt.UserSpec{Name: "new", Password: "Secret-Pass1", Realms: []amt.Realm{2}})
			return err
		},
		"UpdateUser": func() error {
			return s.UpdateUser(2, amt.UserSpec{Name: "user1", Password: "Secret-Pass1", Realms: []amt.Realm{2}})
		},
		"RemoveUser":     func() error { return s.RemoveUser(2) },
		"SetUserEnabled": func() error { return s.SetUserEnabled(2, false) },
	}
	for name, w := range writes {
		if err := w(); !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret body") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := len(dev.ConfigWrites()); n != 0 {
		t.Errorf("%d writes recorded despite WriteErr", n)
	}
}

func TestSessionRiskyChangeNeedsConfirmation(t *testing.T) {
	dev := configFake()
	s := openInfoSession(t, dev)
	static := staticChange()

	err := s.SetNetwork(static, amt.Confirmation{})
	var re *amt.RiskyChangeError
	if !errors.As(err, &re) || !errors.Is(err, amt.ErrRiskyChange) || !reflect.DeepEqual(re.Fields, []string{amt.RiskNetwork}) {
		t.Fatalf("unconfirmed: %v", err)
	}
	if err := s.SetFeatures(amt.FeatureChange{KVM: ptr(false), Redirection: ptr(true)}, amt.Confirmation{}); !errors.As(err, &re) || !reflect.DeepEqual(re.Fields, []string{amt.RiskKVM}) {
		t.Fatalf("features: %v", err)
	}
	if err := s.SetEnvironmentDetection([]string{"a.example.com"}, amt.Confirmation{}); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("environment: %v", err)
	}
	if n := len(dev.ConfigWrites()); n != 0 {
		t.Fatalf("unconfirmed changes were written: %v", dev.ConfigWrites())
	}

	if err := s.SetNetwork(static, conf(amt.OpNetwork)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFeatures(amt.FeatureChange{SOL: ptr(true), IDER: ptr(false)}, conf(amt.OpFeaturesOff)); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Settings()
	if st.Network.DHCP || st.Network.Gateway != "192.0.2.1" || st.Features.IDER || !st.Features.SOL {
		t.Errorf("state after writes: %+v %+v", st.Network, st.Features)
	}
	if want := []string{"SetNetwork", "SetFeatures"}; !slices.Equal(dev.ConfigWrites(), want) {
		t.Errorf("writes %v", dev.ConfigWrites())
	}
}

func TestSessionHostNameAndConfirmationBinding(t *testing.T) {
	dev := configFake()
	s := openInfoSession(t, dev)
	change := amt.GeneralChange{HostName: ptr("renamed")}
	var re *amt.RiskyChangeError
	if err := s.SetGeneral(change, amt.Confirmation{}); !errors.As(err, &re) || re.Op != amt.OpHostName || !slices.Equal(re.Fields, []string{amt.RiskHostName}) {
		t.Fatalf("unconfirmed: %v", err)
	}
	other, _ := amt.Confirm(amt.OpHostName, "other.example.com", amt.ConfirmWord)
	if err := s.SetGeneral(change, other); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("confirmation of another device: %v", err)
	}
	if err := s.SetGeneral(change, conf(amt.OpNetwork)); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("confirmation of another operation: %v", err)
	}
	if err := s.SetGeneral(amt.GeneralChange{PingResponse: ptr(false)}, amt.Confirmation{}); err != nil {
		t.Fatalf("ping is not risky: %v", err)
	}
	if err := s.SetGeneral(change, conf(amt.OpHostName)); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Settings(); st.General.HostName != "renamed" || st.General.PingResponse {
		t.Errorf("general after writes: %+v", st.General)
	}
}

func TestSessionConfigNoChangeWritesNothing(t *testing.T) {
	dev := configFake()
	s := openInfoSession(t, dev)
	for name, err := range map[string]error{
		"general":     s.SetGeneral(amt.GeneralChange{HostName: ptr("amt-test")}, amt.Confirmation{}),
		"network":     s.SetNetwork(amt.NetworkChange{DHCP: ptr(true)}, amt.Confirmation{}),
		"features":    s.SetFeatures(amt.FeatureChange{KVM: ptr(true)}, amt.Confirmation{}),
		"consent":     s.SetConsent(amt.ConsentKVM),
		"environment": s.SetEnvironmentDetection(nil, amt.Confirmation{}),
		"enabled":     s.SetUserEnabled(2, true),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := len(dev.ConfigWrites()); n != 0 {
		t.Errorf("writes without change: %v", dev.ConfigWrites())
	}
}

func TestSessionUserGuards(t *testing.T) {
	dev := configFake()
	s := openInfoSession(t, dev)
	spec := amt.UserSpec{Name: "user9", Password: "Secret-Pass1", Realms: []amt.Realm{2}}
	for _, h := range []int{1, 3} { // system account, own account
		if err := s.RemoveUser(h); !errors.Is(err, amt.ErrProtectedAccount) {
			t.Errorf("remove %d: %v", h, err)
		}
		if err := s.UpdateUser(h, spec); !errors.Is(err, amt.ErrProtectedAccount) {
			t.Errorf("update %d: %v", h, err)
		}
		if err := s.SetUserEnabled(h, false); !errors.Is(err, amt.ErrProtectedAccount) {
			t.Errorf("disable %d: %v", h, err)
		}
	}
	var me *amt.MethodError
	if err := s.RemoveUser(42); !errors.As(err, &me) || me.ReturnValue != 2053 {
		t.Errorf("unknown handle: %v", err)
	}
	if _, err := s.AddUser(amt.UserSpec{Name: "user1", Password: "Secret-Pass1", Realms: []amt.Realm{2}}); !errors.As(err, &me) || me.ReturnValue != 2058 {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := s.AddUser(amt.UserSpec{Name: "weak", Password: "weak", Realms: []amt.Realm{2}}); !errors.Is(err, amt.ErrInvalidValue) {
		t.Errorf("weak password: %v", err)
	}

	h, err := s.AddUser(spec)
	if err != nil || h != 4 {
		t.Fatalf("add: %d %v", h, err)
	}
	if err := s.UpdateUser(2, amt.UserSpec{Name: "user2", Password: "Secret-Pass1", Access: amt.AccessLocal, Realms: []amt.Realm{5, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveUser(4); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Users()
	if len(l.Users) != 3 || l.Users[1].Name != "user2" || !slices.Equal(l.Users[1].Realms, []amt.Realm{2, 5}) {
		t.Errorf("users after writes: %+v", l.Users)
	}
	if want := []string{"AddUser", "UpdateUser", "RemoveUser"}; !slices.Equal(dev.ConfigWrites(), want) {
		t.Errorf("writes %v", dev.ConfigWrites())
	}
}

func TestSessionConfigCallsAreSerialized(t *testing.T) {
	dev := configFake()
	s := openInfoSession(t, dev)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); s.Status() }()
		go func() { defer wg.Done(); s.Settings() }()
		go func() { defer wg.Done(); s.Users() }()
	}
	wg.Wait()
	if dev.MaxBusy() != 1 {
		t.Fatalf("max concurrent device calls = %d", dev.MaxBusy())
	}
}

func TestFakeNetworkDHCPKeepsLeaseAddresses(t *testing.T) {
	dev := configFake()
	dev.Cfg.Settings.Network = amt.NetworkSettings{Available: true, IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0", Gateway: "192.0.2.1"}
	s := openInfoSession(t, dev)
	if err := s.SetNetwork(amt.NetworkChange{DHCP: ptr(true), IPAddress: ptr("192.0.2.99")}, conf(amt.OpNetwork)); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Settings()
	if !st.Network.DHCP || st.Network.IPAddress != "192.0.2.10" {
		t.Errorf("network %+v", st.Network)
	}
}
