package amt

import (
	"context"
	"errors"
	"fmt"

	"github.com/Srv-02/openlamt/internal/sol"
	redirection "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/redirection"
)

// SOLError is returned when the device rejects a Serial-over-LAN activation step.
type SOLError struct {
	Step        string
	ReturnValue int
}

func (e *SOLError) Error() string {
	return fmt.Sprintf("sol %s failed: device returned ReturnValue %d", e.Step, e.ReturnValue)
}

// EnableSOL makes the redirection listener available and switches SOL on, keeping IDE-R as it
// is. It writes only when needed.
func (d *Device) EnableSOL() error {
	cur, err := d.ensureListener()
	if err != nil {
		return err
	}
	var want redirection.RequestedState
	switch int(cur.EnabledState) {
	case redirSOL, redirIDERAndSOL:
		return nil
	case redirDisabled:
		want = redirSOL
	case redirIDER:
		want = redirIDERAndSOL
	default:
		return fmt.Errorf("enable sol: unexpected redirection service state %d", int(cur.EnabledState))
	}
	return d.requestRedirectionState("enable sol", want, func(rv int) error {
		return &SOLError{Step: "RedirectionService.RequestStateChange", ReturnValue: rv}
	})
}

// OpenSOL opens the host's serial console: it runs EnableSOL and the user consent (needed for
// SOL only when OptInRequired is All, like IDE-R) and dials the redirection listener with the
// pinned TLS config.
func (d *Device) OpenSOL(ctx context.Context, prompt ConsentPrompt) (*sol.Client, error) {
	if err := d.EnableSOL(); err != nil {
		return nil, err
	}
	if err := d.userConsent(prompt, true); err != nil {
		return nil, err
	}
	if d.cfg.UseTLS && d.tlsCfg == nil {
		// Without the pinned config sol.Dial would connect in plain text.
		return nil, errors.New("sol: TLS is enabled but the device has no pinned TLS config")
	}
	return sol.Dial(ctx, sol.Config{
		Addr:        d.cfg.RedirectionAddress(),
		Username:    d.cfg.Username,
		Password:    d.cfg.Password,
		TLS:         d.tlsCfg,
		DialContext: d.cfg.dialContext,
	})
}
