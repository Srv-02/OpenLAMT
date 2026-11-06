package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

type probeHarness struct {
	p       *statusProber
	keys    *keychain.Memory
	dev     *session.Fake
	results chan map[string]probeResult // one map per round of n favorites
	mu      sync.Mutex
	round   map[string]probeResult
	n       int
}

func newProbeHarness(t *testing.T, n int, interval time.Duration, connect func(*session.Fake) session.ConnectFunc) *probeHarness {
	t.Helper()
	h := &probeHarness{keys: keychain.NewMemory(), dev: &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}},
		results: make(chan map[string]probeResult, 16), round: map[string]probeResult{}, n: n}
	if connect == nil {
		connect = session.ConnectFake
	}
	h.p = newStatusProber(h.keys, connect(h.dev), func() time.Duration { return interval }, func(key string, r probeResult) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.round[key] = r
		if len(h.round) == h.n {
			h.results <- h.round
			h.round = map[string]probeResult{}
		}
	})
	t.Cleanup(func() { h.p.Stop(); <-h.p.Done() })
	return h
}

func (h *probeHarness) next(t *testing.T) map[string]probeResult {
	t.Helper()
	select {
	case r := <-h.results:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("no probe round")
	}
	return nil
}

func TestProberReadsOnlyPinnedFavoritesWithPassword(t *testing.T) {
	h := newProbeHarness(t, 4, time.Hour, nil)
	favs := []favorites.Favorite{
		{Host: "on.example.com", Username: "admin", UseTLS: true}, // password and pin
		{Host: "plain.example.com", Username: "admin"},            // password, no TLS: never probed
		{Host: "nopin.example.com", Username: "admin", UseTLS: true},
		{Host: "nopw.example.com", Username: "admin", UseTLS: true},
	}
	h.keys.Set("on.example.com", "admin", "secret-1")
	h.keys.SetFingerprint("on.example.com", "admin", fp)
	h.keys.Set("plain.example.com", "admin", "secret-2")
	h.keys.Set("nopin.example.com", "admin", "secret-3")
	h.keys.SetFingerprint("nopw.example.com", "admin", fp)
	h.p.SetFavorites(favs)
	h.p.Start()
	r := h.next(t)
	on := r[deviceKey("on.example.com", "admin")]
	if on.dot != widgets.DotOn || on.st.AMTVersion != "16.1.25" || !on.known() {
		t.Fatalf("on: %+v", on)
	}
	for _, host := range []string{"plain.example.com", "nopin.example.com", "nopw.example.com"} {
		if got := r[deviceKey(host, "admin")]; got.dot != widgets.DotUnknown || got.known() {
			t.Fatalf("%s: %+v", host, got)
		}
	}
	// Only the pinned TLS favorite connected: no unencrypted login, no trust on first use.
	cfgs := h.dev.Configs()
	if len(cfgs) != 1 || cfgs[0].Host != "on.example.com" {
		t.Fatalf("%d connects: %+v", len(cfgs), cfgs)
	}
	for _, c := range cfgs {
		if c.UseTLS && c.Fingerprint != fp {
			t.Fatalf("TLS probe without the pin: %+v", c)
		}
	}
	if h.dev.IdleClosed() != 1 {
		t.Fatalf("connections not closed after the probe: %d", h.dev.IdleClosed())
	}
}

// powerDot is the one mapping from power state to dot that the sidebar, the toolbar and the
// status bar share (issue #120): green on, red off, orange standby or hibernation, gray ring
// for unknown. A read with a state without a name counts as read but shows the ring.
func TestPowerDotMapping(t *testing.T) {
	cases := map[models.PowerState]widgets.DotState{
		models.PowerStateOn:         widgets.DotOn,
		models.PowerStateOffHard:    widgets.DotOff,
		models.PowerStateOffSoft:    widgets.DotOff,
		models.PowerStateSleepLight: widgets.DotAsleep,
		models.PowerStateSleepDeep:  widgets.DotAsleep,
		models.PowerStateHibernate:  widgets.DotAsleep,
		models.PowerState(0):        widgets.DotUnknown,
		models.PowerState(99):       widgets.DotUnknown,
	}
	for ps, want := range cases {
		if got := powerDot(true, ps); got != want {
			t.Errorf("%v: %v, want %v", ps, got, want)
		}
		if got := powerDot(false, ps); got != widgets.DotUnknown {
			t.Errorf("%v not read: %v, want unknown", ps, got)
		}
		if r := readProbe(amt.Status{PowerState: ps}); r.dot != want || !r.known() {
			t.Errorf("%v probe: %+v", ps, r)
		}
	}
	if (probeResult{dot: widgets.DotUnknown}).known() {
		t.Error("neutral result counts as read")
	}
}

// pinChecking is ConnectFake plus the pin check of amt.Connect: a pin other than the
// device's fingerprint fails with a mismatch.
func pinChecking(f *session.Fake) session.ConnectFunc {
	inner := session.ConnectFake(f)
	return func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
		if cfg.UseTLS && cfg.Fingerprint != "" && cfg.Fingerprint != f.FP {
			return nil, &amt.FingerprintMismatchError{Host: cfg.Host, Expected: cfg.Fingerprint, Actual: f.FP}
		}
		return inner(ctx, cfg, prompt)
	}
}

func TestProberNeutralOnErrorsAndOffState(t *testing.T) {
	h := newProbeHarness(t, 1, time.Hour, pinChecking)
	f := favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true}
	key := deviceKey(f.Host, f.Username)
	h.keys.Set(f.Host, f.Username, "secret-1")
	h.keys.SetFingerprint(f.Host, f.Username, fp)
	h.dev.St.PowerState = models.PowerStateOffSoft
	h.p.SetFavorites([]favorites.Favorite{f})
	h.p.Start()
	if r := h.next(t)[key]; r.dot != widgets.DotOff || !r.known() {
		t.Fatalf("off: %+v", r)
	}
	h.dev.SetStatusErr(errors.New("unreachable"))
	h.p.Wake()
	if r := h.next(t)[key]; r.dot != widgets.DotUnknown {
		t.Fatalf("error: %+v", r)
	}
	// A changed certificate is refused without a dialog: neutral, and the pin stays.
	h.dev.SetStatusErr(nil)
	h.keys.SetFingerprint(f.Host, f.Username, strings.Repeat("cd", 32))
	h.p.Wake()
	if r := h.next(t)[key]; r.dot != widgets.DotUnknown {
		t.Fatalf("mismatch: %+v", r)
	}
	if pin, _ := h.keys.GetFingerprint(f.Host, f.Username); pin != strings.Repeat("cd", 32) {
		t.Fatal("pin changed by the probe")
	}
}

func TestProberDoesNotRetryRejectedPassword(t *testing.T) {
	h := newProbeHarness(t, 1, time.Hour, nil)
	f := favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true}
	h.keys.SetFingerprint(f.Host, f.Username, fp)
	h.keys.Set(f.Host, f.Username, "wrong-1")
	h.dev.SetStatusErr(errors.New("wsman.Client post received: 401 Unauthorized"))
	h.p.SetFavorites([]favorites.Favorite{f})
	h.p.Start()
	h.next(t)
	h.p.Wake()
	h.next(t)
	if n := h.dev.Calls(); n != 1 {
		t.Fatalf("rejected password tried %d times", n)
	}
	// A new password is tried again.
	h.dev.SetStatusErr(nil)
	h.keys.Set(f.Host, f.Username, "secret-1")
	h.p.Wake()
	if r := h.next(t)[deviceKey(f.Host, f.Username)]; !r.known() {
		t.Fatalf("new password: %+v", r)
	}
	if n := h.dev.Calls(); n != 2 {
		t.Fatalf("%d status calls", n)
	}
}

func TestProberLimitsParallelProbesAndRepeats(t *testing.T) {
	var cur, peak atomic.Int32
	slow := func(f *session.Fake) session.ConnectFunc {
		inner := session.ConnectFake(f)
		return func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
			n := cur.Add(1)
			defer cur.Add(-1)
			for {
				m := peak.Load()
				if n <= m || peak.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			return inner(ctx, cfg, prompt)
		}
	}
	h := newProbeHarness(t, 6, 10*time.Millisecond, slow)
	var favs []favorites.Favorite
	for _, host := range []string{"a", "b", "c", "d", "e", "f"} {
		f := favorites.Favorite{Host: host + ".example.com", Username: "admin", UseTLS: true}
		h.keys.Set(f.Host, f.Username, "secret-1")
		h.keys.SetFingerprint(f.Host, f.Username, fp)
		favs = append(favs, f)
	}
	h.p.SetFavorites(favs)
	h.p.Start()
	h.next(t)
	h.next(t) // the interval repeats the round
	if p := peak.Load(); p > probeLimit || p < 2 {
		t.Fatalf("peak %d parallel probes, limit %d", p, probeLimit)
	}
}

func TestProberStopsAndReportsNothingAfterStop(t *testing.T) {
	gate := make(chan struct{})
	blocking := func(f *session.Fake) session.ConnectFunc {
		inner := session.ConnectFake(f)
		return func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return inner(ctx, cfg, prompt)
		}
	}
	h := newProbeHarness(t, 1, time.Hour, blocking)
	f := favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true}
	h.keys.Set(f.Host, f.Username, "secret-1")
	h.keys.SetFingerprint(f.Host, f.Username, fp)
	h.p.SetFavorites([]favorites.Favorite{f})
	h.p.Start()
	time.Sleep(20 * time.Millisecond) // the probe waits at the gate
	h.p.Stop()
	select {
	case <-h.p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("prober did not stop")
	}
	close(gate)
	select {
	case r := <-h.results:
		t.Fatalf("result after stop: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	h.p.Start() // no effect after Stop
}

func TestProberNeverLogsInWithoutTLS(t *testing.T) {
	h := newProbeHarness(t, 1, time.Hour, nil)
	f := favorites.Favorite{Host: "plain.example.com", Username: "admin"}
	h.keys.Set(f.Host, f.Username, "secret-1")
	h.p.SetFavorites([]favorites.Favorite{f})
	h.p.Start()
	if r := h.next(t)[deviceKey(f.Host, f.Username)]; r.dot != widgets.DotUnknown || r.known() {
		t.Fatalf("plain: %+v", r)
	}
	if n := len(h.dev.Configs()); n != 0 || h.dev.Calls() != 0 {
		t.Fatalf("favorite without TLS was probed: %d connects, %d status calls", n, h.dev.Calls())
	}
}

func TestProberSkipsFavoritesWithOpenWindow(t *testing.T) {
	h := newProbeHarness(t, 1, time.Hour, nil)
	favs := []favorites.Favorite{
		{Host: "open.example.com", Username: "admin", UseTLS: true},
		{Host: "other.example.com", Username: "admin", UseTLS: true},
	}
	for _, f := range favs {
		h.keys.Set(f.Host, f.Username, "secret-1")
		h.keys.SetFingerprint(f.Host, f.Username, fp)
	}
	kOpen := deviceKey("open.example.com", "admin")
	h.p.SetSkip(kOpen, true)
	h.p.round(favs) // synchronous; the started loop is not needed
	r := h.next(t)
	if _, ok := r[deviceKey("other.example.com", "admin")]; !ok {
		t.Fatalf("other favorite not probed: %v", r)
	}
	if cfgs := h.dev.Configs(); len(cfgs) != 1 || cfgs[0].Host != "other.example.com" {
		t.Fatalf("connects: %+v", cfgs)
	}
	h.p.SetSkip(kOpen, false)
	h.p.round(favs[:1])
	if r := h.next(t); r[kOpen].dot != widgets.DotOn {
		t.Fatalf("not probed after the window closed: %v", r)
	}
}

// TestProberReportsUnreadableAtOnce: a favorite the prober cannot read (no TLS, no password, no
// pin) gets its neutral result in the first round right away, also when every probe slot is
// taken by devices that do not answer yet. The status row of the detail card stays empty until
// a result is there, so it would otherwise stay empty instead of showing "Unknown".
func TestProberReportsUnreadableAtOnce(t *testing.T) {
	gate := make(chan struct{})
	blocking := func(f *session.Fake) session.ConnectFunc {
		inner := session.ConnectFake(f)
		return func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return inner(ctx, cfg, prompt)
		}
	}
	reported := make(chan string, 16)
	h := newProbeHarness(t, 100, time.Hour, blocking)
	report := h.p.report
	h.p.report = func(key string, r probeResult) { report(key, r); reported <- key }
	var favs []favorites.Favorite
	for _, host := range []string{"a", "b", "c"} { // more slow devices than probe slots
		f := favorites.Favorite{Host: host + ".example.com", Username: "admin", UseTLS: true}
		h.keys.Set(f.Host, f.Username, "secret-1")
		h.keys.SetFingerprint(f.Host, f.Username, fp)
		favs = append(favs, f)
	}
	favs = append(favs, favorites.Favorite{Host: "plain.example.com", Username: "admin"},
		favorites.Favorite{Host: "nopw.example.com", Username: "admin", UseTLS: true})
	h.p.SetFavorites(favs)
	h.p.Start()
	want := map[string]bool{deviceKey("plain.example.com", "admin"): true, deviceKey("nopw.example.com", "admin"): true}
	for len(want) > 0 {
		select {
		case k := <-reported:
			if !want[k] {
				t.Fatalf("%s reported before the unreadable favorites", k)
			}
			delete(want, k)
		case <-time.After(3 * time.Second):
			t.Fatalf("unreadable favorites not reported while the probe slots are busy: %v", want)
		}
	}
	close(gate)
}
