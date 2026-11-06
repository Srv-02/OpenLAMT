package ui

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/session"
)

func TestDescribe(t *testing.T) {
	englishUI(t)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: strings.Repeat("ab", 32), Actual: strings.Repeat("cd", 32)}
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("open: %w", session.ErrAuth), "Authentication failed"},
		{mm, "device.example.com"},
		{amt.ErrIPv6Unsupported, "IPv6 addresses are not supported"},
		{&favorites.KeychainError{Err: errors.New("no secret service")}, "The keychain could not be used: no secret service"},
		{ErrInterval, "between 1 and 3600"},
		{errors.New("dial tcp: connection refused"), "Unexpected error: dial tcp: connection refused"},
		{userError("Host is required."), "Host is required."},
		{fmt.Errorf("power on: %w", fmt.Errorf("%w: 500 Internal Server Error", errors.New("wsman.Client post received"))), "The device rejected the request (500 Internal Server Error)."},
		{fmt.Errorf("get opt-in service: %w: IPS_OptInService", amt.ErrIncompleteResponse), "The device sent an incomplete answer."},
		{&kvm.RedirectionError{Stage: "auth", Status: 1}, "KVM login failed: wrong username or password."},
		{&kvm.RedirectionError{Stage: "start", Status: 1}, "The device refused the KVM session (status 1)."},
		{fmt.Errorf("read: %w", kvm.ErrRedirProtocol), "unexpected way"},
		{kvm.ErrTileTooLarge, "KVM picture could not be decoded"},
		{favorites.ErrExists, "already exists"},
		{fmt.Errorf("parse x: %w", &json.SyntaxError{}), "The favorites file is damaged"},
		{fmt.Errorf("write: %w", &fs.PathError{Op: "open", Path: "/home/example/favorites.json", Err: fs.ErrPermission}), "could not read or write /home/example/favorites.json"},
		{session.ErrStopped, "The connection to the device was closed."},
	}
	for _, c := range cases {
		if got := describe(c.err); !strings.Contains(got, c.want) {
			t.Errorf("describe(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestDescribeKVMErrors(t *testing.T) {
	englishUI(t)
	cases := map[error]string{
		&amt.ConsentError{Step: "SendOptInCode", ReturnValue: 2}:                    "The device did not accept the consent code. Check the six digits on the device screen and try again.",
		&amt.ConsentError{Step: "StartOptIn", ReturnValue: 2}:                       "The device could not start the consent request (return value 2). Try again.",
		&amt.KVMError{Step: "KVMRedirectionSAP.RequestStateChange", ReturnValue: 1}: "The device refused to turn on KVM (return value 1). KVM may be disabled in the AMT settings of the device.",
		amt.ErrConsentRequired:                               "The device requires user consent, but no consent code was entered.",
		fmt.Errorf("wrapped: %w", amt.ErrInvalidConsentCode): "The consent code must be six digits.",
		kvm.ErrClosed:                                        "KVM session closed.",
	}
	for err, want := range cases {
		if got := describe(err); got != want {
			t.Errorf("%v: %q", err, got)
		}
	}
}

// dialErr builds the error chain a failed WS-Man call produces: fmt wrap, *url.Error, *net.OpError.
func dialErr(port int, cause error) error {
	op := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: port},
		Err: &os.SyscallError{Syscall: "connect", Err: cause}}
	return fmt.Errorf("enumerate power state: %w", &url.Error{Op: "Post", URL: "http://192.0.2.10:16992/wsman", Err: op})
}

func TestDescribeNetworkErrors(t *testing.T) {
	englishUI(t)
	cases := []struct {
		err  error
		want string
	}{
		{dialErr(16992, syscall.ECONNREFUSED), `192.0.2.10 does not accept connections without TLS on port 16992. Many devices only allow TLS: turn on “Use TLS” and connect again.`},
		{dialErr(16993, syscall.ECONNREFUSED), `192.0.2.10 does not accept TLS connections on port 16993. If TLS is not set up on the device, turn off “Use TLS” and connect again.`},
		{dialErr(16992, syscall.EHOSTUNREACH), "192.0.2.10 cannot be reached from this computer."},
		{dialErr(16993, syscall.ECONNRESET), "192.0.2.10 closed the connection unexpectedly. Connect again."},
		{dialErr(16992, errnoRefused[len(errnoRefused)-1]), "does not accept connections without TLS"},
		{fmt.Errorf("kvm: %w", io.EOF), "The device closed the connection unexpectedly."},
		{tls.AlertError(40), "The TLS handshake with the device failed."},
		{fmt.Errorf("x: %w", &net.DNSError{Name: "device.example.com", Err: "no such host", IsNotFound: true}), "The name device.example.com could not be found."},
		{fmt.Errorf("fetch: %w", context.DeadlineExceeded), "The device did not answer in time."},
		{&net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 16993}, Err: os.ErrDeadlineExceeded}, "192.0.2.10 did not answer in time."},
		{tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "The device does not speak TLS on this port."},
		{errors.New("something odd"), "Unexpected error: something odd"},
	}
	for _, c := range cases {
		if got := describe(c.err); !strings.Contains(got, c.want) {
			t.Errorf("describe(%v)\n got %q\nwant %q", c.err, got, c.want)
		}
	}
}

// TestDescribeRealRefusedDial checks the error chain of a real dial against a closed port.
func TestDescribeRealRefusedDial(t *testing.T) {
	englishUI(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, err = net.Dial("tcp", addr)
	if err == nil {
		t.Skip("port was reused")
	}
	if got := describe(fmt.Errorf("enumerate power state: %w", err)); !strings.Contains(got, "127.0.0.1 refused the connection") {
		t.Fatalf("%q", got)
	}
}

// "Copy details" puts the technical error on the clipboard for a bug report and confirms with
// "Copied"; an error whose text is already the message has no details button.
func TestErrorDialogCopyDetails(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	w := a.NewWindow("test")
	w.Resize(fyne.NewSize(600, 400))
	err := fmt.Errorf("power on: %w", errors.New("dial tcp: connection refused"))
	showError(w, err)
	b := overlayButton(t, w, "Copy details")
	test.Tap(b)
	if got := a.Clipboard().Content(); got != err.Error() {
		t.Fatalf("clipboard %q", got)
	}
	if b.Text != "Copied" || !b.Disabled() {
		t.Fatalf("button %q disabled=%v", b.Text, b.Disabled())
	}
	test.Tap(overlayButton(t, w, lang.L("OK")))

	showError(w, userError("Host is required."))
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.Button); ok && x.Text == "Copy details" {
			t.Fatal("details button for a plain message")
		}
	})
}
