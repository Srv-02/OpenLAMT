package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// refusal is a call the server rejects before anything reaches the device: policy, input or a
// power rule. Its text is the message for the agent.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) error { return &refusal{msg: fmt.Sprintf(format, args...)} }

func isRefusal(err error) bool {
	var r *refusal
	return errors.As(err, &r)
}

// powerReturnValues are the CIM_PowerManagementService.RequestPowerStateChange return values.
var powerReturnValues = map[int]string{
	1:    "not supported",
	2:    "unknown or unspecified error; AMT returns it for off and cycle while a KVM session to the device is open",
	3:    "cannot complete within the timeout period",
	4:    "failed",
	5:    "invalid parameter",
	6:    "in use",
	4096: "method parameters checked, job started",
	4097: "invalid state transition",
	4098: "use of timeout parameter not supported",
	4099: "busy",
}

// explain turns an error into an English sentence for the agent: what happened and, where
// possible, what to do. These texts are the MCP server's own; the app's live in internal/ui.
func explain(err error) string {
	var (
		ref *refusal
		mm  *amt.FingerprintMismatchError
		ce  *amt.ConsentError
		pe  *amt.PowerError
		oe  *net.OpError
		dns *net.DNSError
	)
	switch {
	case errors.As(err, &ref):
		return ref.msg
	case errors.Is(err, errConsentCodeNeeded):
		return "This action needs user consent. The device now shows a six-digit code on its screen. " +
			"Ask a person at the device for the code and call boot_to_bios again with consent_code. " +
			"The code is valid for a few minutes only."
	case errors.As(err, &ce) && ce.Step == "SendOptInCode":
		return "The device did not accept the consent code. Ask the person at the device for the six digits " +
			"it shows now and try again; without a code the device starts a new consent request."
	case errors.As(err, &ce):
		return fmt.Sprintf("The device could not start the user consent request (AMT ReturnValue %d). Try again.", ce.ReturnValue)
	case errors.Is(err, amt.ErrConsentRequired):
		return "This action needs user consent, but no consent code was given."
	case errors.Is(err, amt.ErrInvalidConsentCode):
		return "consent_code must be exactly six digits."
	case errors.Is(err, session.ErrAuth):
		return "Login failed: the device rejected the user name or password stored for this favorite. " +
			"Update the password in the OpenLAMT app."
	case errors.Is(err, amt.ErrAccessDenied):
		return "The AMT account of this favorite lacks the permission for this operation."
	case errors.As(err, &mm):
		return fmt.Sprintf("The TLS certificate of the device changed (pinned fingerprint %s, device sent %s). "+
			"The connection was refused. If the change is expected, connect with the OpenLAMT app and accept the new certificate there.",
			amt.FormatFingerprint(mm.Expected), amt.FormatFingerprint(mm.Actual))
	case errors.As(err, &pe) && pe.Action == amt.BootToBIOS:
		return fmt.Sprintf("The device refused the boot to BIOS (AMT ReturnValue %d). "+
			"The BIOS setup boot option may still be set on the device.", pe.ReturnValue)
	case errors.As(err, &pe):
		meaning := powerReturnValues[pe.ReturnValue]
		if meaning == "" {
			meaning = "unknown value"
		}
		return fmt.Sprintf("The device refused power %s (AMT ReturnValue %d: %s).", pe.Action, pe.ReturnValue, meaning)
	case errors.Is(err, amt.ErrIPv6Unsupported):
		return "IPv6 addresses are not supported. Use a host name or an IPv4 address in the favorite."
	case errors.Is(err, amt.ErrIncompleteResponse):
		return "The device sent an incomplete answer: " + err.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "The device did not answer in time: " + err.Error()
	case errors.As(err, &dns):
		return "Cannot resolve the host name of the device: " + err.Error()
	case errors.As(err, &oe):
		return "Cannot reach the device: " + err.Error()
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "The device did not answer in time: " + err.Error()
	}
	if s := err.Error(); containsDial(s) {
		return "Cannot reach the device: " + s
	}
	return err.Error()
}

// containsDial recognizes dial errors the WS-Man library only passes on as text.
func containsDial(s string) bool {
	for _, p := range []string{"dial tcp", "connection refused", "no route to host", "network is unreachable", "i/o timeout"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
