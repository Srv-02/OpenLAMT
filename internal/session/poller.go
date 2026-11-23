package session

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
)

// ErrStopped is passed to a Power callback when the poller was stopped before the action ran.
var ErrStopped = errors.New("session closed")

// DefaultInterval is used when a non-positive interval is given.
const DefaultInterval = 10 * time.Second

type job struct {
	action amt.PowerAction
	prompt amt.ConsentPrompt // BootToBIOS only: the user consent asked before the boot options are set
	fn     func() error      // Do: runs instead of the action
	done   func(error)
}

// Poller reads the status on one goroutine: immediately after Start, then every interval
// (measured from the end of the previous read), after every Refresh and after every Power
// action. While a Power action runs no status is read. Refresh requests collapse into a
// channel of capacity 1; Power actions queue separately (capacity 8).
//
// The supported power states (amt.Status.PowerStates) are read once per connection, before the
// first status is delivered, and again after a failed read; RefreshPowerStates reads them once
// more after the next status was delivered, so that status is not delayed.
type Poller struct {
	s        *Session
	interval atomic.Int64
	onStatus func(amt.Status, error)
	jobs     chan job
	refresh  chan struct{}
	stop     chan struct{}
	done     chan struct{}
	started  atomic.Bool

	states      []power.PowerState // last read power states; poller goroutine only
	statesKnown bool               // states were read successfully; poller goroutine only
	statesStale atomic.Bool        // RefreshPowerStates was called

	mu      sync.Mutex // guards stopped and the hand-over of jobs after Stop
	stopped bool
}

func NewPoller(s *Session, interval time.Duration, onStatus func(amt.Status, error)) *Poller {
	p := &Poller{s: s, onStatus: onStatus, jobs: make(chan job, 8), refresh: make(chan struct{}, 1),
		stop: make(chan struct{}), done: make(chan struct{})}
	p.interval.Store(int64(positive(interval)))
	return p
}

func positive(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultInterval
	}
	return d
}

func (p *Poller) Interval() time.Duration { return time.Duration(p.interval.Load()) }

func (p *Poller) Start() {
	if p.started.CompareAndSwap(false, true) {
		go p.loop()
	}
}

// Stop ends the loop; it waits for a running action and returns ErrStopped to queued ones.
// Idempotent. Safe before Start.
func (p *Poller) Stop() {
	p.mu.Lock()
	first := !p.stopped
	p.stopped = true
	p.mu.Unlock()
	if first {
		close(p.stop)
	}
	if p.started.Load() {
		<-p.done
	}
}

// SetInterval stores d and triggers a read so the new interval starts now.
func (p *Poller) SetInterval(d time.Duration) {
	p.interval.Store(int64(positive(d)))
	p.Refresh()
}

// Refresh requests a status read; pending requests collapse into one.
func (p *Poller) Refresh() {
	select {
	case p.refresh <- struct{}{}:
	default:
	}
}

// RefreshPowerStates requests a status read followed by a read of the supported power states.
// The status is delivered first, with the states known so far; the new states follow in a second
// delivery of the same status only if they changed.
func (p *Poller) RefreshPowerStates() {
	p.statesStale.Store(true)
	p.Refresh()
}

// Power queues action; done is called on the poller goroutine with the result, or with
// ErrStopped (on the caller's goroutine or during Stop) if the poller is stopped.
// done must not call back into the Poller synchronously (Power, Stop): it runs on the poller
// goroutine. Power itself can block while 8 actions are already waiting.
//
// BootToBIOS runs through Session.BootToBIOS without a prompt: where the device requires user
// consent it fails with amt.ErrConsentRequired before anything is changed. Use BootToBIOS to ask.
func (p *Poller) Power(action amt.PowerAction, done func(error)) {
	p.queue(job{action: action, done: done})
}

// BootToBIOS queues Session.BootToBIOS like Power queues an action: the user consent (prompt may
// block on a dialog) and the boot-to-BIOS sequence run as one job on the poller goroutine, so no
// status read runs in between. Stop waits for a running job: a prompt that blocks must end when
// the caller stops the poller (cancel its context first).
func (p *Poller) BootToBIOS(prompt amt.ConsentPrompt, done func(error)) {
	p.queue(job{action: amt.BootToBIOS, prompt: prompt, done: done})
}

// Do queues fn like Power queues an action: it runs as one job on the poller goroutine (no
// status read in between, a read after it) and done gets its error. The boot options of the Boot
// Options dialog (Session.Boot, Session.BootFromFile) run through it with their consent prompt;
// as with BootToBIOS, a prompt that blocks must end when the caller stops the poller.
func (p *Poller) Do(fn func() error, done func(error)) {
	p.queue(job{fn: fn, done: done})
}

func (p *Poller) queue(j job) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		j.done(ErrStopped)
		return
	}
	p.jobs <- j
}

// run performs j on the poller goroutine.
func (p *Poller) run(j job) error {
	if j.fn != nil {
		return j.fn()
	}
	if j.action == amt.BootToBIOS {
		return p.s.BootToBIOS(j.prompt)
	}
	return p.s.Power(j.action)
}

func (p *Poller) loop() {
	defer p.finish()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-timer.C:
			p.read()
		case <-p.refresh:
			stopTimer(timer)
			p.read()
		case j := <-p.jobs:
			stopTimer(timer)
			if p.isStopped() {
				j.done(ErrStopped)
				return
			}
			j.done(p.run(j))
			p.read()
		}
		timer.Reset(p.Interval())
	}
}

// finish answers jobs that were queued before Stop took effect; Power cannot add more
// because stopped is already set under mu.
func (p *Poller) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		select {
		case j := <-p.jobs:
			j.done(ErrStopped)
		default:
			close(p.done)
			return
		}
	}
}

func (p *Poller) isStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func (p *Poller) read() {
	st, err := p.s.Status()
	if err != nil {
		p.onStatus(st, err)
		return
	}
	if !p.statesKnown {
		p.statesStale.Store(false)
		p.readStates()
	}
	st.PowerStates = p.states
	p.onStatus(st, nil)
	if p.statesStale.Swap(false) {
		old := p.states
		p.readStates()
		if !slices.Equal(old, p.states) {
			st.PowerStates = p.states
			p.onStatus(st, nil)
		}
	}
}

// readStates reads the supported power states; a failure leaves them unknown (nil) and is
// retried by the next run. A refused read (limited account) counts as read: it stays empty and
// is not asked again, so the device does not receive a refused request on every run.
func (p *Poller) readStates() {
	states, err := p.s.PowerStates()
	if err != nil {
		states = nil
	}
	p.states, p.statesKnown = states, err == nil || errors.Is(err, amt.ErrAccessDenied)
}
