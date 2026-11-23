package session

import "github.com/Srv-02/openlamt/internal/amt"

// AuditSettingsDevice is the capability of the audit log settings (issue #15).
type AuditSettingsDevice interface {
	AuditSettings() (amt.AuditSettings, error)
	SetAuditSettings(c amt.AuditChange, confirm amt.Confirmation) error
	ClearAuditLog(confirm amt.Confirmation) error
}

var _ AuditSettingsDevice = (*amt.Device)(nil)

// AuditSettings reads the audit log settings and policy.
func (s *Session) AuditSettings() (amt.AuditSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[AuditSettingsDevice](s)
	if err != nil {
		return amt.AuditSettings{}, err
	}
	v, err := d.AuditSettings()
	return v, classify(err)
}

// SetAuditSettings writes c; switching the recording off needs confirm for amt.OpAuditSettings.
func (s *Session) SetAuditSettings(c amt.AuditChange, confirm amt.Confirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[AuditSettingsDevice](s)
	if err != nil {
		return err
	}
	return classify(d.SetAuditSettings(c, confirm))
}

// ClearAuditLog deletes every audit record; it needs confirm for amt.OpAuditClear.
func (s *Session) ClearAuditLog(confirm amt.Confirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[AuditSettingsDevice](s)
	if err != nil {
		return err
	}
	return classify(d.ClearAuditLog(confirm))
}
