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
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/mkiso"
	"github.com/Srv-02/openlamt/internal/redir"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/sol"
	"github.com/Srv-02/openlamt/internal/update"
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
	var tile *kvm.TileError
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
		return lang.L("The device refused to enable KVM (return value {{.Code}}). KVM may be disabled in the AMT settings of the device.", map[string]any{"Code": kvmErr.ReturnValue})
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
	case errors.Is(err, kvm.ErrZlibHeader), errors.Is(err, kvm.ErrTileTooLarge), errors.As(err, &tile):
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
	if msg, ok := describeDevice(err); ok {
		return msg
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

// describeDevice covers the errors of AMT writes, power, redirection sessions and the ISO
// builder. TestDescribeCoversExportedErrors lists every exported error and checks that it
// ends up here or in describe.
func describeDevice(err error) (string, bool) {
	var me *amt.MethodError
	var pw *amt.PowerError
	var sl *amt.SOLError
	var id *amt.IDERError
	var rfb *kvm.RFBError
	var mp *mkiso.PathError
	switch {
	case errors.Is(err, amt.ErrInvalidValue):
		// The technical reason (ValidationError.Reason) stays in the details.
		return lang.L("Some of the values you entered are not valid. Check them and try again. Nothing was changed."), true
	case errors.As(err, &me):
		return describeMethod(me), true
	case errors.Is(err, amt.ErrNotModifiable):
		return lang.L("The device does not allow changing this setting."), true
	case errors.Is(err, amt.ErrAccessDenied):
		return lang.L("This account is not allowed to do this on the device."), true
	case errors.Is(err, amt.ErrProtectedAccount):
		return lang.L("This account cannot be changed or removed here: it is a system account or the account of this connection."), true
	case errors.Is(err, amt.ErrRiskyChange):
		return lang.L("This change could cut the connection to the device and needs a confirmation. Nothing was changed."), true
	case errors.Is(err, amt.ErrConfirmationMismatch):
		return lang.L("The confirmation does not match. Nothing was changed."), true
	case errors.Is(err, amt.ErrCredentialsUncertain):
		return lang.L("It is unclear whether the device accepted the new login. Try to sign in with the new login; if that fails, use the old one."), true
	case errors.Is(err, amt.ErrCertificateUncertain):
		return lang.L("It is unclear whether the device switched to the new certificate. Connect again and compare the fingerprint before you trust it."), true
	case errors.Is(err, session.ErrNotSaved):
		return lang.L("The device accepted the change, but OpenLAMT could not save it. Check that the keychain is unlocked."), true
	case errors.As(err, &pw):
		return lang.L("The device refused the power request (return value {{.Code}}). Try again later.", map[string]any{"Code": pw.ReturnValue}), true
	case errors.As(err, &sl):
		return lang.L("The device refused to enable Serial over LAN (return value {{.Code}}). SOL may be disabled in the AMT settings of the device.", map[string]any{"Code": sl.ReturnValue}), true
	case errors.As(err, &id):
		return lang.L("The device refused to enable media redirection (return value {{.Code}}). IDE-R may be disabled in the AMT settings of the device.", map[string]any{"Code": id.ReturnValue}), true
	case errors.Is(err, amt.ErrIDERUnsupported):
		return lang.L("This device does not support media redirection (IDE-R)."), true
	case errors.Is(err, sol.ErrStartFailed):
		return lang.L("The device did not start the serial console. Try again; Serial over LAN may be disabled in the AMT settings of the device."), true
	case errors.Is(err, sol.ErrClosedByDevice):
		return lang.L("The device closed the serial console."), true
	case errors.Is(err, ider.ErrToggleFailed):
		return lang.L("The device did not switch the virtual drive. Try again."), true
	case errors.Is(err, ider.ErrClosedByDevice):
		return lang.L("The device closed the media session."), true
	case errors.Is(err, redir.ErrFieldTooLong), errors.Is(err, redir.ErrAuthDataTooLong):
		return lang.L("The user name or password is too long for the login to the device. Use a shorter one."), true
	case errors.As(err, &rfb):
		return lang.L("The device sent a KVM message that OpenLAMT cannot handle. Connect KVM again."), true
	case errors.Is(err, update.ErrDigest):
		return lang.L("The downloaded file does not match its checksum and was deleted. Try again later."), true
	case errors.As(err, &mp):
		return lang.L("{{.Path}}: {{.Reason}}", map[string]any{"Path": mp.Path, "Reason": describe(mp.Err)}), true
	}
	return describeISO(err)
}

// describeMethod names the cause for the AMT return values that are known; the others keep
// the method and the number.
func describeMethod(e *amt.MethodError) string {
	if e.Method == "AddUserAclEntryEx" {
		switch e.ReturnValue {
		case 2058:
			return lang.L("The device already has an account with this name. Choose a different name.")
		case 23:
			return lang.L("The device cannot store more accounts. Remove an account you do not need and try again.")
		}
	}
	return lang.L("The device refused the request {{.Method}} (return value {{.Code}}).", map[string]any{"Method": e.Method, "Code": e.ReturnValue})
}

// describeISO covers the limits of the ISO image built from a folder.
func describeISO(err error) (string, bool) {
	switch {
	case errors.Is(err, mkiso.ErrEmpty):
		return lang.L("The folder contains no files. Choose a folder with at least one file."), true
	case errors.Is(err, mkiso.ErrFileTooLarge):
		return lang.L("A file in the folder is 4 GiB or larger and does not fit on the image."), true
	case errors.Is(err, mkiso.ErrNameTooLong):
		return lang.L("A file or folder name is too long for the image (at most 103 characters). Rename it."), true
	case errors.Is(err, mkiso.ErrNameConflict):
		return lang.L("Two names in the folder are the same on the image (upper and lower case count as equal). Rename one of them."), true
	case errors.Is(err, mkiso.ErrTooManyDirs):
		return lang.L("The folder contains too many subfolders for the image (at most 65535)."), true
	case errors.Is(err, mkiso.ErrDirTooLarge):
		return lang.L("A folder holds too many entries for the image. Split the files into several folders."), true
	case errors.Is(err, mkiso.ErrFileChanged):
		return lang.L("A file changed while the image was being built. Try again when nothing writes to the folder."), true
	case errors.Is(err, mkiso.ErrTooLarge):
		return lang.L("The content of the folder is too large for the image."), true
	}
	return "", false
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
		return lang.L("{{.Host}} does not accept connections without TLS on port {{.Port}}. Many devices only allow TLS: enable “Use TLS” and connect again.", args), true
	case isErrno(err, errnoRefused) && port == amt.PortTLS:
		return lang.L("{{.Host}} does not accept TLS connections on port {{.Port}}. If TLS is not set up on the device, disable “Use TLS” and connect again.", args), true
	case isErrno(err, errnoRefused):
		return lang.L("{{.Host}} refused the connection on port {{.Port}}. Check that the address belongs to the AMT interface of the device.", args), true
	case errors.As(err, &rec): // from the fingerprint fetch, without a dial error around it
		return lang.L("The device does not speak TLS on this port. Disable “Use TLS” and connect again."), true
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
