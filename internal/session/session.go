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
)

// Device is the part of *amt.Device that the GUI uses.
type Device interface {
	Status() (amt.Status, error)
	Power(amt.PowerAction) error
	PowerStates() ([]power.PowerState, error)
	Fingerprint() string
	OpenKVM(ctx context.Context, quality kvm.Quality, prompt amt.ConsentPrompt) (*kvm.Client, error)
	OpenIDER(ctx context.Context, img ider.Image, start ider.StartMode, prompt amt.ConsentPrompt) (*ider.Client, error)
	NextBootFromISO() error
	ClearISOBoot() error
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
	cfg amt.Config
	dev Device
	mu  sync.Mutex
}

// Open connects and reads the status once to verify the password. The returned Config
// carries the confirmed fingerprint.
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

// classify maps the WS-Man client's 401 error to ErrAuth; everything else passes through.
func classify(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "wsman.Client post received: 401") {
		return ErrAuth
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
// user cancelled). Devices without idle connections ignore it.
func (s *Session) Close() {
	if c, ok := s.dev.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// Config returns host, user, password (in memory) and the confirmed fingerprint.
func (s *Session) Config() amt.Config { return s.cfg }

func (s *Session) Fingerprint() string { return s.cfg.Fingerprint }

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
