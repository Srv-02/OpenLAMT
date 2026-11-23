package mcpserver

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
)

// kvmOpener opens a KVM session to the host and user of fav with the consent code (may be
// empty). agreed: the person in the MCP client agreed to turn KVM on (see server.mayEnable).
type kvmOpener func(ctx context.Context, fav favorites.Favorite, code consentCode, agreed bool) (*kvm.Client, []string, error)

// kvmManager keeps one KVM session per device across tool calls. AMT allows one KVM session per
// device, so the manager owns it: kvm_connect opens it, the other KVM tools use it, a session
// that AMT ended or a power action closed is opened again by the next call, an idle session is
// closed after the idle timeout, and Close ends all of them when the client goes away.
//
// Lock order: an entry's mu may be held while taking m.mu, never the other way round.
type kvmManager struct {
	mu      sync.Mutex
	entries map[string]*kvmEntry // by favKey
	closed  bool
	idle    time.Duration // 0: never
	open    kvmOpener
	now     func() time.Time // clock of the idle timeout; tests replace it
}

// kvmEntry is the KVM session of one device. Its mutex serializes the tool calls on it.
type kvmEntry struct {
	mu       sync.Mutex
	key      string      // favKey of the device
	client   *kvm.Client // nil after a power action closed it; opened again on use
	warnings []string    // of the last open, e.g. no TLS
	removed  bool        // disconnected, idle or shut down: the entry is no longer in the manager
	lastUsed time.Time
	timer    *time.Timer

	imgW, imgH   int        // size of the last screenshot; 0 before the first one
	buttons      kvm.Button // mouse buttons held by kvm_mouse down
	lastX, lastY int        // last pointer position on the screen
}

func newKVMManager(idle time.Duration, open kvmOpener) *kvmManager {
	return &kvmManager{entries: map[string]*kvmEntry{}, idle: idle, open: open, now: time.Now}
}

var errShuttingDown = refuse("The MCP server is shutting down.")

func noSession(fav favorites.Favorite) error {
	return refuse("No KVM session is open for %q: call kvm_connect first.", fav.DisplayName())
}

// alive reports whether the client runs.
func (e *kvmEntry) alive() bool {
	if e.client == nil {
		return false
	}
	select {
	case <-e.client.Done():
		return false
	default:
		return true
	}
}

// release lets go of held mouse buttons and keys, so nothing stays pressed on the host.
func (e *kvmEntry) release() {
	if !e.alive() {
		return
	}
	if e.buttons != 0 {
		_ = e.client.PointerEvent(e.lastX, e.lastY, 0)
	}
	e.buttons = 0
	_ = e.client.ReleaseAll()
}

// shut releases input and closes the client; the caller holds e.mu.
func (e *kvmEntry) shut() {
	if e.timer != nil {
		e.timer.Stop()
	}
	e.release()
	if e.client != nil {
		_ = e.client.Close()
		e.client = nil
	}
}

// has reports whether the device of fav has a session entry (open, or to be opened again on use).
func (m *kvmManager) has(fav favorites.Favorite) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[favKey(fav)] != nil
}

// connect opens the session of the device of fav, or keeps the running one (already true), and
// runs fn with the entry locked. A session that ended is opened again with code; agreed goes to
// the opener.
func (m *kvmManager) connect(ctx context.Context, fav favorites.Favorite, code consentCode, agreed bool, fn func(e *kvmEntry, already bool) error) error {
	key := favKey(fav)
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return errShuttingDown
		}
		e, created := m.entries[key], false
		if e == nil {
			e, created = &kvmEntry{key: key}, true
			m.entries[key] = e
		}
		m.mu.Unlock()

		e.mu.Lock()
		if e.removed { // closed meanwhile: start over
			e.mu.Unlock()
			continue
		}
		already := e.alive()
		if !already {
			c, warnings, err := m.open(ctx, fav, code, agreed)
			if err != nil {
				if created {
					m.remove(e)
				}
				e.mu.Unlock()
				return err
			}
			e.client, e.warnings, e.buttons = c, warnings, 0
			if created && m.idle > 0 {
				e.timer = time.AfterFunc(m.idle, func() { m.expire(e) })
			}
		}
		e.lastUsed = m.now()
		err := fn(e, already)
		e.lastUsed = m.now()
		e.mu.Unlock()
		return err
	}
}

// reopenError: the session had ended and opening it again failed.
type reopenError struct{ err error }

func (e *reopenError) Error() string { return "reopen the KVM session: " + e.err.Error() }
func (e *reopenError) Unwrap() error { return e.err }

// use runs fn on the open session of the device of fav with the entry locked. A session that
// ended is opened again first, without a consent code; if consent is needed then, the error asks
// for kvm_connect with the code.
func (m *kvmManager) use(ctx context.Context, fav favorites.Favorite, fn func(e *kvmEntry) error) error {
	m.mu.Lock()
	closed, e := m.closed, m.entries[favKey(fav)]
	m.mu.Unlock()
	switch {
	case closed:
		return errShuttingDown
	case e == nil:
		return noSession(fav)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return noSession(fav)
	}
	if !e.alive() {
		e.release()
		c, warnings, err := m.open(ctx, fav, "", false)
		switch {
		case errors.Is(err, errConsentCodeNeeded):
			return &consentNeededError{tool: "kvm_connect", reopen: true}
		case err != nil:
			return &reopenError{err: err}
		}
		e.client, e.warnings, e.buttons = c, warnings, 0
	}
	e.lastUsed = m.now()
	err := fn(e)
	e.lastUsed = m.now()
	return err
}

// remove takes e out of the manager; the caller holds e.mu.
func (m *kvmManager) remove(e *kvmEntry) {
	e.removed = true
	m.mu.Lock()
	if m.entries[e.key] == e {
		delete(m.entries, e.key)
	}
	m.mu.Unlock()
}

// expire closes e once it was not used for the idle timeout; a call in progress delays it.
func (m *kvmManager) expire(e *kvmEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return
	}
	if left := m.idle - m.now().Sub(e.lastUsed); left > 0 {
		e.timer.Reset(left)
		return
	}
	m.remove(e)
	e.shut()
}

// disconnect closes and removes the session of the device of fav; it reports whether there was one.
func (m *kvmManager) disconnect(fav favorites.Favorite) bool {
	m.mu.Lock()
	e := m.entries[favKey(fav)]
	m.mu.Unlock()
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return false
	}
	m.remove(e)
	e.shut()
	return true
}

// closeForPower closes a running session of the device of fav before a power action that AMT
// refuses while KVM is open. The entry stays, so the next KVM call opens the session again. It
// reports whether a running session was closed.
func (m *kvmManager) closeForPower(fav favorites.Favorite) bool {
	m.mu.Lock()
	e := m.entries[favKey(fav)]
	m.mu.Unlock()
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed || !e.alive() {
		return false
	}
	e.release()
	_ = e.client.Close()
	e.client = nil
	return true
}

// closeAll ends every session and refuses new ones (the client went away).
func (m *kvmManager) closeAll() {
	m.mu.Lock()
	m.closed = true
	entries := make([]*kvmEntry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	clear(m.entries)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Go(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.removed = true
			e.shut()
		})
	}
	wg.Wait()
}
