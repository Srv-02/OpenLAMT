//go:build linux || windows

package localme

import (
	"errors"
	"testing"
)

// TestActivationChecksLMSFirst: the activation stops at a foreign listener before rpc-go is
// called (no Intel ME is needed to see that: rpc-go would fail on it otherwise).
func TestActivationChecksLMSFirst(t *testing.T) {
	old := checkLMS
	calls := 0
	checkLMS = func() error { calls++; return &ForeignLMSError{Port: 16993} }
	t.Cleanup(func() { checkLMS = old })
	var foreign *ForeignLMSError
	if err := (meiChanger{}).ActivateCCM("Secr3t!pw"); !errors.As(err, &foreign) || foreign.Port != 16993 || calls != 1 {
		t.Fatalf("%v, %d checks", err, calls)
	}
}
