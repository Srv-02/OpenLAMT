package amt

import (
	"encoding/base64"
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
// Records the device pads with zero bytes are dropped.
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
	for i, rec := range out.RecordArray {
		if i >= len(out.RawEventData) || i >= len(out.RefinedEventData) {
			break
		}
		if allZero(rec) {
			continue
		}
		raw, refined := out.RawEventData[i], out.RefinedEventData[i]
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

// allZero reports whether the Base64 record decodes to zero bytes only (any length).
func allZero(b64 string) bool {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return false
	}
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
