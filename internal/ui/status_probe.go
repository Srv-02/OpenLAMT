package ui

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"time"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

const (
	// probeLimit is how many favorites are probed at the same time.
	probeLimit = 2
	// probeTimeout bounds connect and TLS check of one probe (amt.ConnectTimeout applies inside).
	probeTimeout = 20 * time.Second
)

// probeResult is what the sidebar shows for one favorite.
type probeResult struct {
	dot  widgets.DotState // powerDot of st, or DotUnknown (neutral)
	st   amt.Status       // valid when read
	read bool             // the device was read; false is the neutral state
}

// readProbe is the result of a successful status read.
func readProbe(st amt.Status) probeResult {
	return probeResult{dot: powerDot(true, st.PowerState), st: st, read: true}
}

// known reports whether the probe read the device.
func (r probeResult) known() bool { return r.read }

// powerDot is the dot of a power state, shared by the sidebar, the toolbar and the status bar
// of the device window: green on, red off, orange standby or hibernation, the gray ring when
// the state was not read (known false) or has no name.
func powerDot(known bool, ps models.PowerState) widgets.DotState {
	switch {
	case !known:
		return widgets.DotUnknown
	case ps == models.PowerStateOn:
		return widgets.DotOn
	case isOff(ps):
		return widgets.DotOff
	case isAsleep(ps):
		return widgets.DotAsleep
	}
	return widgets.DotUnknown
}

// statusProber reads the power state of every favorite with a saved password in the
// background, while the manager window is open, for the status dots of the sidebar. Only
// favorites with TLS and an already pinned fingerprint are probed: without TLS the digest
// login would go over the network unencrypted every few seconds without the user doing
// anything, and without a pin there is no trust on first use in the background. It never asks
// anything; every failure (no TLS, no password, no pin, wrong password, changed certificate,
// unreachable) yields the neutral state. A password the device rejected is not tried again
// until it changes, so a stale password cannot lock the AMT account. At most probeLimit
// probes run at once; each closes its connections afterwards. Favorites whose device window is
// open are skipped (SetSkip): that window's poller already reads the device and reports the
// dot, so the probe would only take a second of the device's few connection slots.
type statusProber struct {
	keys     keychain.Store
	connect  session.ConnectFunc
	interval func() time.Duration // read before every pause
	report   func(key string, r probeResult)

	mu      sync.Mutex
	favs    []favorites.Favorite
	authBad map[string][sha256.Size]byte // deviceKey → hash of the rejected password
	skip    map[string]bool              // deviceKey → device window open, not probed

	wake    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

func newStatusProber(keys keychain.Store, connect session.ConnectFunc, interval func() time.Duration, report func(string, probeResult)) *statusProber {
	ctx, cancel := context.WithCancel(context.Background())
	return &statusProber{keys: keys, connect: connect, interval: interval, report: report,
		authBad: map[string][sha256.Size]byte{}, skip: map[string]bool{}, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// Start begins the first round right away. Calling it again has no effect.
func (p *statusProber) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || p.ctx.Err() != nil {
		return
	}
	p.started = true
	go p.loop()
}

// Stop ends the background work; running probes are cancelled and report nothing more.
// Done is closed once the loop has returned.
func (p *statusProber) Stop() {
	p.cancel()
	p.mu.Lock()
	started := p.started
	p.started = true // a later Start must not run
	p.mu.Unlock()
	if !started {
		close(p.done)
	}
}

// Done is closed when the prober has stopped.
func (p *statusProber) Done() <-chan struct{} { return p.done }

// SetFavorites replaces the probed list and starts a new round soon.
func (p *statusProber) SetFavorites(list []favorites.Favorite) {
	p.mu.Lock()
	p.favs = append([]favorites.Favorite(nil), list...)
	p.mu.Unlock()
	p.Wake()
}

// SetSkip marks the favorite key as served by its open device window (on) or hands it back
// to the probe (off). A probe of key that has not started yet is dropped.
func (p *statusProber) SetSkip(key string, on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		p.skip[key] = true
	} else {
		delete(p.skip, key)
	}
}

func (p *statusProber) skipped(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.skip[key]
}

// Wake ends the current pause, e.g. after the interval changed.
func (p *statusProber) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *statusProber) loop() {
	defer close(p.done)
	for {
		select { // a wake requested before this round is served by it
		case <-p.wake:
		default:
		}
		p.mu.Lock()
		favs := p.favs
		p.mu.Unlock()
		p.round(favs)
		t := time.NewTimer(p.interval())
		select {
		case <-p.ctx.Done():
			t.Stop()
			return
		case <-p.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// round probes favs with at most probeLimit at once and returns when all are done. Favorites
// that cannot be read (readable) get their neutral result first, before any probe, so they do
// not wait for a probe slot behind slow or unreachable devices: the status row of the detail
// card stays empty until a result is there and shows "Unknown" only from such a result.
func (p *statusProber) round(favs []favorites.Favorite) {
	type job struct {
		key string
		cfg amt.Config
	}
	var jobs []job
	for _, f := range favs {
		key := deviceKey(f.Host, f.Username)
		if p.skipped(key) {
			continue
		}
		cfg, ok := p.readable(f)
		if p.ctx.Err() != nil {
			return
		}
		if !ok {
			p.report(key, probeResult{dot: widgets.DotUnknown})
			continue
		}
		jobs = append(jobs, job{key, cfg})
	}
	sem := make(chan struct{}, probeLimit)
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, j := range jobs {
		key, cfg := j.key, j.cfg
		select {
		case sem <- struct{}{}:
		case <-p.ctx.Done():
			return
		}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if p.skipped(key) { // the window opened while this probe waited for a slot
				return
			}
			r := p.probe(cfg)
			if p.ctx.Err() == nil {
				p.report(key, r)
			}
		}()
	}
}

// refuseTrust answers every trust question with no: the prober never pins a certificate.
type refuseTrust struct{}

func (refuseTrust) ConfirmFingerprint(string, string) (bool, error) { return false, nil }

// readable returns the login of f when the prober may read it: TLS, a saved password the
// device has not rejected, a pinned fingerprint. Only keychain lookups, no network.
func (p *statusProber) readable(f favorites.Favorite) (amt.Config, bool) {
	if !f.UseTLS {
		return amt.Config{}, false // no unencrypted login in the background
	}
	key := deviceKey(f.Host, f.Username)
	pw, err := p.keys.Get(f.Host, f.Username)
	if err != nil || pw == "" {
		return amt.Config{}, false
	}
	sum := sha256.Sum256([]byte(pw))
	p.mu.Lock()
	bad, rejected := p.authBad[key]
	p.mu.Unlock()
	if rejected && bad == sum {
		return amt.Config{}, false
	}
	pin, err := p.keys.GetFingerprint(f.Host, f.Username)
	if err != nil || strings.TrimSpace(pin) == "" {
		return amt.Config{}, false
	}
	return amt.Config{Host: f.Host, Username: f.Username, Password: pw, UseTLS: true, Fingerprint: pin}, true
}

// probe reads the status of the readable login cfg once; every failure is the neutral state.
func (p *statusProber) probe(cfg amt.Config) probeResult {
	neutral := probeResult{dot: widgets.DotUnknown}
	key := deviceKey(cfg.Host, cfg.Username)
	sum := sha256.Sum256([]byte(cfg.Password))
	ctx, cancel := context.WithTimeout(p.ctx, probeTimeout)
	defer cancel()
	s, st, err := session.Open(ctx, cfg, refuseTrust{}, p.connect)
	if err != nil {
		if errors.Is(err, session.ErrAuth) {
			p.mu.Lock()
			p.authBad[key] = sum
			p.mu.Unlock()
		}
		return neutral
	}
	s.Close() // the probe is done: do not keep one of the device's few connection slots
	return readProbe(st)
}
