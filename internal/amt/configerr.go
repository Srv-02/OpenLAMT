package amt

import (
	"errors"
	"fmt"
	"strings"
)

// ErrRiskyChange: the change can cut the connection to the device (network address, redirection,
// environment detection) and was not confirmed. The concrete error is *RiskyChangeError.
var ErrRiskyChange = errors.New("the change can cut the connection to the device and needs confirmation")

// Risk names in RiskyChangeError.Fields.
const (
	RiskNetwork     = "Network"
	RiskRedirection = "Features.Redirection"
	RiskSOL         = "Features.SOL"
	RiskIDER        = "Features.IDER"
	RiskKVM         = "Features.KVM"
	RiskEnvironment = "Environment"
)

// RiskyChangeError lists the risky parts of an unconfirmed change. Nothing was written.
type RiskyChangeError struct{ Fields []string }

func (e *RiskyChangeError) Error() string {
	return "risky change needs confirmation: " + strings.Join(e.Fields, ", ")
}

func (e *RiskyChangeError) Is(target error) bool { return target == ErrRiskyChange }

// ErrProtectedAccount: the account cannot be changed or removed through OpenLAMT, because it is
// an AMT system account ("$$" prefix) or the account this session is logged in with.
var ErrProtectedAccount = errors.New("the account is protected: a system account or the account of this session")

// ErrInvalidValue: a value fails the client-side check. The concrete error is *ValidationError.
var ErrInvalidValue = errors.New("invalid value")

// ValidationError names the field (as in the change type, e.g. "HostName", "Password") and why
// it was rejected. Nothing was written.
type ValidationError struct{ Field, Reason string }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid value for %s: %s", e.Field, e.Reason)
}

func (e *ValidationError) Is(target error) bool { return target == ErrInvalidValue }

func invalid(field, reason string) error { return &ValidationError{Field: field, Reason: reason} }

// ErrNotModifiable: the device does not allow this change (e.g. CanModifyOptInPolicy is 0,
// or the class is missing on this firmware).
var ErrNotModifiable = errors.New("the device does not allow changing this setting")

// MethodError is a non-zero ReturnValue of an AMT method, e.g. AddUserAclEntryEx 2058 (duplicate).
type MethodError struct {
	Method      string
	ReturnValue int
}

func (e *MethodError) Error() string {
	return fmt.Sprintf("%s failed: device returned ReturnValue %d", e.Method, e.ReturnValue)
}
