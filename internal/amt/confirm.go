package amt

import (
	"errors"
	"strings"
)

// Operation names a change that needs a typed confirmation (double opt-in) before the device
// is written. The UI shows RiskOf(op) as a red warning and asks for ConfirmPhrase(op, host).
type Operation string

const (
	OpHostName       Operation = "HostName"          // host name, domain, shared FQDN or dynamic DNS mode
	OpFeaturesOff    Operation = "FeaturesOff"       // switching redirection, SOL, IDE-R or KVM off
	OpOwnAccount     Operation = "OwnAccount"        // name, password, access or realms of the session's own ACL account
	OpAdminAccount   Operation = "AdminAccount"      // name or password of the AMT admin account
	OpNetwork        Operation = "Network"           // IPv4 configuration of the wired port
	OpEnvironment    Operation = "Environment"       // environment detection domains
	OpTLS            Operation = "TLS"               // TLS certificate and settings
	OpDisableNetwork Operation = "DisableAMTNetwork" // AMTNetworkEnabled off
)

// RiskSeverity orders how hard a confirmed change is to undo (Severity is the event log's).
type RiskSeverity int

const (
	// SeverityHigh interrupts sessions or the reachability under a name; it can be undone over
	// the network.
	SeverityHigh RiskSeverity = iota + 1
	// SeverityLockout can lock OpenLAMT out of the device; undoing it may need the device itself
	// (its operating system, physical access or MEBx).
	SeverityLockout
	// SeverityIrreversibleRemotely cannot be undone over the network; only MEBx at the device.
	SeverityIrreversibleRemotely
)

// Risk describes the warning the UI shows in red before asking for the confirmation phrase.
type Risk struct {
	Op       Operation
	Severity RiskSeverity
	Warning  string // stable English key for the UI's translated warning text
}

var risks = map[Operation]Risk{
	OpHostName:       {OpHostName, SeverityHigh, "risk.host_name"},
	OpFeaturesOff:    {OpFeaturesOff, SeverityHigh, "risk.features_off"},
	OpOwnAccount:     {OpOwnAccount, SeverityHigh, "risk.own_account"},
	OpAdminAccount:   {OpAdminAccount, SeverityLockout, "risk.admin_account"},
	OpNetwork:        {OpNetwork, SeverityLockout, "risk.network"},
	OpEnvironment:    {OpEnvironment, SeverityLockout, "risk.environment_detection"},
	OpTLS:            {OpTLS, SeverityLockout, "risk.tls"},
	OpDisableNetwork: {OpDisableNetwork, SeverityIrreversibleRemotely, "risk.disable_amt_network"},
}

// RiskOf returns the risk of op; the zero Risk for an unknown operation.
func RiskOf(op Operation) Risk { return risks[op] }

// ConfirmWord is the phrase for SeverityHigh operations.
const ConfirmWord = "CONFIRM"

// ErrConfirmationMismatch: the typed text is not the confirmation phrase.
var ErrConfirmationMismatch = errors.New("the typed confirmation does not match")

// ConfirmPhrase returns the text the user types to confirm op on the device reached as host
// (Session.Config().Host): ConfirmWord for SeverityHigh, the host for the more severe
// operations, so the confirmation names the device it hits.
func ConfirmPhrase(op Operation, host string) string {
	host = strings.TrimSpace(host)
	if RiskOf(op).Severity >= SeverityLockout && host != "" {
		return host
	}
	return ConfirmWord
}

// Confirmation is the proof that the user typed the phrase for one operation on one device.
// Its fields are unexported: outside this package a valid value only comes from Confirm, and the
// zero value confirms nothing. Create it right before the call it confirms.
type Confirmation struct {
	op   Operation
	host string
}

// Confirm checks typed against ConfirmPhrase(op, host): ConfirmWord exactly, a host without
// regard to case; surrounding spaces are ignored. It returns ErrConfirmationMismatch otherwise.
func Confirm(op Operation, host, typed string) (Confirmation, error) {
	if RiskOf(op).Severity == 0 {
		return Confirmation{}, ErrConfirmationMismatch
	}
	phrase, typed := ConfirmPhrase(op, host), strings.TrimSpace(typed)
	if phrase == ConfirmWord && typed != phrase || phrase != ConfirmWord && !strings.EqualFold(typed, phrase) {
		return Confirmation{}, ErrConfirmationMismatch
	}
	return Confirmation{op: op, host: normHost(host)}, nil
}

func normHost(h string) string { return strings.ToLower(strings.TrimSpace(h)) }

// Allows reports whether c confirms op on host. Fakes use it for the same check as *Device.
func (c Confirmation) Allows(op Operation, host string) bool {
	return c.op != "" && c.op == op && c.host == normHost(host)
}

// confirmed returns nil if c confirms op on this device, else *RiskyChangeError with fields.
func (d *Device) confirmed(c Confirmation, op Operation, fields ...string) error {
	if c.Allows(op, d.cfg.Host) {
		return nil
	}
	return &RiskyChangeError{Op: op, Fields: fields}
}
