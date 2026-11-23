package session

import "github.com/Srv-02/openlamt/internal/amt"

// System Defense (issue #28) and the agent presence watchdogs (issue #19) as session
// capabilities (capabilities.go). A device without them answers ErrUnsupported.

// DefenseDevice is what the System Defense page needs for policies and filters.
type DefenseDevice interface {
	GetSystemDefense() (amt.SystemDefense, error)
	AddDefenseFilter(amt.FilterSpec) error
	DeleteDefenseFilter(id string) error
	AddDefensePolicy(amt.PolicySpec) error
	DeleteDefensePolicy(id string) error
	ActivateDefensePolicy(id string, confirm amt.Confirmation) error
	DeactivateDefensePolicy() error
}

// WatchdogDevice is what the System Defense page needs for the agent watchdogs.
type WatchdogDevice interface {
	GetWatchdogs() (amt.Watchdogs, error)
	AddWatchdog(amt.WatchdogSpec, amt.Confirmation) error
	DeleteWatchdog(id string) error
}

var (
	_ DefenseDevice  = (*amt.Device)(nil)
	_ WatchdogDevice = (*amt.Device)(nil)
)

// defenseCall runs fn on the DefenseDevice of s under s.mu and classifies its error.
func defenseCall[D any](s *Session, fn func(D) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[D](s)
	if err != nil {
		return err
	}
	return classify(fn(d))
}

// SystemDefense reads policies, filters, the active policy and its statistics.
func (s *Session) SystemDefense() (amt.SystemDefense, error) {
	var sd amt.SystemDefense
	err := defenseCall(s, func(d DefenseDevice) (err error) {
		sd, err = d.GetSystemDefense()
		return err
	})
	return sd, err
}

// AddDefenseFilter creates a System Defense filter.
func (s *Session) AddDefenseFilter(f amt.FilterSpec) error {
	return defenseCall(s, func(d DefenseDevice) error { return d.AddDefenseFilter(f) })
}

// DeleteDefenseFilter deletes the filter id.
func (s *Session) DeleteDefenseFilter(id string) error {
	return defenseCall(s, func(d DefenseDevice) error { return d.DeleteDefenseFilter(id) })
}

// AddDefensePolicy creates a System Defense policy.
func (s *Session) AddDefensePolicy(p amt.PolicySpec) error {
	return defenseCall(s, func(d DefenseDevice) error { return d.AddDefensePolicy(p) })
}

// DeleteDefensePolicy deletes the policy id.
func (s *Session) DeleteDefensePolicy(id string) error {
	return defenseCall(s, func(d DefenseDevice) error { return d.DeleteDefensePolicy(id) })
}

// ActivateDefensePolicy enables the policy id on the wired port. A policy that drops traffic
// needs a confirmation for amt.OpDefenseDrop.
func (s *Session) ActivateDefensePolicy(id string, confirm amt.Confirmation) error {
	return defenseCall(s, func(d DefenseDevice) error { return d.ActivateDefensePolicy(id, confirm) })
}

// DeactivateDefensePolicy turns the active policy off.
func (s *Session) DeactivateDefensePolicy() error {
	return defenseCall(s, func(d DefenseDevice) error { return d.DeactivateDefensePolicy() })
}

// Watchdogs reads the agent watchdogs, their actions and the policy they switch.
func (s *Session) Watchdogs() (amt.Watchdogs, error) {
	var w amt.Watchdogs
	err := defenseCall(s, func(d WatchdogDevice) (err error) {
		w, err = d.GetWatchdogs()
		return err
	})
	return w, err
}

// AddWatchdog creates an agent watchdog. One that activates a policy which drops traffic needs
// a confirmation for amt.OpDefenseDrop.
func (s *Session) AddWatchdog(w amt.WatchdogSpec, confirm amt.Confirmation) error {
	return defenseCall(s, func(d WatchdogDevice) error { return d.AddWatchdog(w, confirm) })
}

// DeleteWatchdog deletes the watchdog of agent id.
func (s *Session) DeleteWatchdog(id string) error {
	return defenseCall(s, func(d WatchdogDevice) error { return d.DeleteWatchdog(id) })
}
