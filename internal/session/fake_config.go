package session

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeConfig is the AMT settings and user state of a Fake. Writes run the same validation,
// risk, confirmation and account checks as *amt.Device (the device's host is the Host of the
// last ConnectFake config), then change this state and append the method name to Writes
// (nothing is recorded without an effective change). WriteErr, if set, is returned by every
// write after the checks, without changing anything.
type FakeConfig struct {
	Settings    amt.Settings
	SettingsErr error
	Users       amt.UserList
	UsersErr    error
	// Certificates is the certificate store; SetTLS with a new certificate moves the TLS mark
	// and, in a TLS session, sets the fake's FP to the certificate's fingerprint, as the device
	// switches its pin.
	Certificates []amt.Certificate
	CertsErr     error
	WriteErr     error
	Writes       []string
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

// confirmed is the fake's version of the device check: nil if c confirms op on the connected
// host, else *amt.RiskyChangeError. Call it under f.mu.
func (f *Fake) confirmed(c amt.Confirmation, op amt.Operation, fields []string) error {
	host := ""
	if n := len(f.configs); n > 0 {
		host = f.configs[n-1].Host
	}
	if c.Allows(op, host) {
		return nil
	}
	return &amt.RiskyChangeError{Op: op, Fields: fields}
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

func (f *Fake) SetGeneral(c amt.GeneralChange, confirm amt.Confirmation) error {
	g := &f.Cfg.Settings.General
	return f.cfgWrite("SetGeneral", func() (bool, error) {
		if err := c.Validate(); err != nil {
			return false, err
		}
		if risky := c.Risky(*g); risky != nil {
			if err := f.confirmed(confirm, amt.OpHostName, risky); err != nil {
				return false, err
			}
		}
		return c.Apply(*g) != *g, nil
	}, func() { *g = c.Apply(*g) })
}

func (f *Fake) SetNetwork(c amt.NetworkChange, confirm amt.Confirmation) error {
	n := &f.Cfg.Settings.Network
	return f.cfgWrite("SetNetwork", func() (bool, error) {
		if !n.Available {
			return false, amt.ErrNotModifiable
		}
		if err := c.Validate(*n); err != nil {
			return false, err
		}
		risky := c.Risky(*n)
		if risky == nil {
			return false, nil
		}
		return true, f.confirmed(confirm, amt.OpNetwork, risky)
	}, func() {
		next := c.Apply(*n)
		if next.DHCP || next.IPSync { // the device drops address fields in these modes
			next.IPAddress, next.SubnetMask, next.Gateway, next.PrimaryDNS, next.SecondaryDNS =
				n.IPAddress, n.SubnetMask, n.Gateway, n.PrimaryDNS, n.SecondaryDNS
		}
		*n = next
	})
}

func (f *Fake) SetFeatures(c amt.FeatureChange, confirm amt.Confirmation) error {
	cur := &f.Cfg.Settings.Features
	return f.cfgWrite("SetFeatures", func() (bool, error) {
		if c.KVM != nil && *c.KVM && !cur.KVMAvailable {
			return false, amt.ErrNotModifiable
		}
		if c.Apply(*cur) == *cur {
			return false, nil
		}
		if risky := c.Risky(*cur); risky != nil {
			if err := f.confirmed(confirm, amt.OpFeaturesOff, risky); err != nil {
				return false, err
			}
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

func (f *Fake) SetEnvironmentDetection(domains []string, confirm amt.Confirmation) error {
	e := &f.Cfg.Settings.Environment
	return f.cfgWrite("SetEnvironmentDetection", func() (bool, error) {
		if err := amt.ValidateEnvironmentDomains(domains); err != nil {
			return false, err
		}
		if !e.Available {
			return false, amt.ErrNotModifiable
		}
		risky := amt.EnvironmentRisky(*e, domains)
		if risky == nil {
			return false, nil
		}
		return true, f.confirmed(confirm, amt.OpEnvironment, risky)
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

func (f *Fake) UpdateUser(h int, u amt.UserSpec, confirm amt.Confirmation) (amt.LoginChange, error) {
	var res amt.LoginChange
	i := -1
	err := f.cfgWrite("UpdateUser", func() (bool, error) {
		if err := u.Validate(); err != nil {
			return false, err
		}
		var err error
		if i, err = f.fakeUser("UpdateUser", h); err != nil {
			return false, err
		}
		cur := f.Cfg.Users.Users[i]
		if cur.System {
			return false, fmt.Errorf("UpdateUser: %w", amt.ErrProtectedAccount)
		}
		if cur.Name == "" {
			return false, amt.ErrNotModifiable
		}
		if cur.Self {
			if u.Access == amt.AccessLocal {
				return false, &amt.ValidationError{Field: "Access", Reason: "the account of this connection needs network access"}
			}
			if err := f.confirmed(confirm, amt.OpOwnAccount, []string{amt.RiskOwnAccount}); err != nil {
				return false, err
			}
		}
		res = amt.LoginChange{OldUsername: cur.Name, Username: u.Name, Self: cur.Self}
		return true, nil
	}, func() {
		n := specUser(h, u)
		cur := f.Cfg.Users.Users[i]
		n.Enabled, n.EnabledKnown, n.Self = cur.Enabled, cur.EnabledKnown, cur.Self
		f.Cfg.Users.Users[i] = n
	})
	if err != nil {
		return amt.LoginChange{}, err
	}
	return res, nil
}

// SetAdminAccount checks like *amt.Device and renames Users.Admin. The change is the session's
// own login if the last ConnectFake user is the admin.
func (f *Fake) SetAdminAccount(c amt.AccountChange, confirm amt.Confirmation) (amt.LoginChange, error) {
	var res amt.LoginChange
	err := f.cfgWrite("SetAdminAccount", func() (bool, error) {
		if err := c.Validate(); err != nil {
			return false, err
		}
		if err := f.confirmed(confirm, amt.OpAdminAccount, []string{amt.RiskAdmin}); err != nil {
			return false, err
		}
		user := ""
		if n := len(f.configs); n > 0 {
			user = f.configs[n-1].Username
		}
		res = amt.LoginChange{OldUsername: f.Cfg.Users.Admin, Username: f.Cfg.Users.Admin, Self: strings.EqualFold(user, f.Cfg.Users.Admin)}
		if c.Name != "" {
			res.Username = c.Name
		}
		return true, nil
	}, func() { f.Cfg.Users.Admin = res.Username })
	if err != nil {
		return amt.LoginChange{}, err
	}
	return res, nil
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

func (f *Fake) TLSCertificates() ([]amt.Certificate, error) {
	var out []amt.Certificate
	var err error
	f.cfgRead(func() {
		if f.Cfg.CertsErr != nil {
			err = f.Cfg.CertsErr
			return
		}
		out = slices.Clone(f.Cfg.Certificates)
	})
	return out, err
}

func (f *Fake) SetTLS(c amt.TLSChange, confirm amt.Confirmation) error {
	t := &f.Cfg.Settings.TLS
	var fields []string
	return f.cfgWrite("SetTLS", func() (bool, error) {
		var err error
		if fields, err = c.Check(*t, f.Cfg.Certificates); err != nil || fields == nil {
			return false, err
		}
		return true, f.confirmed(confirm, amt.OpTLS, fields)
	}, func() {
		*t = c.Apply(*t)
		if slices.Contains(fields, amt.RiskTLSCertificate) {
			for i := range f.Cfg.Certificates {
				cert := &f.Cfg.Certificates[i]
				cert.TLS = cert.ID == *c.Certificate
				if cert.TLS && len(f.configs) > 0 && f.configs[len(f.configs)-1].UseTLS { // as the device: a plain session has no pin
					f.FP = cert.Fingerprint
				}
			}
		}
	})
}

// DisableAMTNetwork checks the confirmation like *amt.Device and clears General.NetworkEnabled.
func (f *Fake) DisableAMTNetwork(confirm amt.Confirmation) error {
	g := &f.Cfg.Settings.General
	return f.cfgWrite("DisableAMTNetwork", func() (bool, error) {
		if err := f.confirmed(confirm, amt.OpDisableNetwork, []string{amt.RiskAMTNetwork}); err != nil {
			return false, err
		}
		return g.NetworkEnabled, nil
	}, func() { g.NetworkEnabled = false })
}
