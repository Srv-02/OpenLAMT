package session

import "github.com/Srv-02/openlamt/internal/amt"

// ConfigDevice is the AMT settings and user management part of *amt.Device. Writes read the
// current state first and change only what differs; see the amt package for risks and guards.
type ConfigDevice interface {
	Settings() (amt.Settings, error)
	SetGeneral(amt.GeneralChange) error
	SetNetwork(c amt.NetworkChange, confirmRisky bool) error
	SetFeatures(c amt.FeatureChange, confirmRisky bool) error
	SetConsent(amt.ConsentPolicy) error
	SetEnvironmentDetection(domains []string, confirmRisky bool) error
	SyncClock() error

	Users() (amt.UserList, error)
	AddUser(amt.UserSpec) (handle int, err error)
	UpdateUser(handle int, u amt.UserSpec) error
	RemoveUser(handle int) error
	SetUserEnabled(handle int, enabled bool) error
}

// Settings reads the AMT settings under the session mutex (about 0.4 s on AMT 16).
func (s *Session) Settings() (amt.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.dev.Settings()
	if err != nil {
		return amt.Settings{}, classify(err)
	}
	return st, nil
}

// SetGeneral writes host name, domain, ping and dynamic DNS settings.
func (s *Session) SetGeneral(c amt.GeneralChange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.SetGeneral(c))
}

// SetNetwork writes the wired IPv4 configuration. Without confirmRisky an effective change
// returns *amt.RiskyChangeError (errors.Is amt.ErrRiskyChange) and nothing is written.
func (s *Session) SetNetwork(c amt.NetworkChange, confirmRisky bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.SetNetwork(c, confirmRisky))
}

// SetFeatures switches redirection, SOL, IDE-R and KVM; switching one off needs confirmRisky.
func (s *Session) SetFeatures(c amt.FeatureChange, confirmRisky bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.SetFeatures(c, confirmRisky))
}

// SetConsent writes the user consent policy.
func (s *Session) SetConsent(p amt.ConsentPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.SetConsent(p))
}

// SetEnvironmentDetection writes the intranet domain suffixes; a change needs confirmRisky.
func (s *Session) SetEnvironmentDetection(domains []string, confirmRisky bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.SetEnvironmentDetection(domains, confirmRisky))
}

// SyncClock sets the AMT clock to the local time.
func (s *Session) SyncClock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.SyncClock())
}

// Users reads the admin name and the user ACL entries.
func (s *Session) Users() (amt.UserList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.dev.Users()
	if err != nil {
		return amt.UserList{}, classify(err)
	}
	return l, nil
}

// AddUser creates a digest account and returns its handle.
func (s *Session) AddUser(u amt.UserSpec) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, err := s.dev.AddUser(u)
	if err != nil {
		return 0, classify(err)
	}
	return h, nil
}

// UpdateUser replaces name, password, access and realms of an account.
func (s *Session) UpdateUser(handle int, u amt.UserSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.UpdateUser(handle, u))
}

// RemoveUser deletes an account.
func (s *Session) RemoveUser(handle int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.RemoveUser(handle))
}

// SetUserEnabled enables or disables an account.
func (s *Session) SetUserEnabled(handle int, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.SetUserEnabled(handle, enabled))
}
