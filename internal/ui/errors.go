package ui

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/session"
)

// pinReadError: the keychain could not say whether a pin exists for a favorite, so the TLS
// connection was refused instead of silently falling back to trust on first use.
type pinReadError struct{ Err error }

func (e *pinReadError) Error() string { return "fingerprint not readable: " + e.Err.Error() }
func (e *pinReadError) Unwrap() error { return e.Err }

// plainError carries a message that is already written for the user (validation).
type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }

// userError returns an error whose text describe shows unchanged.
func userError(msg string) error { return &plainError{msg: msg} }

// describe turns an error into a sentence for the user: what happened and, where possible,
// what to do. The technical text stays available as details (see showError).
func describe(err error) string {
	var plain *plainError
	var mm *amt.FingerprintMismatchError
	var ke *favorites.KeychainError
	var pe *pinReadError
	var ce *amt.ConsentError
	var kvmErr *amt.KVMError
	var re *kvm.RedirectionError
	var syn *json.SyntaxError
	var path *fs.PathError
	switch {
	case errors.As(err, &plain):
		return plain.msg
	case errors.As(err, &ce) && ce.Step == "SendOptInCode":
		return lang.L("The device did not accept the consent code. Check the six digits on the device screen and try again.")
	case errors.As(err, &ce):
		return lang.L("The device could not start the consent request (return value {{.Code}}). Try again.", map[string]any{"Code": ce.ReturnValue})
	case errors.As(err, &kvmErr):
		return lang.L("The device refused to turn on KVM (return value {{.Code}}). KVM may be disabled in the AMT settings of the device.", map[string]any{"Code": kvmErr.ReturnValue})
	case errors.Is(err, amt.ErrConsentRequired):
		return lang.L("The device requires user consent, but no consent code was entered.")
	case errors.Is(err, amt.ErrInvalidConsentCode):
		return lang.L("The consent code must be six digits.")
	case errors.Is(err, kvm.ErrClosed):
		return lang.L("KVM session closed.")
	case errors.As(err, &re) && re.Stage == "auth":
		return lang.L("KVM login failed: wrong username or password.")
	case errors.As(err, &re):
		return lang.L("The device refused the KVM session (status {{.Status}}). Another KVM session to this device may still be open.", map[string]any{"Status": re.Status})
	case errors.Is(err, kvm.ErrAuthUnsupported):
		return lang.L("The device offers no login method for KVM that OpenLAMT supports.")
	case errors.Is(err, kvm.ErrRedirProtocol):
		return lang.L("The device answered the KVM connection in an unexpected way. Connect KVM again.")
	case errors.Is(err, kvm.ErrZlibHeader), errors.Is(err, kvm.ErrTileTooLarge):
		return lang.L("The KVM picture could not be decoded. Connect KVM again, or switch the quality.")
	case errors.As(err, &pe):
		return lang.L("The trusted certificate could not be read from the keychain, so OpenLAMT did not connect. Check that the keychain is unlocked.")
	case errors.As(err, &mm):
		return lang.L("The certificate of {{.Host}} does not match the trusted fingerprint.", map[string]any{"Host": mm.Host})
	case errors.Is(err, session.ErrAuth):
		return lang.L("Authentication failed: wrong username or password.")
	case errors.Is(err, session.ErrStopped):
		return lang.L("The connection to the device was closed.")
	case errors.Is(err, amt.ErrIncompleteResponse):
		return lang.L("The device sent an incomplete answer. Try again; if this keeps happening, the device may not support this function.")
	case errors.Is(err, amt.ErrIPv6Unsupported):
		return lang.L("IPv6 addresses are not supported.")
	case errors.Is(err, ErrInterval):
		return lang.L("Enter a whole number of seconds between 1 and 3600.")
	case errors.Is(err, favorites.ErrExists):
		return lang.L("A favorite with this host and username already exists.")
	case errors.Is(err, favorites.ErrNotFound):
		return lang.L("This favorite no longer exists.")
	case errors.As(err, &ke):
		return lang.L("The keychain could not be used: {{.Err}}", map[string]any{"Err": ke.Err.Error()})
	case errors.As(err, &syn):
		return lang.L("The favorites file is damaged and could not be read.")
	}
	if msg, ok := describeNetwork(err); ok {
		return msg
	}
	if status, ok := httpStatus(err); ok {
		return lang.L("The device rejected the request ({{.Status}}).", map[string]any{"Status": status})
	}
	if errors.As(err, &path) {
		return lang.L("OpenLAMT could not read or write {{.Path}}.", map[string]any{"Path": path.Path})
	}
	return lang.L("Unexpected error: {{.Err}}", map[string]any{"Err": err.Error()})
}

// httpStatus extracts the status line the WS-Man client reports for a non-200 answer.
func httpStatus(err error) (string, bool) {
	const marker = "wsman.Client post received: "
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return "", false
	}
	status := msg[i+len(marker):]
	if j := strings.IndexByte(status, '\n'); j >= 0 {
		status = status[:j]
	}
	return strings.TrimSpace(status), true
}

// describeNetwork turns dial, DNS, timeout and TLS failures into plain text. The port tells
// which transport was used: 16992 without TLS, 16993 with TLS.
func describeNetwork(err error) (string, bool) {
	var dns *net.DNSError
	var rec tls.RecordHeaderError
	var alert tls.AlertError
	host, port := peer(err)
	args := map[string]any{"Host": host, "Port": port}
	switch {
	case errors.As(err, &dns):
		return lang.L("The name {{.Host}} could not be found. Check the host name, or use the IP address of the device.", map[string]any{"Host": dns.Name}), true
	case isErrno(err, errnoRefused) && port == amt.PortPlain:
		return lang.L("{{.Host}} does not accept connections without TLS on port {{.Port}}. Many devices only allow TLS: turn on “Use TLS” and connect again.", args), true
	case isErrno(err, errnoRefused) && port == amt.PortTLS:
		return lang.L("{{.Host}} does not accept TLS connections on port {{.Port}}. If TLS is not set up on the device, turn off “Use TLS” and connect again.", args), true
	case isErrno(err, errnoRefused):
		return lang.L("{{.Host}} refused the connection on port {{.Port}}. Check that the address belongs to the AMT interface of the device.", args), true
	case errors.As(err, &rec): // from the fingerprint fetch, without a dial error around it
		return lang.L("The device does not speak TLS on this port. Turn off “Use TLS” and connect again."), true
	case isErrno(err, errnoUnreachable):
		return lang.L("{{.Host}} cannot be reached from this computer. Check the address and the network; on macOS, OpenLAMT also needs access under Privacy & Security, Local Network.", args), true
	case isTimeout(err):
		return lang.L("{{.Host}} did not answer in time. Check the address and that the device is connected to the network.", args), true
	case isErrno(err, errnoReset), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return lang.L("{{.Host}} closed the connection unexpectedly. Connect again.", args), true
	case errors.As(err, &alert):
		return lang.L("The TLS handshake with the device failed. The device may use TLS settings that OpenLAMT does not support."), true
	}
	return "", false
}

// peer reads host and port from the innermost dial error; host stays empty when unknown.
func peer(err error) (host, port string) {
	var op *net.OpError
	if !errors.As(err, &op) || op.Addr == nil {
		return lang.L("The device"), ""
	}
	h, p, splitErr := net.SplitHostPort(op.Addr.String())
	if splitErr != nil {
		return op.Addr.String(), ""
	}
	if _, convErr := strconv.Atoi(p); convErr != nil {
		p = ""
	}
	return h, p
}

// isErrno reports whether err wraps one of the given errno values.
func isErrno(err error, list []syscall.Errno) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	for _, e := range list {
		if errno == e {
			return true
		}
	}
	return false
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
