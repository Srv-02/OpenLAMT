package session

import (
	"fmt"

	"github.com/Srv-02/openlamt/internal/amt"
)

// Remote Secure Erase and Remote Platform Erase (issue #20) as a session capability. The erase
// is a boot option, so it asks for the user consent first, as Boot does: a failed or cancelled
// consent writes nothing. The erase capabilities come with BootInfo.

// EraseDevice is what the Erase Drives dialog and the Remote Platform Erase switch need.
type EraseDevice interface {
	Erase(amt.EraseOptions, amt.Confirmation) error
	SetRPE(on bool) error
}

var _ EraseDevice = (*amt.Device)(nil)

// Erase checks the confirmation (amt.OpErase) and, for Remote Platform Erase, TLS, then runs
// the user consent (prompt may be nil when no code is at hand) and arms the erase, which
// restarts or powers on the device; all under the mutex. Nothing reaches the device before the
// checks pass.
func (s *Session) Erase(prompt amt.ConsentPrompt, o amt.EraseOptions, confirm amt.Confirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !confirm.Allows(amt.OpErase, s.Config().Host) {
		return &amt.RiskyChangeError{Op: amt.OpErase, Fields: []string{"Erase"}}
	}
	if o.Platform && !s.Config().UseTLS {
		return fmt.Errorf("erase: %w", amt.ErrEraseNeedsTLS)
	}
	d, err := capability[EraseDevice](s)
	if err != nil {
		return err
	}
	if err := s.dev.BootConsent(prompt); err != nil {
		return classify(err)
	}
	return classify(d.Erase(o, confirm))
}

// SetRPE allows or forbids Remote Platform Erase in the AMT boot service.
func (s *Session) SetRPE(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[EraseDevice](s)
	if err != nil {
		return err
	}
	return classify(d.SetRPE(on))
}
