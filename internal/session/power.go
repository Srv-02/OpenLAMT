package session

import "github.com/Srv-02/openlamt/internal/amt"

// Wake alarms (issue #14) and the power policy (issue #27) as session capabilities
// (capabilities.go). A device without them answers ErrUnsupported.

// AlarmDevice is what the Power page needs for the wake alarms.
type AlarmDevice interface {
	GetAlarms() ([]amt.Alarm, error)
	AddAlarm(amt.AlarmSpec) error
	DeleteAlarm(name string) error
}

// PowerPolicyDevice is what the Power page needs for the power packages and the idle timeout.
type PowerPolicyDevice interface {
	GetPowerPolicy() (amt.PowerPolicy, error)
	SetPowerScheme(id string, confirm amt.Confirmation) error
	SetIdleTimeout(minutes int) error
}

var (
	_ AlarmDevice       = (*amt.Device)(nil)
	_ PowerPolicyDevice = (*amt.Device)(nil)
)

// Alarms lists the wake alarms of the device.
func (s *Session) Alarms() ([]amt.Alarm, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[AlarmDevice](s)
	if err != nil {
		return nil, err
	}
	list, err := d.GetAlarms()
	return list, classify(err)
}

// AddAlarm creates a wake alarm (amt.Device.AddAlarm for the refusals).
func (s *Session) AddAlarm(a amt.AlarmSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[AlarmDevice](s)
	if err != nil {
		return err
	}
	return classify(d.AddAlarm(a))
}

// DeleteAlarm deletes the wake alarm called name.
func (s *Session) DeleteAlarm(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[AlarmDevice](s)
	if err != nil {
		return err
	}
	return classify(d.DeleteAlarm(name))
}

// PowerPolicy reads the power packages, the active one and the idle timeout.
func (s *Session) PowerPolicy() (amt.PowerPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[PowerPolicyDevice](s)
	if err != nil {
		return amt.PowerPolicy{}, err
	}
	p, err := d.GetPowerPolicy()
	return p, classify(err)
}

// SetPowerScheme activates the power package id. A change that turns AMT off in S4-S5
// (amt.PowerPolicy.RisksRemoteStart) needs a confirmation for amt.OpPowerPolicy.
func (s *Session) SetPowerScheme(id string, confirm amt.Confirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[PowerPolicyDevice](s)
	if err != nil {
		return err
	}
	return classify(d.SetPowerScheme(id, confirm))
}

// SetIdleTimeout sets the idle timeout of the ME in minutes.
func (s *Session) SetIdleTimeout(minutes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[PowerPolicyDevice](s)
	if err != nil {
		return err
	}
	return classify(d.SetIdleTimeout(minutes))
}
