package amt

import (
	"errors"
	"fmt"
	"time"

	messagelog "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/messagelog"
)

// Severity classifies an event log entry.
type Severity int

const (
	SeverityInfo     Severity = iota // IPMI 0 Unspecified, 1 Monitor, 2 Information, 4 OK, unknown values
	SeverityWarning                  // IPMI 8 Non-critical
	SeverityCritical                 // IPMI 16 Critical, 32 Non-recoverable
)

func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	}
	return "info"
}

// severityFromIPMI maps the raw IPMI event severity byte.
func severityFromIPMI(raw uint8) Severity {
	switch raw {
	case 8:
		return SeverityWarning
	case 16, 32:
		return SeverityCritical
	}
	return SeverityInfo
}

// EventLogEntry is one record of AMT_MessageLog.
type EventLogEntry struct {
	Time        time.Time // UTC
	Severity    Severity
	Source      string // entity from the library table, e.g. "BIOS", "Intel(r) ME"; "" if unknown
	Description string // English text from the library decoder
	SensorType  uint8  // raw IPMI fields, for a later localized decoder
	EventOffset uint8
	EventData   [8]byte
}

// EventLog reads the AMT event log (at most 390 entries, one request), newest first.
// Records the device pads with zero bytes are dropped, and so are records the library
// cannot decode (invalid Base64, shorter than 21 bytes): the result can hold fewer
// entries than the device returned.
func (d *Device) EventLog() ([]EventLogEntry, error) {
	resp, err := d.msgs.AMT.MessageLog.GetRecords(1, messagelog.MaxAMTRecords)
	if err != nil {
		if denied := accessDenied(err, "read event log"); errors.Is(denied, ErrAccessDenied) {
			return nil, denied
		}
		return nil, fmt.Errorf("read event log: %w", err)
	}
	out := resp.Body.GetRecordsResponse
	switch messagelog.GetRecordsReturnValue(out.ReturnValue) {
	case 0:
	case 3: // No record exists
		return nil, nil
	default:
		return nil, fmt.Errorf("read event log: device returned ReturnValue %d", out.ReturnValue)
	}

	var entries []EventLogEntry
	for i, raw := range out.RawEventData {
		if i >= len(out.RefinedEventData) {
			break
		}
		if isPadding(raw) {
			continue
		}
		refined := out.RefinedEventData[i]
		source := refined.Entity
		if source == "Unspecified" || source == "Unknown" {
			source = ""
		}
		e := EventLogEntry{
			Time:        refined.TimeStamp.UTC(),
			Severity:    severityFromIPMI(raw.EventSeverity),
			Source:      source,
			Description: refined.Description,
			SensorType:  refined.EventSensorType,
			EventOffset: refined.EventOffset,
		}
		copy(e.EventData[:], refined.EventData)
		entries = append(entries, e)
	}
	return entries, nil
}

// isPadding reports whether a decoded record is all zero, as AMT pads the log with such records.
func isPadding(r messagelog.RawEventData) bool {
	for _, v := range []uint8{r.DeviceAddress, r.EventSensorType, r.EventType, r.EventOffset, r.EventSourceType, r.EventSeverity, r.SensorNumber, r.Entity, r.EntityInstance} {
		if v != 0 {
			return false
		}
	}
	if r.TimeStamp != 0 {
		return false
	}
	for _, c := range r.EventData {
		if c != 0 {
			return false
		}
	}
	return true
}
