package mcpserver

import (
	"errors"
	"strings"

	"github.com/Srv-02/openlamt/internal/amt"
)

// errConsentCodeNeeded: the device needs user consent and the call carried no consent_code.
// By the time it is returned the opt-in has been started, so the device shows the code.
var errConsentCodeNeeded = errors.New("user consent code needed")

// consentCode is the consent_code parameter as an amt.ConsentPrompt. The same parameter serves
// every tool that may need the CCM user consent (boot to BIOS now; KVM, IDE-R and SOL later).
type consentCode string

func (c consentCode) ConsentCode() (string, error) {
	if c == "" {
		return "", errConsentCodeNeeded
	}
	return string(c), nil
}

var _ amt.ConsentPrompt = consentCode("")

// parseConsentCode accepts an empty value (no code yet) or exactly six digits.
func parseConsentCode(s string) (consentCode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) != 6 || strings.Trim(s, "0123456789") != "" {
		return "", amt.ErrInvalidConsentCode
	}
	return consentCode(s), nil
}
