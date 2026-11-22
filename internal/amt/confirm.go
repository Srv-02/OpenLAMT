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
	OpTLS            Operation = "TLS"               // TLS certificate and settings; SeverityHigh: the session follows the new pin
	OpDisableNetwork Operation = "DisableAMTNetwork" // AMTNetworkEnabled off
)

// Operations of the approved overall design (gesamtdesign-2026-11-23, overview table). One
// operation per feature and level: the same feature can need a simple confirmation for one
// action and an orange or red one for another. The device methods that use them follow with the
// feature runs.
const (
	OpBootFile           Operation = "BootFile"           // #21 One Click Recovery from a local file: temporary root certificate on the device
	OpAlarmDelete        Operation = "AlarmDelete"        // #14 delete a wake-up time
	OpAuditSettings      Operation = "AuditSettings"      // #15 audit policy that stops recording ("Record events" off)
	OpAuditClear         Operation = "AuditClear"         // #15 clear the audit log
	OpCertDelete         Operation = "CertDelete"         // #16 delete an unused certificate or key
	OpRootCertDelete     Operation = "RootCertDelete"     // #16 delete a trusted root certificate
	OpSubscriptionDelete Operation = "SubscriptionDelete" // #18 delete an event forwarding target
	OpWatchdogDelete     Operation = "WatchdogDelete"     // #19 delete an agent watchdog
	OpDefenseDelete      Operation = "DefenseDelete"      // #28 delete a System Defense policy or filter
	OpDefenseDrop        Operation = "DefenseDrop"        // #28, #19 activate or change a policy that drops traffic (also by a watchdog)
	OpErase              Operation = "Erase"              // #20 secure or platform erase of the drives
	OpWiFiDelete         Operation = "WiFiDelete"         // #22 delete a Wi-Fi profile
	OpWiFiLockout        Operation = "WiFiLockout"        // #22 delete a profile or switch Wi-Fi off while the session runs over Wi-Fi
	OpPowerPolicy        Operation = "PowerPolicy"        // #27 power policy without remote start
	OpSleepHibernate     Operation = "SleepHibernate"     // #27 sleep or hibernate the host
	OpBootOptions        Operation = "BootOptions"        // #13 restart into PXE, BIOS, ISO or URL boot from the Boot Options dialog
)

// Level is the confirmation dialog of the design: simple (a tile with a red action button),
// orange (warning box, checkbox) or red (warning box, extra fields, checkbox).
type Level int

const (
	// LevelSimple is a short confirmation without checkbox.
	LevelSimple Level = iota + 1
	// LevelOrange is the orange warning with the checkbox "I understand the risk for ...".
	LevelOrange
	// LevelRed is the red warning with the checkbox; the change can lock the user out or lose data.
	LevelRed
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
	Level    Level
	Warning  string // stable English key for the UI's translated warning text
}

// risk builds a table entry. The level fixes the severity (and with it the phrase of
// ConfirmPhrase): simple and orange are SeverityHigh, red is SeverityLockout; an operation that
// cannot be undone over the network passes SeverityIrreversibleRemotely.
func risk(op Operation, l Level, warning string, sev ...RiskSeverity) Risk {
	s := SeverityHigh
	if l == LevelRed {
		s = SeverityLockout
	}
	if len(sev) > 0 {
		s = sev[0]
	}
	return Risk{op, s, l, warning}
}

var risks = func() map[Operation]Risk {
	m := map[Operation]Risk{}
	for _, r := range []Risk{
		// Existing operations (their dialog levels: manage_settings*.go, user_dialogs.go).
		risk(OpHostName, LevelOrange, "risk.host_name"),
		risk(OpFeaturesOff, LevelOrange, "risk.features_off"),
		risk(OpOwnAccount, LevelOrange, "risk.own_account"),
		risk(OpTLS, LevelOrange, "risk.tls"),
		risk(OpAdminAccount, LevelRed, "risk.admin_account"),
		risk(OpNetwork, LevelRed, "risk.network"),
		risk(OpEnvironment, LevelRed, "risk.environment_detection"),
		risk(OpDisableNetwork, LevelRed, "risk.disable_amt_network", SeverityIrreversibleRemotely),
		// The overall design, per feature.
		risk(OpBootOptions, LevelSimple, "risk.boot_options"),
		risk(OpBootFile, LevelOrange, "risk.boot_file"),
		risk(OpAlarmDelete, LevelSimple, "risk.alarm_delete"),
		risk(OpAuditSettings, LevelOrange, "risk.audit_settings"),
		risk(OpAuditClear, LevelRed, "risk.audit_clear"),
		risk(OpCertDelete, LevelSimple, "risk.cert_delete"),
		risk(OpRootCertDelete, LevelOrange, "risk.root_cert_delete"),
		risk(OpSubscriptionDelete, LevelSimple, "risk.subscription_delete"),
		risk(OpWatchdogDelete, LevelSimple, "risk.watchdog_delete"),
		risk(OpDefenseDelete, LevelSimple, "risk.defense_delete"),
		risk(OpDefenseDrop, LevelOrange, "risk.defense_drop"),
		risk(OpErase, LevelRed, "risk.erase", SeverityIrreversibleRemotely),
		risk(OpWiFiDelete, LevelSimple, "risk.wifi_delete"),
		risk(OpWiFiLockout, LevelRed, "risk.wifi_lockout"),
		risk(OpPowerPolicy, LevelOrange, "risk.power_policy"),
		risk(OpSleepHibernate, LevelSimple, "risk.sleep_hibernate"),
	} {
		m[r.Op] = r
	}
	return m
}()

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
// Its fields are unexported: outside this package a valid value only comes from Confirm or
// ConfirmChecked, and the zero value confirms nothing. Create it right before the call it
// confirms.
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

// ConfirmChecked is the confirmation of the UI: the user ticked the checkbox "I understand the
// risk for <device>" instead of typing a phrase. It confirms every known operation, but only
// when checked is true; otherwise, and for an unknown operation, it returns
// ErrConfirmationMismatch.
func ConfirmChecked(op Operation, host string, checked bool) (Confirmation, error) {
	if !checked || RiskOf(op).Severity == 0 {
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
