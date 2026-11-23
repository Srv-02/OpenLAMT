package session

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeDefense is the System Defense and agent watchdog state of a Fake. The writes run the
// checks of *amt.Device (spec validation, names in use, a filter a policy uses, the active
// policy, the confirmation of a policy that drops traffic, the shared watchdog policy) and then
// change the state. Err, if set, is returned by every read and write after the checks;
// WatchdogErr only by the watchdog calls.
type FakeDefense struct {
	mu          sync.Mutex
	Err         error
	WatchdogErr error
	Defense     amt.SystemDefense
	Watchdogs   amt.Watchdogs
	Writes      []string // method names, e.g. "ActivateDefensePolicy"
	next        int
}

// DefensePage returns the state, created on first use.
func (f *Fake) DefensePage() *FakeDefense { return fakeState[FakeDefense](f, "defense") }

// Lock and Unlock guard the state for tests that change it while a page reads it.
func (p *FakeDefense) Lock()   { p.mu.Lock() }
func (p *FakeDefense) Unlock() { p.mu.Unlock() }

// Written returns the recorded writes.
func (p *FakeDefense) Written() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.Writes)
}

func (p *FakeDefense) clone() amt.SystemDefense {
	sd := p.Defense
	sd.Policies = slices.Clone(sd.Policies)
	sd.Filters = slices.Clone(sd.Filters)
	for i := range sd.Policies {
		sd.Policies[i].Active = sd.Policies[i].ID == sd.Active && sd.Active != ""
	}
	return sd
}

// defenseWrite runs fn on the locked state of f unless a check of fn or Err refuses.
func (f *Fake) defenseWrite(name string, fn func(p *FakeDefense) error) error {
	f.enter()
	defer f.leave()
	p := f.DefensePage()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := fn(p); err != nil {
		return err
	}
	p.Writes = append(p.Writes, name)
	return nil
}

func (f *Fake) confirmedDrop(c amt.Confirmation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.confirmed(c, amt.OpDefenseDrop, []string{"Policy"})
}

func (f *Fake) GetSystemDefense() (amt.SystemDefense, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	p := f.DefensePage()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return amt.SystemDefense{}, p.Err
	}
	return p.clone(), nil
}

func (f *Fake) AddDefenseFilter(s amt.FilterSpec) error {
	return f.defenseWrite("AddDefenseFilter", func(p *FakeDefense) error {
		if err := s.Validate(); err != nil {
			return err
		}
		name := strings.TrimSpace(s.Name)
		if slices.ContainsFunc(p.Defense.Filters, func(x amt.DefenseFilter) bool { return strings.EqualFold(x.Name, name) }) {
			return amt.ErrDefenseNameExists
		}
		if p.Err != nil {
			return p.Err
		}
		p.next++
		p.Defense.Filters = append(p.Defense.Filters, amt.DefenseFilter{ID: "f" + strconv.Itoa(p.next), Name: name, Kind: s.Kind,
			Direction: s.Direction, Protocol: s.Protocol, PortStart: s.PortStart, PortEnd: s.PortEnd, Remote: s.Remote.Masked(),
			Drop: s.Drop, Count: s.Count, Event: s.Event})
		return nil
	})
}

func (f *Fake) DeleteDefenseFilter(id string) error {
	return f.defenseWrite("DeleteDefenseFilter", func(p *FakeDefense) error {
		if _, ok := p.Defense.Filter(id); !ok {
			return amt.ErrDefenseNotFound
		}
		for _, pol := range p.Defense.Policies {
			if slices.Contains(pol.Filters, id) {
				return &amt.DefenseFilterInUseError{Policy: pol.Name}
			}
		}
		if p.Err != nil {
			return p.Err
		}
		p.Defense.Filters = slices.DeleteFunc(p.Defense.Filters, func(x amt.DefenseFilter) bool { return x.ID == id })
		return nil
	})
}

func (f *Fake) AddDefensePolicy(s amt.PolicySpec) error {
	return f.defenseWrite("AddDefensePolicy", func(p *FakeDefense) error {
		if err := s.Validate(); err != nil {
			return err
		}
		name := strings.TrimSpace(s.Name)
		if slices.ContainsFunc(p.Defense.Policies, func(x amt.DefensePolicy) bool { return strings.EqualFold(x.Name, name) }) {
			return amt.ErrDefenseNameExists
		}
		for _, id := range s.Filters {
			if _, ok := p.Defense.Filter(id); !ok {
				return &amt.ValidationError{Field: "Filters", Reason: "the device has no such filter"}
			}
		}
		if p.Err != nil {
			return p.Err
		}
		p.next++
		p.Defense.Policies = append(p.Defense.Policies, amt.DefensePolicy{ID: "p" + strconv.Itoa(p.next), Name: name,
			Precedence: s.Precedence, DropIn: s.Drop, DropOut: s.Drop, Filters: slices.Clone(s.Filters)})
		return nil
	})
}

func (f *Fake) DeleteDefensePolicy(id string) error {
	return f.defenseWrite("DeleteDefensePolicy", func(p *FakeDefense) error {
		if _, ok := p.Defense.Policy(id); !ok {
			return amt.ErrDefenseNotFound
		}
		if p.Defense.Active == id {
			return amt.ErrDefensePolicyActive
		}
		if p.Err != nil {
			return p.Err
		}
		p.Defense.Policies = slices.DeleteFunc(p.Defense.Policies, func(x amt.DefensePolicy) bool { return x.ID == id })
		return nil
	})
}

func (f *Fake) ActivateDefensePolicy(id string, c amt.Confirmation) error {
	return f.defenseWrite("ActivateDefensePolicy", func(p *FakeDefense) error {
		if _, ok := p.Defense.Policy(id); !ok {
			return amt.ErrDefenseNotFound
		}
		if p.Defense.Drops(id) && p.Defense.Active != id {
			if err := f.confirmedDrop(c); err != nil {
				return err
			}
		}
		if p.Err != nil {
			return p.Err
		}
		p.Defense.Active = id
		return nil
	})
}

func (f *Fake) DeactivateDefensePolicy() error {
	return f.defenseWrite("DeactivateDefensePolicy", func(p *FakeDefense) error {
		if p.Err != nil {
			return p.Err
		}
		p.Defense.Active = ""
		return nil
	})
}

func (f *Fake) GetWatchdogs() (amt.Watchdogs, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	p := f.DefensePage()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.watchdogErr(); err != nil {
		return amt.Watchdogs{}, err
	}
	w := p.Watchdogs
	w.List = slices.Clone(w.List)
	return w, nil
}

func (p *FakeDefense) watchdogErr() error {
	if p.WatchdogErr != nil {
		return p.WatchdogErr
	}
	return p.Err
}

func (f *Fake) AddWatchdog(s amt.WatchdogSpec, c amt.Confirmation) error {
	return f.defenseWrite("AddWatchdog", func(p *FakeDefense) error {
		if err := s.Validate(); err != nil {
			return err
		}
		for _, w := range p.Watchdogs.List {
			if strings.EqualFold(w.ID, s.ID) {
				return amt.ErrWatchdogExists
			}
		}
		switches := s.OnStop.Policy != amt.PolicyKeep || s.OnStart.Policy != amt.PolicyKeep
		if switches {
			pol, ok := p.Defense.Policy(s.Policy)
			if !ok {
				return &amt.ValidationError{Field: "Policy", Reason: "the device has no such policy"}
			}
			shared := p.Watchdogs.Policy
			if shared != "" && shared != s.Policy && slices.ContainsFunc(p.Watchdogs.List, func(w amt.Watchdog) bool {
				return w.OnStop.Policy != amt.PolicyKeep || w.OnStart.Policy != amt.PolicyKeep
			}) {
				name := shared
				if sp, ok := p.Defense.Policy(shared); ok {
					name = sp.Name
				}
				return &amt.WatchdogPolicySharedError{Policy: name}
			}
			if s.OnStop.Policy == amt.PolicyActivate && p.Defense.Drops(pol.ID) {
				if err := f.confirmedDrop(c); err != nil {
					return err
				}
			}
		}
		if err := p.watchdogErr(); err != nil {
			return err
		}
		if switches {
			p.Watchdogs.Policy = s.Policy
		}
		p.Watchdogs.List = append(p.Watchdogs.List, amt.Watchdog{ID: strings.ToLower(s.ID), Name: strings.TrimSpace(s.Name),
			Startup: s.Startup, Heartbeat: s.Heartbeat, State: amt.WatchdogNotStarted, OnStop: s.OnStop, OnStart: s.OnStart})
		return nil
	})
}

func (f *Fake) DeleteWatchdog(id string) error {
	return f.defenseWrite("DeleteWatchdog", func(p *FakeDefense) error {
		if !slices.ContainsFunc(p.Watchdogs.List, func(w amt.Watchdog) bool { return strings.EqualFold(w.ID, id) }) {
			return amt.ErrWatchdogNotFound
		}
		if err := p.watchdogErr(); err != nil {
			return err
		}
		p.Watchdogs.List = slices.DeleteFunc(p.Watchdogs.List, func(w amt.Watchdog) bool { return strings.EqualFold(w.ID, id) })
		return nil
	})
}

var (
	_ DefenseDevice  = (*Fake)(nil)
	_ WatchdogDevice = (*Fake)(nil)
)
