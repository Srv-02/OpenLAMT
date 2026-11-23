package session

import "github.com/Srv-02/openlamt/internal/amt"

// WiFiDevice is the Wi-Fi capability (issue #22, see capabilities.go).
type WiFiDevice interface {
	WiFi() (amt.WiFiSettings, error)
	SetWiFiState(s amt.WiFiState, confirm amt.Confirmation) error
	SetWiFiSync(on bool) error
	SetWiFiUEFIShare(on bool) error
	AddWiFiProfile(p amt.WiFiProfileSpec) error
	UpdateWiFiProfile(instanceID string, p amt.WiFiProfileSpec) error
	DeleteWiFiProfile(instanceID string, confirm amt.Confirmation) error
}

var _ WiFiDevice = (*amt.Device)(nil)

// wifiCall runs fn with the Wi-Fi capability under s.mu and classifies its error.
func wifiCall(s *Session, fn func(WiFiDevice) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[WiFiDevice](s)
	if err != nil {
		return err
	}
	return classify(fn(d))
}

// WiFi reads the Wi-Fi state and the AMT profiles.
func (s *Session) WiFi() (amt.WiFiSettings, error) {
	var w amt.WiFiSettings
	err := wifiCall(s, func(d WiFiDevice) (err error) {
		w, err = d.WiFi()
		return err
	})
	return w, err
}

// SetWiFiState sets when Intel AMT uses Wi-Fi; Off over a Wi-Fi session needs amt.OpWiFiLockout.
func (s *Session) SetWiFiState(st amt.WiFiState, confirm amt.Confirmation) error {
	return wifiCall(s, func(d WiFiDevice) error { return d.SetWiFiState(st, confirm) })
}

// SetWiFiSync switches the use of the operating system's Wi-Fi profiles.
func (s *Session) SetWiFiSync(on bool) error {
	return wifiCall(s, func(d WiFiDevice) error { return d.SetWiFiSync(on) })
}

// SetWiFiUEFIShare switches the sharing of the Wi-Fi profiles with UEFI.
func (s *Session) SetWiFiUEFIShare(on bool) error {
	return wifiCall(s, func(d WiFiDevice) error { return d.SetWiFiUEFIShare(on) })
}

// AddWiFiProfile creates a Wi-Fi profile.
func (s *Session) AddWiFiProfile(p amt.WiFiProfileSpec) error {
	return wifiCall(s, func(d WiFiDevice) error { return d.AddWiFiProfile(p) })
}

// UpdateWiFiProfile changes the Wi-Fi profile instanceID.
func (s *Session) UpdateWiFiProfile(instanceID string, p amt.WiFiProfileSpec) error {
	return wifiCall(s, func(d WiFiDevice) error { return d.UpdateWiFiProfile(instanceID, p) })
}

// DeleteWiFiProfile deletes the Wi-Fi profile instanceID; over a Wi-Fi session it needs
// amt.OpWiFiLockout.
func (s *Session) DeleteWiFiProfile(instanceID string, confirm amt.Confirmation) error {
	return wifiCall(s, func(d WiFiDevice) error { return d.DeleteWiFiProfile(instanceID, confirm) })
}
