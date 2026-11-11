package amt

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/common"
	wsmanamt "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt"
	auditlog "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/auditlog"
	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

const (
	auditPageLimit    = 64 // at most 64 ReadRecords calls (about 7 680 records)
	auditInvalidIndex = 35 // PT_STATUS_INVALID_INDEX
)

// AuditLogEntry is one record of AMT_AuditLog.
type AuditLogEntry struct {
	Time          time.Time // UTC
	AppID         int       // raw audit application id, e.g. 16
	EventID       int
	App           string // e.g. "Security Admin Events", "" if unknown
	Event         string // e.g. "AMT Provisioning Started", "" if unknown
	InitiatorType uint8  // 0 digest user, 1 Kerberos, 2 local, 3 KVM default port
	Initiator     string // digest user name, Kerberos SID, "" for local/KVM (UI derives a label from InitiatorType)
	Address       string // network address of the initiator, "" if none
	Details       string // decoded extended data, "" if none or undecodable
}

// auditCapture remembers the last answer of the wrapped client, so the audit log frame can be
// parsed independently of the library decoder.
type auditCapture struct {
	client.WSMan
	body []byte
	err  error
}

func (c *auditCapture) Post(msg string) ([]byte, error) {
	b, err := c.WSMan.Post(msg)
	c.body, c.err = b, err
	return b, err
}

// auditPage is the parsed answer of one ReadRecords call.
type auditPage struct {
	Total       int
	Records     []string // Base64
	ReturnValue int
}

// readAuditPage sends ReadRecords through the library, which may panic while decoding a record,
// and parses the stored answer itself.
func (d *Device) readAuditPage(start int) (auditPage, error) {
	cp := &auditCapture{WSMan: d.msgs.Client}
	func() {
		defer func() { _ = recover() }()
		_, _ = wsmanamt.NewMessages(cp).AuditLog.ReadRecords(start)
	}()
	if cp.err != nil {
		return auditPage{}, cp.err
	}
	if len(cp.body) == 0 {
		return auditPage{}, fmt.Errorf("%w: empty response", ErrIncompleteResponse)
	}
	var env struct {
		Total       int      `xml:"Body>ReadRecords_OUTPUT>TotalRecordCount"`
		Records     []string `xml:"Body>ReadRecords_OUTPUT>EventRecords"`
		ReturnValue int      `xml:"Body>ReadRecords_OUTPUT>ReturnValue"`
	}
	if err := xml.Unmarshal(cp.body, &env); err != nil {
		return auditPage{}, fmt.Errorf("%w: unreadable response: %v", ErrIncompleteResponse, err)
	}
	return auditPage{Total: env.Total, Records: env.Records, ReturnValue: env.ReturnValue}, nil
}

// AuditLog reads all audit log records page by page (AMT returns up to 120 per request) and
// returns them newest first. A missing permission is reported as ErrAccessDenied.
func (d *Device) AuditLog() ([]AuditLogEntry, error) {
	var entries []AuditLogEntry
	start := 1
	for range auditPageLimit {
		page, err := d.readAuditPage(start)
		if err != nil {
			if denied := accessDenied(err, "read audit log"); errors.Is(denied, ErrAccessDenied) {
				return nil, denied
			}
			return nil, fmt.Errorf("read audit log: %w", err)
		}
		switch {
		case page.ReturnValue == 0:
		case page.ReturnValue == auditInvalidIndex && start == 1:
			return nil, nil
		default:
			return nil, fmt.Errorf("read audit log: device returned ReturnValue %d", page.ReturnValue)
		}
		for _, rec := range page.Records {
			b, err := base64.StdEncoding.DecodeString(rec)
			if err != nil || len(b) < 4 {
				continue
			}
			entries = append(entries, parseAuditRecord(b))
		}
		start += len(page.Records)
		if len(page.Records) == 0 || start > page.Total {
			// The device order is chronological (index 1 is the oldest record).
			slices.Reverse(entries)
			return entries, nil
		}
	}
	return nil, fmt.Errorf("read audit log: %w: log did not end after %d pages", ErrIncompleteResponse, auditPageLimit)
}

// parseAuditRecord decodes the frame of one audit record with a length check at every field. A
// record that is too short for a field keeps that field and all following ones empty. Fewer than
// 4 bytes yield the zero value.
func parseAuditRecord(b []byte) AuditLogEntry {
	var e AuditLogEntry
	if len(b) < 4 {
		return e
	}
	e.AppID = int(b[0])<<8 | int(b[1])
	e.EventID = int(b[2])<<8 | int(b[3])
	e.App = auditlog.AMTAppIDToString[e.AppID]
	e.Event = auditlog.AMTAuditLogEventToString[e.AppID*100+e.EventID]
	if len(b) < 5 {
		return e
	}
	e.InitiatorType = b[4]
	p := 5
	switch e.InitiatorType {
	case 0: // digest user: length at 5, name from 6
		if len(b) < 6 || len(b) < 6+int(b[5]) {
			return e
		}
		n := int(b[5])
		e.Initiator = string(b[6 : 6+n])
		p = 6 + n
	case 1: // Kerberos: length at 9, SID from 10
		if len(b) < 10 || len(b) < 10+int(b[9]) {
			return e
		}
		n := int(b[9])
		e.Initiator = sidString(b[10 : 10+n])
		p = 10 + n
	case 2, 3: // local, KVM default port: no initiator data
	default:
		return e
	}

	if len(b) < p+4 {
		return e
	}
	e.Time = time.Unix(int64(uint32(b[p])<<24|uint32(b[p+1])<<16|uint32(b[p+2])<<8|uint32(b[p+3])), 0).UTC()
	p += 4

	p++ // MCLocationType
	if len(b) < p+1 {
		return e
	}
	n := int(b[p])
	p++
	if len(b) < p+n {
		return e
	}
	e.Address = strings.ReplaceAll(string(b[p:p+n]), "0000:0000:0000:0000:0000:0000:0000:0001", "::1")
	p += n

	if len(b) < p+1 {
		return e
	}
	n = int(b[p])
	p++
	if len(b) < p+n {
		return e
	}
	e.Details = auditDetails(e.AppID, e.EventID, string(b[p:p+n]))
	return e
}

// sidString renders a binary Kerberos SID; the library function panics on fewer than 8 bytes.
func sidString(sid []byte) (s string) {
	if len(sid) < 8 {
		return ""
	}
	defer func() {
		if recover() != nil {
			s = ""
		}
	}()
	return common.GetSidString(string(sid))
}

// auditDetails decodes the extended data with the library; a panic or an unknown event yields "".
func auditDetails(app, event int, ex string) (s string) {
	defer func() {
		if recover() != nil {
			s = ""
		}
	}()
	s = auditlog.GetAuditLogExtendedDataString(app, event, ex)
	if s == auditlog.UnknownEventID || s == "Unknown Event Group ID" {
		return ""
	}
	return s
}
