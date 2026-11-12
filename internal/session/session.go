// Package session owns one connected AMT device per window: login check, serialized
// WS-Man access and periodic status polling. It has no Fyne dependency.
package session

import (
	"context"
	"errors"
	"strings"
	"sync"

	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/sol"
)

// Device is the part of *amt.Device that the GUI uses.
type Device interface {
	Status() (amt.Status, error)
	HardwareInfo() (amt.HardwareInfo, error)
	EventLog() ([]amt.EventLogEntry, error)
	AuditLog() ([]amt.AuditLogEntry, error)
	ConfigDevice
	Power(amt.PowerAction) error
	PowerStates() ([]power.PowerState, error)
	Fingerprint() string
	OpenKVM(ctx context.Context, quality kvm.Quality, prompt amt.ConsentPrompt) (*kvm.Client, error)
	OpenIDER(ctx context.Context, img ider.Image, start ider.StartMode, prompt amt.ConsentPrompt) (*ider.Client, error)
	NextBootFromISO() error
	ClearISOBoot() error
	OpenSOL(ctx context.Context, prompt amt.ConsentPrompt) (*sol.Client, error)
}

var _ Device = (*amt.Device)(nil)

type ConnectFunc func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (Device, error)

// AMTConnect adapts amt.Connect and never returns a typed nil in the interface.
func AMTConnect(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (Device, error) {
	d, err := amt.Connect(ctx, cfg, prompt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ErrAuth replaces the library's 401 error, whose text contains the response body.
var ErrAuth = errors.New("authentication failed: wrong username or password")

// Session owns one connected device. All WS-Man calls go through its mutex.
type Session struct {
	cfgMu sync.Mutex // guards cfg: account and certificate changes replace login and pin
	cfg   amt.Config
	dev   Device
	mu    sync.Mutex
}

// Open connects and reads the status once to verify the password. The returned Config
// carries the confirmed fingerprint. The only requirement is that Status returns without an
// error: a part the account may not read is reported in Status.Denied, and AMT answers such a
// refusal only after the Digest login succeeded, while wrong credentials give 401 (ErrAuth).
func Open(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt, connect ConnectFunc) (*Session, amt.Status, error) {
	dev, err := connect(ctx, cfg, prompt)
	if err != nil {
		return nil, amt.Status{}, err
	}
	cfg.Fingerprint = dev.Fingerprint()
	s := &Session{cfg: cfg, dev: dev}
	st, err := s.Status()
	if err != nil {
		s.Close() // the device is discarded: do not leave its kept-alive sockets behind
		return nil, amt.Status{}, err
	}
	return s, st, nil
}

// classify maps the WS-Man client's 401 error to ErrAuth and a missing permission to
// amt.ErrAccessDenied; everything else passes through.
// *amt.CredentialsUncertainError and *amt.CertificateUncertainError keep their type; only their
// cause is classified.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var ue *amt.CredentialsUncertainError
	if errors.As(err, &ue) {
		c := *ue
		c.Err = classify(ue.Err)
		return &c
	}
	var ce *amt.CertificateUncertainError
	if errors.As(err, &ce) {
		c := *ce
		c.Err = classify(ce.Err)
		return &c
	}
	msg := err.Error()
	if strings.Contains(msg, "wsman.Client post received: 401") {
		return ErrAuth
	}
	// A missing permission (SOAP fault AccessDenied or HTTP 403) is not a wrong password. Which
	// faults Status tolerates is decided there (amt.coreClassDenied), not here.
	if amt.IsAccessDenied(err) {
		if errors.Is(err, amt.ErrAccessDenied) {
			return err // already wrapped by amt, keeps its operation text
		}
		return amt.ErrAccessDenied // like ErrAuth: the fault text is replaced
	}
	// The library appends the HTTP body to the status line; keep only the status line.
	if strings.Contains(msg, "wsman.Client post received") {
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			return errors.New(msg[:i])
		}
	}
	return err
}

// Close releases idle connections of a session that is no longer used (e.g. an attempt the
// user cancelled). Devices without idle connections ignore it. It waits for a running device
// call, so call it off the Fyne thread when the session may be busy.
func (s *Session) Close() {
	s.mu.Lock() // an account or certificate change may be replacing the device's transport
	defer s.mu.Unlock()
	if c, ok := s.dev.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// Config returns host, user, password (in memory) and the confirmed fingerprint. After an
// accepted change of the session's own login or of the TLS certificate it returns the new values.
func (s *Session) Config() amt.Config {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.cfg
}

func (s *Session) Fingerprint() string { return s.Config().Fingerprint }

func (s *Session) Status() (amt.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.dev.Status()
	return st, classify(err)
}

func (s *Session) Power(a amt.PowerAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.Power(a))
}

func (s *Session) PowerStates() ([]power.PowerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.dev.PowerStates()
	return st, classify(err)
}

// OpenKVM enables KVM, runs the user consent (prompt may block on a dialog) and dials the
// redirection session. The mutex is held throughout, so no status read or power action runs
// in parallel to the WS-Man steps; the returned client has its own socket and is used without
// the session afterwards.
func (s *Session) OpenKVM(ctx context.Context, quality kvm.Quality, prompt amt.ConsentPrompt) (*kvm.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.dev.OpenKVM(ctx, quality, prompt)
	if err != nil {
		return nil, classify(err)
	}
	return c, nil
}

// OpenIDER mounts img via IDE-R like OpenKVM opens KVM: the mutex is held during the WS-Man
// steps and the consent prompt; the returned client has its own socket and is used without the
// session afterwards. Image ownership passes to the client only on success; on error the caller
// still owns img.
func (s *Session) OpenIDER(ctx context.Context, img ider.Image, start ider.StartMode, prompt amt.ConsentPrompt) (*ider.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.dev.OpenIDER(ctx, img, start, prompt)
	if err != nil {
		return nil, classify(err)
	}
	return c, nil
}

// NextBootFromISO sets the IDE-R boot option without a power command (see amt.Device.NextBootFromISO).
func (s *Session) NextBootFromISO() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.NextBootFromISO())
}

// ClearISOBoot switches the IDE-R boot option off again (see amt.Device.ClearISOBoot).
func (s *Session) ClearISOBoot() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return classify(s.dev.ClearISOBoot())
}
