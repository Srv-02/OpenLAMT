package ui

import (
	"context"
	"errors"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
)

// connectRequest describes one connection attempt started from the start window.
type connectRequest struct {
	cfg          amt.Config // Password may be empty: keychain, then dialog
	name         string     // display name; empty means host
	savePassword bool       // set by the password dialog; only honoured for favorites
	isFavorite   bool       // set by prepare: host and user belong to a favorite
	fromKeychain bool       // set by prepare: password came from the keychain
	tofu         bool       // set by dial: cfg.Fingerprint was confirmed in this flow, not yet stored
	att          *attempt   // set by start
}

// attempt is one running connection; cancel aborts it. A cancelled attempt ends at once for
// the start window; its goroutine may still finish later and is then discarded silently.
type attempt struct {
	ctx        context.Context
	cancel     context.CancelFunc
	host, user string
}

// connector runs the connection flow. start validates on the Fyne thread; favorites file,
// keychain and the connection itself run on a goroutine; dialogs come back via fyne.Do.
type connector struct {
	mgr     *favorites.Manager
	keys    keychain.Store
	connect session.ConnectFunc

	mu       sync.Mutex
	attempts map[string]*attempt // key: deviceKey(host, user)

	// Outputs, all called on the Fyne thread; set by the start window.
	onOpened func(s *session.Session, st amt.Status, name string)
	onError  func(error)
	onDone   func(host, user string) // end of an attempt, after onOpened or the error, or at cancel
	// onPasswordSaved: "Save password" was used; called before onOpened, also when the attempt was cancelled meanwhile.
	onPasswordSaved func(host, user string)

	// Dialogs, replaced in tests.
	askPassword func(host, user string, offerSave bool, answer func(password string, save, ok bool))
	askTrust    func(host, fp string, answer func(bool))
}

func newConnector(parent fyne.Window, mgr *favorites.Manager, keys keychain.Store, connect session.ConnectFunc) *connector {
	c := &connector{mgr: mgr, keys: keys, connect: connect, attempts: map[string]*attempt{},
		onOpened: func(*session.Session, amt.Status, string) {}, onDone: func(string, string) {}}
	c.onError = func(err error) { showError(parent, err) }
	c.askPassword = func(host, user string, offerSave bool, answer func(string, bool, bool)) {
		showPasswordDialog(parent, host, user, offerSave, answer)
	}
	c.askTrust = func(host, fp string, answer func(bool)) { showTrustDialog(parent, host, fp, answer) }
	return c
}

// normalizeHost trims and lower-cases the host and rejects ports and IPv6 literals.
func normalizeHost(host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", userError(lang.L("Host is required."))
	}
	if strings.Contains(host, ":") {
		return "", userError(lang.L("The host must not contain a port or be an IPv6 address."))
	}
	return host, nil
}

// start validates the request on the Fyne thread and hands it to a goroutine. It returns
// false when validation failed (the error was reported, onDone is not called).
func (c *connector) start(req connectRequest) bool {
	host, err := normalizeHost(req.cfg.Host)
	if err != nil {
		c.onError(err)
		return false
	}
	req.cfg.Host = host
	req.cfg.Username = strings.TrimSpace(req.cfg.Username)
	if req.cfg.Username == "" {
		c.onError(userError(lang.L("Username is required.")))
		return false
	}
	req.name = strings.TrimSpace(req.name)
	ctx, cancel := context.WithCancel(context.Background())
	req.att = &attempt{ctx: ctx, cancel: cancel, host: host, user: req.cfg.Username}
	c.mu.Lock()
	c.attempts[deviceKey(host, req.cfg.Username)] = req.att
	c.mu.Unlock()
	go c.prepare(req)
	return true
}

// take ends the attempt of req if it is still running and reports whether it was; after a
// cancel it returns false and the caller drops its result.
func (c *connector) take(req connectRequest) bool {
	k := deviceKey(req.cfg.Host, req.cfg.Username)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempts[k] != req.att {
		return false
	}
	delete(c.attempts, k)
	req.att.cancel()
	return true
}

// finish ends the attempt on the Fyne thread and reports it to the start window.
func (c *connector) finish(req connectRequest) {
	if c.take(req) {
		c.onDone(req.cfg.Host, req.cfg.Username)
	}
}

// cancelAll aborts every running attempt; onDone is called for each right away. A dial in
// progress stops at its context or at amt.ConnectTimeout; a WS-Man call that is already
// running finishes and its session is closed and dropped.
func (c *connector) cancelAll() {
	c.mu.Lock()
	ended := make([]*attempt, 0, len(c.attempts))
	for k, a := range c.attempts {
		a.cancel()
		delete(c.attempts, k)
		ended = append(ended, a)
	}
	c.mu.Unlock()
	for _, a := range ended {
		c.onDone(a.host, a.user)
	}
}

// prepare runs on a goroutine: favorites lookup, pinned fingerprint, stored password.
// Pins exist only for favorites, so only a favorite looks one up; a favorite whose pin cannot
// be read is refused (never downgraded to trust on first use), an unknown host asks the user.
func (c *connector) prepare(req connectRequest) {
	host, user := req.cfg.Host, req.cfg.Username
	_, found, err := c.mgr.Find(host, user)
	if err != nil {
		c.fail(req, err)
		return
	}
	req.isFavorite = found
	if req.cfg.UseTLS && req.isFavorite {
		switch stored, err := c.keys.GetFingerprint(host, user); {
		case err == nil:
			req.cfg.Fingerprint = stored
		case errors.Is(err, keychain.ErrNotFound):
		default:
			c.fail(req, &pinReadError{Err: err})
			return
		}
	}
	if req.cfg.Password == "" && req.isFavorite {
		if pw, err := c.keys.Get(host, user); err == nil {
			req.cfg.Password, req.fromKeychain = pw, true
		}
	}
	if req.cfg.Password == "" {
		c.askPasswordThen(req, req.isFavorite)
		return
	}
	c.dial(req)
}

// askPasswordThen shows the password dialog on the Fyne thread and dials with the answer.
func (c *connector) askPasswordThen(req connectRequest, offerSave bool) {
	fyne.Do(func() {
		if req.att.ctx.Err() != nil { // cancelled while the lookup ran
			return
		}
		c.askPassword(req.cfg.Host, req.cfg.Username, offerSave, func(pw string, save, ok bool) {
			if !ok {
				c.finish(req)
				return
			}
			req.cfg.Password, req.savePassword, req.fromKeychain = pw, save, false
			go c.dial(req)
		})
	})
}

// dial runs on a goroutine; the trust prompt blocks it until the user answers.
func (c *connector) dial(req connectRequest) {
	tp := &trustPrompt{ask: c.askTrust, ctx: req.att.ctx}
	s, st, err := session.Open(req.att.ctx, req.cfg, tp, c.connect)
	if err != nil {
		if errors.Is(err, session.ErrAuth) && tp.confirmed != "" && req.cfg.Fingerprint == "" {
			// The user already confirmed the certificate; the retry after a new password pins it
			// instead of asking again. A changed certificate still fails with a mismatch.
			req.cfg.Fingerprint, req.tofu = tp.confirmed, true
		}
		c.fail(req, err)
		return
	}
	var perr error
	if req.isFavorite && req.att.ctx.Err() == nil {
		perr = c.persist(req, s)
	}
	name := req.name
	if name == "" {
		name = req.cfg.Host
	}
	saved := req.isFavorite && req.savePassword && !req.fromKeychain
	fyne.Do(func() {
		if saved && c.onPasswordSaved != nil { // also after a cancel: the keychain changed either way
			c.onPasswordSaved(req.cfg.Host, req.cfg.Username)
		}
		if !c.take(req) { // cancelled: the user no longer expects a window
			s.Close()
			return
		}
		c.onOpened(s, st, name)
		if perr != nil {
			c.onError(perr)
		}
		c.onDone(req.cfg.Host, req.cfg.Username)
	})
}

// fail reports a failed attempt on the Fyne thread. A stale keychain password is asked once more.
func (c *connector) fail(req connectRequest, err error) {
	if errors.Is(err, session.ErrAuth) && req.fromKeychain && req.att.ctx.Err() == nil {
		c.askPasswordThen(req, true)
		return
	}
	fyne.Do(func() {
		if !c.take(req) { // cancelled: the error is a consequence of the cancel
			return
		}
		if !errors.Is(err, amt.ErrTrustDeclined) {
			c.onError(err)
		}
		c.onDone(req.cfg.Host, req.cfg.Username)
	})
}

// persist stores the password (if the dialog asked for it) and, after trust on first use, the
// fingerprint of a favorite. It runs on the connect goroutine; all failures are joined into
// one error. Without a favorite nothing goes into the keychain (no orphaned entries).
func (c *connector) persist(req connectRequest, s *session.Session) error {
	host, user := req.cfg.Host, req.cfg.Username
	var errs []error
	if req.savePassword && !req.fromKeychain {
		if err := c.keys.Set(host, user, req.cfg.Password); err != nil {
			errs = append(errs, &favorites.KeychainError{Err: err})
		}
	}
	if req.cfg.UseTLS && (req.cfg.Fingerprint == "" || req.tofu) && s.Fingerprint() != "" { // trust on first use happened
		if err := c.keys.SetFingerprint(host, user, s.Fingerprint()); err != nil {
			errs = append(errs, &favorites.KeychainError{Err: err})
		}
	}
	return errors.Join(errs...)
}

// trustPrompt implements amt.TrustPrompt: ConfirmFingerprint runs on the connect goroutine,
// shows the dialog on the Fyne thread and blocks until the user answers.
// A cancelled attempt answers "no" without showing the dialog.
type trustPrompt struct {
	ask func(host, fp string, answer func(bool))
	ctx context.Context // nil = never cancelled

	confirmed string // fingerprint the user accepted; written on the connect goroutine only
}

func (p *trustPrompt) ConfirmFingerprint(host, fp string) (bool, error) {
	ch := make(chan bool, 1)
	fyne.Do(func() {
		if p.ctx != nil && p.ctx.Err() != nil {
			ch <- false
			return
		}
		p.ask(host, fp, func(ok bool) { ch <- ok })
	})
	ok := <-ch
	if ok {
		p.confirmed = fp
	}
	return ok, nil
}
