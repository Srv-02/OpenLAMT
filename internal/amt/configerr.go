package amt

import (
	"errors"
	"fmt"
	"strings"
)

// ErrRiskyChange: the change can cut the connection to the device or lock OpenLAMT out and was
// not confirmed (see Confirm). The concrete error is *RiskyChangeError.
var ErrRiskyChange = errors.New("the change can cut the connection to the device and needs confirmation")

// Risk names in RiskyChangeError.Fields.
const (
	RiskNetwork     = "Network"
	RiskRedirection = "Features.Redirection"
	RiskSOL         = "Features.SOL"
	RiskIDER        = "Features.IDER"
	RiskKVM         = "Features.KVM"
	RiskEnvironment = "Environment"
	RiskHostName    = "General.HostName"
	RiskDomainName  = "General.DomainName"
	RiskSharedFQDN  = "General.SharedFQDN"
	RiskDynamicDNS  = "General.DynamicDNS"
	RiskAdmin       = "Users.Admin"
	RiskOwnAccount  = "Users.Self"
	RiskTLS         = "TLS"
	RiskAMTNetwork  = "General.AMTNetworkEnabled"
)

// RiskyChangeError names the operation that needs a confirmation (Confirm with Op) and lists the
// risky parts of the change. Nothing was written.
type RiskyChangeError struct {
	Op     Operation
	Fields []string
}

func (e *RiskyChangeError) Error() string {
	return "risky change needs confirmation (" + string(e.Op) + "): " + strings.Join(e.Fields, ", ")
}

// Risk returns RiskOf(e.Op) for the UI's warning.
func (e *RiskyChangeError) Risk() Risk { return RiskOf(e.Op) }

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
