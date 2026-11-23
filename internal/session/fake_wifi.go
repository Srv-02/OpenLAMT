package session

import (
	"fmt"
	"slices"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeWiFi is the Wi-Fi state of a Fake. Writes run the checks of *amt.Device (validation, no
// adapter, the lockout confirmation while State.OverWiFi), then change State and record the
// call; nothing is recorded without an effective change. WriteErr, if set, is returned after
// the checks without a change (a refusal of the device). Only touched under the Fake's mutex.
type FakeWiFi struct {
	State    amt.WiFiSettings
	ReadErr  error
	WriteErr error
	Calls    []FakeWiFiCall
}

// FakeWiFiCall is one effective write. Spec keeps the passphrase so tests can check what was
// sent; the real device keeps nothing.
type FakeWiFiCall struct {
	Method     string
	InstanceID string
	Spec       amt.WiFiProfileSpec
	State      amt.WiFiState
	On         bool
}

// WiFiFake returns the Wi-Fi state of f, created on first use.
func (f *Fake) WiFiFake() *FakeWiFi { return fakeState[FakeWiFi](f, "wifi") }

// WiFiCalls returns the recorded Wi-Fi writes.
func (f *Fake) WiFiCalls() []FakeWiFiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.WiFiFake().Calls)
}

var _ WiFiDevice = (*Fake)(nil)

func (f *Fake) WiFi() (amt.WiFiSettings, error) {
	var s amt.WiFiSettings
	var err error
	f.cfgRead(func() {
		w := f.WiFiFake()
		s, err = w.State, w.ReadErr
		s.Profiles = slices.Clone(s.Profiles)
		s.Securities = slices.Clone(s.Securities)
	})
	if err != nil {
		return amt.WiFiSettings{}, err
	}
	return s, nil
}

// wifiWrite runs check and apply like cfgWrite, for the Wi-Fi state.
func (f *Fake) wifiWrite(call FakeWiFiCall, check func(w *FakeWiFi) (changed bool, err error), apply func(w *FakeWiFi)) error {
	var err error
	f.cfgRead(func() {
		w := f.WiFiFake()
		var changed bool
		if changed, err = check(w); err != nil || !changed {
			return
		}
		if w.WriteErr != nil {
			err = w.WriteErr
			return
		}
		apply(w)
		w.Calls = append(w.Calls, call)
	})
	return err
}

func (f *Fake) SetWiFiState(s amt.WiFiState, confirm amt.Confirmation) error {
	return f.wifiWrite(FakeWiFiCall{Method: "SetWiFiState", State: s}, func(w *FakeWiFi) (bool, error) {
		switch {
		case s != amt.WiFiOff && s != amt.WiFiS0 && s != amt.WiFiS0Sx:
			return false, &amt.ValidationError{Field: "State", Reason: "Off, S0 or S0 and Sx"}
		case !w.State.Available:
			return false, amt.ErrNotModifiable
		case w.State.State == s:
			return false, nil
		case s == amt.WiFiOff && w.State.OverWiFi:
			return true, f.confirmed(confirm, amt.OpWiFiLockout, []string{amt.RiskWiFi})
		}
		return true, nil
	}, func(w *FakeWiFi) { w.State.State = s })
}

func (f *Fake) SetWiFiSync(on bool) error {
	return f.wifiWrite(FakeWiFiCall{Method: "SetWiFiSync", On: on}, func(w *FakeWiFi) (bool, error) {
		if !w.State.SyncKnown {
			return false, amt.ErrNotModifiable
		}
		return w.State.Sync != on, nil
	}, func(w *FakeWiFi) { w.State.Sync = on })
}

func (f *Fake) SetWiFiUEFIShare(on bool) error {
	return f.wifiWrite(FakeWiFiCall{Method: "SetWiFiUEFIShare", On: on}, func(w *FakeWiFi) (bool, error) {
		if !w.State.UEFIShareKnown {
			return false, amt.ErrNotModifiable
		}
		return w.State.UEFIShare != on, nil
	}, func(w *FakeWiFi) { w.State.UEFIShare = on })
}

func (f *Fake) AddWiFiProfile(p amt.WiFiProfileSpec) error {
	id := "Intel(r) AMT:WiFi Endpoint Settings " + p.Name
	return f.wifiWrite(FakeWiFiCall{Method: "AddWiFiProfile", InstanceID: id, Spec: p}, func(w *FakeWiFi) (bool, error) {
		if err := p.Validate(true); err != nil {
			return false, err
		}
		if slices.ContainsFunc(w.State.Profiles, func(x amt.WiFiProfile) bool { return x.InstanceID == id }) {
			return false, &amt.MethodError{Method: "AddWiFiSettings", ReturnValue: 2}
		}
		return true, nil
	}, func(w *FakeWiFi) {
		w.State.Profiles = append(w.State.Profiles, amt.WiFiProfile{InstanceID: id, Name: p.Name, SSID: p.SSID, Priority: p.Priority, Security: p.Security})
	})
}

func (f *Fake) UpdateWiFiProfile(instanceID string, p amt.WiFiProfileSpec) error {
	var i int
	return f.wifiWrite(FakeWiFiCall{Method: "UpdateWiFiProfile", InstanceID: instanceID, Spec: p}, func(w *FakeWiFi) (bool, error) {
		if err := p.Validate(false); err != nil {
			return false, err
		}
		i = slices.IndexFunc(w.State.Profiles, func(x amt.WiFiProfile) bool { return x.InstanceID == instanceID })
		if i < 0 {
			return false, &amt.MethodError{Method: "UpdateWiFiSettings", ReturnValue: 4}
		}
		return true, nil
	}, func(w *FakeWiFi) {
		pr := &w.State.Profiles[i]
		pr.SSID, pr.Priority, pr.Security = p.SSID, p.Priority, p.Security
	})
}

func (f *Fake) DeleteWiFiProfile(instanceID string, confirm amt.Confirmation) error {
	return f.wifiWrite(FakeWiFiCall{Method: "DeleteWiFiProfile", InstanceID: instanceID}, func(w *FakeWiFi) (bool, error) {
		if !slices.ContainsFunc(w.State.Profiles, func(x amt.WiFiProfile) bool { return x.InstanceID == instanceID }) {
			return false, fmt.Errorf("fake: no Wi-Fi profile %q", instanceID)
		}
		if w.State.OverWiFi {
			return true, f.confirmed(confirm, amt.OpWiFiLockout, []string{amt.RiskWiFi})
		}
		return true, nil
	}, func(w *FakeWiFi) {
		w.State.Profiles = slices.DeleteFunc(w.State.Profiles, func(x amt.WiFiProfile) bool { return x.InstanceID == instanceID })
	})
}
