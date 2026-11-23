package session

import (
	"slices"
	"sync"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakePower is the wake alarm and power policy state of a Fake (Fake.PowerPage; the name
// Power is the power action). The writes run the checks of *amt.Device
// (spec validation, duplicate name, the limit of five alarms, the confirmation of a change
// that loses the remote start) and then change the state. Err, if set, is returned by every
// read and write after the checks.
type FakePower struct {
	mu     sync.Mutex
	Err    error
	Alarms []amt.Alarm
	Policy amt.PowerPolicy
	Writes []string // "AddAlarm", "DeleteAlarm", "SetPowerScheme", "SetIdleTimeout"
	Reads  int      // GetAlarms and GetPowerPolicy calls
}

// PowerPage returns the state, created on first use.
func (f *Fake) PowerPage() *FakePower { return fakeState[FakePower](f, "power") }

// Lock and Unlock guard the state for tests that change it while a page reads it.
func (p *FakePower) Lock()   { p.mu.Lock() }
func (p *FakePower) Unlock() { p.mu.Unlock() }

// Written returns the recorded writes.
func (p *FakePower) Written() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.Writes)
}

func (f *Fake) GetAlarms() ([]amt.Alarm, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	p := f.PowerPage()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Reads++
	return slices.Clone(p.Alarms), p.Err
}

func (f *Fake) AddAlarm(s amt.AlarmSpec) error {
	f.enter()
	defer f.leave()
	p := f.PowerPage()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := s.Validate(time.Now()); err != nil {
		return err
	}
	for _, a := range p.Alarms {
		if a.Name == s.Name {
			return amt.ErrAlarmExists
		}
	}
	if len(p.Alarms) >= amt.MaxAlarms {
		return amt.ErrAlarmsFull
	}
	if p.Err != nil {
		return p.Err
	}
	p.Alarms = append(p.Alarms, amt.Alarm{Name: s.Name, Start: s.Start.UTC(), Interval: s.Interval, DeleteOnCompletion: s.DeleteOnCompletion})
	p.Writes = append(p.Writes, "AddAlarm")
	return nil
}

func (f *Fake) DeleteAlarm(name string) error {
	f.enter()
	defer f.leave()
	p := f.PowerPage()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return p.Err
	}
	p.Alarms = slices.DeleteFunc(p.Alarms, func(a amt.Alarm) bool { return a.Name == name })
	p.Writes = append(p.Writes, "DeleteAlarm")
	return nil
}

func (f *Fake) GetPowerPolicy() (amt.PowerPolicy, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	p := f.PowerPage()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Reads++
	pol := p.Policy
	pol.Schemes = slices.Clone(pol.Schemes)
	return pol, p.Err
}

func (f *Fake) SetPowerScheme(id string, c amt.Confirmation) error {
	f.enter()
	defer f.leave()
	p := f.PowerPage()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.Policy.Scheme(id); !ok {
		return &amt.ValidationError{Field: "Scheme", Reason: "the device offers no such power package"}
	}
	if p.Policy.RisksRemoteStart(id) {
		f.mu.Lock()
		err := f.confirmed(c, amt.OpPowerPolicy, []string{"Scheme"})
		f.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if p.Err != nil {
		return p.Err
	}
	p.Policy.Current = id
	p.Writes = append(p.Writes, "SetPowerScheme")
	return nil
}

func (f *Fake) SetIdleTimeout(minutes int) error {
	f.enter()
	defer f.leave()
	p := f.PowerPage()
	p.mu.Lock()
	defer p.mu.Unlock()
	if minutes < amt.MinIdleTimeout || minutes > amt.MaxIdleTimeout {
		return &amt.ValidationError{Field: "IdleWakeTimeoutMinutes", Reason: "between 1 and 65535"}
	}
	if p.Err != nil {
		return p.Err
	}
	p.Policy.IdleTimeoutMinutes = minutes
	p.Writes = append(p.Writes, "SetIdleTimeout")
	return nil
}

var (
	_ AlarmDevice       = (*Fake)(nil)
	_ PowerPolicyDevice = (*Fake)(nil)
)
