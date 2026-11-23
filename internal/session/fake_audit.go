package session

import (
	"slices"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeAudit is the audit settings state of a Fake: AuditSettings returns Settings (or ReadErr),
// SetAuditSettings applies a change to it and ClearAuditLog empties Fake.Audit, with the
// confirmation checks of *amt.Device. WriteErr fails every write. Use it under Fake.mu only
// through the Fake's methods; tests read it after the writes ended.
type FakeAudit struct {
	Settings amt.AuditSettings
	ReadErr  error
	WriteErr error
	Changes  []amt.AuditChange // every accepted SetAuditSettings
	Clears   int
}

// AuditState returns the audit settings state of f.
func (f *Fake) AuditState() *FakeAudit { return fakeState[FakeAudit](f, "audit") }

func (f *Fake) AuditSettings() (amt.AuditSettings, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.AuditState()
	if a.ReadErr != nil {
		return amt.AuditSettings{}, a.ReadErr
	}
	s := a.Settings
	s.Policy = slices.Clone(s.Policy)
	return s, nil
}

func (f *Fake) SetAuditSettings(c amt.AuditChange, confirm amt.Confirmation) error {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Risky() {
		if err := f.confirmed(confirm, amt.OpAuditSettings, []string{"AuditLog.Recording"}); err != nil {
			return err
		}
	}
	a := f.AuditState()
	if a.WriteErr != nil {
		return a.WriteErr
	}
	a.Changes = append(a.Changes, c)
	s := &a.Settings
	if c.Recording != nil {
		s.Recording = *c.Recording
	}
	if c.Storage != nil {
		s.Storage, s.MinDaysToKeep = *c.Storage, 0
		if *c.Storage == amt.AuditOverwriteAfterDays {
			s.MinDaysToKeep = *c.MinDaysToKeep
		}
	}
	for _, e := range c.Disable {
		s.Policy = slices.DeleteFunc(s.Policy, func(x amt.AuditEvent) bool { return x == e })
	}
	for _, e := range c.Enable {
		if !slices.Contains(s.Policy, e) {
			s.Policy = append(s.Policy, e)
		}
	}
	return nil
}

func (f *Fake) ClearAuditLog(confirm amt.Confirmation) error {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.confirmed(confirm, amt.OpAuditClear, []string{"AuditLog"}); err != nil {
		return err
	}
	a := f.AuditState()
	if a.WriteErr != nil {
		return a.WriteErr
	}
	a.Clears++
	a.Settings.Records = 1
	f.Audit = nil
	return nil
}
