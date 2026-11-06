package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
)

const fp = "abababababababababababababababababababababababababababababababab"

type opened struct {
	s    *session.Session
	st   amt.Status
	name string
}

type askArgs struct {
	host, user string
	offerSave  bool
}

type harness struct {
	c              *connector
	mgr            *favorites.Manager
	keys           *keychain.Memory
	dev            *session.Fake
	opened         chan opened
	errs           chan error
	asked          chan askArgs
	trusted        chan string
	done           chan string
	trustAnswer    bool
	passwordAnswer func() (string, bool, bool)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	englishUI(t)
	test.NewTempApp(t)
	win := test.NewTempWindow(t, widget.NewLabel(""))
	h := &harness{keys: keychain.NewMemory(), dev: &session.Fake{FP: fp, St: amt.Status{AMTVersion: "16.1.25"}},
		opened: make(chan opened, 4), errs: make(chan error, 8), asked: make(chan askArgs, 4), trusted: make(chan string, 4),
		done: make(chan string, 8), trustAnswer: true}
	h.mgr = favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), h.keys)
	h.c = newConnector(win, h.mgr, h.keys, session.ConnectFake(h.dev))
	h.c.onOpened = func(s *session.Session, st amt.Status, name string) { h.opened <- opened{s, st, name} }
	h.c.onError = func(err error) { h.errs <- err }
	h.c.onDone = func(host, _ string) { h.done <- host }
	h.c.askPassword = func(host, user string, offerSave bool, answer func(string, bool, bool)) {
		h.asked <- askArgs{host, user, offerSave}
		if h.passwordAnswer != nil {
			answer(h.passwordAnswer())
		}
	}
	h.c.askTrust = func(_, got string, answer func(bool)) { h.trusted <- got; answer(h.trustAnswer) }
	return h
}

func (h *harness) waitOpened(t *testing.T) opened {
	t.Helper()
	select {
	case o := <-h.opened:
		return o
	case err := <-h.errs:
		t.Fatalf("error instead of session: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("no session opened")
	}
	return opened{}
}

func (h *harness) waitError(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.errs:
		return err
	case o := <-h.opened:
		t.Fatalf("session %q instead of error", o.name)
	case <-time.After(2 * time.Second):
		t.Fatal("no error")
	}
	return nil
}

func (h *harness) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatal("onDone not called")
	}
}

func (h *harness) expectNothing(t *testing.T) {
	t.Helper()
	select {
	case o := <-h.opened:
		t.Fatalf("unexpected session %q", o.name)
	case err := <-h.errs:
		t.Fatalf("unexpected error %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func favRequest() connectRequest {
	return connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true}, name: "Büro"}
}

// addFavorite stores the favorite favRequest connects to.
func (h *harness) addFavorite(t *testing.T) {
	t.Helper()
	if err := h.mgr.Upsert(favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectFavoritePinsFingerprintOnFirstUse(t *testing.T) {
	h := newHarness(t)
	h.addFavorite(t)
	h.c.start(favRequest())
	o := h.waitOpened(t)
	h.waitDone(t)
	if got := <-h.trusted; got != fp {
		t.Fatalf("trust dialog got %q", got)
	}
	if o.name != "Büro" || o.st.AMTVersion != "16.1.25" || o.s.Config().Password != "secret-1" || o.s.Fingerprint() != fp {
		t.Fatalf("%+v", o)
	}
	if _, err := h.keys.Get("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("typed password stored without being asked to")
	}
	if got, _ := h.keys.GetFingerprint("device.example.com", "admin"); got != fp {
		t.Fatal("fingerprint not saved")
	}
	if len(h.errs) != 0 {
		t.Fatalf("error shown: %v", <-h.errs)
	}
}

func TestConnectUsesStoredPasswordAndFingerprint(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.keys.SetFingerprint("device.example.com", "admin", fp)
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true}, name: "Büro"})
	o := h.waitOpened(t)
	if len(h.asked) != 0 || len(h.trusted) != 0 {
		t.Fatal("dialog shown although everything is stored")
	}
	cfgs := h.dev.Configs()
	if len(cfgs) != 1 || cfgs[0].Password != "secret-1" || cfgs[0].Fingerprint != fp {
		t.Fatalf("%+v", cfgs)
	}
	if o.s.Config().Password != "secret-1" {
		t.Fatal("session lacks password")
	}
}

func TestConnectPinnedHostKeepsStoredPin(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.SetFingerprint("device.example.com", "admin", fp)
	h.dev.FP = strings.Repeat("cd", 32) // a device reporting another fingerprint must not replace the pin
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true}})
	h.waitOpened(t)
	if got, _ := h.keys.GetFingerprint("device.example.com", "admin"); got != fp {
		t.Fatalf("pin replaced: %q", got)
	}
}

func TestConnectFavoriteWithUnreadablePinIsRefused(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.SetFingerprint("device.example.com", "admin", fp)
	h.keys.Err = errors.New("no secret service")
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true}})
	err := h.waitError(t)
	var pe *pinReadError
	if !errors.As(err, &pe) || !strings.Contains(describe(err), "could not be read from the keychain") || !strings.Contains(err.Error(), "no secret service") {
		t.Fatalf("err = %v (%q)", err, describe(err))
	}
	h.waitDone(t)
	if len(h.trusted) != 0 || len(h.dev.Configs()) != 0 {
		t.Fatal("first-use prompt or connection although the pin could not be read")
	}
}

func TestConnectAdHocWithBrokenKeychainUsesFirstUse(t *testing.T) {
	h := newHarness(t)
	h.keys.Err = errors.New("no secret service")
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true}})
	h.waitOpened(t)
	h.waitDone(t)
	if got := <-h.trusted; got != fp {
		t.Fatal("trust dialog not shown")
	}
	if len(h.errs) != 0 {
		t.Fatalf("error shown: %v", <-h.errs)
	}
}

func TestConnectUnreadableFavoritesFileAborts(t *testing.T) {
	h := newHarness(t)
	h.mgr = favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), h.keys)
	path := filepath.Join(t.TempDir(), "favorites.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	h.c.mgr = favorites.NewManager(favorites.NewStore(path), h.keys)
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true}})
	if err := h.waitError(t); !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
	h.waitDone(t)
	if len(h.dev.Configs()) != 0 {
		t.Fatal("connected although the favorites file is unreadable")
	}
}

func TestConnectKeychainUnavailablePlainStillConnects(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"})
	h.keys.Err = errors.New("no secret service")
	req := favRequest()
	req.cfg.UseTLS = false
	h.c.start(req)
	o := h.waitOpened(t)
	if o.name != "Büro" {
		t.Fatalf("%+v", o)
	}
	h.waitDone(t)
	if len(h.errs) != 0 {
		t.Fatalf("typed password and no TLS need no keychain, got: %v", <-h.errs)
	}
}

func TestConnectAsksPasswordWhenNotStored(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	h.passwordAnswer = func() (string, bool, bool) { return "typed-1", true, true }
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true}})
	o := h.waitOpened(t)
	if a := <-h.asked; a.host != "device.example.com" || a.user != "admin" || !a.offerSave {
		t.Fatalf("%+v", a)
	}
	if o.name != "device.example.com" || o.s.Config().Password != "typed-1" {
		t.Fatalf("%+v", o)
	}
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "typed-1" {
		t.Fatal("password not saved")
	}
}

func TestConnectAdHocOffersNoSaveAndStoresNothing(t *testing.T) {
	h := newHarness(t)
	h.passwordAnswer = func() (string, bool, bool) { return "typed-1", true, true }
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true}, savePassword: true})
	h.waitOpened(t)
	if a := <-h.asked; a.offerSave {
		t.Fatal("save offered for ad-hoc connection")
	}
	if got := <-h.trusted; got != fp {
		t.Fatal("trust dialog not shown")
	}
	if _, err := h.keys.Get("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("password stored for ad-hoc connection")
	}
	if _, err := h.keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("fingerprint stored for ad-hoc connection")
	}
	if list, _ := h.mgr.List(); len(list) != 0 {
		t.Fatal("favorite created")
	}
}

func TestConnectCancelPasswordDialog(t *testing.T) {
	h := newHarness(t)
	h.passwordAnswer = func() (string, bool, bool) { return "", false, false }
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin"}})
	<-h.asked
	h.waitDone(t)
	h.expectNothing(t)
	if len(h.dev.Configs()) != 0 {
		t.Fatal("connected without password")
	}
}

func TestConnectStalePasswordAsksAgain(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("device.example.com", "admin", "old-1")
	h.keys.SetFingerprint("device.example.com", "admin", fp)
	h.dev.SetStatusErr(fmt.Errorf("enumerate power state: %w", fmt.Errorf("%w: 401 Unauthorized\n<html/>", errors.New("wsman.Client post received"))))
	h.passwordAnswer = func() (string, bool, bool) { h.dev.SetStatusErr(nil); return "new-1", true, true }
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true}})
	o := h.waitOpened(t)
	if len(h.asked) != 1 {
		t.Fatalf("asked %d times", len(h.asked))
	}
	if o.s.Config().Password != "new-1" {
		t.Fatal("session uses old password")
	}
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "new-1" {
		t.Fatal("stale password not replaced")
	}
	if len(h.errs) != 0 {
		t.Fatalf("error shown: %v", <-h.errs)
	}
}

func TestConnectWrongTypedPasswordReportsError(t *testing.T) {
	h := newHarness(t)
	h.dev.SetStatusErr(fmt.Errorf("%w: 401 Unauthorized", errors.New("wsman.Client post received")))
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "wrong"}})
	if err := h.waitError(t); !errors.Is(err, session.ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	h.waitDone(t)
	if len(h.asked) != 0 {
		t.Fatal("asked again although the password was typed")
	}
}

func TestConnectTrustDeclined(t *testing.T) {
	h := newHarness(t)
	h.addFavorite(t)
	h.trustAnswer = false
	h.c.start(favRequest())
	<-h.trusted
	h.waitDone(t)
	h.expectNothing(t)
	if _, err := h.keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("fingerprint pinned although trust was declined")
	}
}

func TestConnectMismatchReported(t *testing.T) {
	h := newHarness(t)
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	h.c.connect = func(context.Context, amt.Config, amt.TrustPrompt) (session.Device, error) { return nil, mm }
	h.c.start(favRequest())
	err := h.waitError(t)
	var got *amt.FingerprintMismatchError
	if !errors.As(err, &got) || got.Actual != mm.Actual {
		t.Fatalf("err = %v", err)
	}
	h.waitDone(t)
}

func TestConnectRejectsPortAndIPv6(t *testing.T) {
	h := newHarness(t)
	for _, host := range []string{"device.example.com:16993", "2001:db8::10", "", "   "} {
		req := favRequest()
		req.cfg.Host = host
		if h.c.start(req) {
			t.Fatalf("%q accepted", host)
		}
		err := <-h.errs
		want := "The host must not contain a port"
		if strings.TrimSpace(host) == "" {
			want = "Host is required"
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: %v", host, err)
		}
	}
	req := favRequest()
	req.cfg.Username = " "
	if h.c.start(req) {
		t.Fatal("empty username accepted")
	}
	if err := <-h.errs; !strings.Contains(err.Error(), "Username is required") {
		t.Fatalf("err = %v", err)
	}
	if len(h.dev.Configs()) != 0 || len(h.done) != 0 {
		t.Fatal("invalid request reached the connection")
	}
}

func TestConnectNormalizesHost(t *testing.T) {
	h := newHarness(t)
	req := favRequest()
	req.cfg.Host = " Device.Example.COM "
	req.name = "  "
	if !h.c.start(req) {
		t.Fatal("rejected")
	}
	o := h.waitOpened(t)
	if o.name != "device.example.com" || o.s.Config().Host != "device.example.com" {
		t.Fatalf("%+v", o)
	}
	if host := <-h.done; host != "device.example.com" {
		t.Fatalf("done host %q", host)
	}
}

func TestTrustPromptBlocksUntilAnswer(t *testing.T) {
	test.NewTempApp(t)
	p := &trustPrompt{ask: func(_, _ string, answer func(bool)) {
		go func() { time.Sleep(20 * time.Millisecond); answer(false) }()
	}}
	ok, err := p.ConfirmFingerprint("device.example.com", fp)
	if ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}

// blockingConnect returns a connect func whose calls wait for release or the context.
func blockingConnect(dev *session.Fake, release <-chan struct{}) session.ConnectFunc {
	return func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
		select {
		case <-release:
			return session.ConnectFake(dev)(ctx, cfg, prompt)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func TestCancelEndsAttemptAtOnceAndShowsNothing(t *testing.T) {
	h := newHarness(t)
	h.c.connect = blockingConnect(h.dev, make(chan struct{}))
	h.c.start(favRequest())
	h.c.cancelAll()
	h.waitDone(t)
	h.expectNothing(t)
	if len(h.done) != 0 {
		t.Fatal("onDone called twice")
	}
}

func TestCancelDropsLateSession(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	ignoreCtx := func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
		<-release // a dial that does not honour the context, like a WS-Man call
		return session.ConnectFake(h.dev)(context.Background(), cfg, prompt)
	}
	h.c.connect = ignoreCtx
	h.c.start(favRequest())
	h.c.cancelAll()
	h.waitDone(t)
	close(release)
	h.expectNothing(t)
	if len(h.done) != 0 {
		t.Fatal("onDone called again for the cancelled attempt")
	}
}

func TestCancelledAttemptDoesNotEndTheNextOne(t *testing.T) {
	h := newHarness(t)
	first := make(chan struct{})
	calls := make(chan struct{}, 4)
	var n atomic.Int32
	h.c.connect = func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
		isFirst := n.Add(1) == 1
		calls <- struct{}{}
		if isFirst {
			<-first // the cancelled attempt returns only after the second one started
		}
		return session.ConnectFake(h.dev)(context.Background(), cfg, prompt)
	}
	h.c.start(favRequest())
	<-calls
	h.c.cancelAll()
	h.waitDone(t)
	h.c.start(favRequest())
	<-calls
	o := h.waitOpened(t)
	h.waitDone(t)
	close(first)
	h.expectNothing(t)
	if o.name != "Büro" || len(h.done) != 0 {
		t.Fatalf("%+v, %d extra onDone", o, len(h.done))
	}
}

func TestTrustPromptOfCancelledAttemptDoesNotAsk(t *testing.T) {
	test.NewTempApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	asked := false
	p := &trustPrompt{ctx: ctx, ask: func(_, _ string, answer func(bool)) { asked = true; answer(true) }}
	if ok, err := p.ConfirmFingerprint("device.example.com", fp); ok || err != nil || asked {
		t.Fatalf("ok=%v err=%v asked=%v", ok, err, asked)
	}
}

// Pin missing and keychain password stale: the fingerprint dialog appears once, the retry with
// the new password reuses the confirmed fingerprint and pins it.
func TestConnectStalePasswordWithoutPinAsksTrustOnce(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("device.example.com", "admin", "old-1")
	h.dev.SetStatusErr(fmt.Errorf("%w: 401 Unauthorized", errors.New("wsman.Client post received")))
	h.passwordAnswer = func() (string, bool, bool) { h.dev.SetStatusErr(nil); return "new-1", true, true }
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true}})
	o := h.waitOpened(t)
	if len(h.trusted) != 1 {
		t.Fatalf("trust dialog shown %d times", len(h.trusted))
	}
	if len(h.asked) != 1 {
		t.Fatalf("password asked %d times", len(h.asked))
	}
	if o.s.Config().Password != "new-1" {
		t.Fatal("session uses old password")
	}
	if got, err := h.keys.GetFingerprint("device.example.com", "admin"); err != nil || got != fp {
		t.Fatalf("pin = %q, %v", got, err)
	}
	if len(h.errs) != 0 {
		t.Fatalf("error shown: %v", <-h.errs)
	}
}

// A certificate that changes between the two attempts still produces the mismatch error.
func TestConnectStalePasswordCertChangedReportsMismatch(t *testing.T) {
	h := newHarness(t)
	h.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("device.example.com", "admin", "old-1")
	h.dev.SetStatusErr(fmt.Errorf("%w: 401 Unauthorized", errors.New("wsman.Client post received")))
	h.passwordAnswer = func() (string, bool, bool) { return "new-1", true, true }
	fake := session.ConnectFake(h.dev)
	other := strings.Repeat("cd", 32)
	h.c.connect = func(ctx context.Context, cfg amt.Config, p amt.TrustPrompt) (session.Device, error) {
		if cfg.Fingerprint != "" {
			if cfg.Fingerprint != fp {
				t.Errorf("retry pin = %q", cfg.Fingerprint)
			}
			return nil, &amt.FingerprintMismatchError{Host: cfg.Host, Expected: cfg.Fingerprint, Actual: other}
		}
		return fake(ctx, cfg, p)
	}
	h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true}})
	var got *amt.FingerprintMismatchError
	if err := h.waitError(t); !errors.As(err, &got) {
		t.Fatalf("err = %v", err)
	}
	h.waitDone(t)
	if len(h.trusted) != 1 {
		t.Fatalf("trust dialog shown %d times", len(h.trusted))
	}
	if _, err := h.keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("fingerprint pinned despite mismatch")
	}
}
