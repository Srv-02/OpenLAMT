package amt

import (
	"errors"
	"testing"
)

var allOps = []Operation{OpHostName, OpFeaturesOff, OpOwnAccount, OpAdminAccount, OpNetwork, OpEnvironment, OpTLS, OpDisableNetwork}

func TestRiskOfEveryOperation(t *testing.T) {
	want := map[Operation]RiskSeverity{
		OpHostName: SeverityHigh, OpFeaturesOff: SeverityHigh, OpOwnAccount: SeverityHigh, OpTLS: SeverityHigh,
		OpAdminAccount: SeverityLockout, OpNetwork: SeverityLockout, OpEnvironment: SeverityLockout,
		OpDisableNetwork: SeverityIrreversibleRemotely,
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

func TestConfirmCheckedOnlyForHighSeverity(t *testing.T) {
	const host = "device.example.com"
	for _, op := range allOps {
		c, err := ConfirmChecked(op, host, true)
		if RiskOf(op).Severity == SeverityHigh {
			if err != nil || !c.Allows(op, "DEVICE.example.com ") {
				t.Errorf("%s checked: %v", op, err)
			}
			if c.Allows(op, "other.example.com") {
				t.Errorf("%s: confirmation must be bound to the host", op)
			}
		} else if !errors.Is(err, ErrConfirmationMismatch) || c.Allows(op, host) {
			t.Errorf("%s needs the typed phrase, checkbox accepted: %v", op, err)
		}
		if c, err := ConfirmChecked(op, host, false); !errors.Is(err, ErrConfirmationMismatch) || c.Allows(op, host) {
			t.Errorf("%s unchecked: %v", op, err)
		}
	}
	if _, err := ConfirmChecked("unknown", host, true); !errors.Is(err, ErrConfirmationMismatch) {
		t.Errorf("unknown operation: %v", err)
	}
}
