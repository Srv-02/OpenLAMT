package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/kvm"
)

// Fake implements Device for tests in this package and internal/ui.
type Fake struct {
	St        amt.Status
	StatusErr error
	PowerErr  error
	FP        string
	PowerGate chan struct{}
	KVMGate   chan struct{} // OpenKVM waits for it after a successful dial and returns the session even if ctx was cancelled meanwhile

	// States is returned by PowerStates unless StatesErr is set; change it with SetPowerStates.
	States    []power.PowerState
	StatesErr error

	// KVM: OpenKVM returns KVMErr if set; otherwise, with Consent set, it asks the prompt and
	// accepts only ConsentCode (another code is a ConsentError on SendOptInCode); then it dials
	// KVMAddr (a kvmtest server) with KVMUser and KVMPassword, or fails with ErrConsentRequired
	// style errors as *amt.Device would.
	KVMErr      error
	Consent     bool
	ConsentCode string
	KVMAddr     string
	KVMUser     string
	KVMPassword string

	// IDE-R: OpenIDER returns IDERErr if set (the caller keeps the image); otherwise it applies the
	// Consent rules like OpenKVM and dials IDERAddr (an idertest server). NextBootFromISO and
	// ClearISOBoot return NextBootErr and ClearISOBootErr and are counted (see ISOCalls).
	IDERErr         error
	IDERAddr        string
	IDERUser        string
	IDERPassword    string
	NextBootErr     error
	ClearISOBootErr error

	SOL FakeSOL // Serial-over-LAN, see fake_sol.go

	mu           sync.Mutex
	iderStarts   []ider.StartMode
	nextBoots    int
	clearISOBoot int
	statusCalls  int
	statesCalls  int
	powered      []amt.PowerAction
	configs      []amt.Config
	qualities    []kvm.Quality
	busy         atomic.Int32
	maxBusy      atomic.Int32
	powerStarted atomic.Bool
	idleClosed   atomic.Int32
}

func (f *Fake) enter() {
	n := f.busy.Add(1)
	for {
		m := f.maxBusy.Load()
		if n <= m || f.maxBusy.CompareAndSwap(m, n) {
			return
		}
	}
}
func (f *Fake) leave() { f.busy.Add(-1) }

func (f *Fake) Status() (amt.Status, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	return f.St, f.StatusErr
}
func (f *Fake) Power(a amt.PowerAction) error {
	f.enter()
	defer f.leave()
	f.powerStarted.Store(true)
	if f.PowerGate != nil {
		<-f.PowerGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.powered = append(f.powered, a)
	return f.PowerErr
}
func (f *Fake) PowerStates() ([]power.PowerState, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statesCalls++
	if f.StatesErr != nil {
		return nil, f.StatesErr
	}
	return append([]power.PowerState(nil), f.States...), nil
}

// SetPowerStates changes the result of later PowerStates calls.
func (f *Fake) SetPowerStates(states []power.PowerState, err error) {
	f.mu.Lock()
	f.States, f.StatesErr = states, err
	f.mu.Unlock()
}

// StatesCalls is the number of PowerStates calls.
func (f *Fake) StatesCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.statesCalls }

func (f *Fake) Fingerprint() string { return f.FP }

// CloseIdleConnections counts the calls (see IdleClosed).
func (f *Fake) CloseIdleConnections() { f.idleClosed.Add(1) }

// IdleClosed is the number of CloseIdleConnections calls.
func (f *Fake) IdleClosed() int { return int(f.idleClosed.Load()) }

// OpenKVM mirrors the steps of (*amt.Device).OpenKVM against the fake server (see the field comments).
func (f *Fake) OpenKVM(ctx context.Context, quality kvm.Quality, prompt amt.ConsentPrompt) (*kvm.Client, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	f.qualities = append(f.qualities, quality)
	f.mu.Unlock()
	if f.KVMErr != nil {
		return nil, f.KVMErr
	}
	if f.Consent {
		if prompt == nil {
			return nil, amt.ErrConsentRequired
		}
		code, err := prompt.ConsentCode()
		if err != nil {
			return nil, fmt.Errorf("read consent code: %w", err)
		}
		if len(strings.TrimSpace(code)) != 6 {
			return nil, amt.ErrInvalidConsentCode
		}
		if strings.TrimSpace(code) != f.ConsentCode {
			return nil, &amt.ConsentError{Step: "SendOptInCode", ReturnValue: 2}
		}
	}
	f.mu.Lock()
	major := amt.MajorVersion(f.St.AMTVersion)
	f.mu.Unlock()
	c, err := kvm.Dial(ctx, kvm.Config{Addr: f.KVMAddr, Username: f.KVMUser, Password: f.KVMPassword, Quality: quality, AMTMajor: major})
	if err == nil && f.KVMGate != nil {
		<-f.KVMGate // the session is open; a cancel arriving now comes too late
	}
	return c, err
}

// Qualities returns the quality of every OpenKVM call.
func (f *Fake) Qualities() []kvm.Quality {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kvm.Quality(nil), f.qualities...)
}

func (f *Fake) Calls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.statusCalls }
func (f *Fake) Powered() []amt.PowerAction {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]amt.PowerAction(nil), f.powered...)
}
func (f *Fake) PowerStarted() bool { return f.powerStarted.Load() }
func (f *Fake) MaxBusy() int       { return int(f.maxBusy.Load()) }
func (f *Fake) Configs() []amt.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]amt.Config(nil), f.configs...)
}
func (f *Fake) SetStatusErr(err error) { f.mu.Lock(); f.StatusErr = err; f.mu.Unlock() }

// SetStatus changes the result of later Status calls.
func (f *Fake) SetStatus(st amt.Status) { f.mu.Lock(); f.St = st; f.mu.Unlock() }

// ConnectFake records cfg; TLS without pin asks prompt with f.FP.
func ConnectFake(f *Fake) ConnectFunc {
	return func(_ context.Context, cfg amt.Config, prompt amt.TrustPrompt) (Device, error) {
		f.mu.Lock()
		f.configs = append(f.configs, cfg)
		f.mu.Unlock()
		if cfg.UseTLS && strings.TrimSpace(cfg.Fingerprint) == "" {
			if prompt == nil {
				return nil, amt.ErrTrustDeclined
			}
			ok, err := prompt.ConfirmFingerprint(cfg.Host, f.FP)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, amt.ErrTrustDeclined
			}
		}
		return f, nil
	}
}

// OpenIDER mirrors (*amt.Device).OpenIDER against the fake server (see the IDE-R field comments).
func (f *Fake) OpenIDER(ctx context.Context, img ider.Image, start ider.StartMode, prompt amt.ConsentPrompt) (*ider.Client, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	f.iderStarts = append(f.iderStarts, start)
	f.mu.Unlock()
	if f.IDERErr != nil {
		return nil, f.IDERErr
	}
	if f.Consent {
		if prompt == nil {
			return nil, amt.ErrConsentRequired
		}
		code, err := prompt.ConsentCode()
		if err != nil {
			return nil, fmt.Errorf("read consent code: %w", err)
		}
		if len(strings.TrimSpace(code)) != 6 {
			return nil, amt.ErrInvalidConsentCode
		}
		if strings.TrimSpace(code) != f.ConsentCode {
			return nil, &amt.ConsentError{Step: "SendOptInCode", ReturnValue: 2}
		}
	}
	return ider.Dial(ctx, ider.Config{Addr: f.IDERAddr, Username: f.IDERUser, Password: f.IDERPassword, Image: img, Start: start})
}

func (f *Fake) NextBootFromISO() error {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextBoots++
	return f.NextBootErr
}

func (f *Fake) ClearISOBoot() error {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearISOBoot++
	return f.ClearISOBootErr
}

// IDERStarts returns the start mode of every OpenIDER call.
func (f *Fake) IDERStarts() []ider.StartMode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ider.StartMode(nil), f.iderStarts...)
}

// ISOCalls returns the number of NextBootFromISO and ClearISOBoot calls.
func (f *Fake) ISOCalls() (nextBoot, clear int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextBoots, f.clearISOBoot
}
