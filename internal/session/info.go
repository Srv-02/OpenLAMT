package session

import "github.com/Srv-02/openlamt/internal/amt"

// HardwareInfo reads the hardware inventory under the session mutex (about 1–1.5 s on AMT 16).
func (s *Session) HardwareInfo() (amt.HardwareInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hw, err := s.dev.HardwareInfo()
	if err != nil {
		return amt.HardwareInfo{}, classify(err)
	}
	return hw, nil
}

// EventLog reads the AMT event log under the mutex, newest first (up to about 2 s).
func (s *Session) EventLog() ([]amt.EventLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.dev.EventLog()
	if err != nil {
		return nil, classify(err)
	}
	return entries, nil
}

// AuditLog reads the whole AMT audit log under the mutex, newest first (0.2–2 s).
// A missing permission is reported as amt.ErrAccessDenied.
func (s *Session) AuditLog() ([]amt.AuditLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.dev.AuditLog()
	if err != nil {
		return nil, classify(err)
	}
	return entries, nil
}
