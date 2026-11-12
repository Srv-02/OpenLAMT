package session

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/sol"
)

// FakeSOL configures Fake.OpenSOL: it returns Err if set; otherwise it applies the Consent rules
// of the Fake like OpenKVM and dials Addr (a soltest server) with User and Password.
type FakeSOL struct {
	Err            error
	Addr           string
	User, Password string

	calls atomic.Int32
}

// Calls is the number of OpenSOL calls.
func (s *FakeSOL) Calls() int { return int(s.calls.Load()) }

// OpenSOL mirrors (*amt.Device).OpenSOL against the fake server (see FakeSOL).
func (f *Fake) OpenSOL(ctx context.Context, prompt amt.ConsentPrompt) (*sol.Client, error) {
	f.enter()
	defer f.leave()
	f.SOL.calls.Add(1)
	if f.SOL.Err != nil {
		return nil, f.SOL.Err
	}
	if f.Consent {
		if prompt == nil {
			return nil, amt.ErrConsentRequired
		}
		code, err := prompt.ConsentCode()
		if err != nil {
			return nil, fmt.Errorf("read consent code: %w", err)
		}
		if len(strings.TrimSpace(code)) != 6 {
			return nil, amt.ErrInvalidConsentCode
		}
		if strings.TrimSpace(code) != f.ConsentCode {
			return nil, &amt.ConsentError{Step: "SendOptInCode", ReturnValue: 2}
		}
	}
	return sol.Dial(ctx, sol.Config{Addr: f.SOL.Addr, Username: f.SOL.User, Password: f.SOL.Password})
}
