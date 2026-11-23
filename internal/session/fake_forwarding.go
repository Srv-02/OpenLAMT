package session

import (
	"fmt"
	"slices"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeForwarding is the event forwarding state of a Fake: EventForwarding returns Data (or
// ReadErr); AddForwardingTarget validates like *amt.Device and appends a target (AddErr fails
// it), DeleteForwardingTarget removes one (DeleteErr fails it).
type FakeForwarding struct {
	Data      amt.Forwarding
	ReadErr   error
	AddErr    error
	DeleteErr error
	Added     []amt.TargetSpec
	Deleted   []amt.ForwardingTarget
}

// Forwarding returns the event forwarding state of f.
func (f *Fake) Forwarding() *FakeForwarding { return fakeState[FakeForwarding](f, "forwarding") }

func (f *Fake) EventForwarding() (amt.Forwarding, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.Forwarding()
	if w.ReadErr != nil {
		return amt.Forwarding{}, w.ReadErr
	}
	return amt.Forwarding{Filters: slices.Clone(w.Data.Filters), Targets: slices.Clone(w.Data.Targets)}, nil
}

func (f *Fake) AddForwardingTarget(s amt.TargetSpec) error {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := s.Validate(); err != nil {
		return err
	}
	w := f.Forwarding()
	if w.AddErr != nil {
		return w.AddErr
	}
	w.Added = append(w.Added, s)
	name := s.FilterID
	for _, fl := range w.Data.Filters {
		if fl.ID == s.FilterID {
			name = fl.Name
		}
	}
	w.Data.Targets = append(w.Data.Targets, amt.ForwardingTarget{Address: s.Address, Delivery: s.Delivery, FilterID: s.FilterID,
		FilterName: name, Listener: fmt.Sprintf("Listener Destination %d", len(w.Added)+100)})
	return nil
}

func (f *Fake) DeleteForwardingTarget(t amt.ForwardingTarget) error {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.Forwarding()
	if w.DeleteErr != nil {
		return w.DeleteErr
	}
	w.Deleted = append(w.Deleted, t)
	w.Data.Targets = slices.DeleteFunc(w.Data.Targets, func(x amt.ForwardingTarget) bool {
		return x.Listener == t.Listener && x.FilterID == t.FilterID
	})
	return nil
}
