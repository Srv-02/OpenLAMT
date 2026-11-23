package session

import "github.com/Srv-02/openlamt/internal/amt"

// ForwardingDevice is the capability of event forwarding (issue #18).
type ForwardingDevice interface {
	EventForwarding() (amt.Forwarding, error)
	AddForwardingTarget(s amt.TargetSpec) error
	DeleteForwardingTarget(t amt.ForwardingTarget) error
}

var _ ForwardingDevice = (*amt.Device)(nil)

// EventForwarding reads the event filters and the forwarding targets.
func (s *Session) EventForwarding() (amt.Forwarding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[ForwardingDevice](s)
	if err != nil {
		return amt.Forwarding{}, err
	}
	v, err := d.EventForwarding()
	return v, classify(err)
}

// AddForwardingTarget subscribes a new target.
func (s *Session) AddForwardingTarget(spec amt.TargetSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[ForwardingDevice](s)
	if err != nil {
		return err
	}
	return classify(d.AddForwardingTarget(spec))
}

// DeleteForwardingTarget ends the subscription of t.
func (s *Session) DeleteForwardingTarget(t amt.ForwardingTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[ForwardingDevice](s)
	if err != nil {
		return err
	}
	return classify(d.DeleteForwardingTarget(t))
}
