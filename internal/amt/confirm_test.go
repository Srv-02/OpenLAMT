package amt

import (
	"errors"
	"testing"
)

var allOps = []Operation{OpHostName, OpFeaturesOff, OpOwnAccount, OpAdminAccount, OpNetwork, OpEnvironment, OpTLS, OpDisableNetwork,
	OpBootOptions, OpBootFile, OpAlarmDelete, OpAuditSettings, OpAuditClear, OpCertDelete, OpRootCertDelete, OpSubscriptionDelete,
	OpWatchdogDelete, OpDefenseDelete, OpDefenseDrop, OpErase, OpWiFiDelete, OpWiFiLockout, OpPowerPolicy, OpSleepHibernate}

func TestRiskOfEveryOperation(t *testing.T) {
	want := map[Operation]RiskSeverity{
		OpHostName: SeverityHigh, OpFeaturesOff: SeverityHigh, OpOwnAccount: SeverityHigh, OpTLS: SeverityHigh,
		OpAdminAccount: SeverityLockout, OpNetwork: SeverityLockout, OpEnvironment: SeverityLockout,
		OpDisableNetwork: SeverityIrreversibleRemotely,
		OpBootOptions:    SeverityHigh, OpBootFile: SeverityHigh, OpAlarmDelete: SeverityHigh, OpAuditSettings: SeverityHigh,
		OpAuditClear: SeverityLockout, OpCertDelete: SeverityHigh, OpRootCertDelete: SeverityHigh,
		OpSubscriptionDelete: SeverityHigh, OpWatchdogDelete: SeverityHigh, OpDefenseDelete: SeverityHigh,
		OpDefenseDrop: SeverityHigh, OpErase: SeverityIrreversibleRemotely, OpWiFiDelete: SeverityHigh,
		OpWiFiLockout: SeverityLockout, OpPowerPolicy: SeverityHigh, OpSleepHibernate: SeverityHigh,
	}
	seen := map[string]bool{}
	for _, op := range allOps {
		r := RiskOf(op)
		if r.Op != op || r.Severity != want[op] {
			t.Errorf("%s: %+v", op, r)
		}
		if r.Warning == "" || seen[r.Warning] {
			t.Errorf("%s: warning key %q empty or duplicate", op, r.Warning)
		}
		seen[r.Warning] = true
	}
	if r := RiskOf("unknown"); r.Severity != 0 || r.Warning != "" {
		t.Errorf("unknown operation: %+v", r)
	}
}

func TestConfirmPhrase(t *testing.T) {
	for _, op := range allOps {
		got := ConfirmPhrase(op, "Device.Example.com")
		want := ConfirmWord
		if RiskOf(op).Severity >= SeverityLockout {
			want = "Device.Example.com"
		}
		if got != want {
			t.Errorf("%s: phrase %q, want %q", op, got, want)
		}
	}
	if got := ConfirmPhrase(OpNetwork, "  "); got != ConfirmWord {
		t.Errorf("empty host falls back to %q, got %q", ConfirmWord, got)
	}
}

func TestConfirmAcceptsOnlyThePhrase(t *testing.T) {
	const host = "device.example.com"
	c, err := Confirm(OpNetwork, host, " DEVICE.example.COM ")
	if err != nil || !c.Allows(OpNetwork, host) {
		t.Fatalf("host typed in other case: %v", err)
	}
	if c.Allows(OpTLS, host) || c.Allows(OpNetwork, "other.example.com") {
		t.Error("confirmation must be bound to operation and host")
	}
	if _, err := Confirm(OpNetwork, host, "CONFIRM"); !errors.Is(err, ErrConfirmationMismatch) {
		t.Errorf("CONFIRM for a lockout operation: %v", err)
	}
	// A TLS change is the orange level (Michael, 11.11.2026): CONFIRM or the checkbox, not the host.
	if c, err := ConfirmChecked(OpTLS, host, true); err != nil || !c.Allows(OpTLS, host) {
		t.Errorf("TLS with the checkbox: %v", err)
	}
	if _, err := Confirm(OpTLS, host, host); !errors.Is(err, ErrConfirmationMismatch) {
		t.Errorf("TLS with the typed host: %v", err)
	}
	c, err = Confirm(OpHostName, host, "CONFIRM")
	if err != nil || !c.Allows(OpHostName, "DEVICE.example.com") {
		t.Fatalf("CONFIRM: %v", err)
	}
	for _, typed := range []string{"confirm", "", "CONFIRMED", host} {
		if _, err := Confirm(OpHostName, host, typed); !errors.Is(err, ErrConfirmationMismatch) {
			t.Errorf("%q accepted", typed)
		}
	}
	if _, err := Confirm("unknown", host, "CONFIRM"); !errors.Is(err, ErrConfirmationMismatch) {
		t.Errorf("unknown operation: %v", err)
	}
	var zero Confirmation
	for _, op := range allOps {
		if zero.Allows(op, host) || zero.Allows(op, "") {
			t.Errorf("zero confirmation allows %s", op)
		}
	}
}

func TestRiskyChangeErrorCarriesRisk(t *testing.T) {
	err := error(&RiskyChangeError{Op: OpNetwork, Fields: []string{RiskNetwork}})
	var re *RiskyChangeError
	if !errors.As(err, &re) || !errors.Is(err, ErrRiskyChange) || re.Risk().Severity != SeverityLockout {
		t.Fatalf("%v", err)
	}
}

func TestConfirmCheckedForEveryOperation(t *testing.T) {
	const host = "device.example.com"
	for _, op := range allOps {
		c, err := ConfirmChecked(op, host, true)
		if err != nil || !c.Allows(op, "DEVICE.example.com ") {
			t.Errorf("%s checked: %v", op, err)
		}
		if c.Allows(op, "other.example.com") {
			t.Errorf("%s: confirmation must be bound to the host", op)
		}
		if c, err := ConfirmChecked(op, host, false); !errors.Is(err, ErrConfirmationMismatch) || c.Allows(op, host) {
			t.Errorf("%s unchecked: %v", op, err)
		}
	}
	if _, err := ConfirmChecked("unknown", host, true); !errors.Is(err, ErrConfirmationMismatch) {
		t.Errorf("unknown operation: %v", err)
	}
}

// TestDesignLevels pins the confirmation level of every operation to the overview table of the
// approved overall design (simple / orange / red), and the existing ones to their dialogs.
func TestDesignLevels(t *testing.T) {
	want := map[Operation]Level{
		// existing
		OpHostName: LevelOrange, OpFeaturesOff: LevelOrange, OpOwnAccount: LevelOrange, OpTLS: LevelOrange,
		OpAdminAccount: LevelRed, OpNetwork: LevelRed, OpEnvironment: LevelRed, OpDisableNetwork: LevelRed,
		// #13 boot options: the dialog is the confirmation; #21 file source: temporary root certificate
		OpBootOptions: LevelSimple, OpBootFile: LevelOrange,
		OpAlarmDelete:   LevelSimple,                         // #14
		OpAuditSettings: LevelOrange, OpAuditClear: LevelRed, // #15
		OpCertDelete: LevelSimple, OpRootCertDelete: LevelOrange, // #16
		OpSubscriptionDelete: LevelSimple,                             // #18
		OpWatchdogDelete:     LevelSimple,                             // #19
		OpDefenseDelete:      LevelSimple, OpDefenseDrop: LevelOrange, // #28, #19
		OpErase:      LevelRed,                             // #20
		OpWiFiDelete: LevelSimple, OpWiFiLockout: LevelRed, // #22
		OpPowerPolicy: LevelOrange, OpSleepHibernate: LevelSimple, // #27
	}
	if len(want) != len(allOps) {
		t.Errorf("table has %d operations, allOps %d", len(want), len(allOps))
	}
	for _, op := range allOps {
		r := RiskOf(op)
		if r.Level != want[op] || r.Level == 0 {
			t.Errorf("%s: level %d, want %d", op, r.Level, want[op])
		}
		// red is the host phrase, simple and orange are CONFIRM
		if (r.Level == LevelRed) != (r.Severity >= SeverityLockout) {
			t.Errorf("%s: level %d does not match severity %d", op, r.Level, r.Severity)
		}
	}
	if RiskOf("unknown").Level != 0 {
		t.Error("unknown operation has a level")
	}
}
