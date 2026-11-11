package session

import (
	"fmt"
	"slices"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeConfig is the AMT settings and user state of a Fake. Writes run the same validation,
// risk and account checks as *amt.Device, then change this state and append the method name
// to Writes (nothing is recorded without an effective change). WriteErr, if set, is returned
// by every write after the checks, without changing anything.
type FakeConfig struct {
	Settings    amt.Settings
	SettingsErr error
	Users       amt.UserList
	UsersErr    error
	WriteErr    error
	Writes      []string
}

func (f *Fake) cfgRead(fn func()) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// cfgWrite runs check under the fake's mutex; a nil error from check with changed true records
// method and applies the change through apply, unless WriteErr is set.
func (f *Fake) cfgWrite(method string, check func() (changed bool, err error), apply func()) error {
	var err error
	f.cfgRead(func() {
		var changed bool
		if changed, err = check(); err != nil || !changed {
			return
		}
		if f.Cfg.WriteErr != nil {
			err = f.Cfg.WriteErr
			return
		}
		apply()
		f.Cfg.Writes = append(f.Cfg.Writes, method)
	})
	return err
}

// ConfigWrites returns the recorded write methods.
func (f *Fake) ConfigWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Cfg.Writes)
}

func (f *Fake) Settings() (amt.Settings, error) {
	var s amt.Settings
	var err error
	f.cfgRead(func() {
		if f.Cfg.SettingsErr != nil {
			err = f.Cfg.SettingsErr
			return
		}
		s = f.Cfg.Settings
		s.TLS.Remote.TrustedCNs = slices.Clone(s.TLS.Remote.TrustedCNs)
		s.TLS.Local.TrustedCNs = slices.Clone(s.TLS.Local.TrustedCNs)
		s.Environment.Domains = slices.Clone(s.Environment.Domains)
	})
	return s, err
}

func (f *Fake) SetGeneral(c amt.GeneralChange) error {
	g := &f.Cfg.Settings.General
	return f.cfgWrite("SetGeneral", func() (bool, error) {
		if err := c.Validate(); err != nil {
			return false, err
		}
		return c.Apply(*g) != *g, nil
	}, func() { *g = c.Apply(*g) })
}

func (f *Fake) SetNetwork(c amt.NetworkChange, confirmRisky bool) error {
	n := &f.Cfg.Settings.Network
	return f.cfgWrite("SetNetwork", func() (bool, error) {
		if !n.Available {
			return false, amt.ErrNotModifiable
		}
		if err := c.Validate(*n); err != nil {
			return false, err
		}
		risky := c.Risky(*n)
		if risky != nil && !confirmRisky {
			return false, &amt.RiskyChangeError{Fields: risky}
		}
		return risky != nil, nil
	}, func() { *n = c.Apply(*n) })
}

func (f *Fake) SetFeatures(c amt.FeatureChange, confirmRisky bool) error {
	cur := &f.Cfg.Settings.Features
	return f.cfgWrite("SetFeatures", func() (bool, error) {
		if c.KVM != nil && !cur.KVMAvailable {
			return false, amt.ErrNotModifiable
		}
		if c.Apply(*cur) == *cur {
			return false, nil
		}
		if risky := c.Risky(*cur); risky != nil && !confirmRisky {
			return false, &amt.RiskyChangeError{Fields: risky}
		}
		return true, nil
	}, func() { *cur = c.Apply(*cur) })
}

func (f *Fake) SetConsent(p amt.ConsentPolicy) error {
	c := &f.Cfg.Settings.Consent
	return f.cfgWrite("SetConsent", func() (bool, error) {
		if p < amt.ConsentNone || p > amt.ConsentAll {
			return false, &amt.ValidationError{Field: "Policy", Reason: "unknown consent policy"}
		}
		if c.Policy == p {
			return false, nil
		}
		if !c.CanModify {
			return false, amt.ErrNotModifiable
		}
		return true, nil
	}, func() { c.Policy = p })
}

func (f *Fake) SetEnvironmentDetection(domains []string, confirmRisky bool) error {
	e := &f.Cfg.Settings.Environment
	return f.cfgWrite("SetEnvironmentDetection", func() (bool, error) {
		if err := amt.ValidateEnvironmentDomains(domains); err != nil {
			return false, err
		}
		if !e.Available {
			return false, amt.ErrNotModifiable
		}
		risky := amt.EnvironmentRisky(*e, domains)
		if risky != nil && !confirmRisky {
			return false, &amt.RiskyChangeError{Fields: risky}
		}
		return risky != nil, nil
	}, func() { e.Domains = slices.Clone(domains) })
}

func (f *Fake) SyncClock() error {
	c := &f.Cfg.Settings.Clock
	return f.cfgWrite("SyncClock", func() (bool, error) { return true, nil },
		func() { c.Time, c.Offset = time.Now().UTC().Truncate(time.Second), 0 })
}

func (f *Fake) Users() (amt.UserList, error) {
	var l amt.UserList
	var err error
	f.cfgRead(func() {
		if f.Cfg.UsersErr != nil {
			err = f.Cfg.UsersErr
			return
		}
		l.Admin = f.Cfg.Users.Admin
		for _, u := range f.Cfg.Users.Users {
			u.Realms = slices.Clone(u.Realms)
			l.Users = append(l.Users, u)
		}
	})
	return l, err
}

// fakeUser returns the index of handle h or a *amt.MethodError 2053 (invalid handle).
func (f *Fake) fakeUser(method string, h int) (int, error) {
	i := slices.IndexFunc(f.Cfg.Users.Users, func(u amt.User) bool { return u.Handle == h })
	if i < 0 {
		return -1, &amt.MethodError{Method: method, ReturnValue: 2053}
	}
	return i, nil
}

func (f *Fake) changeableFakeUser(method string, h int) (int, error) {
	i, err := f.fakeUser(method, h)
	if err != nil {
		return -1, err
	}
	if f.Cfg.Users.Users[i].Protected() {
		return -1, fmt.Errorf("%s: %w", method, amt.ErrProtectedAccount)
	}
	return i, nil
}

func specUser(h int, u amt.UserSpec) amt.User {
	rs := slices.Clone(u.Realms)
	slices.Sort(rs)
	return amt.User{Handle: h, Name: u.Name, Access: u.Access, Realms: slices.Compact(rs), Enabled: true, EnabledKnown: true}
}

func (f *Fake) AddUser(u amt.UserSpec) (int, error) {
	h := 0
	err := f.cfgWrite("AddUser", func() (bool, error) {
		if err := u.Validate(); err != nil {
			return false, err
		}
		for _, x := range f.Cfg.Users.Users {
			if x.Name == u.Name {
				return false, &amt.MethodError{Method: "AddUserAclEntryEx", ReturnValue: 2058}
			}
			h = max(h, x.Handle)
		}
		h++
		return true, nil
	}, func() { f.Cfg.Users.Users = append(f.Cfg.Users.Users, specUser(h, u)) })
	if err != nil {
		return 0, err
	}
	return h, nil
}

func (f *Fake) UpdateUser(h int, u amt.UserSpec) error {
	i := -1
	return f.cfgWrite("UpdateUser", func() (bool, error) {
		if err := u.Validate(); err != nil {
			return false, err
		}
		var err error
		if i, err = f.changeableFakeUser("UpdateUser", h); err != nil {
			return false, err
		}
		if f.Cfg.Users.Users[i].Name == "" {
			return false, amt.ErrNotModifiable
		}
		return true, nil
	}, func() {
		n := specUser(h, u)
		n.Enabled, n.EnabledKnown = f.Cfg.Users.Users[i].Enabled, f.Cfg.Users.Users[i].EnabledKnown
		f.Cfg.Users.Users[i] = n
	})
}

func (f *Fake) RemoveUser(h int) error {
	i := -1
	return f.cfgWrite("RemoveUser", func() (bool, error) {
		var err error
		i, err = f.changeableFakeUser("RemoveUser", h)
		return err == nil, err
	}, func() { f.Cfg.Users.Users = slices.Delete(f.Cfg.Users.Users, i, i+1) })
}

func (f *Fake) SetUserEnabled(h int, enabled bool) error {
	i := -1
	return f.cfgWrite("SetUserEnabled", func() (bool, error) {
		var err error
		if i, err = f.changeableFakeUser("SetUserEnabled", h); err != nil {
			return false, err
		}
		u := f.Cfg.Users.Users[i]
		return !u.EnabledKnown || u.Enabled != enabled, nil
	}, func() { f.Cfg.Users.Users[i].Enabled, f.Cfg.Users.Users[i].EnabledKnown = enabled, true })
}
