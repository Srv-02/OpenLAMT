package session

import "github.com/Srv-02/openlamt/internal/amt"

// FakeRedirection is the redirection state of a Fake for OpenKVM, OpenSOL and OpenIDER. The
// zero value has everything on. A feature that is off is switched on (and listed in Enabled)
// like *amt.Device does, unless the last ConnectFake config has NeverEnable: then the Open
// call fails with *amt.RedirectionOffError and nothing changes. Set the Off fields before the
// server starts; the Fake reads and writes the struct under its mutex.
type FakeRedirection struct {
	ListenerOff, KVMOff, SOLOff, IDEROff bool
	Enabled                              []string // "redirection", "KVM", "SOL", "IDE-R" in the order they were switched on
}

// Redirection returns the redirection state of f.
func (f *Fake) Redirection() *FakeRedirection { return fakeState[FakeRedirection](f, "redirection") }

// RedirectionEnabled returns the features the Fake switched on, in order.
func (f *Fake) RedirectionEnabled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Redirection().Enabled...)
}

// enableRedirection switches the listener and then feature ("KVM", "SOL" or "IDE-R") on when
// they are off, or refuses with *amt.RedirectionOffError when the last config has NeverEnable.
func (f *Fake) enableRedirection(feature string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.Redirection()
	never := len(f.configs) > 0 && f.configs[len(f.configs)-1].NeverEnable
	if r.ListenerOff {
		if never {
			return &amt.RedirectionOffError{Feature: "redirection"}
		}
		r.ListenerOff = false
		r.Enabled = append(r.Enabled, "redirection")
	}
	off := map[string]*bool{"KVM": &r.KVMOff, "SOL": &r.SOLOff, "IDE-R": &r.IDEROff}[feature]
	if *off {
		if never {
			return &amt.RedirectionOffError{Feature: feature}
		}
		*off = false
		r.Enabled = append(r.Enabled, feature)
	}
	return nil
}
