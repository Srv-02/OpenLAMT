package session

import (
	"context"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/sol"
)

// OpenSOL opens the host's serial console like OpenKVM opens KVM: the mutex is held during the
// WS-Man steps and the consent prompt; the returned client has its own socket and is used
// without the session afterwards.
func (s *Session) OpenSOL(ctx context.Context, prompt amt.ConsentPrompt) (*sol.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.dev.OpenSOL(ctx, prompt)
	if err != nil {
		return nil, classify(err)
	}
	return c, nil
}
