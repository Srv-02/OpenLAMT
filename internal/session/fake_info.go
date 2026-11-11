package session

import (
	"slices"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

func (f *Fake) HardwareInfo() (amt.HardwareInfo, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.HWErr != nil {
		return amt.HardwareInfo{}, f.HWErr
	}
	hw := f.HW
	hw.CPUs = slices.Clone(hw.CPUs)
	hw.Memory = slices.Clone(hw.Memory)
	hw.Drives = slices.Clone(hw.Drives)
	return hw, nil
}

func (f *Fake) EventLog() ([]amt.EventLogEntry, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.EventsErr != nil {
		return nil, f.EventsErr
	}
	return slices.Clone(f.Events), nil
}

func (f *Fake) AuditLog() ([]amt.AuditLogEntry, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.AuditErr != nil {
		return nil, f.AuditErr
	}
	return slices.Clone(f.Audit), nil
}
